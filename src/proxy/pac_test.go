// Package proxy_test — LombokDNSProxy · PAC Generator Tests
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package proxy

import (
	"strings"
	"testing"
)

func TestGenerate_ContainsFindProxyFunction(t *testing.T) {
	cfg := DefaultPACConfig("127.0.0.1:7788", "127.0.0.1:7789")
	pac := Generate(cfg)

	if !strings.Contains(pac, "function FindProxyForURL") {
		t.Error("PAC must contain FindProxyForURL function")
	}
}

func TestGenerate_LocalhostIsDirect(t *testing.T) {
	cfg := DefaultPACConfig("127.0.0.1:7788", "127.0.0.1:7789")
	pac := Generate(cfg)

	if !strings.Contains(pac, "localhost") {
		t.Error("PAC should have a rule for localhost")
	}
	if !strings.Contains(pac, "DIRECT") {
		t.Error("PAC should return DIRECT for localhost")
	}
}

func TestGenerate_WildcardLocalIsDirect(t *testing.T) {
	cfg := DefaultPACConfig("127.0.0.1:7788", "127.0.0.1:7789")
	pac := Generate(cfg)

	if !strings.Contains(pac, "*.local") {
		t.Error("PAC should have a rule for *.local")
	}
}

func TestGenerate_DefaultUsesProxy(t *testing.T) {
	cfg := DefaultPACConfig("127.0.0.1:7788", "127.0.0.1:7789")
	pac := Generate(cfg)

	if !strings.Contains(pac, "PROXY 127.0.0.1:7788") {
		t.Error("PAC should route unknown traffic through HTTP proxy")
	}
	if !strings.Contains(pac, "SOCKS5 127.0.0.1:7789") {
		t.Error("PAC should include SOCKS5 fallback")
	}
}

func TestGenerate_DefaultDirect_NoProxy(t *testing.T) {
	cfg := DefaultPACConfig("127.0.0.1:7788", "127.0.0.1:7789")
	cfg.DefaultDirect = true
	// Remove the catch-all proxy rule
	cfg.Rules = cfg.Rules[:len(cfg.Rules)-1]
	pac := Generate(cfg)

	// With DefaultDirect=true the fallback line should be DIRECT
	lines := strings.Split(pac, "\n")
	var fallbackLine string
	for _, line := range lines {
		if strings.Contains(line, "return \"DIRECT\"") {
			fallbackLine = line
			break
		}
	}
	if fallbackLine == "" {
		t.Error("PAC with DefaultDirect=true should end with return DIRECT")
	}
}

func TestGenerate_CIDRRule(t *testing.T) {
	cfg := PACConfig{
		HTTPProxy:   "127.0.0.1:7788",
		SOCKS5Proxy: "127.0.0.1:7789",
		Rules: []PACRule{
			{Type: RuleDirect, Pattern: "10.0.0.0/8", Comment: "RFC1918"},
		},
	}
	pac := Generate(cfg)

	if !strings.Contains(pac, "cidrMatch") {
		t.Error("CIDR rule should use cidrMatch helper")
	}
	if !strings.Contains(pac, "10.0.0.0/8") {
		t.Error("CIDR range should appear in PAC output")
	}
}

func TestGenerate_CustomSOCKS5Rule(t *testing.T) {
	cfg := PACConfig{
		HTTPProxy:   "127.0.0.1:7788",
		SOCKS5Proxy: "127.0.0.1:7789",
		Rules: []PACRule{
			{
				Type:     RuleSOCKS5,
				Pattern:  "*.gaming.net",
				Comment:  "gaming — use SOCKS5 for UDP",
				ProxyURL: "SOCKS5 127.0.0.1:7789",
			},
		},
	}
	pac := Generate(cfg)

	if !strings.Contains(pac, "SOCKS5 127.0.0.1:7789") {
		t.Error("SOCKS5 rule should appear in PAC")
	}
}

func TestBuildCondition_WildcardSubdomain(t *testing.T) {
	cond := buildCondition("*.example.com")
	if !strings.Contains(cond, "domainMatch") {
		t.Errorf("wildcard subdomain should use domainMatch, got: %s", cond)
	}
}

func TestBuildCondition_ExactDomain(t *testing.T) {
	cond := buildCondition("printer.local")
	if !strings.Contains(cond, "host === 'printer.local'") {
		t.Errorf("exact domain should use equality check, got: %s", cond)
	}
}

func TestBuildCondition_CIDR(t *testing.T) {
	cond := buildCondition("192.168.0.0/16")
	if !strings.Contains(cond, "cidrMatch") {
		t.Errorf("CIDR should use cidrMatch, got: %s", cond)
	}
}

func TestBuildCondition_WildcardAll(t *testing.T) {
	cond := buildCondition("*")
	if cond != "true" {
		t.Errorf("wildcard * should produce 'true', got: %s", cond)
	}
}

func TestGenerate_LombokCommentPresent(t *testing.T) {
	cfg := DefaultPACConfig("127.0.0.1:7788", "127.0.0.1:7789")
	pac := Generate(cfg)
	if !strings.Contains(pac, "LombokDNSProxy") {
		t.Error("PAC output should contain LombokDNSProxy attribution")
	}
}
