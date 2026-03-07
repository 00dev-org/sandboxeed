package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
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
	ctx context.Context
}

func (d *DockerCLI) Build(dockerfile, tag, contextDir string) error {
	cmd := exec.CommandContext(d.ctx, "docker", "build", "--no-cache", "-f", dockerfile, "-t", tag, contextDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (d *DockerCLI) ImageExists(tag string) (bool, error) {
	cmd := exec.CommandContext(d.ctx, "docker", "image", "inspect", tag)
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
	if err := exec.CommandContext(d.ctx, "docker", args...).Run(); err != nil {
		return err
	}

	for _, network := range opts.Networks[1:] {
		connectArgs := []string{"network", "connect"}
		if network.Alias != "" {
			connectArgs = append(connectArgs, "--alias", network.Alias)
		}
		connectArgs = append(connectArgs, network.Name, opts.Name)
		if err := exec.CommandContext(d.ctx, "docker", connectArgs...).Run(); err != nil {
			_ = d.RemoveContainer(opts.Name)
			return err
		}
	}

	return nil
}

func (d *DockerCLI) RunInteractive(opts RunOpts) error {
	args := append([]string{"run", "--rm", "-it"}, runArgs(opts)...)
	cmd := exec.CommandContext(d.ctx, "docker", args...)
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
	if err := exec.CommandContext(d.ctx, "docker", createArgs...).Run(); err != nil {
		return err
	}
	defer func() {
		_ = exec.Command("docker", "rm", "-f", helperName).Run()
	}()

	destPath := "/config/" + destName
	if err := exec.CommandContext(d.ctx, "docker", "cp", srcPath, helperName+":"+destPath).Run(); err != nil {
		return err
	}
	return nil
}

func (d *DockerCLI) RemoveContainer(name string) error {
	return exec.Command("docker", "rm", "-f", name).Run()
}

func (d *DockerCLI) ContainerStatus(name string) (string, error) {
	out, err := exec.CommandContext(d.ctx, "docker", "inspect", "--format={{.State.Status}}", name).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (d *DockerCLI) Exec(container string, cmd ...string) error {
	args := append([]string{"exec", container}, cmd...)
	return exec.CommandContext(d.ctx, "docker", args...).Run()
}

func (d *DockerCLI) Logs(name string) error {
	cmd := exec.CommandContext(d.ctx, "docker", "logs", name)
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
	return exec.CommandContext(d.ctx, "docker", args...).Run()
}

func (d *DockerCLI) RemoveNetwork(name string) error {
	return exec.Command("docker", "network", "rm", name).Run()
}

func (d *DockerCLI) NetworkInternal(name string) (bool, error) {
	out, err := exec.CommandContext(d.ctx, "docker", "network", "inspect", "--format={{.Internal}}", name).Output()
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
	return exec.CommandContext(d.ctx, "docker", args...).Run()
}

func (d *DockerCLI) RemoveVolume(name string) error {
	return exec.Command("docker", "volume", "rm", "-f", name).Run()
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
	if opts.WorkDir != "" {
		args = append(args, "-w", opts.WorkDir)
	}
	args = append(args, opts.Image)
	args = append(args, opts.Cmd...)
	return args
}
