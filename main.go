package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
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
		status, cleanup, err := dockerRun(cfg, sess)
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

func dockerRun(cfg *container.Config, sess ssh.Session) (status int64, cleanup func(), err error) {
	docker, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}
	status = 255
	cleanup = func() {}
	ctx := context.Background()

	log.Printf("User: %s", sess.User())
	cImage := sess.User()

	hostConfig := container.HostConfig{}
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

	res, err := docker.ContainerCreate(ctx, cfg, &hostConfig, &networkingConfig, &platformConfig, "")
	if err != nil {
		return
	}
	cleanup = func() {
		docker.ContainerRemove(ctx, res.ID, container.RemoveOptions{})
	}
	opts := container.AttachOptions{
		Stdin:  cfg.AttachStdin,
		Stdout: cfg.AttachStdout,
		Stderr: cfg.AttachStderr,
		Stream: true,
	}
	stream, err := docker.ContainerAttach(ctx, res.ID, opts)
	if err != nil {
		return
	}
	cleanup = func() {
		docker.ContainerRemove(ctx, res.ID, container.RemoveOptions{})
		stream.Close()
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

	err = docker.ContainerStart(ctx, res.ID, container.StartOptions{})
	if err != nil {
		return
	}
	if cfg.Tty {
		_, winCh, _ := sess.Pty()
		go func() {
			for win := range winCh {
				err := docker.ContainerResize(ctx, res.ID, container.ResizeOptions{
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
	resultC, errC := docker.ContainerWait(ctx, res.ID, container.WaitConditionNotRunning)
	select {
	case err = <-errC:
		return
	case result := <-resultC:
		status = result.StatusCode
	}
	err = <-outputErr
	return
}
