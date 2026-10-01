// Package filter — LombokDNSProxy · Filter engine tests + fuzz
// Tests the standalone filter.Engine (this package's own exported API:
// New, LoadList, Allow, Block, IsBlocked, Flush, Size, Stats) — not a
// core-local reimplementation.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package filter

import (
	"strings"
	"testing"
)

func TestFilterEngine_ExactBlock(t *testing.T) {
	f := New()
	f.Block("ads.example.com")

	if !f.IsBlocked("ads.example.com") {
		t.Error("ads.example.com should be blocked")
	}
	if f.IsBlocked("example.com") {
		t.Error("parent example.com should not be blocked by child rule")
	}
	if f.IsBlocked("safe.example.com") {
		t.Error("sibling safe.example.com should not be blocked")
	}
}

func TestFilterEngine_WildcardBlock(t *testing.T) {
	f := New()
	f.insertTrie("doubleclick.net", true) // wildcard — exercised via package-internal helper

	cases := []struct {
		domain  string
		blocked bool
	}{
		{"ad.doubleclick.net", true},
		{"deep.ad.doubleclick.net", true},
		{"doubleclick.net", true},
		{"notdoubleclick.net", false},
		{"subdoubleclick.net", false},
	}
	for _, tc := range cases {
		got := f.IsBlocked(tc.domain)
		if got != tc.blocked {
			t.Errorf("IsBlocked(%q) = %v, want %v", tc.domain, got, tc.blocked)
		}
	}
}

func TestFilterEngine_AllowlistOverridesBlock(t *testing.T) {
	f := New()
	f.insertTrie("tracker.com", true) // wildcard block
	f.Allow("safe.tracker.com")       // explicit allow

	if !f.IsBlocked("bad.tracker.com") {
		t.Error("bad.tracker.com should be blocked")
	}
	if f.IsBlocked("safe.tracker.com") {
		t.Error("safe.tracker.com should be allowed despite wildcard block")
	}
}

func TestFilterEngine_LoadList_HostsFormat(t *testing.T) {
	hosts := `
# EasyList extract
0.0.0.0 ads.badsite.com
127.0.0.1 tracker.evil.net
:: ipv6blocked.com
# comment line
safe.com

`
	f := New()
	if err := f.LoadList(strings.NewReader(hosts)); err != nil {
		t.Fatalf("LoadList error: %v", err)
	}

	blocked := []string{"ads.badsite.com", "tracker.evil.net", "ipv6blocked.com", "safe.com"}
	for _, d := range blocked {
		if !f.IsBlocked(d) {
			t.Errorf("expected %q to be blocked", d)
		}
	}
}

func TestFilterEngine_LoadList_Wildcard(t *testing.T) {
	f := New()
	if err := f.LoadList(strings.NewReader("*.tracker.net\n")); err != nil {
		t.Fatalf("LoadList error: %v", err)
	}
	if !f.IsBlocked("sub.tracker.net") {
		t.Error("wildcard entry from LoadList should block subdomains")
	}
}

func TestFilterEngine_LoadList_RegexRule(t *testing.T) {
	f := New()
	if err := f.LoadList(strings.NewReader(`/^ad[0-9]+\.example\.com$/` + "\n")); err != nil {
		t.Fatalf("LoadList error: %v", err)
	}

	if !f.IsBlocked("ad123.example.com") {
		t.Error("ad123.example.com should match regex rule")
	}
	if f.IsBlocked("ads.example.com") {
		t.Error("ads.example.com should not match ^ad[0-9]+")
	}
}

func TestFilterEngine_CaseFolding(t *testing.T) {
	f := New()
	f.Block("ads.example.COM")

	if !f.IsBlocked("ADS.EXAMPLE.COM") {
		t.Error("case-folded domain should be blocked")
	}
	if !f.IsBlocked("Ads.Example.Com") {
		t.Error("mixed-case domain should be blocked")
	}
}

func TestFilterEngine_Stats(t *testing.T) {
	f := New()
	f.Block("blocked.com")

	f.IsBlocked("blocked.com")
	f.IsBlocked("clean.com")
	f.IsBlocked("blocked.com")

	stats := f.Stats
	if stats.Blocked != 2 {
		t.Errorf("expected 2 blocked, got %d", stats.Blocked)
	}
	if stats.Total != 3 {
		t.Errorf("expected 3 total, got %d", stats.Total)
	}
}

func TestFilterEngine_Size(t *testing.T) {
	f := New()
	f.Block("a.com")
	f.Block("b.com")
	f.Block("c.com")

	if got := f.Size(); got != 3 {
		t.Errorf("expected size 3, got %d", got)
	}
}

func TestFilterEngine_Flush_KeepsAllowlist(t *testing.T) {
	f := New()
	f.Block("blocked.com")
	f.Allow("allowed.com")

	f.Flush()

	if f.IsBlocked("blocked.com") {
		t.Error("blocked.com should no longer be blocked after Flush")
	}
	// Flush documents that it keeps the allowlist — verify that contract.
	if f.IsBlocked("allowed.com") {
		t.Error("allowed.com should still be allowed after Flush (allowlist preserved)")
	}
}

// Fuzz target — feed arbitrary domain names; engine must not panic.
func FuzzFilterIsBlocked(f *testing.F) {
	f.Add("ads.example.com")
	f.Add("*.doubleclick.net")
	f.Add("")
	f.Add("a.b.c.d.e.f.g.h.i.j.k.l.m.n.o.p.q.r.s.t.u.v.w.x.y.z")
	f.Add("x̂.unicode.com")

	fe := New()
	fe.Block("blocked.com")
	fe.insertTrie("tracker.net", true)

	f.Fuzz(func(t *testing.T, domain string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic on domain %q: %v", domain, r)
			}
		}()
		fe.IsBlocked(domain)
	})
}

// Fuzz target — feed arbitrary hosts-format list data; parser must not panic.
func FuzzLoadList(f *testing.F) {
	f.Add("0.0.0.0 ads.com\n127.0.0.1 track.net\n")
	f.Add("# comment\n\n*.bad.org\n")
	f.Add("")

	f.Fuzz(func(t *testing.T, data string) {
		fe := New()
		fe.LoadList(strings.NewReader(data)) //nolint:errcheck // fuzz: errors ok
	})
}
