//go:build integration

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrationRunSandboxCommand(t *testing.T) {
	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	if err := os.WriteFile(filepath.Join(projectDir, "sandboxeed.yaml"), []byte("sandbox:\n  image: busybox:1.36\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(sandboxeed.yaml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "proof.txt"), []byte("ok\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(proof.txt) error = %v", err)
	}

	bin := buildSandboxeedBinary(t)
	projectName := networkProjectName(projectDir)

	// Run through `script` so the app's `docker run -it` gets a tty in the test process.
	command := fmt.Sprintf("%q sh -lc %q", bin, "pwd; test -f /workspace/proof.txt; echo CORE_OK")
	cmd := exec.Command("script", "-qec", command, "/dev/null")
	cmd.Dir = projectDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("sandboxeed run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "/workspace") {
		t.Fatalf("sandbox output missing working directory:\n%s", out)
	}
	if !strings.Contains(out, "CORE_OK") {
		t.Fatalf("sandbox output missing success marker:\n%s", out)
	}

	assertNoProjectResources(t, projectName)
}

func TestIntegrationSandboxBlocksDirectEgressWithoutProxy(t *testing.T) {
	session := startSandboxSession(t, "sandbox:\n  image: busybox:1.36\n")
	startHTTPService(t, session.egressNetwork, "allowed.test", "ISOLATION_OK\n")

	out, err := execInContainer(
		session.sandboxContainer,
		`sh -lc 'unset http_proxy HTTP_PROXY https_proxy HTTPS_PROXY no_proxy NO_PROXY; if wget -T 5 -qO- http://allowed.test >/dev/null 2>&1; then echo ISOLATION_BROKEN; exit 1; else echo ISOLATION_OK; fi'`,
	)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "ISOLATION_OK") {
		t.Fatalf("sandbox output missing isolation marker:\n%s", out)
	}
}

func TestIntegrationSandboxAllowsWhitelistedDomainViaProxy(t *testing.T) {
	session := startSandboxSession(t, "sandbox:\n  image: busybox:1.36\n  domains:\n    - allowed.test\n")
	startHTTPService(t, session.egressNetwork, "allowed.test", "WHITELIST_OK\n")

	out, err := execInContainer(
		session.sandboxContainer,
		`sh -lc 'export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY" no_proxy="$NO_PROXY"; wget -T 10 -qO- http://allowed.test'`,
	)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "WHITELIST_OK") {
		t.Fatalf("sandbox output missing whitelist body:\n%s", out)
	}
}

func TestIntegrationSandboxBlocksNonWhitelistedDomainViaProxy(t *testing.T) {
	session := startSandboxSession(t, "sandbox:\n  image: busybox:1.36\n  domains:\n    - allowed.test\n")
	startHTTPService(t, session.egressNetwork, "blocked.test", "WHITELIST_BROKEN\n")

	out, err := execInContainer(
		session.sandboxContainer,
		`sh -lc 'export http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY" no_proxy="$NO_PROXY"; if wget -T 5 -qO- http://blocked.test >/dev/null 2>&1; then echo WHITELIST_BROKEN; exit 1; else echo WHITELIST_BLOCK_OK; fi'`,
	)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "WHITELIST_BLOCK_OK") {
		t.Fatalf("sandbox output missing block marker:\n%s", out)
	}
}

func buildSandboxeedBinary(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "sandboxeed")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = testRootDir(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}
	return bin
}

func workspaceTempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp(testRootDir(t), "sandboxeed-int-")
	if err != nil {
		t.Fatalf("MkdirTemp(%q) error = %v", testRootDir(t), err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func testRootDir(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	return dir
}

func writeSandboxConfig(t *testing.T, projectDir, contents string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(projectDir, "sandboxeed.yaml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile(sandboxeed.yaml) error = %v", err)
	}
}

func runSandboxeedScripted(t *testing.T, projectDir, command string) (string, string, error) {
	t.Helper()

	bin := buildSandboxeedBinary(t)
	cmd := exec.Command("script", "-qec", fmt.Sprintf("%q %s", bin, command), "/dev/null")
	cmd.Dir = projectDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

type sandboxSession struct {
	projectName      string
	projectDir       string
	sandboxContainer string
	egressNetwork    string
	cmd              *exec.Cmd
	stdout           *bytes.Buffer
	stderr           *bytes.Buffer
}

func startSandboxSession(t *testing.T, config string) *sandboxSession {
	t.Helper()

	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, config)

	session := &sandboxSession{
		projectName: networkProjectName(projectDir),
		projectDir:  projectDir,
		stdout:      &bytes.Buffer{},
		stderr:      &bytes.Buffer{},
	}

	bin := buildSandboxeedBinary(t)
	command := fmt.Sprintf("%q sh -lc %q", bin, "trap 'exit 0' INT TERM; sleep 60")
	cmd := exec.Command("script", "-qec", command, "/dev/null")
	cmd.Dir = projectDir
	cmd.Stdout = session.stdout
	cmd.Stderr = session.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sandboxeed session: %v", err)
	}
	session.cmd = cmd

	var err error
	session.sandboxContainer, err = waitForProjectContainer(session.projectName, "sandbox")
	if err != nil {
		stopSandboxSession(t, session)
		t.Fatalf("sandbox container did not appear: %v\nstdout:\n%s\nstderr:\n%s", err, session.stdout.String(), session.stderr.String())
	}
	session.egressNetwork, err = waitForProjectNetwork(session.projectName, "egress")
	if err != nil {
		stopSandboxSession(t, session)
		t.Fatalf("egress network did not appear: %v\nstdout:\n%s\nstderr:\n%s", err, session.stdout.String(), session.stderr.String())
	}

	t.Cleanup(func() {
		stopSandboxSession(t, session)
		cleanupProjectResources(t, session.projectName)
		assertNoProjectResources(t, session.projectName)
	})

	return session
}

