// Package server — LombokDNSProxy · Server wiring tests
//
// The server itself holds little logic; its job is to wire the subsystems
// together and to start and stop them cleanly. So these tests focus on the
// two things that actually go wrong in that job: components not being
// connected, and listeners outliving the context that owns them.
//
// Everything stays on loopback with unprivileged ports.
//
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package server

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"
)

// freePort reserves a loopback port, releases it, and returns the number.
// There is an inherent race between release and re-bind, but for a test
// process on an otherwise idle loopback it is reliable enough.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// testConfig returns a Config bound entirely to free loopback ports, with
// the filter updater effectively disabled so tests never touch the network.
func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DNSAddr:        fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		CacheSize:      1000,
		FilterEnabled:  true,
		FilterSources:  nil, // no remote lists — updater has nothing to fetch
		FilterCacheDir: t.TempDir(),
		FilterInterval: time.Hour,
		HTTPProxyAddr:  fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		SOCKS5Addr:     fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		DashboardAddr:  fmt.Sprintf("127.0.0.1:%d", freePort(t)),
	}
}

// recordingWriter captures what ServeDNS writes, standing in for a real
// dns.ResponseWriter without opening a socket.
type recordingWriter struct {
	dns.ResponseWriter
	msg *dns.Msg
}

func (w *recordingWriter) WriteMsg(m *dns.Msg) error {
	w.msg = m
	return nil
}

func (w *recordingWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}

func (w *recordingWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53}
}

func (w *recordingWriter) Close() error      { return nil }
func (w *recordingWriter) TsigStatus() error { return nil }
func (w *recordingWriter) TsigTimersOnly(bool) {}
func (w *recordingWriter) Hijack()             {}
func (w *recordingWriter) Write(b []byte) (int, error) { return len(b), nil }

// ─── Construction ─────────────────────────────────────────────────────────────

