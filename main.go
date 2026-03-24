package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
  sandboxeed [--build] [--no-docker] [--read-only] [--offline] [--unsafe] [command] [args...]
  sandboxeed --help
  sandboxeed --version
  sandboxeed --inspect
  sandboxeed --config
  sandboxeed --cleanup
  sandboxeed --self-update

Flags:
  --build       Build the sandbox image; if a command is provided, run it afterward.
  --no-docker   Skip Docker-in-Docker even if docker: true is set in the config.
  --read-only   Mount all volumes as read-only inside the sandbox.
  --offline     Force no outbound network for this run and skip proxy startup.
  --unsafe      Run DinD in privileged mode (insecure, for testing only).
  --help        Show this help text.
  --version     Print the app version.
  --inspect     Print the effective sandbox configuration.
  --config      Open ~/.sandboxeed/sandboxeed.yaml in the system editor.
  --cleanup     List and remove sandboxeed containers, networks, and volumes (with confirmation).
  --self-update   Download and replace this binary with the latest release.
`)
}

type cliMode string

const (
	cliModeSandbox    cliMode = "sandbox"
	cliModeHelp       cliMode = "help"
	cliModeVersion    cliMode = "version"
	cliModeInspect    cliMode = "inspect"
	cliModeConfig     cliMode = "config"
	cliModeCleanup    cliMode = "cleanup"
	cliModeSelfUpdate cliMode = "self-update"
)

type cliOptions struct {
	mode      cliMode
	build     bool
	noDocker  bool
	readOnly  bool
	offline   bool
	unsafe    bool
	command   string
	args      []string
	extraArgs []string
}

func parseCLIArgs(argv []string) (cliOptions, error) {
	opts := cliOptions{mode: cliModeSandbox}
	fs := flag.NewFlagSet("sandboxeed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.build, "build", false, "")
	fs.BoolVar(&opts.noDocker, "no-docker", false, "")
	fs.BoolVar(&opts.readOnly, "read-only", false, "")
	fs.BoolVar(&opts.offline, "offline", false, "")
	fs.BoolVar(&opts.unsafe, "unsafe", false, "")

	help := fs.Bool("help", false, "")
	fs.BoolVar(help, "h", false, "")
	version := fs.Bool("version", false, "")
	inspect := fs.Bool("inspect", false, "")
	config := fs.Bool("config", false, "")
	cleanup := fs.Bool("cleanup", false, "")
	selfUpdate := fs.Bool("self-update", false, "")

	if err := fs.Parse(argv); err != nil {
		return cliOptions{}, err
	}

	switch {
	case *help:
		opts.mode = cliModeHelp
	case *version:
		opts.mode = cliModeVersion
	case *inspect:
		opts.mode = cliModeInspect
	case *config:
		opts.mode = cliModeConfig
	case *cleanup:
		opts.mode = cliModeCleanup
	case *selfUpdate:
		opts.mode = cliModeSelfUpdate
	}

	rest := fs.Args()
	if opts.mode != cliModeSandbox {
		opts.extraArgs = append(opts.extraArgs, rest...)
		return opts, nil
	}
	if len(rest) == 0 {
		return opts, nil
	}

	opts.command = rest[0]
	opts.args = append(opts.args, rest[1:]...)
	return opts, nil
}

func main() {
	os.Exit(run())
}

func run() int {
	opts, err := parseCLIArgs(os.Args[1:])
	if err != nil {
		stderrf("%v\n", err)
		return 1
	}

	if len(opts.extraArgs) > 0 {
		stderrf("%s does not accept arguments: %s\n", opts.mode, strings.Join(opts.extraArgs, " "))
		return 1
	}

	if opts.mode == cliModeVersion {
		fmt.Println(currentVersion())
		return 0
	}
	if opts.mode == cliModeHelp {
		printHelp()
		return 0
	}
	if opts.mode == cliModeCleanup {
		return runCleanup()
	}
	if opts.mode == cliModeConfig {
		return runConfig()
	}
	if opts.mode == cliModeInspect {
		return runInspect(opts.noDocker, opts.readOnly, opts.offline)
	}
	if opts.mode == cliModeSelfUpdate {
		return runSelfUpdate()
	}

	maybeNotifyUpdate()

	cfg, err := loadConfig()
	if err != nil {
		stderrf("failed to load config: %v\n", err)
		return 1
	}
	applyCLIOverrides(cfg, opts.noDocker, opts.offline)

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

	if opts.build {
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
		if opts.command == "" {
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

	if opts.command == "" {
		opts.command = "bash"
	}

	var sshConfigPath, sshKnownHostsPath string
	if opts.offline {
		if err := ensureNetwork(rt, resources.internalNetwork, resources.projectName, "internal", true); err != nil {
			if ctx.Err() != nil {
				return 0
			}
			stderrf("failed to create internal network: %v\n", err)
			return 1
		}
	} else {
		confPath, err := writeSquidConf(cfg.Sandbox.Domains)
		if err != nil {
			stderrf("failed to write squid.conf: %v\n", err)
			return 1
		}
		defer removePath(filepath.Dir(confPath))

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

		if err := startProxy(ctx, rt, resources, confPath); err != nil {
			if ctx.Err() != nil {
				return 0
			}
			stderrf("failed to start proxy: %v\n", err)
			return 1
		}

		if cfg.Sandbox.Docker {
			if err := startDind(ctx, rt, resources, opts.unsafe); err != nil {
				if ctx.Err() != nil {
					return 0
				}
				stderrf("failed to start docker-in-docker: %v\n", err)
				return 1
			}
		}
	}
	sandboxErr := runSandbox(rt, resources, cfg, opts.build, opts.readOnly, opts.offline, sshConfigPath, sshKnownHostsPath, opts.command, opts.args)
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

func runInspect(noDocker, readOnly, offline bool) int {
	cfg, err := loadConfig()
	if err != nil {
		stderrf("failed to load config: %v\n", err)
		return 1
	}
	applyCLIOverrides(cfg, noDocker, offline)

	dir, err := os.Getwd()
	if err != nil {
		stderrf("failed to determine working directory: %v\n", err)
		return 1
	}

	resolved := resolveSandboxConfig(newRunResources(dir), cfg, false, readOnly, offline, "", "")
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

func runConfig() int {
	path, err := userConfigPath()
	if err != nil {
		stderrf("failed to resolve home directory: %v\n", err)
		return 1
	}

	if err := ensureUserConfigFile(path); err != nil {
		stderrf("failed to prepare %s: %v\n", path, err)
		return 1
	}

	cmd, err := configEditorCommand(path)
	if err != nil {
		stderrf("%v\n", err)
		return 1
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		stderrf("failed to open %s: %v\n", path, err)
		return 1
	}
	return 0
}

func ensureUserConfigFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func configEditorCommand(path string) (*exec.Cmd, error) {
	editorArgs, err := configEditorArgs(path)
	if err != nil {
		return nil, err
	}
	return exec.Command(editorArgs[0], editorArgs[1:]...), nil
}

func configEditorArgs(path string) ([]string, error) {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}

	args := strings.Fields(editor)
	if len(args) == 0 {
		return nil, fmt.Errorf("no editor configured")
	}
	args = append(args, path)
	return args, nil
}

func applyCLIOverrides(cfg *Config, noDocker, offline bool) {
	if noDocker || offline {
		cfg.Sandbox.Docker = false
	}
	if offline {
		cfg.Sandbox.Domains = nil
	}
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
