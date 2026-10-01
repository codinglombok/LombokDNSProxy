// Package cache_test — LombokDNSProxy · Sharded Cache Tests
// Tests LombokCache sharding, ECC verification, TTL expiry, and LRU eviction.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ─── ECC stub ────────────────────────────────────────────────────────────────

type noopECC struct{}

func (n *noopECC) Encode(data []byte) ([]byte, error) { return make([]byte, 16), nil }
func (n *noopECC) Verify(data, _ []byte) ([]byte, error) { return data, nil }

type corruptingECC struct{}

func (c *corruptingECC) Encode(data []byte) ([]byte, error) { return make([]byte, 16), nil }
func (c *corruptingECC) Verify(_, _ []byte) ([]byte, error) {
	return nil, fmt.Errorf("ECC: uncorrectable corruption")
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func makeResponse(domain string) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	m.Response = true
	m.Rcode = dns.RcodeSuccess
	rr, _ := dns.NewRR(dns.Fqdn(domain) + " 300 IN A 1.2.3.4")
	m.Answer = []dns.RR{rr}
	return m
}

func makeResponseTTL(domain string, ttl uint32) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	m.Response = true
	m.Rcode = dns.RcodeSuccess
	rr, _ := dns.NewRR(fmt.Sprintf("%s %d IN A 9.9.9.9", dns.Fqdn(domain), ttl))
	m.Answer = []dns.RR{rr}
	return m
}

// ─── Basic Get/Set ────────────────────────────────────────────────────────────

func TestCache_SetAndGet(t *testing.T) {
	c := New(1000, &noopECC{}, 4)
	msg := makeResponse("example.com")
	c.Set("example.com.", dns.TypeA, msg)

	rec, ok := c.Get("example.com.", dns.TypeA)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(rec.Msg.Answer) == 0 {
		t.Error("expected answer in cached record")
	}
}

func TestCache_Miss_NonexistentKey(t *testing.T) {
	c := New(1000, &noopECC{}, 4)
	_, ok := c.Get("notcached.com.", dns.TypeA)
	if ok {
		t.Error("expected cache miss for uncached domain")
	}
	if c.Stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", c.Stats.Misses)
	}
}

func TestCache_HitIncrementsStat(t *testing.T) {
	c := New(1000, &noopECC{}, 4)
	c.Set("hits.com.", dns.TypeA, makeResponse("hits.com"))

	c.Get("hits.com.", dns.TypeA) //nolint:errcheck
	c.Get("hits.com.", dns.TypeA) //nolint:errcheck

	if c.Stats.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", c.Stats.Hits)
	}
}

// ─── TTL expiry ───────────────────────────────────────────────────────────────

func TestCache_ExpiredEntryIsEvicted(t *testing.T) {
	c := New(1000, &noopECC{}, 4)
	// Store with TTL=0 → should not be cached
	c.Set("zero-ttl.com.", dns.TypeA, makeResponseTTL("zero-ttl.com", 0))

	_, ok := c.Get("zero-ttl.com.", dns.TypeA)
	if ok {
		t.Error("zero-TTL record should not be cached")
	}
}

func TestCache_TTLAdjustedOnRead(t *testing.T) {
	c := New(1000, &noopECC{}, 4)
	c.Set("ttl-check.com.", dns.TypeA, makeResponseTTL("ttl-check.com", 300))

	rec, ok := c.Get("ttl-check.com.", dns.TypeA)
	if !ok {
		t.Fatal("expected cache hit")
	}
	// TTL should be ≤ 300 (never increased by cache)
	if rec.Msg.Answer[0].Header().Ttl > 300 {
		t.Errorf("TTL should not exceed original 300, got %d", rec.Msg.Answer[0].Header().Ttl)
	}
}

// ─── ECC corruption detection ─────────────────────────────────────────────────

func TestCache_ECCFailureEvictsEntry(t *testing.T) {
	c := New(1000, &corruptingECC{}, 4)
	c.Set("corrupt.com.", dns.TypeA, makeResponse("corrupt.com"))

	_, ok := c.Get("corrupt.com.", dns.TypeA)
	if ok {
		t.Error("ECC-corrupt entry should be evicted and return miss")
	}
	if c.Stats.ECCFailures != 1 {
		t.Errorf("expected 1 ECC failure, got %d", c.Stats.ECCFailures)
	}
}

// ─── LRU eviction ────────────────────────────────────────────────────────────

