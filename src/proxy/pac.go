// Package proxy — LombokDNSProxy · PAC Auto-Config Generator
// Generates Proxy Auto-Config (PAC) JavaScript files for automatic proxy routing.
// Supports domain rules, CIDR rules, GeoIP bypass, and smart ISP routing.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package proxy

import (
	"fmt"
	"net/http"
	"strings"
)

// RuleType classifies a PAC routing rule.
type RuleType int

const (
	RuleDirect    RuleType = iota // DIRECT — no proxy
	RuleProxy                     // use HTTP proxy
	RuleSOCKS5                    // use SOCKS5 proxy
	RuleGeoBypass                 // bypass proxy for local/country traffic
)

// PACRule describes one routing rule in order of priority (first match wins).
type PACRule struct {
	Type     RuleType
	Pattern  string // domain pattern, CIDR, or special keyword
	Comment  string
	ProxyURL string // for RuleProxy/RuleSOCKS5
}

// PACConfig holds the full PAC configuration.
type PACConfig struct {
	HTTPProxy    string    // e.g. "127.0.0.1:7788"
	SOCKS5Proxy  string    // e.g. "127.0.0.1:7789"
	Rules        []PACRule // evaluated in order; first match wins
	DefaultDirect bool     // if true: unmatched → DIRECT; else → use HTTP proxy
}

// DefaultPACConfig returns a sensible default PAC config for LombokDNSProxy.
func DefaultPACConfig(httpAddr, socks5Addr string) PACConfig {
	return PACConfig{
		HTTPProxy:   httpAddr,
		SOCKS5Proxy: socks5Addr,
		Rules: []PACRule{
			// Local / internal — always direct
			{Type: RuleDirect, Pattern: "localhost", Comment: "loopback"},
			{Type: RuleDirect, Pattern: "127.0.0.1", Comment: "loopback IPv4"},
			{Type: RuleDirect, Pattern: "::1", Comment: "loopback IPv6"},
			{Type: RuleDirect, Pattern: "*.local", Comment: "mDNS local"},
			{Type: RuleDirect, Pattern: "*.internal", Comment: "internal domains"},
			{Type: RuleDirect, Pattern: "*.corp", Comment: "corporate internal"},
			{Type: RuleDirect, Pattern: "10.*", Comment: "RFC1918 class A"},
			{Type: RuleDirect, Pattern: "172.16.*", Comment: "RFC1918 class B"},
			{Type: RuleDirect, Pattern: "192.168.*", Comment: "RFC1918 class C"},
			// Everything else via LombokDNSProxy HTTP proxy with SOCKS5 fallback
			{
				Type:     RuleProxy,
				Pattern:  "*",
				Comment:  "default — use LombokDNSProxy",
				ProxyURL: fmt.Sprintf("PROXY %s; SOCKS5 %s; DIRECT", httpAddr, socks5Addr),
			},
		},
		DefaultDirect: false,
	}
}

// Generate produces a valid PAC JavaScript file from cfg.
func Generate(cfg PACConfig) string {
	var sb strings.Builder

	sb.WriteString("// LombokDNSProxy — Proxy Auto-Config\n")
	sb.WriteString("// Generated automatically — do not edit by hand.\n")
	sb.WriteString("// Part of the Lombok Ecosystem · Apache 2.0\n\n")

	// Helper functions embedded inline (no external deps in PAC)
	sb.WriteString(`function cidrMatch(ip, cidr) {
  var parts = cidr.split('/');
  var net = parts[0]; var bits = parseInt(parts[1], 10) || 32;
  function toInt(a) {
    var p = a.split('.'); return ((+p[0])*16777216)+(+p[1])*65536+(+p[2])*256+(+p[3]);
  }
  var mask = bits === 0 ? 0 : (~0 << (32 - bits)) >>> 0;
  return (toInt(ip) & mask) === (toInt(net) & mask);
}

function domainMatch(host, pattern) {
  if (pattern === '*') return true;
  if (pattern.charAt(0) === '*') {
    var suffix = pattern.slice(1);
    return host === suffix.slice(1) || host.slice(-(suffix.length)) === suffix;
  }
  return host === pattern;
}

`)

	sb.WriteString("function FindProxyForURL(url, host) {\n")
	sb.WriteString("  var ip = dnsResolveEx ? dnsResolveEx(host) : dnsResolve(host);\n\n")

	for _, rule := range cfg.Rules {
		if rule.Comment != "" {
			sb.WriteString(fmt.Sprintf("  // %s\n", rule.Comment))
		}

		condition := buildCondition(rule.Pattern)
		result := buildResult(rule, cfg)

		sb.WriteString(fmt.Sprintf("  if (%s) return \"%s\";\n", condition, result))
	}

	sb.WriteString("\n  // Default\n")
	if cfg.DefaultDirect {
		sb.WriteString("  return \"DIRECT\";\n")
	} else {
		sb.WriteString(fmt.Sprintf("  return \"PROXY %s; SOCKS5 %s; DIRECT\";\n",
			cfg.HTTPProxy, cfg.SOCKS5Proxy))
	}

	sb.WriteString("}\n")
	return sb.String()
}

// buildCondition translates a pattern into a PAC JS condition expression.
func buildCondition(pattern string) string {
	switch {
	case pattern == "*":
		return "true"
	case pattern == "localhost":
		return "host === 'localhost'"
	case strings.HasSuffix(pattern, ".*"):
		// IP prefix like "10.*" or "192.168.*"
		prefix := strings.TrimSuffix(pattern, "*")
		return fmt.Sprintf("host.indexOf('%s') === 0 || (ip && ip.indexOf('%s') === 0)", prefix, prefix)
	case strings.Contains(pattern, "/"):
		// CIDR notation
		return fmt.Sprintf("(ip && cidrMatch(ip, '%s'))", pattern)
	case strings.HasPrefix(pattern, "*."):
		// Wildcard subdomain
		return fmt.Sprintf("domainMatch(host, '%s')", pattern)
	default:
		return fmt.Sprintf("host === '%s'", pattern)
	}
}

// buildResult returns the PAC return value string for a rule.
func buildResult(rule PACRule, cfg PACConfig) string {
	switch rule.Type {
	case RuleDirect:
		return "DIRECT"
	case RuleSOCKS5:
		if rule.ProxyURL != "" {
			return rule.ProxyURL
		}
		return fmt.Sprintf("SOCKS5 %s; DIRECT", cfg.SOCKS5Proxy)
	case RuleProxy:
		if rule.ProxyURL != "" {
			return rule.ProxyURL
		}
		return fmt.Sprintf("PROXY %s; SOCKS5 %s; DIRECT", cfg.HTTPProxy, cfg.SOCKS5Proxy)
	default:
		return "DIRECT"
	}
}

// PACHandler returns an http.HandlerFunc that serves the PAC file.
// Mount at GET /proxy.pac on the dashboard API server.
func PACHandlerFromConfig(cfg PACConfig) http.HandlerFunc {
	pac := Generate(cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Header().Set("Cache-Control", "max-age=3600")
		fmt.Fprint(w, pac) //nolint:errcheck
	}
}
