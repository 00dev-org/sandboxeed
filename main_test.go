package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoadConfigReturnsDefaultsWhenConfigMissing(t *testing.T) {
	withConfigDirs(t, "", func(projectDir, homeDir string) {
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		if cfg.Sandbox.Image != "" {
			t.Fatalf("loadConfig() default image = %q, want empty string", cfg.Sandbox.Image)
		}
		if len(cfg.Sandbox.Volumes) != 0 {
			t.Fatalf("loadConfig() volumes = %v, want none", cfg.Sandbox.Volumes)
		}
		if len(cfg.Sandbox.Environment) != 0 {
			t.Fatalf("loadConfig() environment = %v, want none", cfg.Sandbox.Environment)
		}
		if len(cfg.Sandbox.Domains) != 0 {
			t.Fatalf("loadConfig() domains = %v, want none", cfg.Sandbox.Domains)
		}
	})
}

func TestLoadConfigLoadsUserConfigWhenPresent(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  build:",
		"    dockerfile: ~/.sandboxeed/Dockerfile",
		"  image: sandboxeed-user:latest",
		"  volumes:",
		"    - ~/.gitconfig:/home/node/.gitconfig:ro",
		"  environment:",
		"    - FOO=user",
		"  domains:",
		"    - user.example.com",
		"  memory: 256m",
		"  cpus: \"1.5\"",
		"  pids: 128",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}

		if got, want := cfg.Sandbox.Volumes, []string{"~/.gitconfig:/home/node/.gitconfig:ro"}; !equalStrings(got, want) {
			t.Fatalf("loadConfig() volumes = %v, want %v", got, want)
		}
		if got, want := cfg.Sandbox.Environment, []string{"FOO=user"}; !equalStrings(got, want) {
			t.Fatalf("loadConfig() environment = %v, want %v", got, want)
		}
		if got, want := cfg.Sandbox.Domains, []string{"user.example.com"}; !equalStrings(got, want) {
			t.Fatalf("loadConfig() domains = %v, want %v", got, want)
		}
		if cfg.Sandbox.Build.Dockerfile != "~/.sandboxeed/Dockerfile" || cfg.Sandbox.Image != "sandboxeed-user:latest" {
			t.Fatalf("loadConfig() build/image = (%q, %q), want (%q, %q)", cfg.Sandbox.Build.Dockerfile, cfg.Sandbox.Image, "~/.sandboxeed/Dockerfile", "sandboxeed-user:latest")
		}
		if cfg.Sandbox.Memory != "256m" || cfg.Sandbox.CPUs != "1.5" || cfg.Sandbox.Pids != 128 {
			t.Fatalf("loadConfig() limits = (%q, %q, %d), want (%q, %q, %d)", cfg.Sandbox.Memory, cfg.Sandbox.CPUs, cfg.Sandbox.Pids, "256m", "1.5", 128)
		}
	})
}

