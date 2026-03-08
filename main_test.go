package main

import (
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
		"  volumes:",
		"    - ~/.gitconfig:/home/node/.gitconfig:ro",
		"  environment:",
		"    - FOO=user",
		"  domains:",
		"    - user.example.com",
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
	})
}

func TestLoadConfigMergesUserAndProjectConfig(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  volumes:",
		"    - ~/.gitconfig:/home/node/.gitconfig:ro",
		"    - ~/.npmrc:/home/node/.npmrc:ro",
		"  environment:",
		"    - FOO=user",
		"    - SHARED=user",
		"  domains:",
		"    - user.example.com",
		"    - shared.example.com",
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
			"",
		}, "\n"))

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}

		if cfg.Sandbox.Image != "alpine:3.22" {
			t.Fatalf("loadConfig() image = %q, want %q", cfg.Sandbox.Image, "alpine:3.22")
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
	})
}

func TestLoadConfigRejectsUnsupportedUserFields(t *testing.T) {
	withConfigDirs(t, strings.Join([]string{
		"sandbox:",
		"  image: alpine:3.22",
		"  docker: true",
		"",
	}, "\n"), func(projectDir, homeDir string) {
		_, err := loadConfig()
		if err == nil {
			t.Fatalf("loadConfig() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "only supports sandbox.volumes, sandbox.environment, and sandbox.domains") {
			t.Fatalf("loadConfig() error = %v, want supported-fields guidance", err)
		}
		if !strings.Contains(err.Error(), "unsupported fields: image, docker") {
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

func TestNewRunTokenProducesCompactHexString(t *testing.T) {
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandVolumeSpec(projectDir, tc.in); got != tc.want {
				t.Fatalf("expandVolumeSpec(%q) = %q, want %q", tc.in, got, tc.want)
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
