package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
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

	bindAddress := flag.String("bind", "127.0.0.1:2222", "bind address, 127.0.0.1:2222, use :2222 for all")
	flag.Parse()

	ssh.Handle(func(sess ssh.Session) {
		_, _, isTty := sess.Pty()
		cfg := &container.Config{
			Image:        sess.User(),
			Cmd:          sess.Command(),
			Env:          sess.Environ(),
			Tty:          isTty,
			OpenStdin:    true,
			AttachStderr: true,
			AttachStdin:  true,
			AttachStdout: true,
			StdinOnce:    true,
		}
		hostcfg := &container.HostConfig{
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
		status, cleanup, err := dockerRun(cfg, hostcfg, sess)
		defer cleanup()
		if err != nil {
			fmt.Fprintln(sess, err)
			ErrorPrint(err.Error())
		}
		sess.Exit(int(status))
	})

	InfoPrint("starting ssh server on %s...", *bindAddress)
	log.Fatal(ssh.ListenAndServe(*bindAddress, nil))
}

func imageExistsLocally(ctx context.Context, imageName string, cli *client.Client) bool {
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

func waitForContainerReady(ctx context.Context, sess ssh.Session, cli *client.Client, containerID string, timeout time.Duration) error {
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
func dockerRun(cfg *container.Config, hostcfg *container.HostConfig, sess ssh.Session) (status int64, cleanup func(), err error) {
	docker, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}
	status = 255
	cleanup = func() {}
	ctx := context.Background()

	InfoPrint("Image: %s", sess.User())
	cImage := sess.User()

	networkingConfig := network.NetworkingConfig{}
	platformConfig := v1.Platform{
		OS:           "linux",
		Architecture: "amd64",
		// Variant:      "minimal",
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
		ErrorPrint("Unable to start container: %v", err)
		sess.Write([]byte("Unable to pull requested image" + string(startErr.Error()) + "\n"))
		return
	}
	InfoPrint("Wait for container %s to be ready", resp.ID)
	err = waitForContainerReady(ctx, sess, docker, resp.ID, 30*time.Second)
	if err != nil {
		sess.Write([]byte("container failed to become ready"))
		log.Print("Container failed to become ready:", err)
		return
	}
	execResp, err := docker.ContainerExecCreate(ctx, resp.ID, container.ExecOptions{
		Cmd:          []string{"/bin/sh"},
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return
	}
	InfoPrint("Attaching container: %s", resp.ID)
	stream, err := docker.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{
		Tty: true,
	})

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
		cleanup = func() {
			InfoPrint("Killing container: %s", resp.ID)
			docker.ContainerKill(ctx, resp.ID, "9")
			InfoPrint("Removing container: %s", resp.ID)
			docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})
		}
		return
	}
}
