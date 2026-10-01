// Package api — LombokDNSProxy · Query Log
// Thread-safe ring buffer for DNS query logs with filtering and search.
// Log entries are kept in memory only (privacy-first).
// Disk persistence is opt-in via config (log.query_log_persist: true).
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package api

import (
	"strings"
	"sync"
	"time"
)

const defaultMaxEntries = 10_000

// QueryEntry is one resolved (or blocked) DNS query.
type QueryEntry struct {
	Timestamp  time.Time `json:"ts"`
	Domain     string    `json:"domain"`
	Qtype      string    `json:"qtype"`
	Result     string    `json:"result"`   // "blocked" | "cached" | "upstream"
	Upstream   string    `json:"upstream"` // upstream name, or "" if blocked/cached
	LatencyMs  float64   `json:"latencyMs"`
	ClientIP   string    `json:"clientIp"`
	Cached     bool      `json:"cached"`
	Blocked    bool      `json:"blocked"`
}

// QueryLog is a thread-safe ring buffer of query log entries.
type QueryLog struct {
	mu         sync.RWMutex
	entries    []QueryEntry
	head       int  // index of next write position
	size       int  // number of valid entries currently stored
	maxEntries int
	enabled    bool
}

// NewQueryLog creates a QueryLog with the given capacity.
// If enabled is false, Log() is a no-op and all reads return empty results.
func NewQueryLog(maxEntries int, enabled bool) *QueryLog {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	return &QueryLog{
		entries:    make([]QueryEntry, maxEntries),
		maxEntries: maxEntries,
		enabled:    enabled,
	}
}

// Log appends one entry to the ring buffer. Safe to call from multiple goroutines.
func (q *QueryLog) Log(entry QueryEntry) {
	if !q.enabled {
		return
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	q.mu.Lock()
	q.entries[q.head] = entry
	q.head = (q.head + 1) % q.maxEntries
	if q.size < q.maxEntries {
		q.size++
	}
	q.mu.Unlock()
}

// QueryFilter controls which entries are returned from Search.
type QueryFilter struct {
	Domain string // substring match on domain (case-insensitive)
	Result string // "blocked" | "cached" | "upstream" | "" (all)
	Limit  int    // max entries to return (0 = default 100)
}

// SearchResult holds the filtered entries and total count.
type SearchResult struct {
	Entries []QueryEntry `json:"entries"`
	Total   int          `json:"total"` // total entries in the log (before filter)
}

// Search returns log entries matching f, newest-first.
func (q *QueryLog) Search(f QueryFilter) SearchResult {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}

	q.mu.RLock()
	defer q.mu.RUnlock()

	total := q.size
	out := make([]QueryEntry, 0, f.Limit)

	// Walk ring buffer newest-first
	for i := 0; i < q.size && len(out) < f.Limit; i++ {
		idx := (q.head - 1 - i + q.maxEntries) % q.maxEntries
		e := q.entries[idx]

		if f.Domain != "" && !strings.Contains(strings.ToLower(e.Domain), strings.ToLower(f.Domain)) {
			continue
		}
		if f.Result != "" && e.Result != f.Result {
			continue
		}
		out = append(out, e)
	}

	return SearchResult{Entries: out, Total: total}
}

// Stats returns aggregate statistics derived from the log buffer.
type LogStats struct {
	TotalQueries   uint64  `json:"totalQueries"`
	BlockedCount   uint64  `json:"blockedCount"`
	CachedCount    uint64  `json:"cachedCount"`
	UpstreamCount  uint64  `json:"upstreamCount"`
	AvgLatencyMs   float64 `json:"avgLatencyMs"`
	CacheHitRate   float64 `json:"cacheHitRate"`
}

// Stats computes aggregate stats from all buffered entries.
func (q *QueryLog) Stats() LogStats {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var s LogStats
	var totalLatency float64

	for i := 0; i < q.size; i++ {
		e := q.entries[i]
		s.TotalQueries++
		totalLatency += e.LatencyMs
		switch e.Result {
		case "blocked":
			s.BlockedCount++
		case "cached":
			s.CachedCount++
		case "upstream":
			s.UpstreamCount++
		}
	}

	if s.TotalQueries > 0 {
		s.AvgLatencyMs = totalLatency / float64(s.TotalQueries)
		resolved := s.TotalQueries - s.BlockedCount
		if resolved > 0 {
			s.CacheHitRate = float64(s.CachedCount) / float64(resolved) * 100
		}
	}
	return s
}

// Flush clears all entries from the log.
func (q *QueryLog) Flush() {
	q.mu.Lock()
	q.head = 0
	q.size = 0
	q.mu.Unlock()
}

// TopBlocked returns the N most-blocked domains.
func (q *QueryLog) TopBlocked(n int) []DomainCount {
	q.mu.RLock()
	counts := make(map[string]int)
	for i := 0; i < q.size; i++ {
		e := q.entries[i]
		if e.Blocked {
			counts[e.Domain]++
		}
	}
	q.mu.RUnlock()

	// Simple sort: build slice, insertion sort top N
	type dc struct {
		domain string
		count  int
	}
	all := make([]dc, 0, len(counts))
	for d, c := range counts {
		all = append(all, dc{d, c})
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].count > all[j-1].count; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}

	if n > len(all) {
		n = len(all)
	}
	out := make([]DomainCount, n)
	for i := range out {
		out[i] = DomainCount{Domain: all[i].domain, Count: all[i].count}
	}
	return out
}

// DomainCount pairs a domain with its occurrence count.
type DomainCount struct {
	Domain string `json:"domain"`
	Count  int    `json:"count"`
}
