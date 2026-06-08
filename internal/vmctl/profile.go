package vmctl

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type EnvironmentProfile struct {
	Name           string            `yaml:"name"`
	Description    string            `yaml:"description"`
	Base           string            `yaml:"base"`
	SystemPackages []string          `yaml:"system_packages"`
	BrewPackages   []string          `yaml:"brew_packages"`
	PostInstall    string            `yaml:"post_install"`
	Env            map[string]string `yaml:"env"`
}

func ResolveProfile(configDir, name string) (EnvironmentProfile, error) {
	candidates := []string{
		filepath.Join(".", "profiles", name+".yaml"),
		filepath.Join(configDir, "profiles", name+".yaml"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return LoadProfile(path)
		}
	}
	return EnvironmentProfile{}, fmt.Errorf("environment profile %q not found (searched: %v)", name, candidates)
}

func LoadProfile(path string) (EnvironmentProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EnvironmentProfile{}, fmt.Errorf("failed to read profile %q: %w", path, err)
	}
	var profile EnvironmentProfile
	if err := yaml.Unmarshal(data, &profile); err != nil {
		return EnvironmentProfile{}, fmt.Errorf("failed to parse profile %q: %w", path, err)
	}
	if profile.Name == "" {
		profile.Name = nameToProfileName(path)
	}
	if profile.Base == "" {
		profile.Base = "alpine:latest"
	}
	return profile, nil
}

func ListProfiles(configDir string) ([]EnvironmentProfile, error) {
	var profiles []EnvironmentProfile
	dirs := []string{
		filepath.Join(".", "profiles"),
		filepath.Join(configDir, "profiles"),
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
				continue
			}
			name := entry.Name()[:len(entry.Name())-5]
			if seen[name] {
				continue
			}
			seen[name] = true
			p, err := LoadProfile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			profiles = append(profiles, p)
		}
	}
	return profiles, nil
}

func ProfileImageName(profile EnvironmentProfile) string {
	return "agent-vm-" + profile.Name
}

func nameToProfileName(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext != "" {
		return base[:len(base)-len(ext)]
	}
	return base
}
