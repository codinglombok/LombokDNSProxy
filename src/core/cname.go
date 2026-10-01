// Package core — LombokDNSProxy · CNAME Flattening
// Follows CNAME chains up to depth 3 and checks each resolved domain
// against the filter engine. Detects CNAME-cloaked tracking domains.
//
// Example: metrics.shopify.com → shopify.edgekey.net → akamai.net
// If any hop is in the blocklist → block the original query.
//
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"context"
	"strings"

	"github.com/miekg/dns"
)

const maxCNAMEDepth = 3

// CNAMEFlattener follows CNAME chains and checks each hop against the filter.
type CNAMEFlattener struct {
	resolver *RacingResolver
	filter   interface {
		IsBlocked(string) bool
	}
}

// NewCNAMEFlattener creates a flattener backed by the given resolver and filter.
func NewCNAMEFlattener(resolver *RacingResolver, filter interface{ IsBlocked(string) bool }) *CNAMEFlattener {
	return &CNAMEFlattener{resolver: resolver, filter: filter}
}

// CheckAndFlatten resolves the CNAME chain for domain and returns:
//   - blocked=true if any hop in the chain is in the blocklist
//   - flatTarget: the final A/AAAA target after following all CNAMEs
//   - chain: all CNAME hops encountered
func (f *CNAMEFlattener) CheckAndFlatten(ctx context.Context, domain string) (blocked bool, flatTarget string, chain []string) {
	current := dns.Fqdn(domain)

	for depth := 0; depth < maxCNAMEDepth; depth++ {
		// Check the current hop
		if f.filter.IsBlocked(strings.TrimSuffix(current, ".")) {
			return true, "", chain
		}

		// Resolve current name
		req := new(dns.Msg)
		req.SetQuestion(current, dns.TypeCNAME)
		req.RecursionDesired = true

		resp, err := f.resolver.Resolve(ctx, req)
		if err != nil || resp == nil {
			break
		}

		// Find CNAME record in answer
		cnameTarget := ""
		for _, rr := range resp.Answer {
			if cname, ok := rr.(*dns.CNAME); ok {
				cnameTarget = cname.Target
				break
			}
		}

		if cnameTarget == "" {
			// No more CNAMEs — we've reached the final name
			flatTarget = current
			break
		}

		chain = append(chain, cnameTarget)
		current = cnameTarget
	}

	// Final check on the resolved target
	if flatTarget != "" && f.filter.IsBlocked(strings.TrimSuffix(flatTarget, ".")) {
		return true, flatTarget, chain
	}

	return false, flatTarget, chain
}

// FlattenResponse rewrites a DNS response to include the final A/AAAA records
// alongside any CNAME chain — "CNAME flattening" for performance.
// This avoids clients having to follow the chain themselves.
func FlattenResponse(resp *dns.Msg, original *dns.Msg) *dns.Msg {
	if resp == nil {
		return original
	}
	// Collect all A/AAAA records from the response
	var directRRs []dns.RR
	for _, rr := range resp.Answer {
		switch rr.(type) {
		case *dns.A, *dns.AAAA:
			// Rewrite the owner name to the original query name
			clone := dns.Copy(rr)
			clone.Header().Name = original.Question[0].Name
			directRRs = append(directRRs, clone)
		}
	}

	if len(directRRs) == 0 {
		return resp
	}

	flat := new(dns.Msg)
	flat.SetReply(original)
	flat.Answer = directRRs
	flat.Authoritative = false
	return flat
}
