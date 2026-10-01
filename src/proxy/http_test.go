// Package proxy — LombokDNSProxy · HTTP/HTTPS proxy tests
//
// Exercises the forward-proxy and CONNECT-tunnel paths against loopback
// origin servers. No external network required.
//
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// startHTTPProxy boots the proxy on a free loopback port and returns its address.
func startHTTPProxy(t *testing.T, cfg HTTPProxyConfig) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg.ListenAddr = addr
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 10 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 10 * time.Second
	}

	p := NewHTTPProxy(cfg, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go p.ListenAndServe(ctx) //nolint:errcheck

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("HTTP proxy never came up on %s", addr)
	return ""
}

// clientVia returns an http.Client that routes through the given proxy address.
func clientVia(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	u, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		t.Fatalf("parsing proxy URL: %v", err)
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
		Timeout:   5 * time.Second,
	}
}

// ─── Plain HTTP forwarding ────────────────────────────────────────────────────

func TestHTTPProxy_ForwardsRequest(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "origin saw %s %s", r.Method, r.URL.Path)
	}))
	defer origin.Close()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})
	resp, err := clientVia(t, proxyAddr).Get(origin.URL + "/hello")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "/hello") {
		t.Errorf("origin did not receive the path; body = %q", body)
	}
}

func TestHTTPProxy_PreservesStatusAndHeaders(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Origin-Marker", "lombok")
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, "short and stout") //nolint:errcheck
	}))
	defer origin.Close()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})
	resp, err := clientVia(t, proxyAddr).Get(origin.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want 418 — the proxy must not rewrite it", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Origin-Marker"); got != "lombok" {
		t.Errorf("origin header lost in transit, got %q", got)
	}
}

func TestHTTPProxy_ForwardsPOSTBody(t *testing.T) {
	var received string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})
	resp, err := clientVia(t, proxyAddr).Post(
		origin.URL, "text/plain", strings.NewReader("payload-through-proxy"))
	if err != nil {
		t.Fatalf("POST through proxy: %v", err)
	}
	defer resp.Body.Close()

	if received != "payload-through-proxy" {
		t.Errorf("origin received body %q, want the posted payload", received)
	}
}

func TestHTTPProxy_DoesNotFollowRedirects(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		io.WriteString(w, "arrived") //nolint:errcheck
	}))
	defer origin.Close()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})

	// Stop the *client* following redirects too, so what we observe is
	// whatever the proxy chose to return.
	c := clientVia(t, proxyAddr)
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := c.Get(origin.URL + "/start")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// The proxy should hand the 302 back rather than resolving it itself —
	// following redirects proxy-side would hide the hop from the client.
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302 passed through", resp.StatusCode)
	}
}

func TestHTTPProxy_RejectsNonAbsoluteURL(t *testing.T) {
	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})

	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// A proxy request must carry an absolute URI; an origin-form path is a
	// client bug and should be reported as such, not silently guessed at.
	fmt.Fprint(conn, "GET /relative HTTP/1.1\r\nHost: example.com\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-absolute proxy URL", resp.StatusCode)
	}
}

func TestHTTPProxy_UnreachableOriginReturns502(t *testing.T) {
	// Reserve then release a port so the connection is refused.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})
	resp, err := clientVia(t, proxyAddr).Get("http://" + dead + "/")
	if err != nil {
		t.Fatalf("expected a response, got transport error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when the origin is unreachable", resp.StatusCode)
	}
}

// ─── CONNECT tunnelling ───────────────────────────────────────────────────────

func TestHTTPProxy_ConnectTunnelsBytes(t *testing.T) {
	// A bare TCP echo server stands in for an HTTPS origin — CONNECT is
	// transparent, so the proxy must not care what flows through it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); io.Copy(c, c) }(c) //nolint:errcheck
		}
	}()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})

	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	target := ln.Addr().String()
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("reading CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}

	payload := "opaque bytes through the tunnel"
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatalf("writing through tunnel: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("reading echo: %v", err)
	}
	if string(got) != payload {
		t.Errorf("tunnel returned %q, sent %q", got, payload)
	}
}

func TestHTTPProxy_ConnectToUnreachableHostFails(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})

	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", dead, dead)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Error("CONNECT to an unreachable host reported 200")
	}
}

// ─── Header hygiene ───────────────────────────────────────────────────────────

func TestHTTPProxy_StripsHopByHopHeaders(t *testing.T) {
	var seen http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{})

	req, _ := http.NewRequest(http.MethodGet, origin.URL, nil)
	req.Header.Set("Proxy-Authorization", "Basic c2hvdWxkLW5vdC1sZWFr")
	req.Header.Set("X-Keep-Me", "kept")

	resp, err := clientVia(t, proxyAddr).Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Proxy-Authorization is between client and proxy; forwarding it leaks
	// the proxy credential to every origin the user visits.
	if v := seen.Get("Proxy-Authorization"); v != "" {
		t.Errorf("Proxy-Authorization leaked to origin: %q", v)
	}
	if seen.Get("X-Keep-Me") != "kept" {
		t.Error("ordinary end-to-end header was dropped")
	}
}

func TestHTTPProxy_StripsConfiguredHeaders(t *testing.T) {
	var seen http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	proxyAddr := startHTTPProxy(t, HTTPProxyConfig{
		BlockedHeaders: []string{"X-Forwarded-For", "X-Real-IP"},
	})

	req, _ := http.NewRequest(http.MethodGet, origin.URL, nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.9")
	req.Header.Set("User-Agent", "lombok-test")

	resp, err := clientVia(t, proxyAddr).Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	for _, h := range []string{"X-Forwarded-For", "X-Real-IP"} {
		if v := seen.Get(h); v != "" {
			t.Errorf("%s reached the origin (%q) — it is configured to be stripped", h, v)
		}
	}
	if seen.Get("User-Agent") != "lombok-test" {
		t.Error("User-Agent should survive; only configured headers are stripped")
	}
}

func TestCopyHeaders_CopiesEveryValue(t *testing.T) {
	src := http.Header{}
	src.Add("Set-Cookie", "a=1")
	src.Add("Set-Cookie", "b=2")
	src.Set("Content-Type", "application/json")

	dst := http.Header{}
	copyHeaders(dst, src)

	// A header can legitimately repeat; collapsing it would drop cookies.
	if got := len(dst.Values("Set-Cookie")); got != 2 {
		t.Errorf("Set-Cookie copied %d times, want 2", got)
	}
	if dst.Get("Content-Type") != "application/json" {
		t.Error("Content-Type not copied")
	}
}

// ─── PAC endpoint ─────────────────────────────────────────────────────────────

func TestPACHandlerFromConfig_ServesPAC(t *testing.T) {
	handler := PACHandlerFromConfig(DefaultPACConfig("127.0.0.1:7788", "127.0.0.1:7789"))

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/proxy.pac", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Browsers ignore a PAC file served under the wrong content type.
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ns-proxy-autoconfig" {
		t.Errorf("Content-Type = %q, want application/x-ns-proxy-autoconfig", ct)
	}
	if !strings.Contains(rec.Body.String(), "function FindProxyForURL") {
		t.Error("PAC body is missing FindProxyForURL")
	}
}
