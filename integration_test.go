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
	projectMountPath := defaultProjectMountPath(projectDir)

	// Run through `script` so the app's `docker run -it` gets a tty in the test process.
	command := fmt.Sprintf("%q sh -lc %q", bin, fmt.Sprintf("pwd; test -f %s/proof.txt; echo CORE_OK", projectMountPath))
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
	if !strings.Contains(out, projectMountPath) {
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

func TestIntegrationEnvironmentVariables(t *testing.T) {
	session := startSandboxSessionWithFlags(t, "sandbox:\n  image: busybox:1.36\n  environment:\n    - SANDBOX_TEST=hello_from_config\n", "")

	out, err := execInContainer(session.sandboxContainer, `sh -lc 'echo "VAR=$SANDBOX_TEST"'`)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "VAR=hello_from_config") {
		t.Fatalf("sandbox output missing custom env var:\n%s", out)
	}
}

func TestIntegrationProxyEnvVars(t *testing.T) {
	session := startSandboxSession(t, "sandbox:\n  image: busybox:1.36\n")

	out, err := execInContainer(session.sandboxContainer, `sh -lc 'echo "HTTP=$HTTP_PROXY HTTPS=$HTTPS_PROXY NOPROXY=$NO_PROXY"'`)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "HTTP=http://proxy:3128") {
		t.Fatalf("sandbox missing HTTP_PROXY:\n%s", out)
	}
	if !strings.Contains(out, "HTTPS=http://proxy:3128") {
		t.Fatalf("sandbox missing HTTPS_PROXY:\n%s", out)
	}
	if !strings.Contains(out, "NOPROXY=localhost,127.0.0.1") {
		t.Fatalf("sandbox missing NO_PROXY:\n%s", out)
	}
}

func TestIntegrationOfflineOmitsProxyEnvVarsAndBlocksNetwork(t *testing.T) {
	session := startSandboxSessionWithFlags(t, "sandbox:\n  image: busybox:1.36\n  domains:\n    - allowed.test\n  environment:\n    - SANDBOX_TEST=hello_from_config\n", "--offline")

	out, err := execInContainer(session.sandboxContainer, `sh -lc 'echo "HTTP=${HTTP_PROXY:-} HTTPS=${HTTPS_PROXY:-} NOPROXY=${NO_PROXY:-} VAR=$SANDBOX_TEST"'`)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "HTTP= HTTPS= NOPROXY= VAR=hello_from_config") {
		t.Fatalf("sandbox output missing offline env state:\n%s", out)
	}

	out, err = execInContainer(
		session.sandboxContainer,
		`sh -lc 'unset http_proxy HTTP_PROXY https_proxy HTTPS_PROXY no_proxy NO_PROXY; if wget -T 5 -qO- http://allowed.test >/dev/null 2>&1; then echo OFFLINE_BROKEN; exit 1; else echo OFFLINE_OK; fi'`,
	)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "OFFLINE_OK") {
		t.Fatalf("sandbox output missing offline block marker:\n%s", out)
	}
}

func TestIntegrationWorkingDir(t *testing.T) {
	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: busybox:1.36\n  working_dir: /tmp\n")

	stdout, stderr, err := runSandboxeedScripted(t, projectDir, fmt.Sprintf("sh -lc %q", "pwd"))
	if err != nil {
		t.Fatalf("sandboxeed failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	if !strings.Contains(stdout, "/tmp") {
		t.Fatalf("sandbox working directory is not /tmp:\n%s", stdout)
	}
}

func TestIntegrationBuildFlag(t *testing.T) {
	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: sandboxeed-build-test\n  build:\n    dockerfile: Dockerfile.sandbox\n")
	if err := os.WriteFile(filepath.Join(projectDir, "Dockerfile.sandbox"), []byte("FROM busybox:1.36\nRUN touch /built-marker\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(Dockerfile.sandbox) error = %v", err)
	}

	// Clean up the image after the test.
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", "sandboxeed-build-test").Run()
	})

	stdout, stderr, err := runSandboxeedScripted(t, projectDir, fmt.Sprintf("--build sh -lc %q", "test -f /built-marker && echo BUILD_OK"))
	if err != nil {
		t.Fatalf("sandboxeed --build failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	if !strings.Contains(stdout, "BUILD_OK") {
		t.Fatalf("sandbox output missing build marker:\n%s", stdout)
	}
}

