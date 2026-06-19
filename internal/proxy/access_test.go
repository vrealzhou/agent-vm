package proxy

import (
	"testing"

	"github.com/vrealzhou/agent-vm/internal/config"
)

func TestMatchDomain(t *testing.T) {
	tests := []struct {
		pattern, host string
		want           bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "api.example.com", false},
		{"example.com", "evil.com", false},
		{"*.example.com", "api.example.com", true},
		{"*.example.com", "sub.api.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "notexample.com", false},
	}
	for _, tt := range tests {
		got := matchDomain(tt.pattern, tt.host)
		if got != tt.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.pattern, tt.host, got, tt.want)
		}
	}
}

func TestMatchAnyRule(t *testing.T) {
	patterns := []string{
		"github.com",
		"*.npmjs.org",
		"https://api.example.com/v1/",
	}

	tests := []struct {
		url, host string
		want       bool
	}{
		// Domain match
		{"https://github.com/user/repo", "github.com", true},
		{"https://api.npmjs.org/pkg", "api.npmjs.org", true},
		{"https://registry.npmjs.org/pkg", "registry.npmjs.org", true},
		// URL prefix match
		{"https://api.example.com/v1/users", "api.example.com", true},
		// No match
		{"https://api.example.com/v2/users", "api.example.com", false},
		{"https://evil.com", "evil.com", false},
	}
	for _, tt := range tests {
		got := matchAnyRule(patterns, tt.url, tt.host)
		if got != tt.want {
			t.Errorf("matchAnyRule(url=%q, host=%q) = %v, want %v", tt.url, tt.host, got, tt.want)
		}
	}
}

func TestCheckAccessWhitelist(t *testing.T) {
	cfg := &config.ProxyConfig{
		Whitelist: []string{"allowed.com", "*.trusted.org"},
	}
	tests := []struct {
		url, host string
		want       bool
	}{
		{"https://allowed.com/api", "allowed.com", true},
		{"https://sub.trusted.org/path", "sub.trusted.org", true},
		{"https://blocked.com", "blocked.com", false},
		{"https://other.com", "other.com", false},
	}
	for _, tt := range tests {
		got, _ := checkAccess(cfg, tt.url, tt.host)
		if got != tt.want {
			t.Errorf("checkAccess(whitelist, url=%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestCheckAccessBlacklist(t *testing.T) {
	cfg := &config.ProxyConfig{
		Blacklist: []string{"evil.com", "*.malware.com"},
	}
	tests := []struct {
		url, host string
		want       bool
	}{
		{"https://evil.com", "evil.com", false},
		{"https://phishing.malware.com", "phishing.malware.com", false},
		{"https://good.com", "good.com", true},
		{"https://example.com", "example.com", true},
	}
	for _, tt := range tests {
		got, _ := checkAccess(cfg, tt.url, tt.host)
		if got != tt.want {
			t.Errorf("checkAccess(blacklist, url=%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestCheckAccessNoConfig(t *testing.T) {
	got, _ := checkAccess(nil, "https://anything.com", "anything.com")
	if !got {
		t.Error("checkAccess(nil cfg) should allow everything")
	}

	cfg := &config.ProxyConfig{}
	got, _ = checkAccess(cfg, "https://anything.com", "anything.com")
	if !got {
		t.Error("checkAccess(empty cfg) should allow everything")
	}
}

func TestMatchProxyHost(t *testing.T) {
	creds := map[string]map[string]string{
		"github.com":      {"Authorization": "Bearer token"},
		"*.internal.com":  {"X-API-Key": "key123"},
	}
	tests := []struct {
		host string
		want map[string]string
	}{
		{"github.com", map[string]string{"Authorization": "Bearer token"}},
		{"api.internal.com", map[string]string{"X-API-Key": "key123"}},
		{"other.com", nil},
		{"internal.com", nil},
	}
	for _, tt := range tests {
		got := matchProxyHost(creds, tt.host)
		if len(got) != len(tt.want) {
			t.Errorf("matchProxyHost(%q) = %v, want %v", tt.host, got, tt.want)
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("matchProxyHost(%q)[%q] = %q, want %q", tt.host, k, got[k], v)
			}
		}
	}
}

func TestCheckAccessWhitelistPrecedence(t *testing.T) {
	cfg := &config.ProxyConfig{
		Whitelist: []string{"allowed.com"},
		Blacklist: []string{"allowed.com"},
	}
	got, _ := checkAccess(cfg, "https://allowed.com", "allowed.com")
	if !got {
		t.Error("whitelist should take precedence: allowed.com is whitelisted, blacklist ignored")
	}
}
