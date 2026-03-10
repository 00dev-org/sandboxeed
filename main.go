package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"

	"gopkg.in/yaml.v3"
)

var version string

func currentVersion() string {
	if version != "" {
		return version
	}

	buildInfo, ok := debug.ReadBuildInfo()
	if ok && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
		return buildInfo.Main.Version
	}
	return "devel"
}

func stderrf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
}

func stdoutf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stdout, format, args...)
}

func removePath(path string) {
	_ = os.RemoveAll(path)
}

func printHelp() {
	fmt.Printf(`sandboxeed

Usage:
  sandboxeed [--build] [--no-docker] [--read-only] [command] [args...]
  sandboxeed version
  sandboxeed help
  sandboxeed inspect
  sandboxeed cleanup

Commands:
  help      Show this help text.
  version   Print the app version.
  inspect   Print the effective sandbox configuration.
  cleanup   List and remove sandboxeed containers, networks, and volumes (with confirmation).

Flags:
  --build       Build the sandbox image; if a command is provided, run it afterward.
  --no-docker   Skip Docker-in-Docker even if docker: true is set in the config.
  --read-only   Mount all volumes as read-only inside the sandbox.
`)
}

func main() {
	os.Exit(run())
}

func run() int {
	build := false
	noDocker := false
	readOnly := false
	command := ""
	var args []string

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--build":
			build = true
		case "--no-docker":
			noDocker = true
		case "--read-only":
			readOnly = true
		default:
			if command == "" {
				command = os.Args[i]
			} else {
				args = append(args, os.Args[i])
			}
		}
	}

	if command == "version" {
		fmt.Println(currentVersion())
		return 0
	}
	if command == "help" || command == "--help" || command == "-h" {
		printHelp()
		return 0
	}
	if command == "cleanup" {
		return runCleanup()
	}
	if command == "inspect" {
		return runInspect(noDocker, readOnly)
	}

	cfg, err := loadConfig()
	if err != nil {
		stderrf("failed to load config: %v\n", err)
		return 1
	}
	if noDocker {
		cfg.Sandbox.Docker = false
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir, err := os.Getwd()
	if err != nil {
		stderrf("failed to determine working directory: %v\n", err)
		return 1
	}

	resources := newRunResources(dir)
	rt := &DockerCLI{
		ctx:    ctx,
		binary: containerBinary(),
		engine: containerEngineType(),
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			cleanupResources(rt, resources)
		})
	}
	defer cleanup()

	go func() {
		<-ctx.Done()
		cleanup()
	}()

	if build {
		dockerfile := cfg.Sandbox.Build.Dockerfile
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		dockerfile, err = resolveDockerfilePath(dir, dockerfile)
		if err != nil {
			stderrf("failed to resolve dockerfile path: %v\n", err)
			return 1
		}
		if err := rt.Build(dockerfile, sandboxImageName(dir, cfg), "."); err != nil {
			if ctx.Err() != nil {
				return 0
			}
			stderrf("build failed: %v\n", err)
			return 1
		}
		if command == "" {
			return 0
		}
	} else if shouldAutoBuild(cfg) {
		image := sandboxImageName(dir, cfg)
		exists, err := rt.ImageExists(image)
		if err != nil {
			stderrf("failed to inspect image %q: %v\n", image, err)
			return 1
		}
		if !exists {
			dockerfile, err := resolveDockerfilePath(dir, cfg.Sandbox.Build.Dockerfile)
			if err != nil {
				stderrf("failed to resolve dockerfile path: %v\n", err)
				return 1
			}
			if err := rt.Build(dockerfile, image, "."); err != nil {
				if ctx.Err() != nil {
					return 0
				}
				stderrf("auto-build failed: %v\n", err)
				return 1
			}
		}
	}

	if command == "" {
		command = "bash"
	}

	confPath, err := writeSquidConf(cfg.Sandbox.Domains)
	if err != nil {
		stderrf("failed to write squid.conf: %v\n", err)
		return 1
	}
	defer removePath(filepath.Dir(confPath))

	var sshConfigPath, sshKnownHostsPath string
	if needsGitHubSSH(cfg.Sandbox.Domains) {
		sp := startSpinner("Fetching GitHub host keys")
		sshConfigPath, sshKnownHostsPath, err = writeSSHFiles()
		sp.Stop()
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			stderrf("failed to prepare SSH config: %v\n", err)
			return 1
		}
		defer removePath(filepath.Dir(sshConfigPath))
	}

	if err := startProxy(rt, resources, confPath); err != nil {
		if ctx.Err() != nil {
			return 0
		}
		stderrf("failed to start proxy: %v\n", err)
		return 1
	}

	if cfg.Sandbox.Docker {
		if err := startDind(rt, resources); err != nil {
			if ctx.Err() != nil {
				return 0
			}
			stderrf("failed to start docker-in-docker: %v\n", err)
			return 1
		}
	}
	sandboxErr := runSandbox(rt, resources, cfg, build, readOnly, sshConfigPath, sshKnownHostsPath, command, args)
	if ctx.Err() != nil {
		return 0
	}
	if sandboxErr != nil {
		if exitErr, ok := sandboxErr.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code == 130 || code == 131 {
				return 0
			}
		}
		stderrf("sandbox command failed: %v\n", sandboxErr)
		return 1
	}
	return 0
}

func runInspect(noDocker, readOnly bool) int {
	cfg, err := loadConfig()
	if err != nil {
		stderrf("failed to load config: %v\n", err)
		return 1
	}
	if noDocker {
		cfg.Sandbox.Docker = false
	}

	dir, err := os.Getwd()
	if err != nil {
		stderrf("failed to determine working directory: %v\n", err)
		return 1
	}

	resolved := resolveSandboxConfig(newRunResources(dir), cfg, false, readOnly, "", "")
	data, err := yaml.Marshal(struct {
		Sandbox ResolvedSandboxConfig `yaml:"sandbox"`
	}{Sandbox: resolved})
	if err != nil {
		stderrf("failed to render config: %v\n", err)
		return 1
	}
	stdoutf("%s", data)
	return 0
}

func resolveDockerfilePath(projectDir, dockerfile string) (string, error) {
	switch {
	case dockerfile == "":
		return dockerfile, nil
	case strings.HasPrefix(dockerfile, "~/"):
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand %q without a home directory: %w", dockerfile, err)
		}
		return filepath.Join(homeDir, dockerfile[2:]), nil
	case filepath.IsAbs(dockerfile):
		return dockerfile, nil
	default:
		return filepath.Join(projectDir, dockerfile), nil
	}
}