func stopSandboxSession(t *testing.T, session *sandboxSession) {
	t.Helper()
	if session.cmd == nil || session.cmd.Process == nil {
		return
	}

	_ = session.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() {
		done <- session.cmd.Wait()
	}()

	select {
	case <-time.After(10 * time.Second):
		_ = session.cmd.Process.Kill()
		<-done
	case <-done:
	}

	session.cmd = nil
}

func startHTTPService(t *testing.T, networkName, alias, body string) {
	t.Helper()

	containerName := fmt.Sprintf("sandboxeed-http-%d", time.Now().UnixNano())
	cmd := exec.Command(
		"docker", "run", "-d", "--rm",
		"--name", containerName,
		"--network", networkName,
		"--network-alias", alias,
		"busybox:1.36",
		"sh", "-lc", fmt.Sprintf("mkdir -p /www && printf %q >/www/index.html && httpd -f -p 80 -h /www", body),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to start HTTP service: %v\n%s", err, string(out))
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	})
}

func execInContainer(containerName, command string) (string, error) {
	out, err := exec.Command("docker", "exec", containerName, "sh", "-lc", command).CombinedOutput()
	return string(out), err
}

func waitForProjectContainer(projectName, resourceType string) (string, error) {
	return waitForProjectResource(
		[]string{
			"ps", "-a",
			"--filter", "label=" + managedLabelKey + "=" + managedLabelValue,
			"--filter", "label=" + projectLabelKey + "=" + projectName,
			"--filter", "label=" + resourceLabelKey + "=" + resourceType,
			"--format", "{{.Names}}",
		},
	)
}

func waitForProjectNetwork(projectName, resourceType string) (string, error) {
	return waitForProjectResource(
		[]string{
			"network", "ls",
			"--filter", "label=" + managedLabelKey + "=" + managedLabelValue,
			"--filter", "label=" + projectLabelKey + "=" + projectName,
			"--filter", "label=" + resourceLabelKey + "=" + resourceType,
			"--format", "{{.Name}}",
		},
	)
}

func waitForProjectResource(args []string) (string, error) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		lines, err := dockerOutputLines(args...)
		if err == nil && len(lines) > 0 {
			return lines[0], nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return "", fmt.Errorf("resource not found for docker %s", strings.Join(args, " "))
}

func ensureDockerImage(t *testing.T, image string) {
	t.Helper()

	if err := exec.Command("docker", "image", "inspect", image).Run(); err == nil {
		return
	}
	if out, err := exec.Command("docker", "pull", image).CombinedOutput(); err != nil {
		t.Fatalf("docker pull %q failed: %v\n%s", image, err, string(out))
	}
}

func ensureDockerImageOrSkip(t *testing.T, image string) {
	t.Helper()

	if err := exec.Command("docker", "image", "inspect", image).Run(); err == nil {
		return
	}
	if out, err := exec.Command("docker", "pull", image).CombinedOutput(); err != nil {
		t.Skipf("required image %q is unavailable in this environment: %v\n%s", image, err, string(out))
	}
}

func assertNoProjectResources(t *testing.T, projectName string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resources, err := projectResources(projectName)
		if err == nil && len(resources) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	resources, err := projectResources(projectName)
	if err != nil {
		t.Fatalf("project resource inspection failed: %v", err)
	}
	t.Fatalf("project resources still exist after run: %v", resources)
}

func cleanupProjectResources(t *testing.T, projectName string) {
	t.Helper()

	resources, err := projectResources(projectName)
	if err != nil {
		t.Fatalf("project resource inspection failed during cleanup: %v", err)
	}

	for _, resource := range resources {
		fields := strings.SplitN(resource, " ", 2)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "container":
			_ = exec.Command("docker", "rm", "-f", fields[1]).Run()
		case "network":
			_ = exec.Command("docker", "network", "rm", fields[1]).Run()
		case "volume":
			_ = exec.Command("docker", "volume", "rm", "-f", fields[1]).Run()
		}
	}
}

func projectResources(projectName string) ([]string, error) {
	args := []string{
		"ps", "-a",
		"--filter", "label=" + managedLabelKey + "=" + managedLabelValue,
		"--filter", "label=" + projectLabelKey + "=" + projectName,
		"--format", "container {{.Names}}",
	}
	containers, err := dockerOutputLines(args...)
	if err != nil {
		return nil, err
	}

	args = []string{
		"network", "ls",
		"--filter", "label=" + managedLabelKey + "=" + managedLabelValue,
		"--filter", "label=" + projectLabelKey + "=" + projectName,
		"--format", "network {{.Name}}",
	}
	networks, err := dockerOutputLines(args...)
	if err != nil {
		return nil, err
	}

	args = []string{
		"volume", "ls",
		"--filter", "label=" + managedLabelKey + "=" + managedLabelValue,
		"--filter", "label=" + projectLabelKey + "=" + projectName,
		"--format", "volume {{.Name}}",
	}
	volumes, err := dockerOutputLines(args...)
	if err != nil {
		return nil, err
	}

	return append(append(containers, networks...), volumes...), nil
}

func dockerOutputLines(args ...string) ([]string, error) {
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result, nil
}
