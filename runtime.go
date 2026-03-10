package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type containerEngine string

const (
	engineDocker containerEngine = "docker"
	enginePodman containerEngine = "podman"
)

// NetworkAttachment describes a network to connect a container to.
type NetworkAttachment struct {
	Name  string
	Alias string
}

// RunOpts holds options for running a container.
type RunOpts struct {
	Name       string
	Networks   []NetworkAttachment
	Volumes    []string
	Env        []string
	Labels     map[string]string
	WorkDir    string
	Image      string
	Cmd        []string
	Privileged bool
	User       string // passed as --user (e.g. "1000:1000")
	Memory     string
	CPUs       string
	PidsLimit  int
}

// ContainerRuntime abstracts container engine operations.
type ContainerRuntime interface {
	Build(dockerfile, tag, contextDir string) error
	ImageExists(tag string) (bool, error)
	RunDetached(opts RunOpts) error
	RunInteractive(opts RunOpts) error
	CopyFileToVolume(volumeName, srcPath, destName string) error
	RemoveContainer(name string) error
	ContainerStatus(name string) (string, error)
	Exec(container string, cmd ...string) error
	Logs(name string) error
	CreateNetwork(name string, internal bool, labels map[string]string) error
	RemoveNetwork(name string) error
	NetworkInternal(name string) (bool, error)
	CreateVolume(name string, labels map[string]string) error
	RemoveVolume(name string) error
}

// DockerCLI implements ContainerRuntime by shelling out to docker.
type DockerCLI struct {
	ctx    context.Context
	binary string
	engine containerEngine
}

