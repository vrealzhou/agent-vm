package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// NetworkConfig defines container networking. Loaded from network.yaml.
// All fields are optional — empty means use Apple Container defaults.
type NetworkConfig struct {
	Network   string   `yaml:"network"`
	DNS       []string `yaml:"dns"`
	DNSSearch []string `yaml:"dns_search"`
	DNSDomain string   `yaml:"dns_domain"`
	Publish   []string `yaml:"publish"`
	MTU       int      `yaml:"mtu"`
}

// NetworkConfigPath returns the path to network.yaml in the state directory.
func NetworkConfigPath() string { return filepath.Join(StateDir(), "network.yaml") }

// LoadNetworkConfig reads and unmarshals network.yaml. It returns nil if the
// file is missing or invalid.
func LoadNetworkConfig() *NetworkConfig {
	data, err := os.ReadFile(NetworkConfigPath())
	if err != nil {
		return nil
	}
	var cfg NetworkConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}