func TestLoadConfigMergesUserAndProjectConfig(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  build:",
		"    dockerfile: ~/.sandboxeed/Dockerfile",
		"  image: sandboxeed-user:latest",
		"  volumes:",
		"    - ~/.gitconfig:/home/node/.gitconfig:ro",
		"    - ~/.npmrc:/home/node/.npmrc:ro",
		"  environment:",
		"    - FOO=user",
		"    - SHARED=user",
		"  domains:",
		"    - user.example.com",
		"    - shared.example.com",
		"  memory: 256m",
		"  cpus: \"1\"",
		"  pids: 64",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  image: alpine:3.22",
			"  working_dir: /app",
			"  volumes:",
			"    - ./project-gitconfig:/home/node/.gitconfig:ro",
			"    - ./cache:/cache",
			"  environment:",
			"    - SHARED=project",
			"    - BAR=project",
			"  domains:",
			"    - shared.example.com",
			"    - project.example.com",
			"  memory: 512m",
			"  cpus: \"2.5\"",
			"  pids: 256",
			"",
		}, "\n"))

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}

		if cfg.Sandbox.Image != "alpine:3.22" {
			t.Fatalf("loadConfig() image = %q, want %q", cfg.Sandbox.Image, "alpine:3.22")
		}
		if cfg.Sandbox.Build.Dockerfile != "" {
			t.Fatalf("loadConfig() dockerfile = %q, want empty when project uses image only", cfg.Sandbox.Build.Dockerfile)
		}
		if cfg.Sandbox.WorkingDir != "/app" {
			t.Fatalf("loadConfig() working dir = %q, want %q", cfg.Sandbox.WorkingDir, "/app")
		}
		if got, want := cfg.Sandbox.Volumes, []string{
			"./project-gitconfig:/home/node/.gitconfig:ro",
			"~/.npmrc:/home/node/.npmrc:ro",
			"./cache:/cache",
		}; !equalStrings(got, want) {
			t.Fatalf("loadConfig() volumes = %v, want %v", got, want)
		}
		if got, want := cfg.Sandbox.Environment, []string{
			"FOO=user",
			"SHARED=project",
			"BAR=project",
		}; !equalStrings(got, want) {
			t.Fatalf("loadConfig() environment = %v, want %v", got, want)
		}
		if got, want := cfg.Sandbox.Domains, []string{
			"user.example.com",
			"shared.example.com",
			"project.example.com",
		}; !equalStrings(got, want) {
			t.Fatalf("loadConfig() domains = %v, want %v", got, want)
		}
		if cfg.Sandbox.Memory != "512m" || cfg.Sandbox.CPUs != "2.5" || cfg.Sandbox.Pids != 256 {
			t.Fatalf("loadConfig() limits = (%q, %q, %d), want (%q, %q, %d)", cfg.Sandbox.Memory, cfg.Sandbox.CPUs, cfg.Sandbox.Pids, "512m", "2.5", 256)
		}
	})
}

func TestLoadConfigRejectsUnsupportedUserFields(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  docker: true",
		"  working_dir: /app",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "only supports sandbox.build.dockerfile, sandbox.image, sandbox.volumes, sandbox.environment, sandbox.domains, sandbox.memory, sandbox.cpus, and sandbox.pids") {
			t.Fatalf("loadConfig() error = %v, want supported-fields guidance", err)
		}
		if !strings.Contains(err.Error(), "unsupported fields: docker, working_dir") {
			t.Fatalf("loadConfig() error = %v, want unsupported field list", err)
		}
	})
}

func TestLoadConfigRequiresSandboxImageWhenProjectConfigPresent(t *testing.T) {
	withConfigDirs(t, "", func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  working_dir: /app",
			"",
		}, "\n"))

		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "sandbox.image is required") {
			t.Fatalf("loadConfig() error = %v, want sandbox.image is required", err)
		}
	})
}

func TestLoadConfigRejectsBlankSandboxImage(t *testing.T) {
	withConfigDirs(t, "", func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  image: \"   \"",
			"",
		}, "\n"))

		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "sandbox.image is required") {
			t.Fatalf("loadConfig() error = %v, want sandbox.image is required", err)
		}
	})
}

func TestLoadConfigDoesNotRequireImageForUserConfigOnly(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  environment:",
		"    - FOO=user",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		if cfg.Sandbox.Image != "" {
			t.Fatalf("loadConfig() image = %q, want empty", cfg.Sandbox.Image)
		}
		if got, want := cfg.Sandbox.Environment, []string{"FOO=user"}; !equalStrings(got, want) {
			t.Fatalf("loadConfig() environment = %v, want %v", got, want)
		}
	})
}

func TestLoadConfigAllowsUserImageAndDockerfileWithoutProjectConfig(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  build:",
		"    dockerfile: ~/.sandboxeed/Dockerfile",
		"  image: sandboxeed-user:latest",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		if cfg.Sandbox.Image != "sandboxeed-user:latest" || cfg.Sandbox.Build.Dockerfile != "~/.sandboxeed/Dockerfile" {
			t.Fatalf("loadConfig() build/image = (%q, %q), want user defaults", cfg.Sandbox.Build.Dockerfile, cfg.Sandbox.Image)
		}
	})
}

