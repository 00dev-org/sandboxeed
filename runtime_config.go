package main

import "strings"

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

func runSandbox(rt ContainerRuntime, resources *runResources, cfg *Config, built bool, sshConfigPath, sshKnownHostsPath, command string, extraArgs []string) error {
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
	environment := make([]string, 0, len(envDefaults)+len(cfg.Sandbox.Environment)+1)
	environment = append(environment, envDefaults...)
	environment = append(environment, cfg.Sandbox.Environment...)
	if cfg.Sandbox.Docker {
		environment = append(environment, "DOCKER_HOST=tcp://dind:2375")
	}

	workingDir := cfg.Sandbox.WorkingDir
	if workingDir == "" {
		workingDir = defaultWorkingDir
	}

	for i, v := range volumes {
		volumes[i] = expandVolumeSpec(resources.projectDir, v)
	}

	image := cfg.Sandbox.Image
	if image == "" {
		if built {
			image = defaultSandboxImage(resources.projectDir)
		} else {
			image = "bash:latest"
		}
	}

	return rt.RunInteractive(RunOpts{
		Name:     resources.sandboxContainer,
		Networks: []NetworkAttachment{{Name: resources.internalNetwork}},
		Volumes:  volumes,
		Env:      environment,
		Labels:   managedLabels(resources.projectName, "sandbox"),
		WorkDir:  workingDir,
		Image:    image,
		Cmd:      append([]string{command}, extraArgs...),
	})
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
