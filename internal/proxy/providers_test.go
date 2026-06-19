package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vrealzhou/agent-vm/internal/config"
	"github.com/vrealzhou/agent-vm/internal/secrets"
)

func TestHeaderProvider(t *testing.T) {
	p := &HeaderProvider{Headers: map[string]string{
		"Authorization": "Bearer xyz",
		"X-Custom":      "val",
	}}
	req, _ := http.NewRequest("GET", "https://api.example.com/v1", nil)
	if err := p.Transform(req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "Bearer xyz" {
		t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
	}
	if req.Header.Get("X-Custom") != "val" {
		t.Errorf("X-Custom = %q", req.Header.Get("X-Custom"))
	}
}

func TestBodyProvider(t *testing.T) {
	p := &BodyProvider{Path: "auth.token", Value: "injected-secret"}
	req, _ := http.NewRequest("POST", "https://api.example.com/login",
		strings.NewReader(`{"user":"alice","auth":{"name":"default"}}`))
	req.Header.Set("Content-Type", "application/json")

	if err := p.Transform(req); err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(req.Body)
	var result map[string]any
	json.Unmarshal(body, &result)

	auth, ok := result["auth"].(map[string]any)
	if !ok {
		t.Fatal("auth field missing or wrong type")
	}
	if auth["token"] != "injected-secret" {
		t.Errorf("auth.token = %v, want injected-secret", auth["token"])
	}

	// Original fields preserved
	if result["user"] != "alice" {
		t.Errorf("user = %v, want alice", result["user"])
	}
	if auth["name"] != "default" {
		t.Errorf("auth.name = %v, want default", auth["name"])
	}
}

func TestBodyProviderNestedCreate(t *testing.T) {
	p := &BodyProvider{Path: "credentials.api_key", Value: "key123"}
	req, _ := http.NewRequest("POST", "https://api.example.com",
		strings.NewReader(`{"data":"ok"}`))
	req.Header.Set("Content-Type", "application/json")

	if err := p.Transform(req); err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(req.Body)
	var result map[string]any
	json.Unmarshal(body, &result)

	creds, ok := result["credentials"].(map[string]any)
	if !ok {
		t.Fatal("credentials field not created")
	}
	if creds["api_key"] != "key123" {
		t.Errorf("credentials.api_key = %v, want key123", creds["api_key"])
	}
}

func TestAWSSigV4Provider(t *testing.T) {
	p := &AWSSigV4Provider{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:    "us-east-1",
		Service:   "iam",
	}
	req, _ := http.NewRequest("GET", "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	req.Host = "iam.amazonaws.com"

	if err := p.Transform(req); err != nil {
		t.Fatal(err)
	}

	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("Authorization missing AWS4 prefix: %s", auth)
	}
	if !strings.Contains(auth, "Credential=AKIAIOSFODNN7EXAMPLE/") {
		t.Errorf("Authorization missing access key: %s", auth)
	}
	if !strings.Contains(auth, "us-east-1/iam/") {
		t.Errorf("Authorization missing region/service: %s", auth)
	}
	if req.Header.Get("X-Amz-Date") == "" {
		t.Error("X-Amz-Date not set")
	}
	if req.Header.Get("X-Amz-Content-Sha256") == "" {
		t.Error("X-Amz-Content-Sha256 not set")
	}
}

func TestResolveProviderLegacy(t *testing.T) {
	cfg := &config.ProxyConfig{
		Credentials: map[string]map[string]string{
			"github.com": {"Authorization": "Bearer legacy"},
		},
	}
	p := ResolveProvider(cfg, "github.com")
	if p == nil {
		t.Fatal("expected provider for github.com")
	}
	req, _ := http.NewRequest("GET", "https://github.com", nil)
	p.Transform(req)
	if req.Header.Get("Authorization") != "Bearer legacy" {
		t.Error("legacy credential not applied")
	}
}

func TestResolveProviderTyped(t *testing.T) {
	cfg := &config.ProxyConfig{
		Providers: map[string]config.ProviderEntry{
			"api.example.com": {
				Type: "header",
				Config: json.RawMessage(`{"headers":{"X-API-Key":"typed-key"}}`),
			},
		},
	}
	p := ResolveProvider(cfg, "api.example.com")
	if p == nil {
		t.Fatal("expected provider for api.example.com")
	}
	req, _ := http.NewRequest("GET", "https://api.example.com", nil)
	p.Transform(req)
	if req.Header.Get("X-API-Key") != "typed-key" {
		t.Error("typed provider not applied")
	}
}

func TestResolveProviderWildcard(t *testing.T) {
	cfg := &config.ProxyConfig{
		Providers: map[string]config.ProviderEntry{
			"*.internal.com": {
				Type:   "header",
				Config: json.RawMessage(`{"headers":{"X-Internal":"yes"}}`),
			},
		},
	}
	p := ResolveProvider(cfg, "api.internal.com")
	if p == nil {
		t.Fatal("expected provider for api.internal.com via wildcard")
	}
}

func TestResolveProviderNoMatch(t *testing.T) {
	cfg := &config.ProxyConfig{
		Credentials: map[string]map[string]string{
			"github.com": {"Authorization": "Bearer"},
		},
	}
	p := ResolveProvider(cfg, "other.com")
	if p != nil {
		t.Error("expected nil provider for unmatched host")
	}
}

func TestRegisterCustomProvider(t *testing.T) {
	RegisterProvider("custom-test", func(cfg json.RawMessage) (CredentialProvider, error) {
		return &HeaderProvider{Headers: map[string]string{"X-Custom-Type": "custom-value"}}, nil
	})

	cfg := &config.ProxyConfig{
		Providers: map[string]config.ProviderEntry{
			"custom.example.com": {
				Type:   "custom-test",
				Config: json.RawMessage(`{}`),
			},
		},
	}
	p := ResolveProvider(cfg, "custom.example.com")
	if p == nil {
		t.Fatal("expected custom provider")
	}
	req, _ := http.NewRequest("GET", "https://custom.example.com", nil)
	p.Transform(req)
	if req.Header.Get("X-Custom-Type") != "custom-value" {
		t.Error("custom provider not applied")
	}
}

// ── Placeholder resolution ──

// writeTestSecrets sets HOME to a temp dir and writes a secrets.yaml there
// using the store's own Save method. The temp dir is cleaned up automatically.
func writeTestSecrets(t *testing.T, store *secrets.Store) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(config.StateDir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPlaceholderConfigHeader(t *testing.T) {
	p := secrets.Placeholder{
		Type:   "header",
		Fields: map[string]string{"Authorization": "Bearer zzz"},
	}
	data := buildPlaceholderConfig(p)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	headers, ok := got["headers"].(map[string]any)
	if !ok {
		t.Fatalf("expected headers map, got %v", got)
	}
	if headers["Authorization"] != "Bearer zzz" {
		t.Errorf("Authorization = %v", headers["Authorization"])
	}
}

func TestBuildPlaceholderConfigAWS(t *testing.T) {
	p := secrets.Placeholder{
		Type: "aws-sigv4",
		Fields: map[string]string{
			"access_key": "AKIA",
			"secret_key": "shh",
			"region":     "us-east-1",
			"service":    "s3",
		},
	}
	data := buildPlaceholderConfig(p)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["access_key"] != "AKIA" {
		t.Errorf("access_key = %v", got["access_key"])
	}
	if got["region"] != "us-east-1" {
		t.Errorf("region = %v", got["region"])
	}
}

func TestResolveProviderPlaceholderHeader(t *testing.T) {
	store := &secrets.Store{Placeholders: map[string]secrets.Placeholder{
		"my-headers": {Type: "header", Fields: map[string]string{"Authorization": "Bearer placeholder"}},
	}}
	writeTestSecrets(t, store)

	cfg := &config.ProxyConfig{
		Providers: map[string]config.ProviderEntry{
			"api.example.com": {Placeholder: "my-headers"},
		},
	}
	p := ResolveProvider(cfg, "api.example.com")
	if p == nil {
		t.Fatal("expected provider resolved from placeholder")
	}
	req, _ := http.NewRequest("GET", "https://api.example.com", nil)
	p.Transform(req)
	if req.Header.Get("Authorization") != "Bearer placeholder" {
		t.Errorf("Authorization = %q, want Bearer placeholder", req.Header.Get("Authorization"))
	}
}

func TestResolveProviderPlaceholderAWS(t *testing.T) {
	store := &secrets.Store{Placeholders: map[string]secrets.Placeholder{
		"aws-prod": {
			Type: "aws-sigv4",
			Fields: map[string]string{
				"access_key": "AKIAIOSFODNN7EXAMPLE",
				"secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"region":     "us-east-1",
				"service":    "iam",
			},
		},
	}}
	writeTestSecrets(t, store)

	cfg := &config.ProxyConfig{
		Providers: map[string]config.ProviderEntry{
			"iam.amazonaws.com": {Placeholder: "aws-prod"},
		},
	}
	p := ResolveProvider(cfg, "iam.amazonaws.com")
	if p == nil {
		t.Fatal("expected aws-sigv4 provider resolved from placeholder")
	}
	req, _ := http.NewRequest("GET", "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	req.Host = "iam.amazonaws.com"
	if err := p.Transform(req); err != nil {
		t.Fatal(err)
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Errorf("expected AWS4 signature, got %q", auth)
	}
}

func TestResolveProviderPlaceholderMissing(t *testing.T) {
	writeTestSecrets(t, &secrets.Store{Placeholders: map[string]secrets.Placeholder{}})

	cfg := &config.ProxyConfig{
		Providers: map[string]config.ProviderEntry{
			"api.example.com": {Placeholder: "does-not-exist"},
		},
	}
	if p := ResolveProvider(cfg, "api.example.com"); p != nil {
		t.Error("expected nil provider for missing placeholder")
	}
}

func TestResolveProviderPlaceholderWildcard(t *testing.T) {
	store := &secrets.Store{Placeholders: map[string]secrets.Placeholder{
		"api-key": {Type: "header", Fields: map[string]string{"X-API-Key": "wildcard-key"}},
	}}
	writeTestSecrets(t, store)

	cfg := &config.ProxyConfig{
		Providers: map[string]config.ProviderEntry{
			"*.internal.com": {Placeholder: "api-key"},
		},
	}
	p := ResolveProvider(cfg, "service.internal.com")
	if p == nil {
		t.Fatal("expected provider via wildcard placeholder")
	}
	req, _ := http.NewRequest("GET", "https://service.internal.com", nil)
	p.Transform(req)
	if req.Header.Get("X-API-Key") != "wildcard-key" {
		t.Errorf("X-API-Key = %q", req.Header.Get("X-API-Key"))
	}
}