func TestLoadConfigProjectConfigCanInheritUserImage(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  build:",
		"    dockerfile: ~/.sandboxeed/Dockerfile",
		"  image: sandboxeed-user:latest",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  environment:",
			"    - FOO=project",
			"",
		}, "\n"))

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		if cfg.Sandbox.Image != "sandboxeed-user:latest" || cfg.Sandbox.Build.Dockerfile != "~/.sandboxeed/Dockerfile" {
			t.Fatalf("loadConfig() build/image = (%q, %q), want inherited user values", cfg.Sandbox.Build.Dockerfile, cfg.Sandbox.Image)
		}
		if got, want := cfg.Sandbox.Environment, []string{"FOO=project"}; !equalStrings(got, want) {
			t.Fatalf("loadConfig() environment = %v, want %v", got, want)
		}
	})
}

func TestLoadConfigRejectsInvalidProjectLimits(t *testing.T) {
	withConfigDirs(t, "", func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  image: alpine:3.22",
			"  cpus: nope",
			"",
		}, "\n"))

		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "sandbox.cpus") {
			t.Fatalf("loadConfig() error = %v, want cpu validation", err)
		}
	})
}

func TestLoadConfigRejectsInvalidMemoryLimit(t *testing.T) {
	withConfigDirs(t, "", func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  image: alpine:3.22",
			"  memory: abc",
			"",
		}, "\n"))

		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "sandbox.memory must be a valid Docker memory value") {
			t.Fatalf("loadConfig() error = %v, want memory validation", err)
		}
	})
}

func TestLoadConfigRejectsProjectDockerfileWithoutImage(t *testing.T) {
	withConfigDirs(t, "", func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  build:",
			"    dockerfile: Dockerfile.sandbox",
			"",
		}, "\n"))

		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "sandbox.image is required when sandbox.build.dockerfile is set") {
			t.Fatalf("loadConfig() error = %v, want build/image pair validation", err)
		}
	})
}

func TestLoadConfigRejectsUserDockerfileWithoutImage(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  build:",
		"    dockerfile: ~/.sandboxeed/Dockerfile",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "sandbox.image is required when sandbox.build.dockerfile is set") {
			t.Fatalf("loadConfig() error = %v, want build/image pair validation", err)
		}
	})
}

func TestLoadConfigRejectsInvalidUserLimits(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  cpus: nope",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "sandbox.cpus must be a positive number") {
			t.Fatalf("loadConfig() error = %v, want cpu validation", err)
		}
	})
}

func TestLoadConfigProjectEmptyLimitsDoNotOverrideUserDefaults(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  memory: 256m",
		"  cpus: \"1.5\"",
		"  pids: 128",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  image: alpine:3.22",
			"  memory: \"\"",
			"  cpus: \"\"",
			"  pids: 0",
			"",
		}, "\n"))

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		if cfg.Sandbox.Memory != "256m" || cfg.Sandbox.CPUs != "1.5" || cfg.Sandbox.Pids != 128 {
			t.Fatalf("loadConfig() limits = (%q, %q, %d), want inherited user defaults", cfg.Sandbox.Memory, cfg.Sandbox.CPUs, cfg.Sandbox.Pids)
		}
	})
}

func TestValidateDomainsAcceptsSupportedPatterns(t *testing.T) {
	input := []string{"example.com", "*.example.com", ".internal.example", "sub-domain.example"}

	got, err := validateDomains(input)
	if err != nil {
		t.Fatalf("validateDomains() error = %v", err)
	}
	if len(got) != len(input) {
		t.Fatalf("validateDomains() len = %d, want %d", len(got), len(input))
	}
	for i := range input {
		if got[i] != input[i] {
			t.Fatalf("validateDomains()[%d] = %q, want %q", i, got[i], input[i])
		}
	}
}

func TestValidateDomainsRejectsInvalidValues(t *testing.T) {
	cases := []string{
		"",
		"bad domain",
		".",
		"*.",
		"bad_domain.example",
		"127.0.0.1",
	}

	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			if _, err := validateDomains([]string{tc}); err == nil {
				t.Fatalf("validateDomains(%q) error = nil, want error", tc)
			}
		})
	}
}

