package config

import (
	"os"
	"path/filepath"
)

// ResolveProxyConfigPath finds the proxy.yaml to use.
// Priority: project .agent-vm/proxy.yaml > profile > global.
func ResolveProxyConfigPath(profile string) string {
	// 1. Project-level: ./.agent-vm/proxy.yaml
	if cwd, err := os.Getwd(); err == nil {
		projectPath := filepath.Join(cwd, ".agent-vm", "proxy.yaml")
		if fileExists(projectPath) {
			return projectPath
		}
	}
	// 2. Profile-level: ~/.config/agent-vm/profiles/<name>.yaml
	if profile != "" {
		profilePath := filepath.Join(StateDir(), "profiles", profile+".yaml")
		if fileExists(profilePath) {
			return profilePath
		}
	}
	// 3. Global: ~/.config/agent-vm/proxy.yaml
	return ProxyConfigPath()
}

// fileExists reports whether the given path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
