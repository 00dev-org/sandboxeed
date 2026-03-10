package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
)

type cleanupTargets struct {
	containers []string
	networks   []string
	volumes    []string
}

func (c cleanupTargets) empty() bool {
	return len(c.containers) == 0 && len(c.networks) == 0 && len(c.volumes) == 0
}

func discoverCleanupTargets() (*cleanupTargets, error) {
	targets := &cleanupTargets{}
	containerSet := map[string]struct{}{}
	networkSet := map[string]struct{}{}
	volumeSet := map[string]struct{}{}

	containers, err := dockerLines("ps", "-a", "--filter", "label="+managedLabelKey+"="+managedLabelValue, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	for _, name := range containers {
		containerSet[name] = struct{}{}
	}

	networks, err := dockerLines("network", "ls", "--filter", "label="+managedLabelKey+"="+managedLabelValue, "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	for _, name := range networks {
		networkSet[name] = struct{}{}
	}

	volumes, err := dockerLines("volume", "ls", "--filter", "label="+managedLabelKey+"="+managedLabelValue, "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	for _, name := range volumes {
		volumeSet[name] = struct{}{}
	}

	targets.containers = sortedKeys(containerSet)
	targets.networks = sortedKeys(networkSet)
	targets.volumes = sortedKeys(volumeSet)
	return targets, nil
}

func runCleanup() int {
	targets, err := discoverCleanupTargets()
	if err != nil {
		stderrf("failed to discover cleanup targets: %v\n", err)
		return 1
	}
	if targets.empty() {
		fmt.Println("No sandboxeed resources found.")
		return 0
	}

	printCleanupTargets(targets)
	confirmed, err := confirmCleanup()
	if err != nil {
		stderrf("failed to read confirmation: %v\n", err)
		return 1
	}
	if !confirmed {
		fmt.Println("cleanup aborted")
		return 0
	}

	for _, name := range targets.containers {
		if err := removeDockerObject("container", name); err != nil {
			stderrf("failed to remove container %q: %v\n", name, err)
			return 1
		}
	}
	for _, name := range targets.networks {
		if err := removeDockerObject("network", name); err != nil {
			stderrf("failed to remove network %q: %v\n", name, err)
			return 1
		}
	}
	for _, name := range targets.volumes {
		if err := removeDockerObject("volume", name); err != nil {
			stderrf("failed to remove volume %q: %v\n", name, err)
			return 1
		}
	}
	return 0
}

func dockerLines(args ...string) ([]string, error) {
	cmd := exec.Command(containerBinary(), args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result, nil
}

func runDockerPassthrough(args ...string) ([]byte, error) {
	cmd := exec.Command(containerBinary(), args...)
	return cmd.CombinedOutput()
}

func printCleanupTargets(targets *cleanupTargets) {
	fmt.Println("The following sandboxeed resources will be removed:")
	for _, name := range targets.containers {
		fmt.Printf("  container  %s\n", name)
	}
	for _, name := range targets.networks {
		fmt.Printf("  network    %s\n", name)
	}
	for _, name := range targets.volumes {
		fmt.Printf("  volume     %s\n", name)
	}
}

func sortedKeys(set map[string]struct{}) []string {
	items := make([]string, 0, len(set))
	for item := range set {
		items = append(items, item)
	}
	slices.Sort(items)
	return items
}

func removeDockerObject(kind, name string) error {
	var args []string
	switch kind {
	case "container":
		args = removeContainerArgs(containerEngineType(), name)
	case "network":
		args = []string{"network", "rm", name}
	case "volume":
		args = []string{"volume", "rm", "-f", name}
	default:
		return fmt.Errorf("unsupported docker object kind %q", kind)
	}

	out, err := runDockerPassthrough(args...)
	if err != nil {
		exists, inspectErr := dockerObjectExists(kind, name)
		if inspectErr == nil && !exists {
			return nil
		}
		if len(out) > 0 {
			stdoutf("%s", out)
		}
		if inspectErr != nil {
			return fmt.Errorf("%w (and failed to verify existence: %v)", err, inspectErr)
		}
		return err
	}
	if len(out) > 0 {
		stdoutf("%s", out)
	}
	return nil
}

func dockerObjectExists(kind, name string) (bool, error) {
	cmd := exec.Command(containerBinary(), kind, "inspect", name)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func confirmCleanup() (bool, error) {
	fmt.Print("Continue with removing these sandboxeed containers, networks, and volumes? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}