func TestGenerateSquidConfIncludesACLsAndAllowRule(t *testing.T) {
	conf, err := generateSquidConf([]string{"example.com", "*.example.org"})
	if err != nil {
		t.Fatalf("generateSquidConf() error = %v", err)
	}

	wantParts := []string{
		"acl allowed dstdomain example.com",
		"acl allowed dstdomain *.example.org",
		"http_access allow allowed",
		"http_access deny all",
	}
	for _, part := range wantParts {
		if !strings.Contains(conf, part) {
			t.Fatalf("generateSquidConf() missing %q in config:\n%s", part, conf)
		}
	}
}

func TestGenerateSquidConfOmitsAllowRuleWithoutDomains(t *testing.T) {
	conf, err := generateSquidConf(nil)
	if err != nil {
		t.Fatalf("generateSquidConf() error = %v", err)
	}
	if strings.Contains(conf, "http_access allow allowed") {
		t.Fatalf("generateSquidConf() unexpectedly contains allow rule:\n%s", conf)
	}
}

func TestCurrentVersionPrefersInjectedVersion(t *testing.T) {
	original := version
	version = "v1.2.3"
	t.Cleanup(func() {
		version = original
	})

	if got := currentVersion(); got != "v1.2.3" {
		t.Fatalf("currentVersion() = %q, want %q", got, "v1.2.3")
	}
}

func TestParseCLIArgsParsesSandboxFlagsBeforeCommand(t *testing.T) {
	got, err := parseCLIArgs([]string{"--build", "--no-docker", "--read-only", "claude", "--read-only"})
	if err != nil {
		t.Fatalf("parseCLIArgs() error = %v", err)
	}

	if got.mode != cliModeSandbox {
		t.Fatalf("parseCLIArgs() mode = %q, want %q", got.mode, cliModeSandbox)
	}
	if !got.build || !got.noDocker || !got.readOnly {
		t.Fatalf("parseCLIArgs() flags = (%v, %v, %v), want all true", got.build, got.noDocker, got.readOnly)
	}
	if got.command != "claude" {
		t.Fatalf("parseCLIArgs() command = %q, want %q", got.command, "claude")
	}
	if want := []string{"--read-only"}; !equalStrings(got.args, want) {
		t.Fatalf("parseCLIArgs() args = %v, want %v", got.args, want)
	}
}

func TestParseCLIArgsTreatsBuiltInNamesAsSandboxCommands(t *testing.T) {
	for _, name := range []string{"cleanup", "inspect", "version", "help"} {
		t.Run(name, func(t *testing.T) {
			got, err := parseCLIArgs([]string{name, "arg"})
			if err != nil {
				t.Fatalf("parseCLIArgs() error = %v", err)
			}
			if got.mode != cliModeSandbox {
				t.Fatalf("parseCLIArgs() mode = %q, want %q", got.mode, cliModeSandbox)
			}
			if got.command != name {
				t.Fatalf("parseCLIArgs() command = %q, want %q", got.command, name)
			}
			if want := []string{"arg"}; !equalStrings(got.args, want) {
				t.Fatalf("parseCLIArgs() args = %v, want %v", got.args, want)
			}
		})
	}
}

func TestParseCLIArgsParsesBuiltInsWithDoubleDash(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		mode cliMode
	}{
		{name: "help", argv: []string{"--help"}, mode: cliModeHelp},
		{name: "help-short", argv: []string{"-h"}, mode: cliModeHelp},
		{name: "version", argv: []string{"--version"}, mode: cliModeVersion},
		{name: "inspect", argv: []string{"--inspect"}, mode: cliModeInspect},
		{name: "cleanup", argv: []string{"--cleanup"}, mode: cliModeCleanup},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCLIArgs(tt.argv)
			if err != nil {
				t.Fatalf("parseCLIArgs() error = %v", err)
			}
			if got.mode != tt.mode {
				t.Fatalf("parseCLIArgs() mode = %q, want %q", got.mode, tt.mode)
			}
			if got.command != "" {
				t.Fatalf("parseCLIArgs() command = %q, want empty", got.command)
			}
		})
	}
}

