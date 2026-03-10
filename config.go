package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const configFile = "sandboxeed.yaml"
const userConfigFile = ".sandboxeed.yaml"

var memoryPattern = regexp.MustCompile(`(?i)^\d+(\.\d+)?([kmgtp]?b|[kmgtp])?$`)

type SandboxConfig struct {
	Build struct {
		Dockerfile string `yaml:"dockerfile"`
	} `yaml:"build"`
	Image       string   `yaml:"image"`
	Volumes     []string `yaml:"volumes"`
	Environment []string `yaml:"environment"`
	WorkingDir  string   `yaml:"working_dir"`
	Domains     []string `yaml:"domains"`
	Docker      bool     `yaml:"docker"`
	Memory      string   `yaml:"memory"`
	CPUs        string   `yaml:"cpus"`
	Pids        int      `yaml:"pids"`
}

type Config struct {
	Sandbox SandboxConfig `yaml:"sandbox"`
}

type UserSandboxConfig struct {
	Build struct {
		Dockerfile string `yaml:"dockerfile"`
	} `yaml:"build"`
	Image       string   `yaml:"image"`
	Volumes     []string `yaml:"volumes"`
	Environment []string `yaml:"environment"`
	Domains     []string `yaml:"domains"`
	Memory      string   `yaml:"memory"`
	CPUs        string   `yaml:"cpus"`
	Pids        int      `yaml:"pids"`
}

type UserConfig struct {
	Sandbox UserSandboxConfig `yaml:"sandbox"`
}

func defaultConfig() *Config {
	return &Config{}
}

func loadConfig() (*Config, error) {
	cfg := defaultConfig()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve home directory: %w", err)
	}
	userPath := filepath.Join(homeDir, userConfigFile)
	userCfg, found, err := loadUserConfigFile(userPath)
	if err != nil {
		return nil, err
	}
	if found {
		cfg.Sandbox.Build = userCfg.Sandbox.Build
		cfg.Sandbox.Image = strings.TrimSpace(userCfg.Sandbox.Image)
		cfg.Sandbox.Volumes = mergeVolumeSpecs(cfg.Sandbox.Volumes, userCfg.Sandbox.Volumes)
		cfg.Sandbox.Environment = mergeEnvironment(cfg.Sandbox.Environment, userCfg.Sandbox.Environment)
		cfg.Sandbox.Domains = mergeDomains(cfg.Sandbox.Domains, userCfg.Sandbox.Domains)
		cfg.Sandbox.Memory = strings.TrimSpace(userCfg.Sandbox.Memory)
		cfg.Sandbox.CPUs = strings.TrimSpace(userCfg.Sandbox.CPUs)
		cfg.Sandbox.Pids = userCfg.Sandbox.Pids
	}

	projectCfg, found, err := loadProjectConfigFile(configFile)
	if err != nil {
		return nil, err
	}
	if !found {
		if err := validateConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	cfg.Sandbox.WorkingDir = projectCfg.Sandbox.WorkingDir
	cfg.Sandbox.Docker = projectCfg.Sandbox.Docker
	cfg.Sandbox.Volumes = mergeVolumeSpecs(cfg.Sandbox.Volumes, projectCfg.Sandbox.Volumes)
	cfg.Sandbox.Environment = mergeEnvironment(cfg.Sandbox.Environment, projectCfg.Sandbox.Environment)
	cfg.Sandbox.Domains = mergeDomains(cfg.Sandbox.Domains, projectCfg.Sandbox.Domains)
	switch {
	case strings.TrimSpace(projectCfg.Sandbox.Build.Dockerfile) != "":
		cfg.Sandbox.Build = projectCfg.Sandbox.Build
		cfg.Sandbox.Image = strings.TrimSpace(projectCfg.Sandbox.Image)
	case strings.TrimSpace(projectCfg.Sandbox.Image) != "":
		cfg.Sandbox.Build.Dockerfile = ""
		cfg.Sandbox.Image = strings.TrimSpace(projectCfg.Sandbox.Image)
	}
	if strings.TrimSpace(projectCfg.Sandbox.Memory) != "" {
		cfg.Sandbox.Memory = strings.TrimSpace(projectCfg.Sandbox.Memory)
	}
	if strings.TrimSpace(projectCfg.Sandbox.CPUs) != "" {
		cfg.Sandbox.CPUs = strings.TrimSpace(projectCfg.Sandbox.CPUs)
	}
	if projectCfg.Sandbox.Pids != 0 {
		cfg.Sandbox.Pids = projectCfg.Sandbox.Pids
	}
	if strings.TrimSpace(cfg.Sandbox.Image) == "" {
		return nil, fmt.Errorf("invalid %s: sandbox.image is required", configFile)
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadProjectConfigFile(path string) (*Config, bool, error) {
	var cfg Config
	found, err := loadYAMLFile(path, &cfg)
	if err != nil || !found {
		return nil, found, err
	}
	if err := validateProjectConfig(&cfg); err != nil {
		return nil, true, fmt.Errorf("invalid %s: %w", path, err)
	}
	return &cfg, true, nil
}

func loadUserConfigFile(path string) (*UserConfig, bool, error) {
	var cfg UserConfig
	found, err := loadYAMLFile(path, &cfg)
	if err != nil || !found {
		return nil, found, rewriteUserConfigError(path, err)
	}
	if err := validateUserConfig(&cfg); err != nil {
		return nil, true, fmt.Errorf("invalid %s: %w", path, err)
	}
	return &cfg, true, nil
}

func loadYAMLFile(path string, out any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return false, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return true, nil
}

func rewriteUserConfigError(path string, err error) error {
	if err == nil {
		return nil
	}

	typeErrors := userConfigTypeErrors(err.Error())
	if len(typeErrors) == 0 {
		return err
	}

	unsupported := make([]string, 0, len(typeErrors))
	for _, msg := range typeErrors {
		field := extractUnknownField(msg)
		if field == "" {
			return err
		}
		unsupported = append(unsupported, field)
	}

	return fmt.Errorf(
		"user config %s only supports sandbox.build.dockerfile, sandbox.image, sandbox.volumes, sandbox.environment, sandbox.domains, sandbox.memory, sandbox.cpus, and sandbox.pids; unsupported fields: %s",
		path,
		strings.Join(unsupported, ", "),
	)
}

func userConfigTypeErrors(message string) []string {
	lines := strings.Split(message, "\n")
	errorsList := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "field ") && strings.Contains(line, "not found in type") {
			errorsList = append(errorsList, line)
		}
	}
	return errorsList
}

