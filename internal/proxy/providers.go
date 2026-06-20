package proxy

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/vrealzhou/agent-vm/internal/config"
	"github.com/vrealzhou/agent-vm/internal/secrets"
)

// ── Interface ──

// CredentialProvider transforms an HTTP request before forwarding.
// Implementations can modify headers, body, URL, method — anything.
type CredentialProvider interface {
	Transform(req *http.Request) error
}

// ProviderFactory creates a provider from raw JSON config.
type ProviderFactory func(config json.RawMessage) (CredentialProvider, error)

// Registry allows users to register custom provider types.
var providerFactories = map[string]ProviderFactory{}

// RegisterProvider registers a custom provider type.
// Call this from init() in your own package to add providers.
func RegisterProvider(typeName string, factory ProviderFactory) {
	providerFactories[typeName] = factory
}

// ResolveProvider builds a CredentialProvider for a host from config.
// Checks `providers` first, then falls back to legacy `credentials`.
func ResolveProvider(cfg *config.ProxyConfig, host string) CredentialProvider {
	if cfg == nil {
		return nil
	}

	// New-style: explicit provider definition
	if entry, ok := matchProviderEntry(cfg.Providers, host); ok {
		// Placeholder reference: resolve from the secrets store.
		if entry.Placeholder != "" {
			return resolvePlaceholderProvider(entry.Placeholder)
		}
		if factory, ok := providerFactories[entry.Type]; ok {
			configJSON, _ := json.Marshal(entry.Config)
			provider, err := factory(configJSON)
			if err == nil {
				return provider
			}
		}
		return nil
	}

	// Legacy: simple header map → auto-wrap as header provider
	if headers := matchProxyHost(cfg.Credentials, host); headers != nil {
		return &HeaderProvider{Headers: headers}
	}

	return nil
}

// resolvePlaceholderProvider looks up a named placeholder in the secrets store
// and builds a provider from its fields.
func resolvePlaceholderProvider(name string) CredentialProvider {
	store, err := secrets.Load()
	if err != nil || store == nil {
		return nil
	}
	p, ok := store.Get(name)
	if !ok {
		return nil
	}
	configJSON := buildPlaceholderConfig(p)
	if factory, ok := providerFactories[p.Type]; ok {
		provider, err := factory(configJSON)
		if err == nil {
			return provider
		}
	}
	return nil
}

// buildPlaceholderConfig converts a placeholder's fields into the JSON config
// expected by the matching provider factory. The header type stores header
// key/values directly as fields, so they are wrapped under "headers".
func buildPlaceholderConfig(p secrets.Placeholder) json.RawMessage {
	if p.Type == "header" {
		configMap := map[string]any{"headers": p.Fields}
		data, _ := json.Marshal(configMap)
		return data
	}
	configMap := make(map[string]any, len(p.Fields))
	for k, v := range p.Fields {
		configMap[k] = v
	}
	data, _ := json.Marshal(configMap)
	return data
}

func matchProviderEntry(providers map[string]config.ProviderEntry, host string) (config.ProviderEntry, bool) {
	if entry, ok := providers[host]; ok {
		return entry, true
	}
	for pattern, entry := range providers {
		if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) {
			return entry, true
		}
	}
	return config.ProviderEntry{}, false
}

// ── Built-in: Header Provider ──

type HeaderProvider struct {
	Headers map[string]string `json:"headers"`
}

func (p *HeaderProvider) Transform(req *http.Request) error {
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	return nil
}

// ── Built-in: Body Provider (JSON path injection) ──

type BodyProvider struct {
	Path  string `json:"path"`  // dot-separated JSON path, e.g. "auth.token"
	Value string `json:"value"` // value to inject
}

func (p *BodyProvider) Transform(req *http.Request) error {
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return err
	}

	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return err
	}

	setJSONPath(data, p.Path, p.Value)

	modified, err := json.Marshal(data)
	if err != nil {
		return err
	}
	req.Body = io.NopCloser(bytes.NewReader(modified))
	req.ContentLength = int64(len(modified))
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(modified)))
	return nil
}

// setJSONPath sets a value at a dot-separated path in a nested map.
func setJSONPath(data any, path string, value string) {
	keys := strings.Split(path, ".")
	m, ok := data.(map[string]any)
	if !ok {
		return
	}
	for i, k := range keys {
		if i == len(keys)-1 {
			m[k] = value
			return
		}
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
}

// ── Built-in: AWS SigV4 Provider ──

type AWSSigV4Provider struct {
	AccessKey    string `json:"access_key"`
	SecretKey    string `json:"secret_key"`
	Region       string `json:"region"`
	Service      string `json:"service"`
	SessionToken string `json:"session_token"` // optional
}

func (p *AWSSigV4Provider) Transform(req *http.Request) error {
	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	// Read and hash body
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}
	payloadHash := hex.EncodeToString(sha256Sum(bodyBytes))

	// Canonical URI
	canonURI := req.URL.EscapedPath()
	if canonURI == "" {
		canonURI = "/"
	}

	// Canonical query string (sorted)
	canonQuery := canonicalQueryString(req.URL.Query())

	// Set required headers before building canonical headers
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if p.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", p.SessionToken)
	}
	req.Host = req.URL.Host

	// Canonical headers (sorted, lowercase)
	canonHeaders, signedHeaders := canonicalHeaders(req.Header)

	// Build canonical request
	canonReq := strings.Join([]string{
		req.Method,
		canonURI,
		canonQuery,
		canonHeaders + "\n",
		signedHeaders,
		payloadHash,
	}, "\n")

	// Credential scope
	scope := strings.Join([]string{datestamp, p.Region, p.Service, "aws4_request"}, "/")

	// String to sign
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(sha256Sum([]byte(canonReq))),
	}, "\n")

	// Derive signing key
	kDate := hmacSHA256([]byte("AWS4"+p.SecretKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(p.Region))
	kService := hmacSHA256(kRegion, []byte(p.Service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	// Signature
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	// Authorization header
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		p.AccessKey, scope, signedHeaders, signature,
	))

	// Restore body
	if bodyBytes != nil {
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
	}

	return nil
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func canonicalQueryString(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range values[k] {
			parts = append(parts, awsURIEscape(k)+"="+awsURIEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func canonicalHeaders(headers http.Header) (canonical string, signedList string) {
	type hdr struct {
		name string
		val  string
	}
	var hdrs []hdr
	for k := range headers {
		hdrs = append(hdrs, hdr{
			name: strings.ToLower(k),
			val:  strings.TrimSpace(headers.Get(k)),
		})
	}
	sort.Slice(hdrs, func(i, j int) bool { return hdrs[i].name < hdrs[j].name })

	var canon, signed []string
	for _, h := range hdrs {
		canon = append(canon, h.name+":"+h.val)
		signed = append(signed, h.name)
	}
	return strings.Join(canon, "\n"), strings.Join(signed, ";")
}

func awsURIEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// ── Register built-in providers ──

func init() {
	RegisterProvider("header", func(cfg json.RawMessage) (CredentialProvider, error) {
		var p HeaderProvider
		return &p, json.Unmarshal(cfg, &p)
	})
	RegisterProvider("body", func(cfg json.RawMessage) (CredentialProvider, error) {
		var p BodyProvider
		return &p, json.Unmarshal(cfg, &p)
	})
	RegisterProvider("aws-sigv4", func(cfg json.RawMessage) (CredentialProvider, error) {
		var p AWSSigV4Provider
		return &p, json.Unmarshal(cfg, &p)
	})
}
