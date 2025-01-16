package main

import (
	"context"
	"encoding/json"
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

func main() {
	ssh.Handle(func(sess ssh.Session) {
		j, je := json.Marshal(sess)
		if je == nil {
			fmt.Println(string(j))
		}
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
		}
		status, cleanup, err := dockerRun(cfg, hostcfg, sess)
		defer cleanup()
		if err != nil {
			fmt.Fprintln(sess, err)
			log.Println(err)
		}
		sess.Exit(int(status))
	})

	log.Println("starting ssh server on port 2222...")
	log.Fatal(ssh.ListenAndServe(":2222", nil))
}

func imageExistsLocally(imageName string) bool {
	ctx := context.Background()

	// Create a Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Error creating Docker client: %v", err)
	}

	// List images with filters
	images, err := cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		log.Fatalf("Error listing images: %v", err)
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

func waitForContainerReady(ctx context.Context, cli *client.Client, containerID string, timeout time.Duration) error {
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
					fmt.Println("Container is running and healthy.")
					return nil
				} else if containerJSON.State.Health.Status == "unhealthy" {
					return fmt.Errorf("container is unhealthy")
				}
			} else {
				fmt.Println("Container is running.")
				return nil
			}
		}

		fmt.Println("Waiting for container to be ready...")
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

	log.Printf("User: %s", sess.User())
	cImage := sess.User()

	networkingConfig := network.NetworkingConfig{}
	platformConfig := v1.Platform{
		OS:           "linux",
		Architecture: "amd64",
		// Variant:      "minimal",
	}
	if imageExistsLocally(cImage) != true {
		sess.Write([]byte("Fetching Image from repository .."))
		reader, pullerr := docker.ImagePull(ctx, cImage, image.PullOptions{})
		if pullerr != nil {
			sess.Write([]byte("Unable to pull requested image" + string(pullerr.Error()) + "\n"))
			log.Printf("Error pulling image: %v", pullerr)
			cleanup = func() {}
			return
		}
		defer reader.Close()
		if _, err := io.Copy(os.Stdout, reader); err != nil {
			log.Printf("Error reading pull output: %v", pullerr)
		}
	}

	resp, err := docker.ContainerCreate(ctx, cfg, hostcfg, &networkingConfig, &platformConfig, "")
	if err != nil {
		log.Printf("Unable to create container: %v", err)
		return
	}
	log.Printf("Created container: %s", resp.ID)
	cleanup = func() {
		docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})
	}
	startErr := docker.ContainerStart(ctx, resp.ID, container.StartOptions{})
	if startErr != nil {
		log.Printf("Unable to start container: %v", err)
		sess.Write([]byte("Unable to pull requested image" + string(startErr.Error()) + "\n"))
		return
	}
	log.Printf("Wait for container %s to be ready", resp.ID)
	err = waitForContainerReady(ctx, docker, resp.ID, 30*time.Second)
	if err != nil {
		log.Fatal("Container failed to become ready:", err)
	}
	execResp, err := docker.ContainerExecCreate(ctx, resp.ID, container.ExecOptions{
		Cmd:          []string{"/bin/bash"},
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return
	}
	log.Printf("Attaching container: %s", resp.ID)
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
					log.Println(err)
					break
				}
			}
		}()
	}
	select {
	case <-outputErr:
		cleanup = func() {
			log.Printf("Killing container: %s", resp.ID)
			docker.ContainerKill(ctx, resp.ID, "9")
			log.Printf("Removing container: %s", resp.ID)
			docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})
		}
		fmt.Println("Exit..")
		return
	}
}
