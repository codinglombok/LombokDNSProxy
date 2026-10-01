// Package core_test — LombokDNSProxy · Upstream Pool Tests
// Tests the upstream pool management, EWMA latency tracking, and failover.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// countingClient wraps mockUpstreamClient and counts Exchange calls.
type countingClient struct {
	mockUpstreamClient
	calls atomic.Int64
}

func (c *countingClient) Exchange(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	c.calls.Add(1)
	return c.mockUpstreamClient.Exchange(ctx, req)
}

// ─── Pool TopN sorting ────────────────────────────────────────────────────────

func TestPool_TopN_SortedByLatency(t *testing.T) {
	upstreams := []*Upstream{
		{Name: "slow", Address: "slow:53", Protocol: ProtocolPlain, Latency: 100 * time.Millisecond, Healthy: true},
		{Name: "fast", Address: "fast:53", Protocol: ProtocolPlain, Latency: 10 * time.Millisecond, Healthy: true},
		{Name: "mid", Address: "mid:53", Protocol: ProtocolPlain, Latency: 50 * time.Millisecond, Healthy: true},
	}
	for _, u := range upstreams {
		u.client = &mockUpstreamClient{latency: u.Latency, response: makeAResponse("test.com")}
	}
	p := &Pool{upstreams: upstreams}

	top := p.TopN(2)
	if len(top) != 2 {
		t.Fatalf("expected 2 upstreams, got %d", len(top))
	}
	if top[0].Name != "fast" {
		t.Errorf("expected fastest first, got %s", top[0].Name)
	}
	if top[1].Name != "mid" {
		t.Errorf("expected second-fastest second, got %s", top[1].Name)
	}
}

func TestPool_TopN_ExcludesUnhealthy(t *testing.T) {
	upstreams := []*Upstream{
		{Name: "healthy", Latency: 10 * time.Millisecond, Healthy: true},
		{Name: "sick", Latency: 5 * time.Millisecond, Healthy: false}, // faster but unhealthy
	}
	for _, u := range upstreams {
		u.client = &mockUpstreamClient{response: makeAResponse("x.com")}
	}
	p := &Pool{upstreams: upstreams}

	top := p.TopN(2)
	if len(top) != 1 {
		t.Fatalf("expected 1 healthy upstream, got %d", len(top))
	}
	if top[0].Name != "healthy" {
		t.Errorf("expected healthy upstream, got %s", top[0].Name)
	}
}

func TestPool_TopN_NGreaterThanAvailable(t *testing.T) {
	upstreams := []*Upstream{
		{Name: "only", Latency: 10 * time.Millisecond, Healthy: true},
	}
	upstreams[0].client = &mockUpstreamClient{response: makeAResponse("a.com")}
	p := &Pool{upstreams: upstreams}

	top := p.TopN(10) // ask for 10, only 1 available
	if len(top) != 1 {
		t.Errorf("expected 1, got %d", len(top))
	}
}

// ─── EWMA latency update ──────────────────────────────────────────────────────

func TestPool_Exchange_UpdatesEWMALatency(t *testing.T) {
	c := &countingClient{
		mockUpstreamClient: mockUpstreamClient{
			latency:  20 * time.Millisecond,
			response: makeAResponse("ewma.com"),
		},
	}
	u := &Upstream{Name: "ewma", Latency: 100 * time.Millisecond, Healthy: true, client: c}
	p := &Pool{upstreams: []*Upstream{u}}

	req := new(dns.Msg)
	req.SetQuestion("ewma.com.", dns.TypeA)

	p.Exchange(context.Background(), u, req) //nolint:errcheck

	if c.calls.Load() != 1 {
		t.Error("expected 1 call to upstream client")
	}
	// EWMA should move toward 20ms from 100ms
	if u.Latency >= 100*time.Millisecond {
		t.Errorf("EWMA latency should have decreased, got %s", u.Latency)
	}
}

func TestPool_Exchange_MarksUnhealthyAfter5Errors(t *testing.T) {
	c := &countingClient{
		mockUpstreamClient: mockUpstreamClient{
			latency: 1 * time.Millisecond,
			err:     errors.New("upstream failure"),
		},
	}
	u := &Upstream{Name: "failing", Latency: 10 * time.Millisecond, Healthy: true, client: c}
	p := &Pool{upstreams: []*Upstream{u}}

	req := new(dns.Msg)
	req.SetQuestion("fail.com.", dns.TypeA)

	// 5 consecutive failures should mark upstream unhealthy
	for i := 0; i < 6; i++ {
		p.Exchange(context.Background(), u, req) //nolint:errcheck
	}

	if u.Healthy {
		t.Error("upstream should be marked unhealthy after 5+ consecutive errors")
	}
}

func TestPool_Exchange_RecoveryAfterSuccess(t *testing.T) {
	c := &countingClient{
		mockUpstreamClient: mockUpstreamClient{
			latency:  5 * time.Millisecond,
			response: makeAResponse("recover.com"),
		},
	}
	u := &Upstream{Name: "recovering", Latency: 10 * time.Millisecond, Healthy: false, Errors: 10, client: c}
	p := &Pool{upstreams: []*Upstream{u}}

	req := new(dns.Msg)
	req.SetQuestion("recover.com.", dns.TypeA)

	p.Exchange(context.Background(), u, req) //nolint:errcheck

	if !u.Healthy {
		t.Error("upstream should be marked healthy after successful exchange")
	}
	if u.Errors != 0 {
		t.Errorf("error count should reset to 0 after success, got %d", u.Errors)
	}
}

// ─── Pool Stats ───────────────────────────────────────────────────────────────

func TestPool_Stats_IncludesAllUpstreams(t *testing.T) {
	upstreams := []*Upstream{
		{Name: "cf", Address: "1.1.1.1:53", Protocol: ProtocolDoH, Latency: 8 * time.Millisecond, Healthy: true},
		{Name: "q9", Address: "9.9.9.9:853", Protocol: ProtocolDoT, Latency: 12 * time.Millisecond, Healthy: true},
	}
	for _, u := range upstreams {
		u.client = &mockUpstreamClient{response: makeAResponse("stats.com")}
	}
	p := &Pool{upstreams: upstreams}

	stats := p.Stats()
	if len(stats) != 2 {
		t.Fatalf("expected 2 stats, got %d", len(stats))
	}
	if stats[0].Name != "cf" {
		t.Errorf("expected cf first, got %s", stats[0].Name)
	}
	if stats[0].Protocol != "DoH" {
		t.Errorf("expected DoH, got %s", stats[0].Protocol)
	}
}

// ─── Protocol string tests ─────────────────────────────────────────────────────

func TestProtocol_String(t *testing.T) {
	cases := []struct {
		p    Protocol
		want string
	}{
		{ProtocolPlain, "Plain"},
		{ProtocolDoH, "DoH"},
		{ProtocolDoT, "DoT"},
		{ProtocolDoQ, "DoQ"},
	}
	for _, tc := range cases {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("Protocol(%d).String() = %q, want %q", tc.p, got, tc.want)
		}
	}
}
