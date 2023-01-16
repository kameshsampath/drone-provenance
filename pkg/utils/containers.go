package utils

import (
	"context"
	"io"
	"os"
	"runtime"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

const (
	busyboxImage = "docker.io/library/busybox"
)

// TriggerUIRefresh starts a container to notify the extension UI to reload the progress actions from the cache.
// The container uses the label "io.drone.desktop.ui.refresh=true" for that purpose and is auto-removed when exited.
// The extension UI is listening for container events with that label. Once an event is received, the extension UI sends a ui refresh action to refresh and reload the pipelines from backend
func TriggerUIRefresh(ctx context.Context, cli *client.Client, labels map[string]string) error {
	// Ensure the image is present before creating the container
	if _, _, err := cli.ImageInspectWithRaw(ctx, busyboxImage); err != nil {
		reader, err := cli.ImagePull(ctx, busyboxImage, types.ImagePullOptions{
			Platform: "linux/" + runtime.GOARCH,
		})
		if err != nil {
			return err
		}
		_, err = io.Copy(os.Stdout, reader)
		if err != nil {
			return err
		}
	}

	cLabels := map[string]string{
		"com.docker.desktop.extension":      "true",
		"com.docker.desktop.extension.name": "Drone CI",
		"com.docker.compose.project":        "drone_drone-ci-docker-extension-desktop-extension",
	}

	//append to default labels
	for k, v := range labels {
		cLabels[k] = v
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:        busyboxImage,
		AttachStdout: true,
		AttachStderr: true,
		Labels:       cLabels,
	}, &container.HostConfig{
		AutoRemove: true,
	}, nil, "")
	if err != nil {
		return err
	}

	err = cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		return err
	}

	return nil
}
