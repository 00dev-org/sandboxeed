package main

import "testing"

func TestBuildArgs(t *testing.T) {
	got := buildArgs("Dockerfile.sandbox", "example:latest", ".")
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
		engine containerEngine
		want   []string
	}{
		{
			name:   "docker",
			engine: engineDocker,
			want:   []string{"rm", "-f", "sandboxeed-test"},
		},
		{
			name:   "podman",
			engine: enginePodman,
			want:   []string{"rm", "-f", "-t", "0", "sandboxeed-test"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := removeContainerArgs(tc.engine, "sandboxeed-test")
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
		Name:       "sandboxeed-test",
		Networks:   []NetworkAttachment{{Name: "internal", Alias: "proxy"}, {Name: "egress"}},
		Volumes:    []string{".:/workspace", "/tmp/cache:/cache"},
		Env:        []string{"HTTP_PROXY=http://proxy:3128", "NO_PROXY=localhost"},
		Labels:     map[string]string{"sandboxeed.managed": "true"},
		WorkDir:    "/workspace",
		Image:      "alpine:3.22",
		Cmd:        []string{"sh", "-lc", "echo ok"},
		Privileged: true,
		Memory:     "512m",
		CPUs:       "1.5",
		PidsLimit:  256,
	}

	got := runArgs(opts)
	want := []string{
		"--privileged",
		"--name", "sandboxeed-test",
		"--network", "internal",
		"--network-alias", "proxy",
		"--network", "egress",
		"-v", ".:/workspace",
		"-v", "/tmp/cache:/cache",
		"-e", "HTTP_PROXY=http://proxy:3128",
		"-e", "NO_PROXY=localhost",
		"--label", "sandboxeed.managed=true",
		"--memory", "512m",
		"--cpus", "1.5",
		"--pids-limit", "256",
		"-w", "/workspace",
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
