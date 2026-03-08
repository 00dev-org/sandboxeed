package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func startDind(rt ContainerRuntime, resources *runResources) error {
	if err := rt.CreateVolume(resources.dindVolume, managedLabels(resources.projectName, "volume")); err != nil {
		return fmt.Errorf("failed to create dind volume: %w", err)
	}

	sp := startSpinner("Starting docker-in-docker")
	if err := rt.RunDetached(RunOpts{
		Name:       resources.dindContainer,
		Privileged: true,
		Networks: []NetworkAttachment{
			{Name: resources.internalNetwork, Alias: "dind"},
		},
		Volumes: []string{resources.dindVolume + ":/var/lib/docker"},
		Env: []string{
			"HTTP_PROXY=http://proxy:3128",
			"HTTPS_PROXY=http://proxy:3128",
			"NO_PROXY=localhost,127.0.0.1,proxy",
			"DOCKER_TLS_CERTDIR=",
		},
		Image:  "docker:dind",
		Cmd:    []string{"--insecure-registry=proxy:3128"},
		Labels: managedLabels(resources.projectName, "dind"),
	}); err != nil {
		sp.Stop()
		return fmt.Errorf("dind container failed to start: %w", err)
	}

	err := waitForDind(rt, resources.dindContainer)
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
	if resources.proxyConfigVol != "" {
		_ = rt.RemoveVolume(resources.proxyConfigVol)
	}
}

func waitForDind(rt ContainerRuntime, containerName string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err := rt.ContainerStatus(containerName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		switch status {
		case "exited", "dead":
			stderrf("dind container failed to start, logs:\n")
			_ = rt.Logs(containerName)
			return fmt.Errorf("dind container exited unexpectedly")
		case "running":
			if rt.Exec(containerName, "docker", "version") == nil {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	stderrf("dind container failed to start, logs:\n")
	_ = rt.Logs(containerName)
	return fmt.Errorf("docker daemon did not become ready")
}
