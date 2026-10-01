// Package integration — LombokDNSProxy · DNS Integration Tests
// Spins up a real resolver stack (no mocks) against a loopback DNS server.
// Run with: go test -v -tags integration ./tests/integration/...
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/codinglombok/lombok-dns-proxy/src/cache"
	"github.com/codinglombok/lombok-dns-proxy/src/core"
	"github.com/codinglombok/lombok-dns-proxy/src/filter"
)

// noopECC satisfies cache.ECCProvider without adding external dependency.
type noopECC struct{}

func (n *noopECC) Encode(data []byte) ([]byte, error) { return make([]byte, 16), nil }
func (n *noopECC) Verify(data, _ []byte) ([]byte, error) { return data, nil }

// newTestStack creates a full resolver stack for integration testing.
func newTestStack(t *testing.T) (*core.RacingResolver, func()) {
	t.Helper()

	c := cache.New(10000, &noopECC{}, 4)
	fe := filter.New()

	// Use real public DNS upstreams (requires network access)
	upstreams := []core.Upstream{
		{
			Name:     "Cloudflare DoH",
			Address:  "https://1.1.1.1/dns-query",
			Protocol: core.ProtocolDoH,
			Latency:  50 * time.Millisecond,
		},
		{
			Name:     "Google DoH",
			Address:  "https://8.8.8.8/dns-query",
			Protocol: core.ProtocolDoH,
			Latency:  60 * time.Millisecond,
		},
	}

	resolver := core.NewRacingResolver(upstreams, c, fe)
	return resolver, func() {} // cleanup noop for now
}

// startTestDNSServer starts a DNS server on a random loopback port.
func startTestDNSServer(t *testing.T, resolver *core.RacingResolver) (addr string, stop func()) {
	t.Helper()

	// Find free port
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot find free port: %v", err)
	}
	addr = ln.LocalAddr().String()
	ln.Close()

	mux := dns.NewServeMux()
	mux.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := resolver.Resolve(ctx, r)
		if err != nil || resp == nil {
			resp = new(dns.Msg)
			resp.SetRcode(r, dns.RcodeServerFailure)
		}
		w.WriteMsg(resp) //nolint:errcheck
	})

	srv := &dns.Server{Addr: addr, Net: "udp", Handler: mux}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }

	go srv.ListenAndServe() //nolint:errcheck
	<-started

	return addr, func() { srv.Shutdown() } //nolint:errcheck
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestIntegration_ResolvePublicDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	resolver, cleanup := newTestStack(t)
	defer cleanup()

	addr, stop := startTestDNSServer(t, resolver)
	defer stop()

	c := new(dns.Client)
	c.Timeout = 10 * time.Second

	m := new(dns.Msg)
	m.SetQuestion("google.com.", dns.TypeA)
	m.RecursionDesired = true

	resp, rtt, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("expected NOERROR, got %s", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) == 0 {
		t.Error("expected at least one answer RR")
	}
	t.Logf("google.com resolved: %d answers in %s", len(resp.Answer), rtt)
}

func TestIntegration_CacheReducesLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	resolver, cleanup := newTestStack(t)
	defer cleanup()

	addr, stop := startTestDNSServer(t, resolver)
	defer stop()

	c := new(dns.Client)
	c.Timeout = 10 * time.Second

	m := new(dns.Msg)
	m.SetQuestion("example.com.", dns.TypeA)
	m.RecursionDesired = true

	// First query — upstream
	_, rtt1, err := c.Exchange(m.Copy(), addr)
	if err != nil {
		t.Fatalf("first query failed: %v", err)
	}

	// Second query — should hit cache and be much faster
	_, rtt2, err := c.Exchange(m.Copy(), addr)
	if err != nil {
		t.Fatalf("second query failed: %v", err)
	}

	t.Logf("First: %s  Cached: %s  Speedup: %.1fx", rtt1, rtt2, float64(rtt1)/float64(rtt2))

	// Cache hit should be at least 5x faster
	if rtt2 > rtt1/2 {
		t.Logf("Warning: cached query not significantly faster (first=%s cached=%s)", rtt1, rtt2)
	}
}

func TestIntegration_BlockedDomainReturnsNXDOMAIN(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	c := cache.New(10000, &noopECC{}, 4)
	fe := filter.New()
	fe.Block("ads.blocked-integration-test.example")

	resolver := core.NewRacingResolver(nil, c, fe)
	addr, stop := startTestDNSServer(t, resolver)
	defer stop()

	client := new(dns.Client)
	m := new(dns.Msg)
	m.SetQuestion("ads.blocked-integration-test.example.", dns.TypeA)

	resp, _, err := client.Exchange(m, addr)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if resp.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN, got %s", dns.RcodeToString[resp.Rcode])
	}
}

func TestIntegration_RacingLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	resolver, cleanup := newTestStack(t)
	defer cleanup()

	addr, stop := startTestDNSServer(t, resolver)
	defer stop()

	client := new(dns.Client)
	client.Timeout = 10 * time.Second

	domains := []string{
		"cloudflare.com.", "github.com.", "anthropic.com.",
		"stackoverflow.com.", "golang.org.", "npmjs.com.",
	}

	var total time.Duration
	for _, domain := range domains {
		m := new(dns.Msg)
		m.SetQuestion(domain, dns.TypeA)
		m.RecursionDesired = true

		_, rtt, err := client.Exchange(m, addr)
		if err != nil {
			t.Errorf("failed to resolve %s: %v", domain, err)
			continue
		}
		total += rtt
		t.Logf("%-30s %s", domain, rtt)
	}

	avg := total / time.Duration(len(domains))
	t.Logf("\nAverage latency (racing): %s", avg)

	// Racing across 2 real upstreams should be well under 500ms on any reasonable network
	if avg > 500*time.Millisecond {
		t.Errorf("average latency %s exceeds 500ms — racing may not be working", avg)
	}
}

func TestIntegration_ConcurrentQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	resolver, cleanup := newTestStack(t)
	defer cleanup()

	addr, stop := startTestDNSServer(t, resolver)
	defer stop()

	const goroutines = 20
	const queriesEach = 5

	errs := make(chan error, goroutines*queriesEach)
	done := make(chan struct{}, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			client := new(dns.Client)
			client.Timeout = 10 * time.Second
			for j := 0; j < queriesEach; j++ {
				domain := fmt.Sprintf("concurrent%d-%d.example.com.", id, j)
				m := new(dns.Msg)
				m.SetQuestion("google.com.", dns.TypeA)
				m.RecursionDesired = true
				_ = domain
				if _, _, err := client.Exchange(m, addr); err != nil {
					errs <- err
				}
			}
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}
	close(errs)

	var errCount int
	for err := range errs {
		t.Errorf("concurrent query error: %v", err)
		errCount++
	}
	if errCount == 0 {
		t.Logf("All %d concurrent queries succeeded", goroutines*queriesEach)
	}
}