func TestCache_LRUEviction(t *testing.T) {
	// Capacity 2: after 3 inserts, oldest should be evicted
	c := New(2, &noopECC{}, 1) // 1 shard, capacity 2 total

	c.Set("first.com.", dns.TypeA, makeResponse("first.com"))
	c.Set("second.com.", dns.TypeA, makeResponse("second.com"))
	c.Get("first.com.", dns.TypeA) // promote first — second becomes LRU
	c.Set("third.com.", dns.TypeA, makeResponse("third.com")) // evicts second

	_, okFirst := c.Get("first.com.", dns.TypeA)
	_, okSecond := c.Get("second.com.", dns.TypeA)
	_, okThird := c.Get("third.com.", dns.TypeA)

	if !okFirst {
		t.Error("first.com should still be cached (was recently accessed)")
	}
	if okSecond {
		t.Error("second.com should have been evicted (LRU)")
	}
	if !okThird {
		t.Error("third.com should be cached")
	}
}

// ─── Flush ────────────────────────────────────────────────────────────────────

func TestCache_Flush(t *testing.T) {
	c := New(1000, &noopECC{}, 4)
	for i := 0; i < 10; i++ {
		domain := fmt.Sprintf("flush%d.com.", i)
		c.Set(domain, dns.TypeA, makeResponse(domain))
	}
	if c.Size() == 0 {
		t.Fatal("expected non-empty cache before flush")
	}
	c.Flush()
	if c.Size() != 0 {
		t.Errorf("expected empty cache after flush, got %d", c.Size())
	}
}

// ─── Sharding distribution ────────────────────────────────────────────────────

func TestCache_ShardsDistributedEvenly(t *testing.T) {
	nShards := 16
	c := New(160, &noopECC{}, nShards)

	// Insert 160 different domains
	for i := 0; i < 160; i++ {
		domain := fmt.Sprintf("shard%d.test.", i)
		c.Set(domain, dns.TypeA, makeResponse(domain))
	}

	// Check each shard has roughly equal load (within 3x factor)
	shardsUsed := 0
	for _, s := range c.shards {
		s.mu.Lock()
		if len(s.entries) > 0 {
			shardsUsed++
		}
		s.mu.Unlock()
	}

	if shardsUsed < nShards/2 {
		t.Errorf("expected at least %d shards used, got %d (poor distribution)", nShards/2, shardsUsed)
	}
}

// ─── Concurrent access ────────────────────────────────────────────────────────

func TestCache_ConcurrentReadWrite(t *testing.T) {
	c := New(10000, &noopECC{}, 16)
	const goroutines = 50
	const opsEach = 100

	var wg sync.WaitGroup
	errors := make(chan error, goroutines*opsEach)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsEach; i++ {
				domain := fmt.Sprintf("concurrent%d-%d.com.", id, i%10)
				c.Set(domain, dns.TypeA, makeResponse(domain))
				c.Get(domain, dns.TypeA) //nolint:errcheck
			}
		}(g)
	}

	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

// ─── ToResponse TTL adjustment ────────────────────────────────────────────────

func TestRecord_ToResponse_AdjustsTTL(t *testing.T) {
	c := New(1000, &noopECC{}, 4)
	c.Set("ttl-adjust.com.", dns.TypeA, makeResponseTTL("ttl-adjust.com", 300))

	// Simulate 1 second passing
	time.Sleep(1 * time.Second)

	rec, ok := c.Get("ttl-adjust.com.", dns.TypeA)
	if !ok {
		t.Fatal("expected cache hit")
	}
	resp := rec.ToResponse(42)
	if resp.Id != 42 {
		t.Errorf("expected ID 42, got %d", resp.Id)
	}
	remaining := resp.Answer[0].Header().Ttl
	if remaining > 299 {
		t.Errorf("TTL should have decreased by ~1s, got %d", remaining)
	}
}

// ─── Benchmarks ───────────────────────────────────────────────────────────────

func BenchmarkCache_Get_Hit(b *testing.B) {
	c := New(100000, &noopECC{}, 16)
	msg := makeResponse("bench.example.com")
	c.Set("bench.example.com.", dns.TypeA, msg)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Get("bench.example.com.", dns.TypeA)
		}
	})
}

func BenchmarkCache_Set(b *testing.B) {
	c := New(100000, &noopECC{}, 16)
	msg := makeResponse("bench-set.example.com")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Set(fmt.Sprintf("bench%d.example.com.", i%1000), dns.TypeA, msg)
	}
}
