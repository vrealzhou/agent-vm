package proxy

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/vrealzhou/agent-vm/internal/config"
)

// ── Leaf cert cache ──

type certCache struct {
	mu    sync.RWMutex
	certs map[string]*tls.Certificate
	ca    *x509.Certificate
	caKey *ecdsa.PrivateKey
}

func newCertCache(ca *x509.Certificate, key *ecdsa.PrivateKey) *certCache {
	return &certCache{certs: make(map[string]*tls.Certificate), ca: ca, caKey: key}
}

func (c *certCache) get(host string) (*tls.Certificate, error) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	c.mu.RLock()
	if cert, ok := c.certs[host]; ok {
		c.mu.RUnlock()
		return cert, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if cert, ok := c.certs[host]; ok {
		return cert, nil
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, c.ca, &leafKey.PublicKey, c.caKey)
	if err != nil {
		return nil, err
	}
	tlsCert := &tls.Certificate{Certificate: [][]byte{certDER, c.ca.Raw}, PrivateKey: leafKey}
	c.certs[host] = tlsCert
	return tlsCert, nil
}

// Dedicated transport for MITM forwarding — avoids stale connection issues.
var mitmTransport = &http.Transport{
	DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout: 15 * time.Second,
	DisableKeepAlives:   true,
	MaxIdleConns:        0,
}

// ── Proxy server ──

func RunProxyServer(port int) error {
	var cfg *config.ProxyConfig
	if raw := os.Getenv("AGENT_VM_PROXY_CONFIG"); raw != "" {
		cfg = &config.ProxyConfig{}
		_ = json.Unmarshal([]byte(raw), cfg)
	}

	var cache *certCache
	if certPEM := os.Getenv("AGENT_VM_PROXY_CA_CERT"); certPEM != "" {
		if block, _ := pem.Decode([]byte(certPEM)); block != nil {
			if ca, err := x509.ParseCertificate(block.Bytes); err == nil {
				if keyBlock, _ := pem.Decode([]byte(os.Getenv("AGENT_VM_PROXY_CA_KEY"))); keyBlock != nil {
					if caKey, err := x509.ParseECPrivateKey(keyBlock.Bytes); err == nil {
						cache = newCertCache(ca, caKey)
					}
				}
			}
		}
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("proxy listen: %w", err)
	}
	fmt.Printf("[agent-vm] MITM proxy listening on 0.0.0.0:%d\n", port)

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			handleConnect(w, r, cfg, cache)
		} else {
			handleHTTP(w, r, cfg)
		}
	})}
	return server.Serve(listener)
}

// handleConnect: check access → MITM for credential domains → transparent tunnel.
func handleConnect(w http.ResponseWriter, r *http.Request, cfg *config.ProxyConfig, cache *certCache) {
	host, _, _ := net.SplitHostPort(r.Host)
	if host == "" {
		host = r.Host
	}
	fmt.Printf("[proxy] CONNECT %s\n", r.Host)

	// Domain-level access check
	if ok, reason := checkAccess(cfg, "https://"+host, host); !ok {
		http.Error(w, reason, http.StatusForbidden)
		return
	}

	needsMITM := ResolveProvider(cfg, host) != nil
	if needsMITM && cache != nil {
		handleMITMConnect(w, r, host, cfg, cache)
	} else {
		handleTransparentConnect(w, r)
	}
}

func handleMITMConnect(w http.ResponseWriter, r *http.Request, host string, cfg *config.ProxyConfig, cache *certCache) {
	leafCert, err := cache.get(host)
	if err != nil {
		handleTransparentConnect(w, r)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		handleTransparentConnect(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	clientConn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	if buf != nil {
		buf.Flush()
	}

	tlsConfig := &tls.Config{Certificates: []tls.Certificate{*leafCert}, NextProtos: []string{"http/1.1"}}
	tlsConn := tls.Server(clientConn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		tlsConn.Close()
		return
	}
	defer tlsConn.Close()

	reader := bufio.NewReader(tlsConn)
	provider := ResolveProvider(cfg, host)

	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			fmt.Printf("[proxy] MITM read error: %v\n", err)
			break
		}
		fmt.Printf("[proxy] MITM %s %s%s\n", req.Method, host, req.URL.RequestURI())
		fullURL := "https://" + host + req.URL.RequestURI()

		// URL-level access check inside MITM
		if ok, reason := checkAccess(cfg, fullURL, host); !ok {
			fmt.Fprintf(tlsConn, "HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\n\r\n%s\n", reason)
			continue
		}

		req.URL.Scheme = "https"
		req.URL.Host = host
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Close = false

		// Apply credential provider (header injection, body rewrite, AWS sig, etc.)
		if provider != nil {
			if err := provider.Transform(req); err != nil {
				fmt.Printf("[proxy] provider error: %v\n", err)
			}
		}

		resp, err := mitmTransport.RoundTrip(req)
		if err != nil {
			fmt.Printf("[proxy] MITM forward error: %v\n", err)
			fmt.Fprintf(tlsConn, "HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/plain\r\n\r\n%s\n", err.Error())
			break
		}
		fmt.Printf("[proxy] MITM response: %d\n", resp.StatusCode)
		resp.Header.Del("Transfer-Encoding")
		if err := resp.Write(tlsConn); err != nil {
			resp.Body.Close()
			break
		}
		resp.Body.Close()
	}
}

func handleTransparentConnect(w http.ResponseWriter, r *http.Request) {
	dest, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	hj, ok := w.(http.Hijacker)
	if !ok {
		dest.Close()
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		dest.Close()
		return
	}
	go io.Copy(dest, client)
	io.Copy(client, dest)
	client.Close()
	dest.Close()
}

func handleHTTP(w http.ResponseWriter, r *http.Request, cfg *config.ProxyConfig) {
	host := r.URL.Hostname()
	if host == "" {
		host, _, _ = net.SplitHostPort(r.Host)
	}
	fullURL := r.URL.String()

	if ok, reason := checkAccess(cfg, fullURL, host); !ok {
		http.Error(w, reason, http.StatusForbidden)
		return
	}

	if provider := ResolveProvider(cfg, host); provider != nil {
		if err := provider.Transform(r); err != nil {
			fmt.Printf("[proxy] provider error: %v\n", err)
		}
	}

	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