func TestIntegrationAutoBuild(t *testing.T) {
	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: sandboxeed-autobuild-test\n  build:\n    dockerfile: Dockerfile.sandbox\n")
	if err := os.WriteFile(filepath.Join(projectDir, "Dockerfile.sandbox"), []byte("FROM busybox:1.36\nRUN touch /auto-marker\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(Dockerfile.sandbox) error = %v", err)
	}

	// Ensure image does not exist so auto-build triggers.
	_ = exec.Command("docker", "rmi", "-f", "sandboxeed-autobuild-test").Run()
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", "sandboxeed-autobuild-test").Run()
	})

	stdout, stderr, err := runSandboxeedScripted(t, projectDir, fmt.Sprintf("sh -lc %q", "test -f /auto-marker && echo AUTOBUILD_OK"))
	if err != nil {
		t.Fatalf("sandboxeed auto-build failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	if !strings.Contains(stdout, "AUTOBUILD_OK") {
		t.Fatalf("sandbox output missing auto-build marker:\n%s", stdout)
	}
}

func TestIntegrationBuildUsesUserImageWithoutProjectConfig(t *testing.T) {
	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	bin := buildSandboxeedBinary(t)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	image := "sandboxeed-user-build-test"
	if err := os.MkdirAll(filepath.Join(homeDir, ".sandboxeed"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.sandboxeed) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, userConfigDir, userConfigFile), []byte(strings.Join([]string{
		"sandbox:",
		"  build:",
		"    dockerfile: ~/.sandboxeed/Dockerfile",
		"  image: sandboxeed-user-build-test",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".sandboxeed", "Dockerfile"), []byte("FROM busybox:1.36\nRUN touch /user-marker\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(user Dockerfile) error = %v", err)
	}

	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", image).Run()
	})

	stdout, stderr, err := runSandboxeedBinaryScripted(bin, projectDir, fmt.Sprintf("--build sh -lc %q", "test -f /user-marker && echo USERBUILD_OK"))
	if err != nil {
		t.Fatalf("sandboxeed --build failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "USERBUILD_OK") {
		t.Fatalf("sandbox output missing build marker:\n%s", stdout)
	}
}

func TestIntegrationVolumeMounts(t *testing.T) {
	session := startSandboxSession(t, "sandbox:\n  image: busybox:1.36\n  volumes:\n    - /etc/hostname:/mounted-hostname:ro\n")

	out, err := execInContainer(session.sandboxContainer, `sh -lc 'cat /mounted-hostname'`)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}

	hostname := strings.TrimSpace(out)
	if hostname == "" {
		t.Fatalf("mounted hostname file is empty")
	}
}

func TestIntegrationInspectReportsMergedConfig(t *testing.T) {
	projectDir := workspaceTempDir(t)
	bin := buildSandboxeedBinary(t)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, userConfigDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(user config dir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, userConfigDir, userConfigFile), []byte("sandbox:\n  memory: 256m\n  cpus: \"1\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: busybox:1.36\n  pids: 64\n")

	cmd := exec.Command(bin, "--inspect")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxeed inspect failed: %v\noutput:\n%s", err, string(out))
	}

	output := string(out)
	for _, part := range []string{
		"image: busybox:1.36",
		"memory: 256m",
		"cpus: \"1\"",
		"pids: 64",
	} {
		if !strings.Contains(output, part) {
			t.Fatalf("inspect output missing %q:\n%s", part, output)
		}
	}
}

