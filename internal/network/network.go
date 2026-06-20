package network

import (
	"strconv"

	"github.com/vrealzhou/agent-vm/internal/config"
)

// NetworkConfigToRunArgs converts the config to `container run` flags.
// extraPublish are appended after config-file publish entries (CLI overrides).
func NetworkConfigToRunArgs(cfg *config.NetworkConfig, extraPublish []string) []string {
	if cfg == nil {
		var args []string
		for _, p := range extraPublish {
			args = append(args, "-p", p)
		}
		return args
	}

	var args []string

	if cfg.Network != "" {
		netSpec := cfg.Network
		if cfg.MTU > 0 {
			netSpec += ",mtu=" + strconv.Itoa(cfg.MTU)
		}
		args = append(args, "--network", netSpec)
	} else if cfg.MTU > 0 {
		args = append(args, "--network", "bridge,mtu="+strconv.Itoa(cfg.MTU))
	}

	for _, dns := range cfg.DNS {
		args = append(args, "--dns", dns)
	}
	for _, s := range cfg.DNSSearch {
		args = append(args, "--dns-search", s)
	}
	if cfg.DNSDomain != "" {
		args = append(args, "--dns-domain", cfg.DNSDomain)
	}

	for _, p := range cfg.Publish {
		args = append(args, "-p", p)
	}
	for _, p := range extraPublish {
		args = append(args, "-p", p)
	}

	return args
}
