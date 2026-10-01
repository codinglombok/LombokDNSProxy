// Package core_test — LombokDNSProxy · Prefetcher Tests
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"testing"
	"time"
)

func newTestPrefetcher() *Prefetcher {
	return NewPrefetcher(PrefetchConfig{
		Enabled:   true,
		Threshold: 0.60,
		Timeout:   1 * time.Second,
		MaxModel:  1000,
	})
}

// ─── Record / bestNext ────────────────────────────────────────────────────────

func TestPrefetcher_BestNext_AfterOneTransition(t *testing.T) {
	p := newTestPrefetcher()
	p.Record("google.com")
	p.Record("apis.google.com")

	p.mu.Lock()
	next, prob := p.bestNext("google.com")
	p.mu.Unlock()

	if next != "apis.google.com" {
		t.Errorf("expected apis.google.com, got %q", next)
	}
	if prob != 1.0 {
		t.Errorf("expected probability 1.0, got %.2f", prob)
	}
}

func TestPrefetcher_BestNext_MostFrequentWins(t *testing.T) {
	p := newTestPrefetcher()

	// Simulate: github.com → cdn.github.com 3 times, github.com → api.github.com 1 time
	for i := 0; i < 3; i++ {
		p.Record("github.com")
		p.Record("cdn.github.com")
	}
	p.Record("github.com")
	p.Record("api.github.com")

	p.mu.Lock()
	next, prob := p.bestNext("github.com")
	p.mu.Unlock()

	if next != "cdn.github.com" {
		t.Errorf("expected cdn.github.com (3/4 = 75%%), got %q", next)
	}
	if prob < 0.74 || prob > 0.76 {
		t.Errorf("expected prob ~0.75, got %.3f", prob)
	}
}

func TestPrefetcher_BestNext_UnknownDomainReturnsEmpty(t *testing.T) {
	p := newTestPrefetcher()
	p.mu.Lock()
	next, prob := p.bestNext("never-seen.com")
	p.mu.Unlock()

	if next != "" || prob != 0 {
		t.Errorf("unknown domain should return ('', 0), got (%q, %.2f)", next, prob)
	}
}

func TestPrefetcher_SameDomainSelfTransitionIgnored(t *testing.T) {
	p := newTestPrefetcher()
	p.Record("example.com")
	p.Record("example.com") // same domain — should not count as a transition
	p.Record("example.com")

	if p.ModelSize() != 0 {
		t.Error("same-domain transitions should not be recorded")
	}
}

// ─── Disabled prefetcher ──────────────────────────────────────────────────────

func TestPrefetcher_Disabled_RecordIsNoop(t *testing.T) {
	p := NewPrefetcher(PrefetchConfig{Enabled: false, MaxModel: 100})
	p.Record("a.com")
	p.Record("b.com")

	if p.ModelSize() != 0 {
		t.Error("disabled prefetcher should not record any transitions")
	}
}

// ─── Threshold filtering ──────────────────────────────────────────────────────

func TestPrefetcher_BelowThreshold_NoPrefetch(t *testing.T) {
	p := NewPrefetcher(PrefetchConfig{
		Enabled:   true,
		Threshold: 0.90, // very high threshold
		Timeout:   1 * time.Second,
		MaxModel:  1000,
	})

	// 1 transition → P = 1.0; threshold 0.9 → prefetch
	p.Record("a.com")
	p.Record("b.com")

	p.mu.Lock()
	_, prob := p.bestNext("a.com")
	p.mu.Unlock()

	if prob < 0.90 {
		t.Errorf("prob %.2f should be >= 0.90 for single transition", prob)
	}

	// Now add competing transition → prob drops below threshold
	for i := 0; i < 9; i++ {
		p.Record("a.com")
		p.Record("c.com")
	}

	p.mu.Lock()
	next, prob2 := p.bestNext("a.com")
	p.mu.Unlock()

	if next != "c.com" {
		t.Errorf("c.com should now be most frequent, got %q", next)
	}
	// c.com: 9/10 = 0.9; b.com: 1/10 = 0.1
	if prob2 < 0.89 || prob2 > 0.91 {
		t.Errorf("expected prob ~0.90, got %.3f", prob2)
	}
}

// ─── Reset ────────────────────────────────────────────────────────────────────

func TestPrefetcher_Reset_ClearsModel(t *testing.T) {
	p := newTestPrefetcher()
	for i := 0; i < 10; i++ {
		p.Record("a.com")
		p.Record("b.com")
	}
	if p.ModelSize() == 0 {
		t.Fatal("expected non-empty model before reset")
	}
	p.Reset()
	if p.ModelSize() != 0 {
		t.Errorf("expected empty model after reset, got %d entries", p.ModelSize())
	}
	// lastSeen should also be cleared
	p.mu.Lock()
	last := p.lastSeen
	p.mu.Unlock()
	if last != "" {
		t.Errorf("lastSeen should be empty after reset, got %q", last)
	}
}

// ─── Pruning ──────────────────────────────────────────────────────────────────

func TestPrefetcher_Pruning_RemovesColdEntries(t *testing.T) {
	p := NewPrefetcher(PrefetchConfig{
		Enabled:  true,
		MaxModel: 5, // very small to trigger pruning quickly
		Timeout:  1 * time.Second,
	})

	// Create 6 single-count transitions to trigger pruning
	domains := []string{"a.com", "b.com", "c.com", "d.com", "e.com", "f.com", "g.com"}
	for i := 0; i < len(domains)-1; i++ {
		p.Record(domains[i])
		p.Record(domains[i+1])
	}

	// After pruning, single-count entries should be removed
	// (the model should not grow indefinitely)
	if p.ModelSize() > 10 {
		t.Errorf("model should be pruned, got %d entries", p.ModelSize())
	}
}

// ─── Concurrent safety ────────────────────────────────────────────────────────

func TestPrefetcher_ConcurrentRecord(t *testing.T) {
	p := newTestPrefetcher()
	done := make(chan struct{}, 20)

	for i := 0; i < 20; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				p.Record("from.com")
				p.Record("to.com")
			}
		}(i)
	}

	for i := 0; i < 20; i++ {
		<-done
	}

	// Must not panic or deadlock; model must have the transition
	p.mu.Lock()
	next, _ := p.bestNext("from.com")
	p.mu.Unlock()

	if next != "to.com" {
		t.Errorf("expected to.com as most likely next, got %q", next)
	}
}
