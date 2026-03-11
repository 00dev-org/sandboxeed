package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const githubKnownHostsCacheTTL = 24 * time.Hour

func needsGitHubSSH(domains []string) bool {
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d == "github.com" || d == ".github.com" || d == "*.github.com" {
			return true
		}
	}
	return false
}

const sshConfigContent = `Host github.com
    ProxyCommand corkscrew proxy 3128 %h %p
    StrictHostKeyChecking yes
    UserKnownHostsFile /dev/null
`

type githubMeta struct {
	SSHKeys []string `json:"ssh_keys"`
}

func fetchGitHubKnownHosts() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/meta")
	if err != nil {
		return "", fmt.Errorf("failed to fetch GitHub host keys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub meta API returned status %d", resp.StatusCode)
	}

	var meta githubMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("failed to parse GitHub meta response: %w", err)
	}
	if len(meta.SSHKeys) == 0 {
		return "", fmt.Errorf("GitHub meta API returned no SSH keys")
	}

	var sb strings.Builder
	for _, key := range meta.SSHKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		sb.WriteString("github.com ")
		sb.WriteString(key)
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func githubKnownHostsCachePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "sandboxeed", "github_known_hosts"), nil
}

func readCachedGitHubKnownHosts(now time.Time) (string, bool, error) {
	cachePath, err := githubKnownHostsCachePath()
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to stat GitHub host key cache: %w", err)
	}
	if now.Sub(info.ModTime()) > githubKnownHostsCacheTTL {
		return "", false, nil
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return "", false, fmt.Errorf("failed to read GitHub host key cache: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", false, nil
	}
	return string(data), true, nil
}

func writeCachedGitHubKnownHosts(contents string) error {
	cachePath, err := githubKnownHostsCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return fmt.Errorf("failed to create GitHub host key cache directory: %w", err)
	}
	if err := os.WriteFile(cachePath, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("failed to write GitHub host key cache: %w", err)
	}
	return nil
}

func loadGitHubKnownHosts() (string, error) {
	cached, ok, err := readCachedGitHubKnownHosts(time.Now())
	if err == nil && ok {
		return cached, nil
	}

	knownHosts, err := fetchGitHubKnownHosts()
	if err != nil {
		return "", err
	}
	_ = writeCachedGitHubKnownHosts(knownHosts)
	return knownHosts, nil
}

func writeSSHFiles() (configPath, knownHostsPath string, err error) {
	dir, err := os.MkdirTemp("", "sandboxeed-ssh-")
	if err != nil {
		return "", "", err
	}

	configPath = filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(configPath, []byte(sshConfigContent), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("failed to write SSH config: %w", err)
	}

	knownHosts, err := loadGitHubKnownHosts()
	if err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}

	knownHostsPath = filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHostsPath, []byte(knownHosts), 0o644); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("failed to write known hosts: %w", err)
	}

	return configPath, knownHostsPath, nil
}
