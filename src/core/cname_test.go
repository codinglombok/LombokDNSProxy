// Package core_test — LombokDNSProxy · CNAME Flattener Tests
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"context"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/codinglombok/lombok-dns-proxy/src/cache"
	"github.com/codinglombok/lombok-dns-proxy/src/filter"
)

// ─── FlattenResponse tests ────────────────────────────────────────────────────

func TestFlattenResponse_FlattensARecords(t *testing.T) {
	original := new(dns.Msg)
	original.SetQuestion("shop.example.com.", dns.TypeA)

	// Simulate a response with CNAME + A record
	resp := new(dns.Msg)
	resp.SetReply(original)
	cname, _ := dns.NewRR("shop.example.com. 300 IN CNAME edge.cdn.net.")
	aRec, _ := dns.NewRR("edge.cdn.net. 60 IN A 1.2.3.4")
	resp.Answer = []dns.RR{cname, aRec}

	flat := FlattenResponse(resp, original)

	// Flattened response should have the A record with the original name
	if len(flat.Answer) == 0 {
		t.Fatal("flattened response should have at least one answer")
	}
	for _, rr := range flat.Answer {
		if _, ok := rr.(*dns.A); !ok {
			t.Errorf("expected A record, got %T", rr)
		}
		if rr.Header().Name != "shop.example.com." {
			t.Errorf("expected owner name shop.example.com., got %s", rr.Header().Name)
		}
	}
}

func TestFlattenResponse_NilRespReturnsOriginal(t *testing.T) {
	original := new(dns.Msg)
	original.SetQuestion("nil.test.", dns.TypeA)

	result := FlattenResponse(nil, original)
	if result != original {
		t.Error("nil response should return original unchanged")
	}
}

func TestFlattenResponse_NoCNAMEReturnsResp(t *testing.T) {
	original := new(dns.Msg)
	original.SetQuestion("direct.com.", dns.TypeA)

	resp := new(dns.Msg)
	resp.SetReply(original)
	aRec, _ := dns.NewRR("direct.com. 300 IN A 5.5.5.5")
	resp.Answer = []dns.RR{aRec}

	flat := FlattenResponse(resp, original)
	if len(flat.Answer) == 0 {
		t.Error("should have answer records")
	}
}

func TestFlattenResponse_EmptyAnswerReturnsResp(t *testing.T) {
	original := new(dns.Msg)
	original.SetQuestion("empty.com.", dns.TypeA)

	resp := new(dns.Msg)
	resp.SetReply(original)
	// No answer records

	flat := FlattenResponse(resp, original)
	if flat != resp {
		t.Error("response with no answers should be returned as-is")
	}
}

// ─── CheckAndFlatten with blocked hops ───────────────────────────────────────

func TestCNAMEFlattener_DirectBlockedDomain(t *testing.T) {
	fe := filter.New()
	fe.Block("ads.tracker.com")
	c := cache.New(100, &mockECC{}, 4)
	resolver := NewRacingResolver(nil, c, fe)
	flattener := NewCNAMEFlattener(resolver, fe)

	// The domain itself is blocked — no CNAME hop needed
	blocked, _, _ := flattener.CheckAndFlatten(testCtx(), "ads.tracker.com")
	if !blocked {
		t.Error("directly blocked domain should return blocked=true")
	}
}

func TestCNAMEFlattener_CleanDomain_NotBlocked(t *testing.T) {
	fe := filter.New() // empty blocklist
	c := cache.New(100, &mockECC{}, 4)
	resolver := NewRacingResolver(nil, c, fe)
	flattener := NewCNAMEFlattener(resolver, fe)

	blocked, _, _ := flattener.CheckAndFlatten(testCtx(), "clean.example.com")
	if blocked {
		t.Error("clean domain should not be blocked")
	}
}

// TestCNAMEFlattener_BlockedHop is the one case that actually exercises a
// CNAME *hop*: the queried name is clean, but it CNAMEs to a blocked target.
// That's the cloaked-tracker scenario the flattener exists for.
func TestCNAMEFlattener_BlockedHop(t *testing.T) {
	fe := filter.New()
	fe.Block("tracking.cloaked.com")

	c := cache.New(100, &mockECC{}, 4)
	mockUpstream := []Upstream{{
		Name:    "mock",
		Latency: 1 * time.Millisecond,
		client: &mockUpstreamClient{
			latency:  1 * time.Millisecond,
			response: makeCNAMEResponse("metrics.shop.com", "tracking.cloaked.com"),
		},
	}}

	resolver := NewRacingResolver(mockUpstream, c, fe)
	flattener := NewCNAMEFlattener(resolver, fe)

	blocked, _, _ := flattener.CheckAndFlatten(context.Background(), "metrics.shop.com")
	if !blocked {
		t.Error("a CNAME hop pointing at a blocked domain should cause a block")
	}
}

// Verify FlattenResponse handles AAAA records correctly
func TestFlattenResponse_HandlesAAAA(t *testing.T) {
	original := new(dns.Msg)
	original.SetQuestion("ipv6.example.com.", dns.TypeAAAA)

	resp := new(dns.Msg)
	resp.SetReply(original)
	cname, _ := dns.NewRR("ipv6.example.com. 300 IN CNAME v6.cdn.net.")
	aaaa, _ := dns.NewRR("v6.cdn.net. 60 IN AAAA 2001:db8::1")
	resp.Answer = []dns.RR{cname, aaaa}

	flat := FlattenResponse(resp, original)

	hasAAAA := false
	for _, rr := range flat.Answer {
		if _, ok := rr.(*dns.AAAA); ok {
			hasAAAA = true
			if rr.Header().Name != "ipv6.example.com." {
				t.Errorf("AAAA owner should be rewritten to ipv6.example.com., got %s", rr.Header().Name)
			}
		}
	}
	if !hasAAAA {
		t.Error("flattened response should contain AAAA record")
	}
}
