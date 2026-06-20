package proxy

import (
	"fmt"
	"strings"

	"github.com/vrealzhou/agent-vm/internal/config"
)

// checkAccess returns (allowed, reason). If whitelist is set, only matching
// entries are allowed. Otherwise, blacklist is enforced.
func checkAccess(cfg *config.ProxyConfig, fullURL, host string) (bool, string) {
	if cfg == nil {
		return true, ""
	}
	if len(cfg.Whitelist) > 0 {
		if matchAnyRule(cfg.Whitelist, fullURL, host) {
			return true, ""
		}
		return false, fmt.Sprintf("not in whitelist: %s", host)
	}
	if matchAnyRule(cfg.Blacklist, fullURL, host) {
		return false, fmt.Sprintf("blocked by blacklist: %s", host)
	}
	return true, ""
}

// matchAnyRule checks if any pattern matches the URL or host.
// Patterns containing "/" are treated as URL-prefix rules.
// Patterns without "/" are treated as domain rules (supports wildcard).
func matchAnyRule(patterns []string, fullURL, host string) bool {
	for _, p := range patterns {
		if strings.Contains(p, "/") {
			if strings.HasPrefix(fullURL, p) {
				return true
			}
		} else if matchDomain(p, host) {
			return true
		}
	}
	return false
}

func matchDomain(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(host, pattern[1:])
	}
	return false
}

func matchProxyHost(creds map[string]map[string]string, host string) map[string]string {
	if h, ok := creds[host]; ok {
		return h
	}
	for pattern, headers := range creds {
		if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) {
			return headers
		}
	}
	return nil
}
