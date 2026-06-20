package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/vrealzhou/agent-vm/internal/config"
)

func GenerateOrLoadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	// Load existing CA (PEM format on disk)
	if pemData, err := os.ReadFile(config.CACertPath()); err == nil {
		if block, _ := pem.Decode(pemData); block != nil {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				if keyDER, err := os.ReadFile(config.CAKeyPath()); err == nil {
					if key, err := x509.ParseECPrivateKey(keyDER); err == nil {
						return cert, key, nil
					}
				}
			}
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "agent-vm Proxy CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create CA cert: %w", err)
	}
	cert, _ := x509.ParseCertificate(certDER)

	_ = os.MkdirAll(config.StateDir(), 0o755)
	// Write cert as PEM (so update-ca-certificates can parse it)
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	_ = os.WriteFile(config.CACertPath(), caCertPEM, 0o644)
	// Write key as DER
	keyDER, _ := x509.MarshalECPrivateKey(key)
	_ = os.WriteFile(config.CAKeyPath(), keyDER, 0o600)

	return cert, key, nil
}
