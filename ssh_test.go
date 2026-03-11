package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
