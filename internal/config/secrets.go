package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SecretEntry holds credentials and environment variables for a single host
// pattern, loaded from secrets.yaml.
type SecretEntry struct {
	Username string            `json:"username" yaml:"username"`
	Secret   string            `json:"secret" yaml:"secret"`
	Env      map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
}

// SecretsConfig describes the credential server configuration loaded from
// secrets.yaml.
type SecretsConfig struct {
	Credentials map[string]SecretEntry `json:"credentials" yaml:"credentials"`
	Env         map[string]string       `json:"env" yaml:"env"`
}

// SecretsConfigPath returns the path to secrets.yaml in the state directory.
func SecretsConfigPath() string { return filepath.Join(StateDir(), "secrets.yaml") }

// LoadSecretsConfig reads and unmarshals secrets.yaml. It returns nil if the
// file is missing or invalid.
func LoadSecretsConfig() *SecretsConfig {
	data, err := os.ReadFile(SecretsConfigPath())
	if err != nil {
		return nil
	}
	var cfg SecretsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

// CredPidPath returns the pid file path for the credential daemon of a container.
func CredPidPath(name string) string {
	return filepath.Join(StateDir(), name+".cred.pid")
}

// CredLogPath returns the log file path for the credential daemon of a container.
func CredLogPath(name string) string {
	return filepath.Join(StateDir(), name+".cred.log")
}