func TestParseCLIArgsRejectsUnknownFlagBeforeCommand(t *testing.T) {
	_, err := parseCLIArgs([]string{"--unknown"})
	if err == nil {
		t.Fatalf("parseCLIArgs() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("parseCLIArgs() error = %v, want unknown flag error", err)
	}
}

func TestParseCLIArgsSupportsEndOfOptionsMarker(t *testing.T) {
	got, err := parseCLIArgs([]string{"--build", "--", "--demo", "arg"})
	if err != nil {
		t.Fatalf("parseCLIArgs() error = %v", err)
	}
	if got.mode != cliModeSandbox {
		t.Fatalf("parseCLIArgs() mode = %q, want %q", got.mode, cliModeSandbox)
	}
	if !got.build {
		t.Fatalf("parseCLIArgs() build = false, want true")
	}
	if got.command != "--demo" {
		t.Fatalf("parseCLIArgs() command = %q, want %q", got.command, "--demo")
	}
	if want := []string{"arg"}; !equalStrings(got.args, want) {
		t.Fatalf("parseCLIArgs() args = %v, want %v", got.args, want)
	}
}

func TestParseCLIArgsAllowsBareEndOfOptionsMarker(t *testing.T) {
	got, err := parseCLIArgs([]string{"--"})
	if err != nil {
		t.Fatalf("parseCLIArgs() error = %v", err)
	}
	if got.mode != cliModeSandbox {
		t.Fatalf("parseCLIArgs() mode = %q, want %q", got.mode, cliModeSandbox)
	}
	if got.command != "" {
		t.Fatalf("parseCLIArgs() command = %q, want empty", got.command)
	}
}

func TestParseCLIArgsTreatsEndOfOptionsMarkerAfterCommandAsArgument(t *testing.T) {
	got, err := parseCLIArgs([]string{"claude", "--", "--help"})
	if err != nil {
		t.Fatalf("parseCLIArgs() error = %v", err)
	}
	if got.command != "claude" {
		t.Fatalf("parseCLIArgs() command = %q, want %q", got.command, "claude")
	}
	if want := []string{"--", "--help"}; !equalStrings(got.args, want) {
		t.Fatalf("parseCLIArgs() args = %v, want %v", got.args, want)
	}
}

func TestParseCLIArgsKeepsAllTokensAfterBuiltInWithBuiltIn(t *testing.T) {
	got, err := parseCLIArgs([]string{"--inspect", "cleanup"})
	if err != nil {
		t.Fatalf("parseCLIArgs() error = %v", err)
	}
	if got.mode != cliModeInspect {
		t.Fatalf("parseCLIArgs() mode = %q, want %q", got.mode, cliModeInspect)
	}
	if want := []string{"cleanup"}; !equalStrings(got.extraArgs, want) {
		t.Fatalf("parseCLIArgs() extraArgs = %v, want %v", got.extraArgs, want)
	}
}

func TestNetworkProjectNameIsDeterministic(t *testing.T) {
	dir := "/tmp/example/project"
	got1 := networkProjectName(dir)
	got2 := networkProjectName(dir)

	if got1 != got2 {
		t.Fatalf("networkProjectName() mismatch: %q != %q", got1, got2)
	}
	if !strings.HasPrefix(got1, "project-") {
		t.Fatalf("networkProjectName() = %q, want prefix %q", got1, "project-")
	}

	other := networkProjectName("/tmp/example/other")
	if got1 == other {
		t.Fatalf("networkProjectName() should differ for different dirs: %q", got1)
	}
}

func TestNetworkProjectNameSanitizesProjectBasename(t *testing.T) {
	got := networkProjectName("/tmp/example/My Project")

	if !strings.HasPrefix(got, "my-project-") {
		t.Fatalf("networkProjectName() = %q, want sanitized prefix %q", got, "my-project-")
	}
	if strings.ContainsAny(got, " /") {
		t.Fatalf("networkProjectName() = %q, want no spaces or path separators", got)
	}
}

func TestNewRunTokenProducesCompactBase32String(t *testing.T) {
	got := newRunToken()

	if !regexp.MustCompile(`^[a-z2-7]{8}$`).MatchString(got) {
		t.Fatalf("newRunToken() = %q, want 8 lowercase base32 characters", got)
	}
}

func TestNewRunResourcesUsesShortNames(t *testing.T) {
	got := newRunResources("/tmp/sandboxeed")

	if !strings.HasPrefix(got.sandboxContainer, "sandboxeed-") {
		t.Fatalf("sandboxContainer = %q, want sandboxeed- prefix", got.sandboxContainer)
	}

	wantPatterns := map[string]string{
		got.sandboxContainer: `^sandboxeed-[0-9a-f]{8}-sandbox-[a-z2-7]{8}$`,
		got.proxyContainer:   `^sandboxeed-[0-9a-f]{8}-proxy-[a-z2-7]{8}$`,
		got.proxyConfigVol:   `^sandboxeed-[0-9a-f]{8}-proxy-config-[a-z2-7]{8}$`,
		got.dindContainer:    `^sandboxeed-[0-9a-f]{8}-dind-[a-z2-7]{8}$`,
		got.dindVolume:       `^sandboxeed-[0-9a-f]{8}-dind-data-[a-z2-7]{8}$`,
		got.internalNetwork:  `^sandboxeed-[0-9a-f]{8}-internal-[a-z2-7]{8}$`,
		got.egressNetwork:    `^sandboxeed-[0-9a-f]{8}-egress-[a-z2-7]{8}$`,
	}
	for value, pattern := range wantPatterns {
		if !regexp.MustCompile(pattern).MatchString(value) {
			t.Fatalf("resource name = %q, want pattern %q", value, pattern)
		}
	}
}

func TestExpandVolumeSpec(t *testing.T) {
	projectDir := "/workspace/demo"
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "dot bind", in: ".:/workspace", want: "/workspace/demo:/workspace"},
		{name: "relative bind", in: "./cache:/cache", want: "/workspace/demo/cache:/cache"},
		{name: "parent bind", in: "../shared:/shared", want: "/workspace/shared:/shared"},
		{name: "absolute bind", in: "/tmp/data:/data:ro", want: "/tmp/data:/data:ro"},
		{name: "named volume", in: "cache-data:/cache", want: "cache-data:/cache"},
		{name: "home tilde bind", in: "~/.gitconfig:/home/node/.gitconfig:ro", want: homeDir + "/.gitconfig:/home/node/.gitconfig:ro"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandVolumeSpec(projectDir, tc.in); got != tc.want {
				t.Fatalf("expandVolumeSpec(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestForceReadOnly(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "no options", in: "/host:/container", want: "/host:/container:ro"},
		{name: "already ro", in: "/host:/container:ro", want: "/host:/container:ro"},
		{name: "replace rw", in: "/host:/container:rw", want: "/host:/container:ro"},
		{name: "preserve other opts", in: "/host:/container:cached", want: "/host:/container:cached,ro"},
		{name: "rw with other opts", in: "/host:/container:rw,cached", want: "/host:/container:ro,cached"},
		{name: "no colon", in: "named-volume", want: "named-volume"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := forceReadOnly(tc.in); got != tc.want {
				t.Fatalf("forceReadOnly(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSandboxImageNamePrefersConfiguredImage(t *testing.T) {
	cfg := &Config{}
	cfg.Sandbox.Image = "custom-sandbox:dev"

	if got := sandboxImageName("/workspace/demo", cfg); got != "custom-sandbox:dev" {
		t.Fatalf("sandboxImageName() = %q, want %q", got, "custom-sandbox:dev")
	}
}

func TestSandboxImageNameUsesProjectSpecificDefault(t *testing.T) {
	cfg := &Config{}

	if got := sandboxImageName("/workspace/My Project", cfg); got != "my-project-sandboxeed" {
		t.Fatalf("sandboxImageName() = %q, want %q", got, "my-project-sandboxeed")
	}
}

func TestResolvedSandboxImage(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *Config
		built bool
		want  string
	}{
		{
			name: "configured image",
			cfg: func() *Config {
				cfg := &Config{}
				cfg.Sandbox.Image = "custom-sandbox:dev"
				return cfg
			}(),
			want: "custom-sandbox:dev",
		},
		{
			name:  "built default image",
			cfg:   &Config{},
			built: true,
			want:  "my-project-sandboxeed",
		},
		{
			name: "fallback image",
			cfg:  &Config{},
			want: "bash:latest",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvedSandboxImage("/workspace/My Project", tc.cfg, tc.built); got != tc.want {
				t.Fatalf("resolvedSandboxImage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShouldAutoBuildRequiresExplicitDockerfile(t *testing.T) {
	cfg := &Config{}
	if shouldAutoBuild(cfg) {
		t.Fatalf("shouldAutoBuild() = true, want false when dockerfile is unset")
	}

	cfg.Sandbox.Build.Dockerfile = "   "
	if shouldAutoBuild(cfg) {
		t.Fatalf("shouldAutoBuild() = true, want false when dockerfile is whitespace")
	}

	cfg.Sandbox.Build.Dockerfile = "Dockerfile.sandbox"
	if !shouldAutoBuild(cfg) {
		t.Fatalf("shouldAutoBuild() = false, want true when dockerfile is set")
	}
}

func TestResolveSandboxConfigIncludesLimits(t *testing.T) {
	cfg := &Config{}
	cfg.Sandbox.Image = "alpine:3.22"
	cfg.Sandbox.Memory = "512m"
	cfg.Sandbox.CPUs = "1.5"
	cfg.Sandbox.Pids = 128
	cfg.Sandbox.Environment = []string{"APP_ENV=test"}

	resolved := resolveSandboxConfig(newRunResources("/workspace/demo"), cfg, false, false, "", "")
	if resolved.Memory != "512m" || resolved.CPUs != "1.5" || resolved.Pids != 128 {
		t.Fatalf("resolveSandboxConfig() limits = (%q, %q, %d), want (%q, %q, %d)", resolved.Memory, resolved.CPUs, resolved.Pids, "512m", "1.5", 128)
	}
}

func TestRunInspectPrintsResolvedConfig(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  memory: 256m",
		"  cpus: \"1\"",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		writeFile(t, filepath.Join(projectDir, configFile), strings.Join([]string{
			"sandbox:",
			"  image: alpine:3.22",
			"",
		}, "\n"))

		stdout, stderr := captureOutput(t, func() {
			if code := runInspect(false, false); code != 0 {
				t.Fatalf("runInspect() = %d, want 0", code)
			}
		})
		if stderr != "" {
			t.Fatalf("runInspect() stderr = %q, want empty", stderr)
		}
		for _, part := range []string{
			"image: alpine:3.22",
			"memory: 256m",
			"cpus: \"1\"",
		} {
			if !strings.Contains(stdout, part) {
				t.Fatalf("runInspect() output missing %q:\n%s", part, stdout)
			}
		}
	})
}

func TestResolveDockerfilePathExpandsHome(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	got, err := resolveDockerfilePath("/workspace/demo", "~/Dockerfile")
	if err != nil {
		t.Fatalf("resolveDockerfilePath() error = %v", err)
	}
	if got != filepath.Join(homeDir, "Dockerfile") {
		t.Fatalf("resolveDockerfilePath() = %q, want %q", got, filepath.Join(homeDir, "Dockerfile"))
	}
}

func TestResolveDockerfilePathFailsWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := resolveDockerfilePath("/workspace/demo", "~/Dockerfile")
	if err == nil {
		t.Fatalf("resolveDockerfilePath() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "cannot expand") {
		t.Fatalf("resolveDockerfilePath() error = %v, want expansion error", err)
	}
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore Chdir(%q) error = %v", wd, err)
		}
	})

	fn()
}

func withConfigDirs(t *testing.T, userConfig string, fn func(projectDir, homeDir string)) {
	t.Helper()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if userConfig != "" {
		writeFile(t, filepath.Join(homeDir, userConfigFile), userConfig)
	}

	withWorkingDir(t, projectDir, func() {
		fn(projectDir, homeDir)
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()

	origStdout := os.Stdout
	origStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdout) error = %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stderr) error = %v", err)
	}
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	t.Cleanup(func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	})

	fn()

	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	stdoutBytes, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("ReadAll(stdout) error = %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("ReadAll(stderr) error = %v", err)
	}
	return string(stdoutBytes), string(stderrBytes)
}
