package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const configFile = "sandboxeed.yaml"
const managedLabelKey = "sandboxeed.managed"
const managedLabelValue = "true"
const resourceLabelKey = "sandboxeed.resource"
const projectLabelKey = "sandboxeed.project"

var version string

const squidConfTemplate = `http_port 3128

%s
acl SSL_ports port 22
acl SSL_ports port 443
acl Safe_ports port 22
acl Safe_ports port 80
acl Safe_ports port 443
acl CONNECT method CONNECT

http_access deny !Safe_ports
http_access deny CONNECT !SSL_ports
%shttp_access deny all
`

type SandboxConfig struct {
	Build struct {
		Dockerfile string `yaml:"dockerfile"`
	} `yaml:"build"`
	Image       string   `yaml:"image"`
	Volumes     []string `yaml:"volumes"`
	Environment []string `yaml:"environment"`
	WorkingDir  string   `yaml:"working_dir"`
	Domains     []string `yaml:"domains"`
	Docker      bool     `yaml:"docker"`
}

type Config struct {
	Sandbox SandboxConfig `yaml:"sandbox"`
}

func defaultConfig() *Config {
	return &Config{}
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configFile)
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", configFile, err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", configFile, err)
	}
	return &cfg, nil
}

var domainPattern = regexp.MustCompile(`^\*?\.?[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*$`)

func validateDomains(domains []string) ([]string, error) {
	validated := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			return nil, fmt.Errorf("domain entries must not be empty")
		}
		if !domainPattern.MatchString(domain) {
			return nil, fmt.Errorf("invalid domain %q", domain)
		}

		host := domain
		if strings.HasPrefix(host, "*.") {
			host = host[2:]
		} else if strings.HasPrefix(host, ".") {
			host = host[1:]
		}
		if host == "" || strings.HasPrefix(host, "-") || strings.HasSuffix(host, "-") {
			return nil, fmt.Errorf("invalid domain %q", domain)
		}
		if net.ParseIP(host) != nil {
			return nil, fmt.Errorf("ip addresses are not supported in domains: %q", domain)
		}

		validated = append(validated, domain)
	}
	return validated, nil
}

func generateSquidConf(domains []string) (string, error) {
	validated, err := validateDomains(domains)
	if err != nil {
		return "", err
	}

	var aclLines strings.Builder
	for _, d := range validated {
		aclLines.WriteString("acl allowed dstdomain ")
		aclLines.WriteString(d)
		aclLines.WriteString("\n")
	}
	allowLine := ""
	if len(validated) > 0 {
		allowLine = "http_access allow allowed\n"
	}
	return fmt.Sprintf(squidConfTemplate, aclLines.String(), allowLine), nil
}

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
  sandboxeed [--build] [command] [args...]
  sandboxeed version
  sandboxeed help
  sandboxeed cleanup

Commands:
  help      Show this help text.
  version   Print the app version.
  cleanup   Force-remove sandboxeed containers, networks, and volumes after confirmation.

Flags:
  --build   Build the sandbox image; if a command is provided, run it afterward.