func extractUnknownField(msg string) string {
	const marker = "field "
	start := strings.Index(msg, marker)
	if start < 0 {
		return ""
	}
	rest := msg[start+len(marker):]
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' {
		rest = rest[1:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return ""
		}
		return rest[:end]
	}
	end := strings.Index(rest, " not found in type")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func validateProjectConfig(cfg *Config) error {
	if err := validateImageBuildPair(cfg.Sandbox.Image, cfg.Sandbox.Build.Dockerfile); err != nil {
		return err
	}
	if err := validateLimits(cfg.Sandbox.Memory, cfg.Sandbox.CPUs, cfg.Sandbox.Pids); err != nil {
		return err
	}
	return nil
}

func validateConfig(cfg *Config) error {
	if err := validateImageBuildPair(cfg.Sandbox.Image, cfg.Sandbox.Build.Dockerfile); err != nil {
		return err
	}
	if err := validateLimits(cfg.Sandbox.Memory, cfg.Sandbox.CPUs, cfg.Sandbox.Pids); err != nil {
		return err
	}
	return nil
}

func validateUserConfig(cfg *UserConfig) error {
	if err := validateImageBuildPair(cfg.Sandbox.Image, cfg.Sandbox.Build.Dockerfile); err != nil {
		return err
	}
	return validateLimits(cfg.Sandbox.Memory, cfg.Sandbox.CPUs, cfg.Sandbox.Pids)
}

func mergeVolumeSpecs(base, override []string) []string {
	return mergeOrdered(base, override, volumeMergeKey)
}

func mergeEnvironment(base, override []string) []string {
	return mergeOrdered(base, override, environmentMergeKey)
}

func mergeDomains(base, override []string) []string {
	return mergeOrdered(base, override, func(value string) string {
		return strings.TrimSpace(value)
	})
}

func mergeOrdered(base, override []string, keyFn func(string) string) []string {
	merged := append([]string(nil), base...)
	indexByKey := make(map[string]int, len(merged))
	for i, value := range merged {
		indexByKey[keyFn(value)] = i
	}

	for _, value := range override {
		key := keyFn(value)
		if idx, ok := indexByKey[key]; ok {
			merged[idx] = value
			continue
		}
		indexByKey[key] = len(merged)
		merged = append(merged, value)
	}
	return merged
}

func volumeMergeKey(spec string) string {
	spec = strings.TrimSpace(spec)
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return spec
	}
	return parts[1]
}

func environmentMergeKey(spec string) string {
	spec = strings.TrimSpace(spec)
	if idx := strings.Index(spec, "="); idx >= 0 {
		return spec[:idx]
	}
	return spec
}

func validateLimits(memory, cpus string, pids int) error {
	if memory != "" && !memoryPattern.MatchString(strings.TrimSpace(memory)) {
		return fmt.Errorf("sandbox.memory must be a valid Docker memory value")
	}
	if value := strings.TrimSpace(cpus); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("sandbox.cpus must be a positive number")
		}
	}
	if pids < 0 {
		return fmt.Errorf("sandbox.pids must be zero or greater")
	}
	return nil
}

func validateImageBuildPair(image, dockerfile string) error {
	image = strings.TrimSpace(image)
	dockerfile = strings.TrimSpace(dockerfile)

	if dockerfile != "" && image == "" {
		return fmt.Errorf("sandbox.image is required when sandbox.build.dockerfile is set")
	}
	return nil
}