func TestIntegrationNoDockerSkipsDind(t *testing.T) {
	session := startSandboxSessionWithFlags(t, "sandbox:\n  image: busybox:1.36\n  docker: true\n", "--no-docker")

	// Verify no DinD container was created for this project.
	containers, err := dockerOutputLines(
		"ps", "-a",
		"--filter", "label="+managedLabelKey+"="+managedLabelValue,
		"--filter", "label="+projectLabelKey+"="+session.projectName,
		"--filter", "label="+resourceLabelKey+"=dind",
		"--format", "{{.Names}}",
	)
	if err != nil {
		t.Fatalf("docker ps failed: %v", err)
	}
	if len(containers) > 0 {
		t.Fatalf("DinD container should not exist with --no-docker, found: %v", containers)
	}

	// Verify DOCKER_HOST is not set.
	out, err := execInContainer(session.sandboxContainer, `sh -lc 'echo "DHOST=${DOCKER_HOST:-unset}"'`)
	if err != nil {
		t.Fatalf("docker exec failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "DHOST=unset") {
		t.Fatalf("DOCKER_HOST should not be set with --no-docker:\n%s", out)
	}
}

func TestIntegrationCleanupRemovesOrphanedResources(t *testing.T) {
	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: busybox:1.36\n")

	session := startSandboxSessionFromDir(t, projectDir)

	// Kill the process without graceful shutdown so resources are orphaned.
	if session.cmd != nil && session.cmd.Process != nil {
		_ = session.cmd.Process.Kill()
		_ = session.cmd.Wait()
		session.cmd = nil
	}

	// Verify resources are still present.
	resources, err := projectResources(session.projectName)
	if err != nil {
		t.Fatalf("projectResources() error = %v", err)
	}
	if len(resources) == 0 {
		t.Fatalf("expected orphaned resources after kill, found none")
	}

	// Run cleanup with "y" piped to stdin.
	bin := buildSandboxeedBinary(t)
	cmd := exec.Command(bin, "--cleanup")
	cmd.Dir = projectDir
	cmd.Stdin = strings.NewReader("y\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxeed cleanup failed: %v\noutput:\n%s", err, string(out))
	}

	assertNoProjectResources(t, session.projectName)
}

