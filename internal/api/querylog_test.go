// Package api_test — LombokDNSProxy · QueryLog Tests
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package api

import (
	"fmt"
	"testing"
	"time"
)

// ─── Basic Log / Search ───────────────────────────────────────────────────────

func TestQueryLog_LogAndSearch_BasicFlow(t *testing.T) {
	q := NewQueryLog(100, true)
	q.Log(QueryEntry{Domain: "example.com", Qtype: "A", Result: "upstream", LatencyMs: 10})
	q.Log(QueryEntry{Domain: "ads.blocked.com", Qtype: "A", Result: "blocked", Blocked: true})
	q.Log(QueryEntry{Domain: "cached.com", Qtype: "A", Result: "cached", Cached: true})

	res := q.Search(QueryFilter{})
	if res.Total != 3 {
		t.Errorf("expected 3 total entries, got %d", res.Total)
	}
	if len(res.Entries) != 3 {
		t.Errorf("expected 3 entries returned, got %d", len(res.Entries))
	}
}

func TestQueryLog_Disabled_NoLog(t *testing.T) {
	q := NewQueryLog(100, false) // disabled
	q.Log(QueryEntry{Domain: "silenced.com", Result: "upstream"})

	res := q.Search(QueryFilter{})
	if res.Total != 0 {
		t.Errorf("disabled log should store nothing, got %d entries", res.Total)
	}
}

func TestQueryLog_Search_NewestFirst(t *testing.T) {
	q := NewQueryLog(100, true)
	for i := 0; i < 5; i++ {
		q.Log(QueryEntry{Domain: fmt.Sprintf("order%d.com", i), Result: "upstream"})
	}

	res := q.Search(QueryFilter{Limit: 5})
	// Newest entry should be first
	if res.Entries[0].Domain != "order4.com" {
		t.Errorf("expected newest entry first, got %s", res.Entries[0].Domain)
	}
}

// ─── Filter by domain ─────────────────────────────────────────────────────────

func TestQueryLog_Search_DomainFilter(t *testing.T) {
	q := NewQueryLog(100, true)
	q.Log(QueryEntry{Domain: "google.com", Result: "upstream"})
	q.Log(QueryEntry{Domain: "googlevideo.com", Result: "upstream"})
	q.Log(QueryEntry{Domain: "facebook.com", Result: "upstream"})

	res := q.Search(QueryFilter{Domain: "google"})
	if len(res.Entries) != 2 {
		t.Errorf("expected 2 google* entries, got %d", len(res.Entries))
	}
}

func TestQueryLog_Search_DomainFilter_CaseInsensitive(t *testing.T) {
	q := NewQueryLog(100, true)
	q.Log(QueryEntry{Domain: "Example.COM", Result: "upstream"})

	res := q.Search(QueryFilter{Domain: "example"})
	if len(res.Entries) != 1 {
		t.Errorf("domain filter should be case-insensitive, got %d results", len(res.Entries))
	}
}

// ─── Filter by result ─────────────────────────────────────────────────────────

func TestQueryLog_Search_ResultFilter(t *testing.T) {
	q := NewQueryLog(100, true)
	q.Log(QueryEntry{Domain: "a.com", Result: "blocked", Blocked: true})
	q.Log(QueryEntry{Domain: "b.com", Result: "cached", Cached: true})
	q.Log(QueryEntry{Domain: "c.com", Result: "upstream"})
	q.Log(QueryEntry{Domain: "d.com", Result: "blocked", Blocked: true})

	blocked := q.Search(QueryFilter{Result: "blocked"})
	if len(blocked.Entries) != 2 {
		t.Errorf("expected 2 blocked entries, got %d", len(blocked.Entries))
	}

	cached := q.Search(QueryFilter{Result: "cached"})
	if len(cached.Entries) != 1 {
		t.Errorf("expected 1 cached entry, got %d", len(cached.Entries))
	}
}

