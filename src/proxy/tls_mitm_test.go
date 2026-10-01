// Package proxy — LombokDNSProxy · TLS inspection (MITM) tests
//
// TLS inspection is the single most dangerous feature in this codebase: it
// mints a local CA and signs certificates for arbitrary hosts. These tests
// exist to pin down the safety properties, not just the happy path —
// specifically that it refuses to initialise unless explicitly enabled, and
// that the CA private key is never written world-readable.
//
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package proxy

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func enabledConfig(t *testing.T) MITMConfig {
	t.Helper()
	dir := t.TempDir()
	return MITMConfig{
		Enabled: true,
		CAPath:  dir,
		CACert:  filepath.Join(dir, "ca.crt"),
		CAKey:   filepath.Join(dir, "ca.key"),
	}
}

// ─── Safety: disabled by default ──────────────────────────────────────────────

func TestNewMITMInspector_RefusesWhenDisabled(t *testing.T) {
	cfg := MITMConfig{Enabled: false, CAPath: t.TempDir()}

	insp, err := NewMITMInspector(cfg)
	if err == nil {
		t.Fatal("NewMITMInspector must refuse to build an inspector when Enabled is false")
	}
	if insp != nil {
		t.Error("no inspector should be returned when disabled")
	}
}

func TestNewMITMInspector_DisabledWritesNoCA(t *testing.T) {
	dir := t.TempDir()
	NewMITMInspector(MITMConfig{Enabled: false, CAPath: dir}) //nolint:errcheck

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a disabled inspector must not generate any CA material, found %d file(s)", len(entries))
	}
}

// ─── CA generation ────────────────────────────────────────────────────────────

func TestNewMITMInspector_GeneratesCA(t *testing.T) {
	cfg := enabledConfig(t)

	insp, err := NewMITMInspector(cfg)
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}
	if insp.caCert == nil || insp.caKey == nil {
		t.Fatal("inspector should hold a parsed CA cert and key")
	}

	for _, name := range []string{"ca.crt", "ca.key"} {
		if _, err := os.Stat(filepath.Join(cfg.CAPath, name)); err != nil {
			t.Errorf("expected %s to be written: %v", name, err)
		}
	}
}

func TestGeneratedCA_IsAValidCA(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	ca := insp.caCert
	if !ca.IsCA {
		t.Error("generated certificate must have IsCA set")
	}
	if !ca.BasicConstraintsValid {
		t.Error("BasicConstraintsValid must be set or browsers reject the chain")
	}
	if ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("CA must carry KeyUsageCertSign")
	}
	if ca.MaxPathLen != 0 {
		t.Errorf("MaxPathLen should be 0 so this CA can't mint further CAs, got %d", ca.MaxPathLen)
	}
	if !ca.NotAfter.After(time.Now()) {
		t.Error("freshly generated CA is already expired")
	}
}

func TestGeneratedCA_KeyIsNotWorldReadable(t *testing.T) {
	cfg := enabledConfig(t)
	if _, err := NewMITMInspector(cfg); err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	info, err := os.Stat(filepath.Join(cfg.CAPath, "ca.key"))
	if err != nil {
		t.Fatalf("stat ca.key: %v", err)
	}

	// The CA key can sign for any host — group/other access would let any
	// local user intercept the machine's TLS traffic.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("ca.key permissions %04o expose the key beyond its owner; want 0600", perm)
	}
}

func TestNewMITMInspector_ReusesExistingCA(t *testing.T) {
	cfg := enabledConfig(t)

	first, err := NewMITMInspector(cfg)
	if err != nil {
		t.Fatalf("first init: %v", err)
	}
	second, err := NewMITMInspector(cfg)
	if err != nil {
		t.Fatalf("second init: %v", err)
	}

	// Re-generating on every start would re-prompt the user to trust a new
	// root each time, which trains exactly the wrong habit.
	if !first.caCert.Equal(second.caCert) {
		t.Error("second inspector regenerated the CA instead of loading the existing one")
	}
}

// ─── Leaf certificate signing ─────────────────────────────────────────────────

