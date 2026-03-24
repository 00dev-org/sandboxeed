package main

import "testing"

func TestBuildArgs(t *testing.T) {
	got := buildArgs("docker", engineDocker, "linux", "Dockerfile.sandbox", "example:latest", ".")
	want := []string{"build", "--no-cache", "-f", "Dockerfile.sandbox", "-t", "example:latest", "."}

	if len(got) != len(want) {
		t.Fatalf("buildArgs() len = %d, want %d\nargs = %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buildArgs()[%d] = %q, want %q\nargs = %v", i, got[i], want[i], got)
		}
	}
}

func TestBuildArgsAddsLoadForDockerCLIAgainstPodmanOnMacOS(t *testing.T) {
	got := buildArgs("docker", enginePodman, "darwin", "Dockerfile.sandbox", "example:latest", ".")
	want := []string{"build", "--no-cache", "--load", "-f", "Dockerfile.sandbox", "-t", "example:latest", "."}

	if len(got) != len(want) {
		t.Fatalf("buildArgs() len = %d, want %d\nargs = %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buildArgs()[%d] = %q, want %q\nargs = %v", i, got[i], want[i], got)
		}
	}
}

func TestShouldLoadBuiltImage(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		engine containerEngine
		goos   string
		want   bool
	}{
		{name: "docker on docker macos", binary: "docker", engine: engineDocker, goos: "darwin", want: false},
		{name: "docker on podman macos", binary: "docker", engine: enginePodman, goos: "darwin", want: true},
		{name: "docker on podman linux", binary: "docker", engine: enginePodman, goos: "linux", want: false},
		{name: "podman native macos", binary: "podman", engine: enginePodman, goos: "darwin", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldLoadBuiltImage(tc.binary, tc.engine, tc.goos); got != tc.want {
				t.Fatalf("shouldLoadBuiltImage(%q, %q, %q) = %v, want %v", tc.binary, tc.engine, tc.goos, got, tc.want)
			}
		})
	}
}

func TestClassifyContainerEngine(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   containerEngine
	}{
		{name: "docker default", output: "Docker Engine - Community", want: engineDocker},
		{name: "podman", output: "podman", want: enginePodman},
		{name: "podman descriptive", output: "Podman Engine", want: enginePodman},
		{name: "podman server output", output: "Client: Podman Engine\nVersion: 5.0.0\nServer: Podman Engine\nVersion: 5.0.0", want: enginePodman},
		{name: "docker with podman server", output: "Client:\n Version: 24.0.0\nServer:\n Engine: Podman\n Version: 5.0.0", want: enginePodman},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyContainerEngine(tc.output); got != tc.want {
				t.Fatalf("classifyContainerEngine(%q) = %q, want %q", tc.output, got, tc.want)
			}
		})
	}
}

func TestRemoveContainerArgs(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		want   []string
	}{
		{
			name:   "docker",
			binary: "docker",
			want:   []string{"rm", "-f", "sandboxeed-test"},
		},
		{
			name:   "podman",
			binary: "podman",
			want:   []string{"rm", "-f", "-t", "0", "sandboxeed-test"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := removeContainerArgs(tc.binary, "sandboxeed-test")
			if len(got) != len(tc.want) {
				t.Fatalf("removeContainerArgs() len = %d, want %d\nargs = %v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("removeContainerArgs()[%d] = %q, want %q\nargs = %v", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestRunArgsIncludesConfiguredOptions(t *testing.T) {
	opts := RunOpts{
		Name:        "sandboxeed-test",
		Networks:    []NetworkAttachment{{Name: "internal", Alias: "proxy"}, {Name: "egress"}},
		Volumes:     []string{".:/home/node/demo", "/tmp/cache:/cache"},
		Env:         []string{"HTTP_PROXY=http://proxy:3128", "NO_PROXY=localhost"},
		Labels:      map[string]string{"sandboxeed.managed": "true"},
		WorkDir:     "/home/node/demo",
		Image:       "alpine:3.22",
		Cmd:         []string{"sh", "-lc", "echo ok"},
		CapAdd:      []string{"SYS_ADMIN"},
		Devices:     []string{"/dev/fuse"},
		SecurityOpt: []string{"seccomp=unconfined"},
		Memory:      "512m",
		CPUs:        "1.5",
		PidsLimit:   256,
	}

	got := runArgs(opts)
	want := []string{
		"--cap-add", "SYS_ADMIN",
		"--device", "/dev/fuse",
		"--security-opt", "seccomp=unconfined",
		"--name", "sandboxeed-test",
		"--network", "internal",
		"--network-alias", "proxy",
		"--network", "egress",
		"-v", ".:/home/node/demo",
		"-v", "/tmp/cache:/cache",
		"-e", "HTTP_PROXY=http://proxy:3128",
		"-e", "NO_PROXY=localhost",
		"--label", "sandboxeed.managed=true",
		"--memory", "512m",
		"--cpus", "1.5",
		"--pids-limit", "256",
		"-w", "/home/node/demo",
		"alpine:3.22",
		"sh", "-lc", "echo ok",
	}

	if len(got) != len(want) {
		t.Fatalf("runArgs() len = %d, want %d\nargs = %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runArgs()[%d] = %q, want %q\nargs = %v", i, got[i], want[i], got)
		}
	}
}

func TestManagedLabelsIncludesExpectedValues(t *testing.T) {
	got := managedLabels("demo", "proxy")

	if got[managedLabelKey] != managedLabelValue {
		t.Fatalf("managedLabels() managed label = %q, want %q", got[managedLabelKey], managedLabelValue)
	}
	if got[projectLabelKey] != "demo" {
		t.Fatalf("managedLabels() project label = %q, want %q", got[projectLabelKey], "demo")
	}
	if got[resourceLabelKey] != "proxy" {
		t.Fatalf("managedLabels() resource label = %q, want %q", got[resourceLabelKey], "proxy")
	}
}
