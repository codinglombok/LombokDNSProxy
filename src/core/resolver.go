// Package core — LombokDNSProxy DNS Racing Resolver
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/codinglombok/lombok-dns-proxy/src/cache"
	"github.com/codinglombok/lombok-dns-proxy/src/filter"
)

// RacingResolver sends DNS queries to multiple upstreams in parallel
// and returns the first valid (non-error) response.
//
// Cache and filter are the standalone src/cache and src/filter packages
// (sharded ECC cache, DAFSA filter trie + auto-updater) rather than a
// core-local reimplementation — core is the consumer, not a second owner.
type RacingResolver struct {
	upstreams []Upstream
	cache     *cache.Cache
	filter    *filter.Engine
	prefetch  *Prefetcher
	mu        sync.RWMutex
	stats     ResolverStats
}

// ResolverStats collects per-session metrics.
type ResolverStats struct {
	TotalQueries  uint64
	CacheHits     uint64
	FilteredCount uint64
	RaceWins      [8]uint64 // which upstream won, index 0 = fastest
	AvgLatencyMs  float64
}

// NewRacingResolver creates a resolver with the given upstreams.
// Upstreams are sorted by EWMA latency at start and re-sorted periodically.
func NewRacingResolver(upstreams []Upstream, c *cache.Cache, f *filter.Engine) *RacingResolver {
	r := &RacingResolver{
		upstreams: upstreams,
		cache:     c,
		filter:    f,
		prefetch:  NewPrefetcher(DefaultPrefetchConfig()),
	}
	go r.latencyRebalancerLoop()
	return r
}

// Resolve handles an incoming DNS message.
// Pipeline: cache check → filter check → upstream race → cache store → prefetch trigger.
func (r *RacingResolver) Resolve(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	domain := extractDomain(req)

	// 1. Cache hit — fastest path, ~0.1ms
	if cached, ok := r.cache.Get(domain, req.Question[0].Qtype); ok {
		resp := cached.ToResponse(req.Id)
		r.incrementStat(&r.stats.CacheHits)
		go r.prefetch.Record(domain) // non-blocking
		return resp, nil
	}

	// 2. Filter check — blocked domain → NXDOMAIN, ~0.01ms
	if r.filter.IsBlocked(domain) {
		r.incrementStat(&r.stats.FilteredCount)
		return nxdomainResponse(req), nil
	}

	// 3. Racing: send to top-N upstreams in parallel
	resp, err := r.race(ctx, req)
	if err != nil {
		return serverFailResponse(req), err
	}

	// 4. Store in ECC-verified cache
	r.cache.Set(domain, req.Question[0].Qtype, resp)

	// 5. Trigger predictive prefetch (async, non-blocking)
	go r.prefetch.MaybePrefetch(domain, r)

	r.incrementStat(&r.stats.TotalQueries)
	return resp, nil
}

// raceResult carries the upstream response and which upstream won.
type raceResult struct {
	msg      *dns.Msg
	err      error
	upstream int
}

// race sends the query to the top N upstreams and returns the first valid response.
// Cancelled contexts automatically stop in-flight requests.
func (r *RacingResolver) race(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	r.mu.RLock()
	upstreams := r.topNUpstreams(4) // race top-4 by latency
	r.mu.RUnlock()

	ch := make(chan raceResult, len(upstreams))
	raceCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	for i, up := range upstreams {
		go func(idx int, u Upstream) {
			msg, err := u.client.Exchange(raceCtx, req)
			ch <- raceResult{msg: msg, err: err, upstream: idx}
		}(i, up)
	}

	for range upstreams {
		result := <-ch
		if result.err == nil && result.msg != nil && result.msg.Rcode == dns.RcodeSuccess {
			cancel() // cancel remaining in-flight requests
			r.stats.RaceWins[min(result.upstream, 7)]++
			return result.msg, nil
		}
	}
	return nil, ErrAllUpstreamsFailed
}

// topNUpstreams returns the N upstreams with lowest EWMA latency.
// Caller must hold r.mu.RLock.
func (r *RacingResolver) topNUpstreams(n int) []Upstream {
	sorted := make([]Upstream, len(r.upstreams))
	copy(sorted, r.upstreams)
	// Simple insertion sort — N is small (≤8), allocation-free
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].Latency < sorted[j-1].Latency; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	if n > len(sorted) {
		n = len(sorted)
	}
	return sorted[:n]
}

// latencyRebalancerLoop probes all upstreams every 30s and updates EWMA.
func (r *RacingResolver) latencyRebalancerLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		r.probeAllUpstreams()
	}
}

func (r *RacingResolver) probeAllUpstreams() {
	probe := new(dns.Msg)
	probe.SetQuestion("health.lombok.internal.", dns.TypeA)
	probe.RecursionDesired = true

	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.upstreams {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		r.upstreams[i].client.Exchange(ctx, probe) //nolint:errcheck // probe only
		cancel()
		elapsed := time.Since(start)
		// EWMA alpha = 0.3
		r.upstreams[i].Latency = time.Duration(
			float64(r.upstreams[i].Latency)*0.7 + float64(elapsed)*0.3,
		)
	}
}

// Helper functions

func extractDomain(msg *dns.Msg) string {
	if len(msg.Question) == 0 {
		return ""
	}
	return dns.Fqdn(msg.Question[0].Name)
}

func nxdomainResponse(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetRcode(req, dns.RcodeNameError)
	resp.Authoritative = true
	return resp
}

func serverFailResponse(req *dns.Msg) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetRcode(req, dns.RcodeServerFailure)
	return resp
}

func (r *RacingResolver) incrementStat(counter *uint64) {
	r.mu.Lock()
	*counter++
	r.mu.Unlock()
}

// ErrAllUpstreamsFailed is returned when every upstream fails or times out.
var ErrAllUpstreamsFailed = &net.DNSError{Err: "all upstreams failed", IsTimeout: true}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
