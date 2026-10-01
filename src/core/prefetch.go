// Package core — LombokDNSProxy · Predictive Prefetcher
// Markov(1) model learns domain access sequences and pre-resolves likely-next
// domains before they are requested. Purely in-memory, per-session, never leaves device.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"context"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	defaultPrefetchThreshold = 0.70
	defaultPrefetchTimeout   = 3 * time.Second
	defaultMaxModelEntries   = 50_000
)

// PrefetchConfig controls prefetch behaviour.
type PrefetchConfig struct {
	Enabled   bool
	Threshold float64
	Timeout   time.Duration
	MaxModel  int
}

// DefaultPrefetchConfig returns sensible defaults.
func DefaultPrefetchConfig() PrefetchConfig {
	return PrefetchConfig{Enabled: true, Threshold: defaultPrefetchThreshold, Timeout: defaultPrefetchTimeout, MaxModel: defaultMaxModelEntries}
}

// Prefetcher learns domain access patterns and pre-resolves likely-next queries.
type Prefetcher struct {
	cfg      PrefetchConfig
	mu       sync.Mutex
	model    map[string]map[string]uint32
	totals   map[string]uint32
	lastSeen string
}

// NewPrefetcher creates a blank Prefetcher with the given config.
func NewPrefetcher(cfg PrefetchConfig) *Prefetcher {
	return &Prefetcher{cfg: cfg, model: make(map[string]map[string]uint32), totals: make(map[string]uint32)}
}

// Record updates the Markov model: lastSeen → domain.
func (p *Prefetcher) Record(domain string) {
	if !p.cfg.Enabled || domain == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prev := p.lastSeen
	p.lastSeen = domain
	if prev == "" || prev == domain {
		return
	}
	if p.model[prev] == nil {
		p.model[prev] = make(map[string]uint32)
	}
	p.model[prev][domain]++
	p.totals[prev]++
	if len(p.model) > p.cfg.MaxModel {
		p.pruneModel()
	}
}

// MaybePrefetch fires an async resolve if P(next|domain) >= threshold.
func (p *Prefetcher) MaybePrefetch(domain string, r *RacingResolver) {
	if !p.cfg.Enabled {
		return
	}
	p.mu.Lock()
	next, prob := p.bestNext(domain)
	p.mu.Unlock()
	if prob < p.cfg.Threshold || next == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), p.cfg.Timeout)
		defer cancel()
		req := new(dns.Msg)
		req.SetQuestion(dns.Fqdn(next), dns.TypeA)
		req.RecursionDesired = true
		r.Resolve(ctx, req) //nolint:errcheck
	}()
}

func (p *Prefetcher) bestNext(domain string) (string, float64) {
	nexts, ok := p.model[domain]
	if !ok || p.totals[domain] == 0 {
		return "", 0
	}
	var bestDomain string
	var bestCount uint32
	for d, count := range nexts {
		if count > bestCount {
			bestCount = count
			bestDomain = d
		}
	}
	return bestDomain, float64(bestCount) / float64(p.totals[domain])
}

// ModelSize returns the number of tracked source domains.
func (p *Prefetcher) ModelSize() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.model)
}

// Reset clears the entire model.
func (p *Prefetcher) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.model = make(map[string]map[string]uint32)
	p.totals = make(map[string]uint32)
	p.lastSeen = ""
}

// PrefetchTransition is one domain-pair transition for the API/dashboard.
type PrefetchTransition struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Probability float64 `json:"probability"`
}

func (p *Prefetcher) pruneModel() {
	for domain, total := range p.totals {
		if total <= 1 {
			delete(p.model, domain)
			delete(p.totals, domain)
		}
	}
}
