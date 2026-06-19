package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// setTempHome points HOME at a temp dir so StateDir() is isolated.
func setTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeYAML(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProxyConfigPathGlobal(t *testing.T) {
	home := setTempHome(t)
	// Only global proxy.yaml exists.
	global := filepath.Join(home, ".config", "agent-vm", "proxy.yaml")
	writeYAML(t, global, &ProxyConfig{})

	// chdir to an empty temp dir so no project config is found.
	wd := t.TempDir()
	t.Chdir(wd)

	got := ResolveProxyConfigPath("")
	if got != global {
		t.Errorf("ResolveProxyConfigPath() = %q, want %q", got, global)
	}
}

func TestResolveProxyConfigPathProject(t *testing.T) {
	setTempHome(t)
	wd := t.TempDir()
	t.Chdir(wd)

	project := filepath.Join(wd, ".agent-vm", "proxy.yaml")
	writeYAML(t, project, &ProxyConfig{})

	got := ResolveProxyConfigPath("")
	want := project
	if got != want {
		t.Errorf("ResolveProxyConfigPath() = %q, want %q", got, want)
	}
}

func TestResolveProxyConfigPathProfile(t *testing.T) {
	home := setTempHome(t)
	wd := t.TempDir()
	t.Chdir(wd)

	profilePath := filepath.Join(home, ".config", "agent-vm", "profiles", "prod.yaml")
	writeYAML(t, profilePath, &ProxyConfig{})

	got := ResolveProxyConfigPath("prod")
	if got != profilePath {
		t.Errorf("ResolveProxyConfigPath(prod) = %q, want %q", got, profilePath)
	}
}

func TestResolveProxyConfigPathProfileMissingFallsBackToGlobal(t *testing.T) {
	home := setTempHome(t)
	wd := t.TempDir()
	t.Chdir(wd)

	global := filepath.Join(home, ".config", "agent-vm", "proxy.yaml")
	writeYAML(t, global, &ProxyConfig{})

	// Profile "staging" does not exist → fall back to global.
	got := ResolveProxyConfigPath("staging")
	if got != global {
		t.Errorf("ResolveProxyConfigPath(staging) = %q, want global %q", got, global)
	}
}

func TestResolveProxyConfigPathProjectBeatsProfile(t *testing.T) {
	home := setTempHome(t)
	wd := t.TempDir()
	t.Chdir(wd)

	project := filepath.Join(wd, ".agent-vm", "proxy.yaml")
	writeYAML(t, project, &ProxyConfig{})
	profilePath := filepath.Join(home, ".config", "agent-vm", "profiles", "prod.yaml")
	writeYAML(t, profilePath, &ProxyConfig{})

	// Project should win even when a profile is given.
	got := ResolveProxyConfigPath("prod")
	if got != project {
		t.Errorf("ResolveProxyConfigPath(prod) = %q, want project %q", got, project)
	}
}

func TestLoadProxyConfigFrom(t *testing.T) {
	home := setTempHome(t)
	global := filepath.Join(home, ".config", "agent-vm", "proxy.yaml")
	writeYAML(t, global, &ProxyConfig{
		Whitelist: []string{"example.com"},
	})

	cfg := LoadProxyConfigFrom(global)
	if cfg == nil {
		t.Fatal("expected config")
	}
	if len(cfg.Whitelist) != 1 || cfg.Whitelist[0] != "example.com" {
		t.Errorf("Whitelist = %v", cfg.Whitelist)
	}
}

func TestLoadProxyConfigFromMissing(t *testing.T) {
	setTempHome(t)
	if cfg := LoadProxyConfigFrom("/nonexistent/proxy.yaml"); cfg != nil {
		t.Error("expected nil for missing file")
	}
}
