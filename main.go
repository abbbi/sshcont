/*
	Copyright (C) 2025  Michael Ablassmeier <abi@grinser.de>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gliderlabs/ssh"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	InfoPrint("%s: %s %s %s ", os.Args[0], version, commit, date)
	bindAddress := flag.String("bind", "127.0.0.1:2222", "bind address, 127.0.0.1:2222, use :2222 for all")
	dockerEndpoint := flag.String("endpoint", "", "Docker endpoint. Default: use environment settings. Example: tcp://192.168.1.100:2375")
	vol := flag.String("vol", "", "Share volume into container, example: /home/:/home_shared")
	image := flag.String("image", "", "Force image to be executed")
	cmd := flag.String("cmd", "", "Execute cmd after login, example: ls")
	exportFolder := flag.String("export", "", "Before removing, export container contents to specified directory, example: /tmp/")
	flag.Parse()

	if *exportFolder != "" {
		*exportFolder = filepath.Clean(*exportFolder)
	}

	ssh.Handle(func(sess ssh.Session) {
		InfoPrint("Connection from: [%s]", sess.RemoteAddr())
		var defaultImage = sess.User()

		if *image != "" {
			InfoPrint("Overriding image with: [%s]", *image)
			sess.Write([]byte("Overriding image with: [" + *image + "]\n"))
			defaultImage = *image
		}
		_, _, isTty := sess.Pty()
		cfg := &container.Config{
			Image:        defaultImage,
			Env:          sess.Environ(),
			Tty:          isTty,
			OpenStdin:    true,
			AttachStderr: true,
			AttachStdin:  true,
			AttachStdout: true,
			StdinOnce:    true,
		}
		shares := []string{}
		if *vol != "" {
			shares = append(shares, *vol)
		}
		hostcfg := &container.HostConfig{
			Binds: shares,
			Tmpfs: map[string]string{
				"/tmp":      "rw,noexec,nosuid",
				"/run":      "rw,noexec,nosuid",
				"/run/lock": "rw,noexec,nosuid",
			},
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeBind,
					Source: "/sys/fs/cgroup",
					Target: "/sys/fs/cgroup",
				},
			},
			CapAdd:       []string{"SYS_ADMIN"},
			CgroupnsMode: "host",
			SecurityOpt:  []string{"apparmor=unconfined"},
		}
		status, cleanup, err := dockerRun(
			*dockerEndpoint,
			cfg,
			hostcfg,
			sess,
			*cmd,
			*exportFolder,
		)
		defer cleanup()
		if err != nil {
			sess.Write([]byte("Error executing container: [" + err.Error() + "]\n"))
			ErrorPrint("Failed to execute: %s", err.Error())
		}
		sess.Exit(int(status))
	})

	InfoPrint("starting ssh server on %s...", *bindAddress)
	log.Fatal(ssh.ListenAndServe(*bindAddress, nil))
}

func imageExistsLocally(
	ctx context.Context,
	imageName string,
	cli *client.Client,
) bool {
	images, err := cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		ErrorPrint("Error listing images: %v", err)
		return false
	}

	// Check if the image exists locally
	for _, image := range images {
		for _, tag := range image.RepoTags {
			if tag == imageName {
				return true
			}
		}
	}
	return false
}

func waitForContainerReady(
	ctx context.Context,
	sess ssh.Session,
	cli *client.Client,
	containerID string,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		containerJSON, err := cli.ContainerInspect(ctx, containerID)
		if err != nil {
			return fmt.Errorf("error inspecting container: %w", err)
		}

		// Check if the container is running
		if containerJSON.State.Running {
			// If a health check is defined, ensure it's healthy
			if containerJSON.State.Health != nil {
				if containerJSON.State.Health.Status == "healthy" {
					InfoPrint("Container %s is running and healthy.", containerID)
					return nil
				} else if containerJSON.State.Health.Status == "unhealthy" {
					return fmt.Errorf("container is unhealthy")
				}
			} else {
				InfoPrint("Container %s is running.", containerID)
				return nil
			}
		}

		sess.Write([]byte("Waiting for container to become ready...\n"))
		InfoPrint("Waiting for container %s to be ready...", containerID)
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timeout waiting for container to be ready")
}
func dockerRun(
	endpoint string,
	cfg *container.Config,
	hostcfg *container.HostConfig,
	sess ssh.Session,
	cmd string,
	exportFolder string,
) (status int, cleanup func(), err error) {
	var docker *client.Client
	status = 255
	cleanup = func() {}
	ctx := context.Background()
	useTty := true
	cImage := cfg.Image

	if endpoint != "" {
		docker, err = client.NewClientWithOpts(
			client.WithHost(endpoint),
			client.WithAPIVersionNegotiation(),
		)
	} else {
		docker, err = client.NewClientWithOpts(
			client.FromEnv,
			client.WithAPIVersionNegotiation(),
		)
	}
	if err != nil {
		panic(err)
	}

	InfoPrint("Image: %s", cImage)
	defaultCmd := []string{"/bin/sh", "-c", "[ -e /bin/bash ] && /bin/bash || /bin/sh"}

	if sess.RawCommand() != "" {
		defaultCmd = sess.Command()
		useTty = false
	}
	if cmd != "" {
		defaultCmd = strings.Fields(cmd)
	}
	InfoPrint("Executing command: %s", defaultCmd)

	networkingConfig := network.NetworkingConfig{}
	platformConfig := v1.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	if imageExistsLocally(ctx, cImage, docker) != true {
		sess.Write([]byte("Image [" + cImage + "] not found, attempting to fetch from repository ..\n"))
		reader, pullerr := docker.ImagePull(ctx, cImage, image.PullOptions{})
		if pullerr != nil {
			sess.Write([]byte("Unable to pull requested image [" + cImage + "]: [" + string(pullerr.Error()) + "]\n"))
			ErrorPrint("Unable to pull requested image [%s]: %v", cImage, pullerr)
			cleanup = func() {}
			return
		}
		defer reader.Close()
		if _, err := io.Copy(os.Stdout, reader); err != nil {
			ErrorPrint("Unable to read pull output: %v", pullerr)
		}
	}

	resp, err := docker.ContainerCreate(ctx, cfg, hostcfg, &networkingConfig, &platformConfig, "")
	if err != nil {
		ErrorPrint("Unable to create container: %v", err)
		return
	}
	InfoPrint("Created container: %s", resp.ID)
	cleanup = func() {
		docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})
	}
	startErr := docker.ContainerStart(ctx, resp.ID, container.StartOptions{})
	if startErr != nil {
		ErrorPrint("Unable to start container: %s", startErr)
		sess.Write([]byte("Unable to start requested image: [" + string(startErr.Error()) + "]\n"))
		return
	}
	InfoPrint("Wait for container %s to be ready", resp.ID)
	err = waitForContainerReady(ctx, sess, docker, resp.ID, 30*time.Second)
	if err != nil {
		sess.Write([]byte("Container failed to become ready: [" + err.Error() + "]\n"))
		log.Print("Container failed to become ready: ", err)
		return
	}
	execResp, err := docker.ContainerExecCreate(ctx, resp.ID, container.ExecOptions{
		Cmd:          defaultCmd,
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		ErrorPrint("Error creating container exec: [%s]", err.Error())
		return
	}
	InfoPrint("Attaching container: %s", resp.ID)
	stream, err := docker.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{
		Tty: useTty,
	})
	if err != nil {
		ErrorPrint("Error during container attach: [%v]", err.Error())
		return
	}

	outputErr := make(chan error)
	go func() {
		var err error
		if cfg.Tty {
			_, err = io.Copy(sess, stream.Reader)
		} else {
			_, err = stdcopy.StdCopy(sess, sess.Stderr(), stream.Reader)
		}
		outputErr <- err
	}()

	go func() {
		defer stream.CloseWrite()
		io.Copy(stream.Conn, sess)
	}()

	if cfg.Tty {
		_, winCh, _ := sess.Pty()
		go func() {
			for win := range winCh {
				err := docker.ContainerResize(ctx, resp.ID, container.ResizeOptions{
					Height: uint(win.Height),
					Width:  uint(win.Width),
				})
				if err != nil {
					ErrorPrint(err.Error())
					break
				}
			}
		}()
	}

	select {
	case <-outputErr:
		execInspect, ierr := docker.ContainerExecInspect(ctx, execResp.ID)
		if ierr != nil {
			WarnPrint("Unable to inspect command exit code: %s", err.Error())
		}
		status = execInspect.ExitCode
		InfoPrint("Exit code from specified command: %d", status)
		if exportFolder != "" {
			InfoPrint("Exporting container to : [%s/%s.tar]", exportFolder, resp.ID)
			stream, eErr := docker.ContainerExport(ctx, resp.ID)
			if eErr != nil {
				WarnPrint("Unable to create export context for container %s: %s", resp.ID, eErr.Error())
			}
			targetFile, fErr := os.Create(exportFolder + "/" + resp.ID + ".tar")
			if fErr != nil {
				WarnPrint("Unable to create export file for container %s: %s", resp.ID, fErr.Error())
			}
			io.Copy(targetFile, stream)
			targetFile.Close()
			stream.Close()
		}
		cleanup = func() {
			InfoPrint("Killing container: %s", resp.ID)
			docker.ContainerKill(ctx, resp.ID, "9")
			InfoPrint("Removing container: %s", resp.ID)
			docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		}
		return
	}
}
