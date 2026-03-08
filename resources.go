package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const managedLabelKey = "sandboxeed.managed"
const managedLabelValue = "true"
const resourceLabelKey = "sandboxeed.resource"
const projectLabelKey = "sandboxeed.project"

type runResources struct {
	projectDir       string
	projectName      string
	sandboxContainer string
	proxyContainer   string
	proxyConfigVol   string
	dindContainer    string
	dindVolume       string
	internalNetwork  string
	egressNetwork    string
}

func defaultSandboxImage(projectDir string) string {
	project := strings.ToLower(filepath.Base(projectDir))
	project = strings.TrimSpace(project)
	if project == "" || project == "." || project == string(filepath.Separator) {
		project = "workspace"
	}

	var b strings.Builder
	lastDash := false
	for _, r := range project {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}

	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "workspace"
	}
	return name + "-sandboxeed"
}

func expandVolumeSpec(projectDir, spec string) string {
	host, rest, ok := strings.Cut(spec, ":")
	if !ok {
		return spec
	}

	switch {
	case host == ".":
		host = projectDir
	case strings.HasPrefix(host, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return spec
		}
		host = filepath.Join(home, host[2:])
	case strings.HasPrefix(host, "./"), strings.HasPrefix(host, "../"):
		host = filepath.Join(projectDir, host)
	case filepath.IsAbs(host):
	case strings.Contains(host, "/"):
		host = filepath.Join(projectDir, host)
	default:
		return spec
	}

	return filepath.Clean(host) + ":" + rest
}

func networkProjectName(dir string) string {
	project := filepath.Base(dir)
	sum := sha256.Sum256([]byte(dir))
	return fmt.Sprintf("%s-%x", project, sum[:4])
}

func newRunToken() string {
	var buf [5]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strings.ToLower(strconv.FormatInt(time.Now().UnixNano(), 36))
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf[:]))
}

func newRunResources(dir string) *runResources {
	project := networkProjectName(dir)
	runToken := newRunToken()
	return &runResources{
		projectDir:       dir,
		projectName:      project,
		sandboxContainer: fmt.Sprintf("%s-sandbox-%s", project, runToken),
		proxyContainer:   fmt.Sprintf("%s-proxy-%s", project, runToken),
		proxyConfigVol:   fmt.Sprintf("%s-proxy-config-%s", project, runToken),
		dindContainer:    fmt.Sprintf("%s-dind-%s", project, runToken),
		dindVolume:       fmt.Sprintf("%s-dind-data-%s", project, runToken),
		internalNetwork:  fmt.Sprintf("%s-internal-%s", project, runToken),
		egressNetwork:    fmt.Sprintf("%s-egress-%s", project, runToken),
	}
}

func managedLabels(projectName, resourceType string) map[string]string {
	return map[string]string{
		managedLabelKey:  managedLabelValue,
		projectLabelKey:  projectName,
		resourceLabelKey: resourceType,
	}
}
