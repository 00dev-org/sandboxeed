package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const squidConfTemplate = `http_port 3128

%s
acl SSL_ports port 22
acl SSL_ports port 443
acl Safe_ports port 22
acl Safe_ports port 80
acl Safe_ports port 443
acl CONNECT method CONNECT

http_access deny !Safe_ports
http_access deny CONNECT !SSL_ports
%shttp_access deny all
`

var domainPattern = regexp.MustCompile(`^\*?\.?[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*$`)

func validateDomains(domains []string) ([]string, error) {
	validated := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			return nil, fmt.Errorf("domain entries must not be empty")
		}
		if !domainPattern.MatchString(domain) {
			return nil, fmt.Errorf("invalid domain %q", domain)
		}

		host := domain
		if strings.HasPrefix(host, "*.") {
			host = host[2:]
		} else if strings.HasPrefix(host, ".") {
			host = host[1:]
		}
		if host == "" || strings.HasPrefix(host, "-") || strings.HasSuffix(host, "-") {
			return nil, fmt.Errorf("invalid domain %q", domain)
		}
		if net.ParseIP(host) != nil {
			return nil, fmt.Errorf("ip addresses are not supported in domains: %q", domain)
		}

		validated = append(validated, domain)
	}
	return validated, nil
}

func generateSquidConf(domains []string) (string, error) {
	validated, err := validateDomains(domains)
	if err != nil {
		return "", err
	}

	var aclLines strings.Builder
	for _, d := range validated {
		aclLines.WriteString("acl allowed dstdomain ")
		aclLines.WriteString(d)
		aclLines.WriteString("\n")
	}
	allowLine := ""
	if len(validated) > 0 {
		allowLine = "http_access allow allowed\n"
	}
	return fmt.Sprintf(squidConfTemplate, aclLines.String(), allowLine), nil
}

func writeSquidConf(domains []string) (string, error) {
	conf, err := generateSquidConf(domains)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "sandboxeed-squid-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "squid.conf")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if _, err := file.WriteString(conf); err != nil {
		_ = file.Close()
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

func startProxy(rt ContainerRuntime, resources *runResources, confPath string) error {
	if err := ensureNetwork(rt, resources.internalNetwork, resources.projectName, "internal", true); err != nil {
		return err
	}
	if err := ensureNetwork(rt, resources.egressNetwork, resources.projectName, "egress", false); err != nil {
		return err
	}
	if err := rt.CreateVolume(resources.proxyConfigVol, managedLabels(resources.projectName, "proxy-config")); err != nil {
		return fmt.Errorf("failed to create proxy config volume: %w", err)
	}
	if err := rt.CopyFileToVolume(resources.proxyConfigVol, confPath, "squid.conf"); err != nil {
		return fmt.Errorf("failed to populate proxy config volume: %w", err)
	}

	sp := startSpinner("Starting proxy")
	if err := rt.RunDetached(RunOpts{
		Name: resources.proxyContainer,
		Networks: []NetworkAttachment{
			{Name: resources.internalNetwork, Alias: "proxy"},
			{Name: resources.egressNetwork},
		},
		Volumes: []string{resources.proxyConfigVol + ":/config:ro"},
		Image:   "ubuntu/squid:latest",
		Cmd:     []string{"-f", "/config/squid.conf", "-NYC"},
		Labels:  managedLabels(resources.projectName, "proxy"),
	}); err != nil {
		sp.Stop()
		return fmt.Errorf("docker run failed: %w", err)
	}

	err := waitForProxy(rt, resources.proxyContainer)
	sp.Stop()
	return err
}

func ensureNetwork(rt ContainerRuntime, name, project, label string, internal bool) error {
	labels := map[string]string{
		"com.docker.compose.network": label,
		"com.docker.compose.project": project,
		managedLabelKey:              managedLabelValue,
		projectLabelKey:              project,
		resourceLabelKey:             label,
	}
	if err := rt.CreateNetwork(name, internal, labels); err == nil {
		return verifyNetworkInternal(rt, name, internal)
	}

	if err := rt.RemoveNetwork(name); err != nil {
		return fmt.Errorf("failed to remove existing network %q: %w", name, err)
	}
	if err := rt.CreateNetwork(name, internal, labels); err != nil {
		return fmt.Errorf("failed to create network %q: %w", name, err)
	}
	return verifyNetworkInternal(rt, name, internal)
}

func verifyNetworkInternal(rt ContainerRuntime, name string, expected bool) error {
	actual, err := rt.NetworkInternal(name)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("network %q internal=%v, expected %v", name, actual, expected)
	}
	return nil
}

func waitForProxy(rt ContainerRuntime, containerName string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err := rt.ContainerStatus(containerName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		switch status {
		case "running":
			if rt.Exec(containerName, "squid", "-k", "check") == nil {
				return nil
			}
		case "exited", "dead":
			stderrf("proxy container failed to start, logs:\n")
			_ = rt.Logs(containerName)
			return fmt.Errorf("proxy container exited unexpectedly")
		}
		time.Sleep(500 * time.Millisecond)
	}
	stderrf("proxy container failed to start, logs:\n")
	_ = rt.Logs(containerName)
	return fmt.Errorf("proxy container did not reach running state")
}
