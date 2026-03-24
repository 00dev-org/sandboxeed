package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	updateRepoOwner      = "00dev-org"
	updateRepoName       = "sandboxeed"
	updateCheckTimeout   = 1500 * time.Millisecond
	selfUpdateTimeout    = 10 * time.Second
	updateDisableEnvName = "SANDBOXEED_DISABLE_UPDATE_CHECK"
)

var (
	updateAPIBaseURL      = "https://api.github.com"
	updateDownloadBaseURL = fmt.Sprintf("https://github.com/%s/%s/releases/download", updateRepoOwner, updateRepoName)
)

type releaseInfo struct {
	TagName string `json:"tag_name"`
}

func maybeNotifyUpdate() {
	if strings.TrimSpace(os.Getenv(updateDisableEnvName)) != "" {
		return
	}

	current := currentVersion()
	if current == "" {
		return
	}

	release, err := fetchLatestRelease(updateCheckTimeout)
	if err != nil {
		return
	}
	if !isVersionNewer(current, release.TagName) {
		return
	}

	stderrf("update available: %s -> %s (run: sandboxeed --self-update)\n", current, release.TagName)
}

func runSelfUpdate() int {
	current := currentVersion()
	if current == "" || current == "devel" {
		stderrf("self-update requires a released build; current version is %q\n", current)
		return 1
	}

	release, err := fetchLatestRelease(selfUpdateTimeout)
	if err != nil {
		stderrf("failed to resolve latest release: %v\n", err)
		return 1
	}
	if !isVersionNewer(current, release.TagName) {
		stdoutf("sandboxeed is already up to date (%s)\n", current)
		return 0
	}

	archiveName, err := releaseArchiveName(release.TagName)
	if err != nil {
		stderrf("self-update is not supported on %s/%s: %v\n", runtime.GOOS, runtime.GOARCH, err)
		return 1
	}

	archiveURL := fmt.Sprintf("%s/%s/%s", updateDownloadBaseURL, release.TagName, archiveName)
	checksumsURL := fmt.Sprintf("%s/%s/checksums.txt", updateDownloadBaseURL, release.TagName)

	archive, err := downloadURL(archiveURL, selfUpdateTimeout)
	if err != nil {
		stderrf("failed to download %s: %v\n", archiveName, err)
		return 1
	}
	checksums, err := downloadURL(checksumsURL, selfUpdateTimeout)
	if err != nil {
		stderrf("failed to download checksums.txt: %v\n", err)
		return 1
	}
	if err := verifyChecksum(archiveName, archive, checksums); err != nil {
		stderrf("checksum verification failed: %v\n", err)
		return 1
	}

	binary, err := extractBinaryFromTarGz(archive, "sandboxeed")
	if err != nil {
		stderrf("failed to unpack release archive: %v\n", err)
		return 1
	}

	exePath, err := os.Executable()
	if err != nil {
		stderrf("failed to locate current executable: %v\n", err)
		return 1
	}
	if err := replaceExecutable(exePath, binary); err != nil {
		stderrf("failed to install update: %v\n", err)
		return 1
	}

	stdoutf("updated sandboxeed from %s to %s\n", current, release.TagName)
	return 0
}

func fetchLatestRelease(timeout time.Duration) (releaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", updateAPIBaseURL, updateRepoOwner, updateRepoName)
	data, err := downloadURL(url, timeout)
	if err != nil {
		return releaseInfo{}, err
	}

	var release releaseInfo
	if err := json.Unmarshal(data, &release); err != nil {
		return releaseInfo{}, fmt.Errorf("failed to parse release response: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return releaseInfo{}, fmt.Errorf("latest release did not include tag_name")
	}
	return release, nil
}

func downloadURL(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func releaseArchiveName(tag string) (string, error) {
	goos, err := releaseGOOS(runtime.GOOS)
	if err != nil {
		return "", err
	}
	goarch, err := releaseGOARCH(runtime.GOARCH)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sandboxeed_%s_%s_%s.tar.gz", tag, goos, goarch), nil
}

func releaseGOOS(goos string) (string, error) {
	switch goos {
	case "linux", "darwin":
		return goos, nil
	default:
		return "", fmt.Errorf("unsupported OS %q", goos)
	}
}

func releaseGOARCH(goarch string) (string, error) {
	switch goarch {
	case "amd64", "arm64":
		return goarch, nil
	default:
		return "", fmt.Errorf("unsupported architecture %q", goarch)
	}
}

func verifyChecksum(name string, contents, checksums []byte) error {
	want := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == name {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("missing checksum entry for %s", name)
	}

	sum := sha256.Sum256(contents)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("got %s, want %s", got, want)
	}
	return nil
}

func extractBinaryFromTarGz(archive []byte, binaryName string) ([]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		return data, nil
	}

	return nil, fmt.Errorf("archive does not contain %q", binaryName)
}

func replaceExecutable(path string, contents []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sandboxeed-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	defer cleanup()

	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

func isVersionNewer(current, latest string) bool {
	currentParts, ok := parseVersionParts(current)
	if !ok {
		return false
	}
	latestParts, ok := parseVersionParts(latest)
	if !ok {
		return false
	}

	maxParts := len(currentParts)
	if len(latestParts) > maxParts {
		maxParts = len(latestParts)
	}
	for i := 0; i < maxParts; i++ {
		var currentPart, latestPart int
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if latestPart > currentPart {
			return true
		}
		if latestPart < currentPart {
			return false
		}
	}
	return false
}

func parseVersionParts(v string) ([]int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil, false
	}
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	if v == "" {
		return nil, false
	}

	parts := strings.Split(v, ".")
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		values = append(values, n)
	}
	return values, true
}
