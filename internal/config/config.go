package config

import (
	"os"
	"path/filepath"
	"strings"
)

// StateDir returns the agent-vm state directory (~/.config/agent-vm),
// falling back to a temp directory if the home directory is unavailable.
func StateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "agent-vm")
	}
	return filepath.Join(home, ".config", "agent-vm")
}

// WorkspaceStatePath returns the path to the workspace state file for a
// container with the given name.
func WorkspaceStatePath(name string) string {
	return filepath.Join(StateDir(), name+".workspace")
}

// SaveWorkspace persists the workspace path associated with a container name.
func SaveWorkspace(name, workspace string) {
	_ = os.MkdirAll(StateDir(), 0o755)
	_ = os.WriteFile(WorkspaceStatePath(name), []byte(workspace), 0o644)
}

// LoadWorkspace reads the workspace path associated with a container name.
// It returns an empty string when no state file exists.
func LoadWorkspace(name string) string {
	data, err := os.ReadFile(WorkspaceStatePath(name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// RemoveWorkspaceState deletes the workspace state file for a container name.
func RemoveWorkspaceState(name string) {
	_ = os.Remove(WorkspaceStatePath(name))
}

// ListManagedContainers returns container names that have state files
// (i.e. were started via agent-vm).
func ListManagedContainers() []string {
	entries, err := os.ReadDir(StateDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if name := strings.TrimSuffix(e.Name(), ".workspace"); name != e.Name() {
			names = append(names, name)
		}
	}
	return names
}