func (d *DockerCLI) Build(dockerfile, tag, contextDir string) error {
	cmd := exec.CommandContext(d.ctx, d.command(), buildArgs(dockerfile, tag, contextDir)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (d *DockerCLI) ImageExists(tag string) (bool, error) {
	cmd := exec.CommandContext(d.ctx, d.command(), "image", "inspect", tag)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *DockerCLI) RunDetached(opts RunOpts) error {
	runOpts := opts
	runOpts.Networks = nil
	if len(opts.Networks) > 0 {
		runOpts.Networks = []NetworkAttachment{opts.Networks[0]}
	}

	args := append([]string{"run", "-d"}, runArgs(runOpts)...)
	if err := exec.CommandContext(d.ctx, d.command(), args...).Run(); err != nil {
		return err
	}

	for _, network := range opts.Networks[1:] {
		connectArgs := []string{"network", "connect"}
		if network.Alias != "" {
			connectArgs = append(connectArgs, "--alias", network.Alias)
		}
		connectArgs = append(connectArgs, network.Name, opts.Name)
		if err := exec.CommandContext(d.ctx, d.command(), connectArgs...).Run(); err != nil {
			_ = d.RemoveContainer(opts.Name)
			return err
		}
	}

	return nil
}

func (d *DockerCLI) RunInteractive(opts RunOpts) error {
	args := append([]string{"run", "--rm", "-it"}, runArgs(opts)...)
	cmd := exec.CommandContext(d.ctx, d.command(), args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (d *DockerCLI) CopyFileToVolume(volumeName, srcPath, destName string) error {
	helperName := fmt.Sprintf("sandboxeed-copy-%d-%d", os.Getpid(), time.Now().UnixNano())
	createArgs := []string{
		"create",
		"--name", helperName,
		"-v", volumeName + ":/config",
		"ubuntu/squid:latest",
		"sh", "-c", "sleep 300",
	}
	if err := exec.CommandContext(d.ctx, d.command(), createArgs...).Run(); err != nil {
		return err
	}
	defer func() {
		_ = exec.Command(d.command(), "rm", "-f", helperName).Run()
	}()

	destPath := "/config/" + destName
	if err := exec.CommandContext(d.ctx, d.command(), "cp", srcPath, helperName+":"+destPath).Run(); err != nil {
		return err
	}
	return nil
}

func (d *DockerCLI) RemoveContainer(name string) error {
	return exec.Command(d.command(), removeContainerArgs(d.engine, name)...).Run()
}

func (d *DockerCLI) ContainerStatus(name string) (string, error) {
	out, err := exec.CommandContext(d.ctx, d.command(), "inspect", "--format={{.State.Status}}", name).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (d *DockerCLI) Exec(container string, cmd ...string) error {
	args := append([]string{"exec", container}, cmd...)
	return exec.CommandContext(d.ctx, d.command(), args...).Run()
}

func (d *DockerCLI) Logs(name string) error {
	cmd := exec.CommandContext(d.ctx, d.command(), "logs", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (d *DockerCLI) CreateNetwork(name string, internal bool, labels map[string]string) error {
	args := []string{"network", "create"}
	for k, v := range labels {
		args = append(args, "--label", k+"="+v)
	}
	if internal {
		args = append(args, "--internal")
	}
	args = append(args, name)
	return exec.CommandContext(d.ctx, d.command(), args...).Run()
}

func (d *DockerCLI) RemoveNetwork(name string) error {
	return exec.Command(d.command(), "network", "rm", name).Run()
}

func (d *DockerCLI) NetworkInternal(name string) (bool, error) {
	out, err := exec.CommandContext(d.ctx, d.command(), "network", "inspect", "--format={{.Internal}}", name).Output()
	if err != nil {
		return false, fmt.Errorf("failed to inspect network %q: %w", name, err)
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

func (d *DockerCLI) CreateVolume(name string, labels map[string]string) error {
	args := []string{"volume", "create"}
	for k, v := range labels {
		args = append(args, "--label", k+"="+v)
	}
	args = append(args, name)
	return exec.CommandContext(d.ctx, d.command(), args...).Run()
}

func (d *DockerCLI) RemoveVolume(name string) error {
	return exec.Command(d.command(), "volume", "rm", "-f", name).Run()
}

func (d *DockerCLI) command() string {
	if d.binary != "" {
		return d.binary
	}
	return "docker"
}

func buildArgs(dockerfile, tag, contextDir string) []string {
	return []string{"build", "--no-cache", "-f", dockerfile, "-t", tag, contextDir}
}

func detectContainerEngine(binary string) containerEngine {
	cmd := exec.Command(binary, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return engineDocker
	}
	return classifyContainerEngine(string(out))
}

var containerBinary = sync.OnceValue(func() string {
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker"
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman"
	}
	return "docker"
})

var containerEngineType = sync.OnceValue(func() containerEngine {
	return detectContainerEngine(containerBinary())
})

func classifyContainerEngine(output string) containerEngine {
	if strings.Contains(strings.ToLower(strings.TrimSpace(output)), "podman") {
		return enginePodman
	}
	return engineDocker
}

func removeContainerArgs(engine containerEngine, name string) []string {
	args := []string{"rm", "-f"}
	if engine == enginePodman {
		args = append(args, "-t", "0")
	}
	args = append(args, name)
	return args
}

func runArgs(opts RunOpts) []string {
	var args []string
	if opts.Privileged {
		args = append(args, "--privileged")
	}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	for _, n := range opts.Networks {
		args = append(args, "--network", n.Name)
		if n.Alias != "" {
			args = append(args, "--network-alias", n.Alias)
		}
	}
	for _, v := range opts.Volumes {
		args = append(args, "-v", v)
	}
	for _, e := range opts.Env {
		args = append(args, "-e", e)
	}
	for k, v := range opts.Labels {
		args = append(args, "--label", k+"="+v)
	}
	if opts.User != "" {
		args = append(args, "--user", opts.User)
	}
	if opts.Memory != "" {
		args = append(args, "--memory", opts.Memory)
	}
	if opts.CPUs != "" {
		args = append(args, "--cpus", opts.CPUs)
	}
	if opts.PidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprintf("%d", opts.PidsLimit))
	}
	if opts.WorkDir != "" {
		args = append(args, "-w", opts.WorkDir)
	}
	args = append(args, opts.Image)
	args = append(args, opts.Cmd...)
	return args
}
