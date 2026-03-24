package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseCLIArgsParsesSelfUpdateBuiltIn(t *testing.T) {
	got, err := parseCLIArgs([]string{"--self-update"})
	if err != nil {
		t.Fatalf("parseCLIArgs() error = %v", err)
	}
	if got.mode != cliModeSelfUpdate {
		t.Fatalf("parseCLIArgs() mode = %q, want %q", got.mode, cliModeSelfUpdate)
	}
	if got.command != "" {
		t.Fatalf("parseCLIArgs() command = %q, want empty", got.command)
	}
}

func TestIsVersionNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{current: "v0.013", latest: "v0.014", want: true},
		{current: "v0.013", latest: "v0.013", want: false},
		{current: "v0.014", latest: "v0.013", want: false},
		{current: "v1.2.0", latest: "v1.10.0", want: true},
		{current: "v0.0.0-20260324214643-a65ba7cc3179+dirty", latest: "v0.013", want: true},
		{current: "devel", latest: "v1.0.0", want: false},
	}

	for _, tt := range tests {
		if got := isVersionNewer(tt.current, tt.latest); got != tt.want {
			t.Fatalf("isVersionNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestVerifyChecksum(t *testing.T) {
	archive := []byte("archive-bytes")
	sum := sha256.Sum256(archive)
	checksums := []byte(hex.EncodeToString(sum[:]) + " sandboxeed_v0.013_linux_amd64.tar.gz\n")

	if err := verifyChecksum("sandboxeed_v0.013_linux_amd64.tar.gz", archive, checksums); err != nil {
		t.Fatalf("verifyChecksum() error = %v", err)
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	archive := mustMakeTarGz(t, map[string]string{
		"sandboxeed_v0.013_linux_amd64/sandboxeed": "binary-contents",
		"sandboxeed_v0.013_linux_amd64/README.md":  "ignored",
	})

	got, err := extractBinaryFromTarGz(archive, "sandboxeed")
	if err != nil {
		t.Fatalf("extractBinaryFromTarGz() error = %v", err)
	}
	if string(got) != "binary-contents" {
		t.Fatalf("extractBinaryFromTarGz() = %q, want %q", string(got), "binary-contents")
	}
}

func TestMaybeNotifyUpdatePrintsNotice(t *testing.T) {
	originalVersion := version
	originalAPIBase := updateAPIBaseURL
	version = "v0.013"
	t.Cleanup(func() {
		version = originalVersion
		updateAPIBaseURL = originalAPIBase
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/00dev-org/sandboxeed/releases/latest" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.014"}`))
	}))
	defer server.Close()

	updateAPIBaseURL = server.URL

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	maybeNotifyUpdate()

	if err := w.Close(); err != nil {
		t.Fatalf("w.Close() error = %v", err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	if !strings.Contains(string(output), "update available: v0.013 -> v0.014") {
		t.Fatalf("maybeNotifyUpdate() output = %q, want update notice", string(output))
	}
}

func mustMakeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	for name, contents := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(contents)),
		}); err != nil {
			t.Fatalf("WriteHeader(%q) error = %v", name, err)
		}
		if _, err := tw.Write([]byte(contents)); err != nil {
			t.Fatalf("Write(%q) error = %v", name, err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close() error = %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzw.Close() error = %v", err)
	}
	return buf.Bytes()
}
