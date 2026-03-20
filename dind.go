package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func startDind(ctx context.Context, rt ContainerRuntime, resources *runResources, unsafe bool) error {
	if err := rt.CreateVolume(resources.dindVolume, managedLabels(resources.projectName, "volume")); err != nil {
		return fmt.Errorf("failed to create dind volume: %w", err)
	}
	if err := rt.CreateVolume(resources.dindSocketVolume, managedLabels(resources.projectName, "volume")); err != nil {
		return fmt.Errorf("failed to create dind socket volume: %w", err)
	}

	opts := RunOpts{
		Name: resources.dindContainer,
		Networks: []NetworkAttachment{
			{Name: resources.internalNetwork, Alias: "dind"},
		},
		Volumes: []string{
			resources.dindVolume + ":/var/lib/containers",
			resources.dindSocketVolume + ":/var/sock",
		},
		Env: []string{
			"HTTP_PROXY=http://proxy:3128",
			"HTTPS_PROXY=http://proxy:3128",
			"NO_PROXY=localhost,127.0.0.1,proxy",
		},
		Image:  "quay.io/podman/stable",
		Cmd:    []string{"sh", "-c", "chmod 755 /var/sock && podman system service --time=0 unix:///var/sock/podman.sock & while [ ! -S /var/sock/podman.sock ]; do sleep 0.2; done && chmod 666 /var/sock/podman.sock && wait"},
		Labels: managedLabels(resources.projectName, "dind"),
	}

	if unsafe {
		opts.Privileged = true
	} else {
		opts.CapAdd = []string{"SYS_ADMIN"}
		opts.Devices = []string{"/dev/fuse"}
		opts.SecurityOpt = []string{"seccomp=unconfined", "apparmor=unconfined"}
	}

	sp := startSpinner("Starting docker-in-docker")
	if err := rt.RunDetached(opts); err != nil {
		sp.Stop()
		return fmt.Errorf("dind container failed to start: %w", err)
	}

	err := waitForDind(ctx, rt, resources.dindContainer)
	sp.Stop()
	return err
}

func cleanupResources(rt ContainerRuntime, resources *runResources) {
	for _, container := range []string{
		resources.sandboxContainer,
		resources.dindContainer,
		resources.proxyContainer,
	} {
		if container != "" {
			_ = rt.RemoveContainer(container)
		}
	}
	for _, network := range []string{
		resources.internalNetwork,
		resources.egressNetwork,
	} {
		if network != "" {
			_ = rt.RemoveNetwork(network)
		}
	}
	if resources.dindVolume != "" {
		_ = rt.RemoveVolume(resources.dindVolume)
	}
	if resources.dindSocketVolume != "" {
		_ = rt.RemoveVolume(resources.dindSocketVolume)
	}
	if resources.proxyConfigVol != "" {
		_ = rt.RemoveVolume(resources.proxyConfigVol)
	}
}

func waitForDind(ctx context.Context, rt ContainerRuntime, containerName string) error {
	timeout := 30 * time.Second
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				stderrf("dind container failed to start, logs:\n")
				_ = rt.Logs(containerName)
				return fmt.Errorf("dind container did not reach running state after %v", timeout)
			}

			status, err := rt.ContainerStatus(containerName)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					return err
				}
				continue
			}
			switch status {
			case "exited", "dead":
				stderrf("dind container failed to start, logs:\n")
				_ = rt.Logs(containerName)
				return fmt.Errorf("dind container exited unexpectedly")
			case "running":
				if rt.Exec(containerName, "sh", "-c", "test -S /var/sock/podman.sock && test -w /var/sock/podman.sock") == nil {
					return nil
				}
			}
		}
	}
}