func TestIntegrationReadOnlyBlocksWrites(t *testing.T) {
	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: busybox:1.36\n")

	script := fmt.Sprintf("if touch %s/new.txt 2>/dev/null; then echo WRITE_ALLOWED; else echo READONLY_OK; fi", defaultProjectMountPath(projectDir))
	stdout, stderr, err := runSandboxeedScripted(t, projectDir, fmt.Sprintf("--read-only sh -lc %q", script))
	if err != nil {
		t.Fatalf("sandboxeed --read-only failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	if !strings.Contains(stdout, "READONLY_OK") {
		t.Fatalf("sandbox was able to write to read-only mount:\n%s", stdout)
	}
}

func TestIntegrationReadOnlyAllowsWriteToNonMountPaths(t *testing.T) {
	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: busybox:1.36\n")

	script := "if touch /tmp/scratch.txt 2>/dev/null; then echo TMPWRITE_OK; else echo TMPWRITE_BLOCKED; fi"
	stdout, stderr, err := runSandboxeedScripted(t, projectDir, fmt.Sprintf("--read-only sh -lc %q", script))
	if err != nil {
		t.Fatalf("sandboxeed --read-only failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	if !strings.Contains(stdout, "TMPWRITE_OK") {
		t.Fatalf("sandbox could not write to non-mount path:\n%s", stdout)
	}
}

func TestIntegrationInterruptDuringStartupAfterProxyAppearsCleansResources(t *testing.T) {
	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: busybox:1.36\n")

	session := startSandboxStartupSession(t, projectDir, "")
	proxyContainer, err := waitForProjectContainer(session.projectName, "proxy")
	if err != nil {
		stopSandboxSession(t, session)
		t.Fatalf("proxy container did not appear: %v\nstdout:\n%s\nstderr:\n%s", err, session.stdout.String(), session.stderr.String())
	}
	if proxyContainer == "" {
		stopSandboxSession(t, session)
		t.Fatal("proxy container name is empty")
	}

	stopSandboxSession(t, session)
	assertNoProjectResources(t, session.projectName)
}

func TestIntegrationInterruptDuringStartupAfterDindAppearsCleansResources(t *testing.T) {
	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: busybox:1.36\n  docker: true\n")

	session := startSandboxStartupSession(t, projectDir, "")
	dindContainer, err := waitForProjectContainer(session.projectName, "dind")
	if err != nil {
		stopSandboxSession(t, session)
		t.Fatalf("dind container did not appear: %v\nstdout:\n%s\nstderr:\n%s", err, session.stdout.String(), session.stderr.String())
	}
	if dindContainer == "" {
		stopSandboxSession(t, session)
		t.Fatal("dind container name is empty")
	}

	stopSandboxSession(t, session)
	assertNoProjectResources(t, session.projectName)
}

func TestIntegrationOfflineSkipsProxyAndDindContainers(t *testing.T) {
	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, "sandbox:\n  image: busybox:1.36\n  docker: true\n  domains:\n    - allowed.test\n")

	session := startSandboxSessionFromDirWithFlags(t, projectDir, "--offline")

	resources, err := projectResources(session.projectName)
	if err != nil {
		t.Fatalf("projectResources() error = %v", err)
	}
	for _, resource := range resources {
		if strings.HasPrefix(resource, "container ") && strings.Contains(resource, "-proxy-") {
			t.Fatalf("offline run unexpectedly started proxy container: %v", resources)
		}
		if strings.HasPrefix(resource, "container ") && strings.Contains(resource, "-dind-") {
			t.Fatalf("offline run unexpectedly started dind container: %v", resources)
		}
		if strings.HasPrefix(resource, "network ") && strings.Contains(resource, "-egress-") {
			t.Fatalf("offline run unexpectedly created egress network: %v", resources)
		}
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

	dir, err := os.MkdirTemp("", "sandboxeed-int-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
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
	return runSandboxeedBinaryScripted(bin, projectDir, command)
}

func runSandboxeedBinaryScripted(bin, projectDir, command string) (string, string, error) {
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

	return startSandboxSessionWithFlags(t, config, "")
}

func startSandboxSessionWithFlags(t *testing.T, config, flags string) *sandboxSession {
	t.Helper()

	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	projectDir := workspaceTempDir(t)
	writeSandboxConfig(t, projectDir, config)

	return startSandboxSessionFromDirWithFlags(t, projectDir, flags)
}

func startSandboxSessionFromDir(t *testing.T, projectDir string) *sandboxSession {
	t.Helper()

	return startSandboxSessionFromDirWithFlags(t, projectDir, "")
}

func startSandboxSessionFromDirWithFlags(t *testing.T, projectDir, flags string) *sandboxSession {
	t.Helper()

	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")

	session := &sandboxSession{
		projectName: networkProjectName(projectDir),
		projectDir:  projectDir,
		stdout:      &bytes.Buffer{},
		stderr:      &bytes.Buffer{},
	}

	bin := buildSandboxeedBinary(t)
	shellCmd := fmt.Sprintf("trap 'exit 0' INT TERM; sleep 60")
	var command string
	if flags != "" {
		command = fmt.Sprintf("%q %s sh -lc %q", bin, flags, shellCmd)
	} else {
		command = fmt.Sprintf("%q sh -lc %q", bin, shellCmd)
	}
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
	if !hasCommandFlag(flags, "--offline") {
		session.egressNetwork, err = waitForProjectNetwork(session.projectName, "egress")
		if err != nil {
			stopSandboxSession(t, session)
			t.Fatalf("egress network did not appear: %v\nstdout:\n%s\nstderr:\n%s", err, session.stdout.String(), session.stderr.String())
		}
	}

	t.Cleanup(func() {
		stopSandboxSession(t, session)
		cleanupProjectResources(t, session.projectName)
		assertNoProjectResources(t, session.projectName)
	})

	return session
}

func hasCommandFlag(command, flag string) bool {
	for _, part := range strings.Fields(command) {
		if part == flag {
			return true
		}
	}
	return false
}

func startSandboxStartupSession(t *testing.T, projectDir, flags string) *sandboxSession {
	t.Helper()

	ensureDockerImage(t, "busybox:1.36")
	ensureDockerImageOrSkip(t, "ubuntu/squid:latest")
	ensureDockerImageOrSkip(t, "quay.io/podman/stable")

	session := &sandboxSession{
		projectName: networkProjectName(projectDir),
		projectDir:  projectDir,
		stdout:      &bytes.Buffer{},
		stderr:      &bytes.Buffer{},
	}

	bin := buildSandboxeedBinary(t)
	args := []string{}
	if flags != "" {
		args = append(args, strings.Fields(flags)...)
	}
	args = append(args, "sh", "-lc", "echo SHOULD_NOT_RUN")

	cmd := exec.Command(bin, args...)
	cmd.Dir = projectDir
	cmd.Stdout = session.stdout
	cmd.Stderr = session.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sandboxeed startup session: %v", err)
	}
	session.cmd = cmd

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
