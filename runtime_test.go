package main

import "testing"

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
