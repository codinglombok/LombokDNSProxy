// Package proxy — LombokDNSProxy · Optional TLS Inspection (MITM)
//
// ⚠️  WARNING: TLS inspection is NEVER enabled by default.
// It requires explicit user opt-in, displaying a clear consent dialog,
// and installing a locally-generated CA certificate.
//
// This file implements the local CA generation and per-connection certificate
// signing needed for HTTPS CONNECT interception when the user has consented.
//
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MITMConfig controls TLS inspection behaviour.
type MITMConfig struct {
	Enabled  bool   // MUST be explicitly set to true; never set programmatically
	CAPath   string // directory for CA cert + key storage
	CACert   string // PEM path for CA cert (auto-generated if absent)
	CAKey    string // PEM path for CA private key
}

// MITMInspector holds the local CA and a cache of leaf certs signed on demand.
type MITMInspector struct {
	cfg     MITMConfig
	caCert  *x509.Certificate
	caKey   *ecdsa.PrivateKey
	certMu  sync.Mutex
	certMap map[string]*tls.Certificate // host → signed leaf cert (cached)
}

// NewMITMInspector creates (or loads) a local CA and returns an inspector.
// Returns an error if Enabled is false — callers must check before creating.
func NewMITMInspector(cfg MITMConfig) (*MITMInspector, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("TLS inspection is disabled; user must explicitly enable it in config")
	}

	m := &MITMInspector{cfg: cfg, certMap: make(map[string]*tls.Certificate)}

	if err := os.MkdirAll(cfg.CAPath, 0o700); err != nil {
		return nil, fmt.Errorf("mitm: create CA dir: %w", err)
	}

	caCertPath := filepath.Join(cfg.CAPath, "ca.crt")
	caKeyPath := filepath.Join(cfg.CAPath, "ca.key")

	// Load existing CA or generate a new one
	if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
		if err := m.generateCA(caCertPath, caKeyPath); err != nil {
			return nil, fmt.Errorf("mitm: generate CA: %w", err)
		}
	}

	if err := m.loadCA(caCertPath, caKeyPath); err != nil {
		return nil, fmt.Errorf("mitm: load CA: %w", err)
	}

	return m, nil
}

// generateCA creates a new ECDSA P-256 CA certificate valid for 2 years.
// The key is stored in the CA directory with 0600 permissions.
func (m *MITMInspector) generateCA(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "LombokDNSProxy Local CA",
			Organization: []string{"LombokDNSProxy (local, do not trust externally)"},
		},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(2 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		// Go treats MaxPathLen: 0 as "unset" and emits an unconstrained CA
		// unless MaxPathLenZero is set alongside it. Without this pair the
		// local root could sign intermediate CAs, which this one never needs
		// to — it only ever signs leaf certs for intercepted hosts.
		MaxPathLen:     0,
		MaxPathLenZero: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}

	// Write cert
	cf, err := os.OpenFile(certPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer cf.Close()
	pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}) //nolint:errcheck

	// Write key (restricted permissions)
	kf, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer kf.Close()
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}) //nolint:errcheck

	return nil
}

// loadCA reads the CA cert and key from disk into m.
func (m *MITMInspector) loadCA(certPath, keyPath string) error {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("mitm: invalid CA cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	m.caCert = cert

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("mitm: invalid CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return err
	}
	m.caKey = key
	return nil
}

// LeafCert returns a TLS certificate for host, signed by the local CA.
// Certificates are cached in memory for the duration of the process.
func (m *MITMInspector) LeafCert(host string) (*tls.Certificate, error) {
	// Strip port
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	m.certMu.Lock()
	defer m.certMu.Unlock()

	if cert, ok := m.certMap[host]; ok {
		return cert, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-1 * time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, m.caCert, &key.PublicKey, m.caKey)
	if err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		func() []byte {
			b, _ := x509.MarshalECPrivateKey(key)
			return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b})
		}(),
	)
	if err != nil {
		return nil, err
	}

	m.certMap[host] = &cert
	return &cert, nil
}

// CACertPEM returns the CA certificate in PEM format, suitable for export
// so the user can install it as a trusted root in their browser/OS.
func (m *MITMInspector) CACertPEM() ([]byte, error) {
	return os.ReadFile(filepath.Join(m.cfg.CAPath, "ca.crt"))
}

// TLSConfig returns a *tls.Config that uses on-demand leaf cert generation for MITM.
func (m *MITMInspector) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return m.LeafCert(info.ServerName)
		},
	}
}
