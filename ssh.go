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

	knownHosts, err := fetchGitHubKnownHosts()
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
