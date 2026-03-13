package main

import (
	"fmt"
	"os"
	"strings"
)

var defaultVolumes = []string{".:/workspace"}
var defaultEnvironment = []string{
	"HTTP_PROXY=http://proxy:3128",
	"HTTPS_PROXY=http://proxy:3128",
	"NO_PROXY=localhost,127.0.0.1",
}
var defaultDockerEnvironment = []string{
	"HTTP_PROXY=http://proxy:3128",
	"HTTPS_PROXY=http://proxy:3128",
	"NO_PROXY=localhost,127.0.0.1,dind",
}

const defaultWorkingDir = "/workspace"

type ResolvedSandboxConfig struct {
	Image       string   `yaml:"image"`
	Volumes     []string `yaml:"volumes"`
	Environment []string `yaml:"environment"`
	WorkingDir  string   `yaml:"working_dir"`
	Docker      bool     `yaml:"docker"`
	Domains     []string `yaml:"domains,omitempty"`
	Memory      string   `yaml:"memory,omitempty"`
	CPUs        string   `yaml:"cpus,omitempty"`
	Pids        int      `yaml:"pids,omitempty"`
	User        string   `yaml:"user,omitempty"`
}

func resolveSandboxConfig(resources *runResources, cfg *Config, built, readOnly, offline bool, sshConfigPath, sshKnownHostsPath string) ResolvedSandboxConfig {
	volumes := make([]string, 0, 2+len(defaultVolumes)+len(cfg.Sandbox.Volumes))
	if sshConfigPath != "" {
		volumes = append(volumes, sshConfigPath+":/etc/ssh/ssh_config:ro")
		volumes = append(volumes, sshKnownHostsPath+":/etc/ssh/ssh_known_hosts:ro")
	}
	volumes = append(volumes, defaultVolumes...)
	volumes = append(volumes, cfg.Sandbox.Volumes...)

	envDefaults := defaultEnvironment
	if cfg.Sandbox.Docker {
		envDefaults = defaultDockerEnvironment
	}
	if offline {
		envDefaults = nil
	}
	environment := make([]string, 0, len(envDefaults)+len(cfg.Sandbox.Environment)+1)
	environment = append(environment, envDefaults...)
	environment = append(environment, cfg.Sandbox.Environment...)
	if cfg.Sandbox.Docker && !offline {
		environment = append(environment, "DOCKER_HOST=tcp://dind:2375")
	}

	workingDir := cfg.Sandbox.WorkingDir
	if workingDir == "" {
		workingDir = defaultWorkingDir
	}

	for i, v := range volumes {
		volumes[i] = expandVolumeSpec(resources.projectDir, v)
	}
	if readOnly {
		for i, v := range volumes {
			volumes[i] = forceReadOnly(v)
		}
	}

	memory := strings.TrimSpace(cfg.Sandbox.Memory)
	cpus := strings.TrimSpace(cfg.Sandbox.CPUs)

	var user string
	if cfg.Sandbox.Image == "" && !built {
		user = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	}

	return ResolvedSandboxConfig{
		Image:       resolvedSandboxImage(resources.projectDir, cfg, built),
		Volumes:     volumes,
		Environment: environment,
		WorkingDir:  workingDir,
		Docker:      cfg.Sandbox.Docker,
		Domains:     append([]string(nil), cfg.Sandbox.Domains...),
		Memory:      memory,
		CPUs:        cpus,
		Pids:        cfg.Sandbox.Pids,
		User:        user,
	}
}

func runSandbox(rt ContainerRuntime, resources *runResources, cfg *Config, built, readOnly, offline bool, sshConfigPath, sshKnownHostsPath, command string, extraArgs []string) error {
	resolved := resolveSandboxConfig(resources, cfg, built, readOnly, offline, sshConfigPath, sshKnownHostsPath)

	return rt.RunInteractive(RunOpts{
		Name:      resources.sandboxContainer,
		Networks:  []NetworkAttachment{{Name: resources.internalNetwork}},
		Volumes:   resolved.Volumes,
		Env:       resolved.Environment,
		Labels:    managedLabels(resources.projectName, "sandbox"),
		WorkDir:   resolved.WorkingDir,
		Image:     resolved.Image,
		Cmd:       append([]string{command}, extraArgs...),
		User:      resolved.User,
		Memory:    resolved.Memory,
		CPUs:      resolved.CPUs,
		PidsLimit: resolved.Pids,
	})
}

func resolvedSandboxImage(projectDir string, cfg *Config, built bool) string {
	if cfg.Sandbox.Image != "" {
		return cfg.Sandbox.Image
	}
	if built {
		return defaultSandboxImage(projectDir)
	}
	return "bash:latest"
}

func sandboxImageName(projectDir string, cfg *Config) string {
	if cfg.Sandbox.Image != "" {
		return cfg.Sandbox.Image
	}
	return defaultSandboxImage(projectDir)
}

func shouldAutoBuild(cfg *Config) bool {
	return strings.TrimSpace(cfg.Sandbox.Build.Dockerfile) != ""
}