func TestLeafCert_SignedByCA(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	leaf, err := insp.LeafCert("example.com")
	if err != nil {
		t.Fatalf("LeafCert: %v", err)
	}

	parsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatalf("parsing leaf: %v", err)
	}

	if parsed.Subject.CommonName != "example.com" {
		t.Errorf("CN = %q, want example.com", parsed.Subject.CommonName)
	}

	pool := x509.NewCertPool()
	pool.AddCert(insp.caCert)
	if _, err := parsed.Verify(x509.VerifyOptions{
		Roots:     pool,
		DNSName:   "example.com",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("leaf should verify against the local CA: %v", err)
	}
}

func TestLeafCert_StripsPort(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	// CONNECT targets arrive as host:port; the SAN must be the bare host or
	// every TLS handshake fails on name mismatch.
	leaf, err := insp.LeafCert("example.com:443")
	if err != nil {
		t.Fatalf("LeafCert: %v", err)
	}
	parsed, _ := x509.ParseCertificate(leaf.Certificate[0])

	if parsed.Subject.CommonName != "example.com" {
		t.Errorf("CN = %q, want the port stripped", parsed.Subject.CommonName)
	}
	for _, name := range parsed.DNSNames {
		if name == "example.com:443" {
			t.Error("DNS SAN still contains the port")
		}
	}
}

func TestLeafCert_IPAddressGoesInSANNotDNS(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	leaf, err := insp.LeafCert("192.0.2.10")
	if err != nil {
		t.Fatalf("LeafCert: %v", err)
	}
	parsed, _ := x509.ParseCertificate(leaf.Certificate[0])

	if len(parsed.IPAddresses) == 0 {
		t.Error("an IP host must land in IPAddresses; a DNS SAN won't match")
	}
	if len(parsed.DNSNames) != 0 {
		t.Errorf("IP host should not produce DNS SANs, got %v", parsed.DNSNames)
	}
}

func TestLeafCert_CachedPerHost(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	first, err := insp.LeafCert("cached.example.com")
	if err != nil {
		t.Fatalf("first LeafCert: %v", err)
	}
	second, err := insp.LeafCert("cached.example.com")
	if err != nil {
		t.Fatalf("second LeafCert: %v", err)
	}

	// Signing a fresh key per connection would make every handshake pay an
	// ECDSA keygen; the cache is what keeps interception usable.
	if first != second {
		t.Error("repeat LeafCert for the same host should return the cached certificate")
	}
}

func TestLeafCert_DistinctPerHost(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	a, _ := insp.LeafCert("a.example.com")
	b, _ := insp.LeafCert("b.example.com")

	if a == b {
		t.Fatal("different hosts must not share a certificate")
	}
	pa, _ := x509.ParseCertificate(a.Certificate[0])
	pb, _ := x509.ParseCertificate(b.Certificate[0])
	if pa.SerialNumber.Cmp(pb.SerialNumber) == 0 {
		t.Error("certificates for different hosts share a serial number")
	}
}

func TestLeafCert_ShortLived(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	leaf, _ := insp.LeafCert("shortlived.example.com")
	parsed, _ := x509.ParseCertificate(leaf.Certificate[0])

	// Leaves are disposable; a long-lived one that leaks is a real problem.
	if lifetime := parsed.NotAfter.Sub(parsed.NotBefore); lifetime > 48*time.Hour {
		t.Errorf("leaf lifetime %v is too long for an intercepted connection", lifetime)
	}
}

// ─── Export for OS trust store ────────────────────────────────────────────────

func TestCACertPEM_IsParseablePEM(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	data, err := insp.CACertPEM()
	if err != nil {
		t.Fatalf("CACertPEM: %v", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("exported CA is not valid PEM — users could not install it")
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("PEM block type = %q, want CERTIFICATE", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("exported CA does not parse: %v", err)
	}
	if !cert.Equal(insp.caCert) {
		t.Error("exported PEM is not the CA this inspector is actually signing with")
	}
}

// ─── TLS config wiring ────────────────────────────────────────────────────────

func TestTLSConfig_ServesCertForSNI(t *testing.T) {
	insp, err := NewMITMInspector(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewMITMInspector: %v", err)
	}

	cfg := insp.TLSConfig()
	if cfg.GetCertificate == nil {
		t.Fatal("TLSConfig must install a GetCertificate callback for on-demand signing")
	}
	if cfg.MinVersion < 0x0303 { // tls.VersionTLS12
		t.Errorf("MinVersion %#x is below TLS 1.2", cfg.MinVersion)
	}
}