func TestNew_WiresEverySubsystem(t *testing.T) {
	srv, err := New(testConfig(t), zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// A nil here means a request would panic at runtime rather than fail
	// cleanly, so each one is worth asserting individually.
	checks := map[string]any{
		"resolver": srv.resolver,
		"cache":    srv.cache,
		"filter":   srv.filter,
		"updater":  srv.updater,
		"http":     srv.http,
		"socks5":   srv.socks5,
	}
	for name, component := range checks {
		if component == nil {
			t.Errorf("%s was not wired up by New", name)
		}
	}
}

func TestNew_ZeroCacheSizeStillUsable(t *testing.T) {
	cfg := testConfig(t)
	cfg.CacheSize = 0 // caller forgot to set it

	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if srv.cache == nil {
		t.Fatal("cache should still be constructed with a zero size")
	}

	// A zero size must not mean a cache that panics on first write.
	msg := new(dns.Msg)
	msg.SetQuestion("zero.example.com.", dns.TypeA)
	msg.Response = true
	rr, _ := dns.NewRR("zero.example.com. 300 IN A 1.2.3.4")
	msg.Answer = []dns.RR{rr}

	srv.cache.Set("zero.example.com.", dns.TypeA, msg)
	srv.cache.Get("zero.example.com.", dns.TypeA)
}

// ─── ServeDNS ─────────────────────────────────────────────────────────────────

func TestServeDNS_BlockedDomainReturnsNXDOMAIN(t *testing.T) {
	srv, err := New(testConfig(t), zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.filter.Block("ads.blocked.example")

	req := new(dns.Msg)
	req.SetQuestion("ads.blocked.example.", dns.TypeA)

	w := &recordingWriter{}
	srv.ServeDNS(w, req)

	if w.msg == nil {
		t.Fatal("ServeDNS wrote no response")
	}
	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("Rcode = %s, want NXDOMAIN", dns.RcodeToString[w.msg.Rcode])
	}
}

func TestServeDNS_ServesFromCache(t *testing.T) {
	srv, err := New(testConfig(t), zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cached := new(dns.Msg)
	cached.SetQuestion("cached.example.com.", dns.TypeA)
	cached.Response = true
	cached.Rcode = dns.RcodeSuccess
	rr, _ := dns.NewRR("cached.example.com. 300 IN A 203.0.113.7")
	cached.Answer = []dns.RR{rr}
	srv.cache.Set("cached.example.com.", dns.TypeA, cached)

	req := new(dns.Msg)
	req.SetQuestion("cached.example.com.", dns.TypeA)
	req.Id = 4242

	w := &recordingWriter{}
	srv.ServeDNS(w, req)

	if w.msg == nil {
		t.Fatal("ServeDNS wrote no response")
	}
	if len(w.msg.Answer) == 0 {
		t.Fatal("cached answer was not returned")
	}
	// The response must carry the requester's ID, not the cached one.
	if w.msg.Id != 4242 {
		t.Errorf("response ID = %d, want the request ID 4242", w.msg.Id)
	}
}

func TestServeDNS_NoUpstreamsStillAnswers(t *testing.T) {
	// With no upstreams configured a query cannot be resolved, but the
	// server must still send something back rather than leaving the client
	// waiting for a timeout.
	srv, err := New(testConfig(t), zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("unreachable.example.com.", dns.TypeA)

	w := &recordingWriter{}
	done := make(chan struct{})
	go func() { srv.ServeDNS(w, req); close(done) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ServeDNS hung with no upstreams configured")
	}

	if w.msg == nil {
		t.Fatal("ServeDNS wrote no response")
	}
	if w.msg.Rcode != dns.RcodeServerFailure {
		t.Errorf("Rcode = %s, want SERVFAIL", dns.RcodeToString[w.msg.Rcode])
	}
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

func TestRun_ReturnsOnContextCancel(t *testing.T) {
	srv, err := New(testConfig(t), zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	time.Sleep(200 * time.Millisecond) // let the listeners bind
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v on a clean cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

func TestRun_ReleasesDNSPortOnShutdown(t *testing.T) {
	cfg := testConfig(t)
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go srv.Run(ctx) //nolint:errcheck
	time.Sleep(300 * time.Millisecond)

	// Confirm it really did bind, otherwise the re-bind below proves nothing.
	if _, err := net.Dial("tcp", cfg.DNSAddr); err != nil {
		t.Skipf("DNS TCP listener never came up on %s, nothing to test: %v", cfg.DNSAddr, err)
	}

	cancel()
	time.Sleep(500 * time.Millisecond)

	// A server that returns from Run but leaves its listeners bound holds the
	// port for the life of the process — restarting in-process then fails
	// with "address already in use".
	ln, err := net.Listen("tcp", cfg.DNSAddr)
	if err != nil {
		t.Fatalf("DNS port %s still held after shutdown: %v", cfg.DNSAddr, err)
	}
	ln.Close()
}

func TestRun_ReleasesProxyPortsOnShutdown(t *testing.T) {
	cfg := testConfig(t)
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go srv.Run(ctx) //nolint:errcheck
	time.Sleep(300 * time.Millisecond)
	cancel()
	time.Sleep(500 * time.Millisecond)

	for _, addr := range []string{cfg.HTTPProxyAddr, cfg.SOCKS5Addr} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Errorf("proxy port %s still held after shutdown: %v", addr, err)
			continue
		}
		ln.Close()
	}
}

func TestRun_AlreadyBoundPortIsReportedNotSwallowed(t *testing.T) {
	cfg := testConfig(t)

	// Occupy the DNS port before the server tries to claim it.
	blocker, err := net.Listen("tcp", cfg.DNSAddr)
	if err != nil {
		t.Fatalf("occupying port: %v", err)
	}
	defer blocker.Close()

	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Starting on an occupied port must surface as an error, not as a
	// silently half-started server.
	if err := srv.Run(ctx); err == nil {
		t.Error("Run returned nil despite the DNS port being unavailable")
	}
}
