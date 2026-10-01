// Package core — LombokDNSProxy · Resolver tests
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/codinglombok/lombok-dns-proxy/src/cache"
	"github.com/codinglombok/lombok-dns-proxy/src/filter"
)

// --- Resolve: cache hit ---

func TestResolve_CacheHit(t *testing.T) {
	c := cache.New(1000, &mockECC{}, 4)
	f := filter.New()
	resolver := NewRacingResolver(nil, c, f)

	domain := "cache.example.com."
	cached := makeAResponse("cache.example.com")
	c.Set(domain, dns.TypeA, cached)

	req := new(dns.Msg)
	req.SetQuestion(domain, dns.TypeA)

	resp, err := resolver.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Answer) == 0 {
		t.Fatal("expected answer from cache, got none")
	}
	if resolver.stats.CacheHits != 1 {
		t.Errorf("expected 1 cache hit, got %d", resolver.stats.CacheHits)
	}
}

// --- Resolve: blocked domain → NXDOMAIN ---

func TestResolve_BlockedDomain(t *testing.T) {
	c := cache.New(1000, &mockECC{}, 4)
	f := filter.New()
	f.Block("ads.blocked.com")

	resolver := NewRacingResolver(nil, c, f)

	req := new(dns.Msg)
	req.SetQuestion("ads.blocked.com.", dns.TypeA)

	resp, err := resolver.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN (%d), got %d", dns.RcodeNameError, resp.Rcode)
	}
	if resolver.stats.FilteredCount != 1 {
		t.Errorf("expected 1 filtered, got %d", resolver.stats.FilteredCount)
	}
}

// --- Resolve: DNS racing returns fastest upstream ---

func TestResolve_Racing_FastestWins(t *testing.T) {
	fast := makeAResponse("fast.example.com")
	slow := makeAResponse("fast.example.com")

	clients := []mockUpstreamClient{
		{latency: 200 * time.Millisecond, response: slow},
		{latency: 10 * time.Millisecond, response: fast}, // should win
		{latency: 150 * time.Millisecond, response: slow},
	}
	resolver := newTestResolverWithClients(clients)

	req := new(dns.Msg)
	req.SetQuestion("fast.example.com.", dns.TypeA)

	start := time.Now()
	resp, err := resolver.Resolve(context.Background(), req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Answer) == 0 {
		t.Fatal("expected answer")
	}
	if elapsed > 80*time.Millisecond {
		t.Errorf("racing took too long: %v (expected ~10ms)", elapsed)
	}
}

// --- Resolve: all upstreams fail ---

func TestResolve_AllUpstreamsFail(t *testing.T) {
	clients := []mockUpstreamClient{
		{latency: 5 * time.Millisecond, err: &net.DNSError{Err: "server failed"}},
		{latency: 5 * time.Millisecond, err: &net.DNSError{Err: "server failed"}},
	}
	resolver := newTestResolverWithClients(clients)

	req := new(dns.Msg)
	req.SetQuestion("fail.example.com.", dns.TypeA)

	resp, _ := resolver.Resolve(context.Background(), req)
	if resp.Rcode != dns.RcodeServerFailure {
		t.Errorf("expected SERVFAIL, got %d", resp.Rcode)
	}
}

// --- Resolve: context cancellation ---

func TestResolve_ContextCancelled(t *testing.T) {
	clients := []mockUpstreamClient{
		{latency: 500 * time.Millisecond, response: makeAResponse("slow.example.com")},
	}
	resolver := newTestResolverWithClients(clients)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	req := new(dns.Msg)
	req.SetQuestion("slow.example.com.", dns.TypeA)

	_, err := resolver.Resolve(ctx, req)
	if err != nil {
		t.Logf("got expected error on cancel: %v", err)
	}
}

// --- Cache benchmarks ---

func BenchmarkCacheGet_Hit(b *testing.B) {
	c := cache.New(100000, &mockECC{}, 16)
	msg := makeAResponse("bench.example.com")
	c.Set("bench.example.com.", dns.TypeA, msg)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Get("bench.example.com.", dns.TypeA)
	}
}

func BenchmarkFilter_IsBlocked_Hit(b *testing.B) {
	f := filter.New()
	for i := 0; i < 10000; i++ {
		f.Block("ads" + string(rune(i%26+'a')) + ".tracker.com")
	}
	f.Block("ads.tracker.com")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.IsBlocked("ads.tracker.com")
	}
}

func BenchmarkFilter_IsBlocked_Miss(b *testing.B) {
	f := filter.New()
	for i := 0; i < 100000; i++ {
		f.Block("blocked" + string(rune(i%26+'a')) + ".bad.com")
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.IsBlocked("clean.example.com")
	}
}