// ─── Limit ────────────────────────────────────────────────────────────────────

func TestQueryLog_Search_LimitRespected(t *testing.T) {
	q := NewQueryLog(200, true)
	for i := 0; i < 50; i++ {
		q.Log(QueryEntry{Domain: fmt.Sprintf("d%d.com", i), Result: "upstream"})
	}

	res := q.Search(QueryFilter{Limit: 10})
	if len(res.Entries) != 10 {
		t.Errorf("expected 10 entries with limit=10, got %d", len(res.Entries))
	}
	if res.Total != 50 {
		t.Errorf("total should be 50 regardless of limit, got %d", res.Total)
	}
}

func TestQueryLog_Search_LimitDefault100(t *testing.T) {
	q := NewQueryLog(200, true)
	for i := 0; i < 150; i++ {
		q.Log(QueryEntry{Domain: fmt.Sprintf("x%d.com", i), Result: "upstream"})
	}

	res := q.Search(QueryFilter{}) // no limit specified
	if len(res.Entries) != 100 {
		t.Errorf("default limit should be 100, got %d", len(res.Entries))
	}
}

func TestQueryLog_Search_LimitCapAt1000(t *testing.T) {
	q := NewQueryLog(2000, true)
	for i := 0; i < 1500; i++ {
		q.Log(QueryEntry{Domain: fmt.Sprintf("cap%d.com", i), Result: "upstream"})
	}

	res := q.Search(QueryFilter{Limit: 9999})
	if len(res.Entries) > 1000 {
		t.Errorf("limit should be capped at 1000, got %d", len(res.Entries))
	}
}

// ─── Ring buffer eviction ──────────────────────────────────────────────────────

func TestQueryLog_RingBuffer_OldestEvicted(t *testing.T) {
	q := NewQueryLog(3, true) // capacity 3

	q.Log(QueryEntry{Domain: "first.com", Result: "upstream"})
	q.Log(QueryEntry{Domain: "second.com", Result: "upstream"})
	q.Log(QueryEntry{Domain: "third.com", Result: "upstream"})
	q.Log(QueryEntry{Domain: "fourth.com", Result: "upstream"}) // evicts first.com

	res := q.Search(QueryFilter{})
	if res.Total != 3 {
		t.Errorf("ring buffer should cap at 3, got %d", res.Total)
	}

	// "first.com" should be gone
	for _, e := range res.Entries {
		if e.Domain == "first.com" {
			t.Error("oldest entry (first.com) should have been evicted")
		}
	}
	// "fourth.com" should be present
	found := false
	for _, e := range res.Entries {
		if e.Domain == "fourth.com" {
			found = true
		}
	}
	if !found {
		t.Error("newest entry (fourth.com) should be in buffer")
	}
}

// ─── Stats ────────────────────────────────────────────────────────────────────

func TestQueryLog_Stats_Counts(t *testing.T) {
	q := NewQueryLog(100, true)

	for i := 0; i < 10; i++ {
		q.Log(QueryEntry{Domain: "up.com", Result: "upstream", LatencyMs: 10})
	}
	for i := 0; i < 5; i++ {
		q.Log(QueryEntry{Domain: "b.com", Result: "blocked", Blocked: true, LatencyMs: 0})
	}
	for i := 0; i < 3; i++ {
		q.Log(QueryEntry{Domain: "c.com", Result: "cached", Cached: true, LatencyMs: 0.5})
	}

	s := q.Stats()
	if s.TotalQueries != 18 {
		t.Errorf("expected 18 total, got %d", s.TotalQueries)
	}
	if s.BlockedCount != 5 {
		t.Errorf("expected 5 blocked, got %d", s.BlockedCount)
	}
	if s.CachedCount != 3 {
		t.Errorf("expected 3 cached, got %d", s.CachedCount)
	}
	if s.UpstreamCount != 10 {
		t.Errorf("expected 10 upstream, got %d", s.UpstreamCount)
	}
}