`)
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

func main() {
	os.Exit(run())
}

type runResources struct {
	projectDir       string
	projectName      string
	sandboxContainer string
	proxyContainer   string
	proxyConfigVol   string
	dindContainer    string
	dindVolume       string
	internalNetwork  string
	egressNetwork    string
}

func run() int {
	build := false
	command := ""
	var args []string

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--build":
			build = true
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

	cfg, err := loadConfig()
	if err != nil {
		stderrf("failed to load config: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir, err := os.Getwd()
	if err != nil {
		stderrf("failed to determine working directory: %v\n", err)
		return 1
	}

	resources := newRunResources(dir)
	rt := &DockerCLI{ctx: ctx}

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
			if err := rt.Build(cfg.Sandbox.Build.Dockerfile, image, "."); err != nil {
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

	sandboxErr := runSandbox(rt, resources, cfg, build, command, args)
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

func runSandbox(rt ContainerRuntime, resources *runResources, cfg *Config, built bool, command string, extraArgs []string) error {
	volumes := make([]string, 0, len(defaultVolumes)+len(cfg.Sandbox.Volumes))
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

	// Expand volume paths
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

func defaultSandboxImage(projectDir string) string {
	project := strings.ToLower(filepath.Base(projectDir))
	project = strings.TrimSpace(project)
	if project == "" || project == "." || project == string(filepath.Separator) {
		project = "workspace"
	}

	var b strings.Builder
	lastDash := false
	for _, r := range project {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}

	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "workspace"
	}
	return name + "-sandboxeed"
}

func expandVolumeSpec(projectDir, spec string) string {
	host, rest, ok := strings.Cut(spec, ":")
	if !ok {
		return spec
	}

	switch {
	case host == ".":
		host = projectDir
	case strings.HasPrefix(host, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return spec
		}
		host = filepath.Join(home, host[2:])
	case strings.HasPrefix(host, "./"), strings.HasPrefix(host, "../"):
		host = filepath.Join(projectDir, host)
	case filepath.IsAbs(host):
		// Already a bind mount path.
	case strings.Contains(host, "/"):
		host = filepath.Join(projectDir, host)
	default:
		return spec
	}

	return filepath.Clean(host) + ":" + rest
}

func networkProjectName(dir string) string {
	project := filepath.Base(dir)
	sum := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("%s-%x", project, sum[:4])
}

func newRunResources(dir string) *runResources {
	project := networkProjectName(dir)
	suffix := fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	return &runResources{
		projectDir:       dir,
		projectName:      project,
		sandboxContainer: fmt.Sprintf("%s-sandbox-%s", project, suffix),
		proxyContainer:   fmt.Sprintf("%s-proxy-%s", project, suffix),
		proxyConfigVol:   fmt.Sprintf("%s-proxy-config-%s", project, suffix),
		dindContainer:    fmt.Sprintf("%s-dind-%s", project, suffix),
		dindVolume:       fmt.Sprintf("%s-dind-data-%s", project, suffix),
		internalNetwork:  fmt.Sprintf("%s-internal-%s", project, suffix),
		egressNetwork:    fmt.Sprintf("%s-egress-%s", project, suffix),
	}
}

func managedLabels(projectName, resourceType string) map[string]string {
	return map[string]string{
		managedLabelKey:  managedLabelValue,
		projectLabelKey:  projectName,
		resourceLabelKey: resourceType,
	}
}

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
	cmd := exec.Command("docker", args...)
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
	cmd := exec.Command("docker", args...)
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
		args = []string{"rm", "-f", name}
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
	cmd := exec.Command("docker", kind, "inspect", name)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func writeSquidConf(domains []string) (string, error) {
	conf, err := generateSquidConf(domains)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "sandboxeed-squid-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "squid.conf")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if _, err := file.WriteString(conf); err != nil {
		_ = file.Close()
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

func startProxy(rt ContainerRuntime, resources *runResources, confPath string) error {
	if err := ensureNetwork(rt, resources.internalNetwork, resources.projectName, "internal", true); err != nil {
		return err
	}
	if err := ensureNetwork(rt, resources.egressNetwork, resources.projectName, "egress", false); err != nil {
		return err
	}
	if err := rt.CreateVolume(resources.proxyConfigVol, managedLabels(resources.projectName, "proxy-config")); err != nil {
		return fmt.Errorf("failed to create proxy config volume: %w", err)
	}
	if err := rt.CopyFileToVolume(resources.proxyConfigVol, confPath, "squid.conf"); err != nil {
		return fmt.Errorf("failed to populate proxy config volume: %w", err)
	}

	sp := startSpinner("Starting proxy")
	if err := rt.RunDetached(RunOpts{
		Name: resources.proxyContainer,
		Networks: []NetworkAttachment{
			{Name: resources.internalNetwork, Alias: "proxy"},
			{Name: resources.egressNetwork},
		},
		Volumes: []string{resources.proxyConfigVol + ":/config:ro"},
		Image:   "ubuntu/squid:latest",
		Cmd:     []string{"-f", "/config/squid.conf", "-NYC"},
		Labels:  managedLabels(resources.projectName, "proxy"),
	}); err != nil {
		sp.Stop()
		return fmt.Errorf("docker run failed: %w", err)
	}

	err := waitForProxy(rt, resources.proxyContainer)
	sp.Stop()
	return err
}

func ensureNetwork(rt ContainerRuntime, name, project, label string, internal bool) error {
	labels := map[string]string{
		"com.docker.compose.network": label,
		"com.docker.compose.project": project,
		managedLabelKey:              managedLabelValue,
		projectLabelKey:              project,
		resourceLabelKey:             label,
	}
	if err := rt.CreateNetwork(name, internal, labels); err == nil {
		return verifyNetworkInternal(rt, name, internal)
	}

	if err := rt.RemoveNetwork(name); err != nil {
		return fmt.Errorf("failed to remove existing network %q: %w", name, err)
	}
	if err := rt.CreateNetwork(name, internal, labels); err != nil {
		return fmt.Errorf("failed to create network %q: %w", name, err)
	}
	return verifyNetworkInternal(rt, name, internal)
}

func verifyNetworkInternal(rt ContainerRuntime, name string, expected bool) error {
	actual, err := rt.NetworkInternal(name)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("network %q internal=%v, expected %v", name, actual, expected)
	}
	return nil
}

func waitForProxy(rt ContainerRuntime, containerName string) error {
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
		case "running":
			if rt.Exec(containerName, "squid", "-k", "check") == nil {
				return nil
			}
		case "exited", "dead":
			stderrf("proxy container failed to start, logs:\n")
			_ = rt.Logs(containerName)
			return fmt.Errorf("proxy container exited unexpectedly")
		}
		time.Sleep(500 * time.Millisecond)
	}
	stderrf("proxy container failed to start, logs:\n")
	_ = rt.Logs(containerName)
	return fmt.Errorf("proxy container did not reach running state")
}

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
