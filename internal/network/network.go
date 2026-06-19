package network

import (
	"strings"

	"github.com/vrealzhou/agent-vm/internal/config"
)

// NetworkConfigToRunArgs converts the config to `container run` flags.
// extraPublish are appended after config-file publish entries (CLI overrides).
func NetworkConfigToRunArgs(cfg *config.NetworkConfig, extraPublish []string) []string {
	if cfg == nil {
		// Still apply CLI-only publish flags
		var args []string
		for _, p := range extraPublish {
			args = append(args, "-p", p)
		}
		return args
	}

	var args []string

	// Network name (with optional MTU)
	if cfg.Network != "" {
		netSpec := cfg.Network
		if cfg.MTU > 0 {
			netSpec += fmtMTU(cfg.MTU)
		}
		args = append(args, "--network", netSpec)
	} else if cfg.MTU > 0 {
		args = append(args, "--network", "bridge"+fmtMTU(cfg.MTU))
	}

	// DNS servers
	for _, dns := range cfg.DNS {
		args = append(args, "--dns", dns)
	}

	// DNS search domains
	for _, s := range cfg.DNSSearch {
		args = append(args, "--dns-search", s)
	}

	// DNS domain
	if cfg.DNSDomain != "" {
		args = append(args, "--dns-domain", cfg.DNSDomain)
	}

	// Port publishing: config file first, then CLI overrides
	for _, p := range cfg.Publish {
		args = append(args, "-p", p)
	}
	for _, p := range extraPublish {
		args = append(args, "-p", p)
	}

	return args
}

func fmtMTU(mtu int) string {
	return strings.Join([]string{"", "mtu=" + intToStr(mtu)}, ",")
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	negative := i < 0
	if negative {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
