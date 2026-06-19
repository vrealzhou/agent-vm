package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProxyConfig describes the MITM proxy configuration loaded from proxy.yaml.
type ProxyConfig struct {
	Credentials map[string]map[string]string `json:"credentials" yaml:"credentials"`
	Providers   map[string]ProviderEntry      `json:"providers" yaml:"providers"`
	Whitelist   []string                      `json:"whitelist" yaml:"whitelist"`
	Blacklist   []string                      `json:"blacklist" yaml:"blacklist"`
	Passthrough []string                      `json:"passthrough" yaml:"passthrough"`
	KafkaProxy  *KafkaProxyConfig             `json:"kafka_proxy" yaml:"kafka_proxy"`
}

// ProviderEntry describes a single credential provider for a host pattern.
// Either Type+Config (inline) or Placeholder (named reference in secrets.yaml)
// should be set.
type ProviderEntry struct {
	Type        string `json:"type" yaml:"type"`
	Config      any    `json:"config,omitempty" yaml:"config,omitempty"`
	Placeholder string `json:"placeholder,omitempty" yaml:"placeholder,omitempty"`
}

// ProxyConfigPath returns the path to proxy.yaml in the state directory.
func ProxyConfigPath() string { return filepath.Join(StateDir(), "proxy.yaml") }

// LoadProxyConfigFrom reads and unmarshals proxy.yaml from the given path. It
// returns nil if the file is missing or invalid.
func LoadProxyConfigFrom(path string) *ProxyConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg ProxyConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[agent-vm] warning: invalid proxy.yaml %q: %v\n", path, err)
		return nil
	}
	return &cfg
}

// LoadProxyConfig reads and unmarshals proxy.yaml from the default path. It
// returns nil if the file is missing or invalid.
func LoadProxyConfig() *ProxyConfig {
	return LoadProxyConfigFrom(ProxyConfigPath())
}

// CACertPath returns the path to the proxy CA certificate.
func CACertPath() string { return filepath.Join(StateDir(), "proxy-ca.crt") }

// CAKeyPath returns the path to the proxy CA private key.
func CAKeyPath() string { return filepath.Join(StateDir(), "proxy-ca.key") }

// ProxyPidPath returns the pid file path for the proxy daemon of a container.
func ProxyPidPath(name string) string {
	return filepath.Join(StateDir(), name+".proxy.pid")
}

// ProxyLogPath returns the log file path for the proxy daemon of a container.
func ProxyLogPath(name string) string {
	return filepath.Join(StateDir(), name+".proxy.log")
}
