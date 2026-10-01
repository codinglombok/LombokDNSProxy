// Package core — LombokDNSProxy · Shared Test Helpers
// Common types, helpers, and fixtures used across all test files in this
// package. Co-located with the source it tests (required for white-box
// access to unexported fields like Upstream.client, Prefetcher.mu, etc.)
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"context"
	"net"
	"time"

	"github.com/miekg/dns"

	"github.com/codinglombok/lombok-dns-proxy/src/cache"
	"github.com/codinglombok/lombok-dns-proxy/src/filter"
)

// ─── ECC stub — satisfies cache.ECCProvider ───────────────────────────────────

type mockECC struct{}

func (m *mockECC) Encode(data []byte) ([]byte, error)    { return make([]byte, 16), nil }
func (m *mockECC) Verify(data, _ []byte) ([]byte, error) { return data, nil }

// ─── Upstream client stub ─────────────────────────────────────────────────────

type mockUpstreamClient struct {
	latency  time.Duration
	response *dns.Msg
	err      error
}

func (m *mockUpstreamClient) Exchange(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(m.latency):
		if m.response != nil {
			resp := m.response.Copy()
			resp.Id = req.Id
			return resp, m.err
		}
		return nil, m.err
	}
}

// ─── DNS message builders ─────────────────────────────────────────────────────

func makeAResponse(domain string) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	m.Response = true
	m.Rcode = dns.RcodeSuccess
	rr, _ := dns.NewRR(dns.Fqdn(domain) + " 300 IN A 1.2.3.4")
	m.Answer = []dns.RR{rr}
	return m
}

func makeCNAMEResponse(from, to string) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(from), dns.TypeCNAME)
	m.Response = true
	m.Rcode = dns.RcodeSuccess
	rr, _ := dns.NewRR(dns.Fqdn(from) + " 300 IN CNAME " + dns.Fqdn(to))
	m.Answer = []dns.RR{rr}
	return m
}

// ─── Test resolver builder ────────────────────────────────────────────────────
// Uses the real standalone cache/filter packages (not a core-local mock),
// matching how NewRacingResolver is actually wired in internal/server.

func newTestResolverWithClients(clients []mockUpstreamClient) *RacingResolver {
	c := cache.New(1000, &mockECC{}, 4)
	f := filter.New()
	upstreams := make([]Upstream, len(clients))
	for i, cl := range clients {
		cc := cl
		upstreams[i] = Upstream{
			Name:     "mock-" + string(rune('A'+i)),
			Latency:  cl.latency,
			Protocol: ProtocolPlain,
			client:   &cc,
		}
	}
	return NewRacingResolver(upstreams, c, f)
}

// ─── Context helper ───────────────────────────────────────────────────────────

func testCtx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cancel // deliberately not deferred here; caller's test scope is short-lived
	return ctx
}

// ─── Network helpers ──────────────────────────────────────────────────────────

// freeUDPPort returns a free UDP port on localhost for test servers.
func freeUDPPort() int {
	conn, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port
}