func TestQueryLog_Stats_CacheHitRate(t *testing.T) {
	q := NewQueryLog(100, true)

	// 6 cached, 4 upstream → hit rate = 6/(6+4) = 60%
	for i := 0; i < 6; i++ {
		q.Log(QueryEntry{Result: "cached", Cached: true})
	}
	for i := 0; i < 4; i++ {
		q.Log(QueryEntry{Result: "upstream"})
	}

	s := q.Stats()
	if s.CacheHitRate < 59 || s.CacheHitRate > 61 {
		t.Errorf("expected cache hit rate ~60%%, got %.1f%%", s.CacheHitRate)
	}
}

func TestQueryLog_Stats_AvgLatency(t *testing.T) {
	q := NewQueryLog(100, true)
	q.Log(QueryEntry{Result: "upstream", LatencyMs: 10})
	q.Log(QueryEntry{Result: "upstream", LatencyMs: 20})
	q.Log(QueryEntry{Result: "upstream", LatencyMs: 30})

	s := q.Stats()
	if s.AvgLatencyMs < 19 || s.AvgLatencyMs > 21 {
		t.Errorf("expected avg latency ~20ms, got %.1fms", s.AvgLatencyMs)
	}
}

// ─── TopBlocked ───────────────────────────────────────────────────────────────

func TestQueryLog_TopBlocked_OrderedByCount(t *testing.T) {
	q := NewQueryLog(500, true)

	// ads.com: 5 times
	for i := 0; i < 5; i++ {
		q.Log(QueryEntry{Domain: "ads.com", Result: "blocked", Blocked: true})
	}
	// tracker.net: 3 times
	for i := 0; i < 3; i++ {
		q.Log(QueryEntry{Domain: "tracker.net", Result: "blocked", Blocked: true})
	}
	// evil.org: 1 time
	q.Log(QueryEntry{Domain: "evil.org", Result: "blocked", Blocked: true})

	top := q.TopBlocked(3)
	if len(top) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(top))
	}
	if top[0].Domain != "ads.com" || top[0].Count != 5 {
		t.Errorf("expected ads.com(5) first, got %s(%d)", top[0].Domain, top[0].Count)
	}
	if top[1].Domain != "tracker.net" || top[1].Count != 3 {
		t.Errorf("expected tracker.net(3) second, got %s(%d)", top[1].Domain, top[1].Count)
	}
}

func TestQueryLog_TopBlocked_NTopLimitedToAvailable(t *testing.T) {
	q := NewQueryLog(100, true)
	q.Log(QueryEntry{Domain: "a.com", Result: "blocked", Blocked: true})

	top := q.TopBlocked(10)
	if len(top) != 1 {
		t.Errorf("expected 1 entry (only 1 available), got %d", len(top))
	}
}

// ─── Flush ────────────────────────────────────────────────────────────────────

func TestQueryLog_Flush_ClearsAll(t *testing.T) {
	q := NewQueryLog(100, true)
	for i := 0; i < 10; i++ {
		q.Log(QueryEntry{Domain: fmt.Sprintf("d%d.com", i), Result: "upstream"})
	}
	q.Flush()

	res := q.Search(QueryFilter{})
	if res.Total != 0 {
		t.Errorf("after flush, total should be 0, got %d", res.Total)
	}
}

// ─── Timestamp auto-fill ──────────────────────────────────────────────────────

func TestQueryLog_Log_AutoTimestamp(t *testing.T) {
	q := NewQueryLog(100, true)
	before := time.Now()
	q.Log(QueryEntry{Domain: "ts.com", Result: "upstream"}) // no timestamp
	after := time.Now()

	res := q.Search(QueryFilter{})
	if len(res.Entries) == 0 {
		t.Fatal("expected 1 entry")
	}
	ts := res.Entries[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("auto-timestamp %v should be between %v and %v", ts, before, after)
	}
}
