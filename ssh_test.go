package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteSSHFilesUsesMountedKnownHostsPath(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	if err := writeCachedGitHubKnownHosts("github.com ssh-ed25519 AAAA\n"); err != nil {
		t.Fatalf("writeCachedGitHubKnownHosts() error = %v", err)
	}

	configPath, knownHostsPath, err := writeSSHFiles()
	if err != nil {
		t.Fatalf("writeSSHFiles() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Dir(configPath))
	})

	configContents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(configPath) error = %v", err)
	}
	if !strings.Contains(string(configContents), "UserKnownHostsFile /etc/ssh/ssh_known_hosts") {
		t.Fatalf("ssh config missing mounted known_hosts path:\n%s", string(configContents))
	}

	knownHostsContents, err := os.ReadFile(knownHostsPath)
	if err != nil {
		t.Fatalf("ReadFile(knownHostsPath) error = %v", err)
	}
	if string(knownHostsContents) != "github.com ssh-ed25519 AAAA\n" {
		t.Fatalf("known_hosts contents = %q, want cached contents", string(knownHostsContents))
	}

	resolved := resolveSandboxConfig(newRunResources(t.TempDir()), &Config{}, false, false, false, configPath, knownHostsPath)
	if !containsString(resolved.Volumes, configPath+":/etc/ssh/ssh_config:ro") {
		t.Fatalf("resolveSandboxConfig() volumes = %v, want ssh config mount", resolved.Volumes)
	}
	if !containsString(resolved.Volumes, knownHostsPath+":/etc/ssh/ssh_known_hosts:ro") {
		t.Fatalf("resolveSandboxConfig() volumes = %v, want known_hosts mount", resolved.Volumes)
	}
}

func TestReadCachedGitHubKnownHostsReturnsFreshContents(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	cachePath, err := githubKnownHostsCachePath()
	if err != nil {
		t.Fatalf("githubKnownHostsCachePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("github.com ssh-ed25519 AAAA\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, ok, err := readCachedGitHubKnownHosts(time.Now())
	if err != nil {
		t.Fatalf("readCachedGitHubKnownHosts() error = %v", err)
	}
	if !ok {
		t.Fatalf("readCachedGitHubKnownHosts() ok = false, want true")
	}
	if got != "github.com ssh-ed25519 AAAA\n" {
		t.Fatalf("readCachedGitHubKnownHosts() = %q, want cached contents", got)
	}
}

func TestReadCachedGitHubKnownHostsTreatsExpiredFileAsMiss(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	cachePath, err := githubKnownHostsCachePath()
	if err != nil {
		t.Fatalf("githubKnownHostsCachePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("github.com ssh-ed25519 AAAA\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	expired := time.Now().Add(-githubKnownHostsCacheTTL - time.Hour)
	if err := os.Chtimes(cachePath, expired, expired); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	got, ok, err := readCachedGitHubKnownHosts(time.Now())
	if err != nil {
		t.Fatalf("readCachedGitHubKnownHosts() error = %v", err)
	}
	if ok {
		t.Fatalf("readCachedGitHubKnownHosts() ok = true, want false")
	}
	if got != "" {
		t.Fatalf("readCachedGitHubKnownHosts() = %q, want empty on cache miss", got)
	}
}

func TestReadCachedGitHubKnownHostsReturnsErrorWhenCachePathIsDirectory(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	cachePath, err := githubKnownHostsCachePath()
	if err != nil {
		t.Fatalf("githubKnownHostsCachePath() error = %v", err)
	}
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	got, ok, err := readCachedGitHubKnownHosts(time.Now())
	if err == nil {
		t.Fatal("readCachedGitHubKnownHosts() error = nil, want error")
	}
	if ok {
		t.Fatalf("readCachedGitHubKnownHosts() ok = true, want false")
	}
	if got != "" {
		t.Fatalf("readCachedGitHubKnownHosts() = %q, want empty on error", got)
	}
}

func TestLoadGitHubKnownHostsFallsBackWhenCacheReadFails(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	cachePath, err := githubKnownHostsCachePath()
	if err != nil {
		t.Fatalf("githubKnownHostsCachePath() error = %v", err)
	}
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	got, err := loadGitHubKnownHosts()
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) && strings.Contains(pathErr.Path, "api.github.com") {
			t.Skipf("network unavailable for GitHub meta API: %v", err)
		}
		t.Fatalf("loadGitHubKnownHosts() error = %v", err)
	}
	if !strings.Contains(got, "github.com ") {
		t.Fatalf("loadGitHubKnownHosts() = %q, want fetched known_hosts content", got)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
