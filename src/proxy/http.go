// Package proxy — LombokDNSProxy · HTTP/HTTPS Proxy Engine
// Handles HTTP forward proxy and HTTPS CONNECT tunneling.
// Optional TLS inspection is opt-in only via local CA (never enabled by default).
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// HTTPProxyConfig configures the HTTP/HTTPS proxy engine.
type HTTPProxyConfig struct {
	ListenAddr     string
	TLSInspect     bool          // NEVER default true — must be explicitly enabled
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	MaxConns       int
	BlockedHeaders []string      // headers to strip (e.g. X-Forwarded-For injection)
}

// HTTPProxy is a forward HTTP/HTTPS proxy server.
type HTTPProxy struct {
	cfg    HTTPProxyConfig
	log    *zap.Logger
	server *http.Server
	dialer *net.Dialer
}

// NewHTTPProxy creates a configured HTTP proxy.
func NewHTTPProxy(cfg HTTPProxyConfig, log *zap.Logger) *HTTPProxy {
	p := &HTTPProxy{
		cfg: cfg,
		log: log,
		dialer: &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
	p.server = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      p,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	return p
}

// ListenAndServe starts the HTTP proxy. Blocks until ctx is cancelled.
func (p *HTTPProxy) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("http-proxy: listen %s: %w", p.cfg.ListenAddr, err)
	}
	p.log.Info("HTTP proxy listening", zap.String("addr", p.cfg.ListenAddr))
	go func() { <-ctx.Done(); p.server.Shutdown(context.Background()) }() //nolint:errcheck
	return p.server.Serve(ln)
}

// ServeHTTP is the main request dispatcher.
// CONNECT requests → TLS tunnel; plain HTTP → forward proxy.
func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.stripHopHeaders(r)
	p.stripBlockedHeaders(r)

	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleConnect establishes a raw TCP tunnel for HTTPS CONNECT.
// The proxy is transparent to TLS — it does NOT decrypt by default.
func (p *HTTPProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	p.log.Debug("CONNECT", zap.String("host", host), zap.String("remote", r.RemoteAddr))

	upstream, err := p.dialer.DialContext(r.Context(), "tcp", host)
	if err != nil {
		http.Error(w, "Could not reach upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	// Signal the client the tunnel is ready
	w.WriteHeader(http.StatusOK)

	// Hijack the client connection to get raw TCP access
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		p.log.Error("ResponseWriter does not support Hijack")
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		p.log.Error("Hijack failed", zap.Error(err))
		return
	}
	defer clientConn.Close()

	relay(clientConn, upstream, defaultRelayTimeout)
}

// handleHTTP forwards a plain HTTP request to the origin server.
func (p *HTTPProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "Request URL must be absolute for proxy use", http.StatusBadRequest)
		return
	}

	p.log.Debug("HTTP forward",
		zap.String("method", r.Method),
		zap.String("url", r.URL.String()),
	)

	transport := &http.Transport{
		DialContext:         p.dialer.DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse // don't follow redirects
		},
	}

	// Build outbound request
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	copyHeaders(outReq.Header, r.Header)

	resp, err := client.Do(outReq)
	if err != nil {
		http.Error(w, "Upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}

// NOTE: PAC generation lives in pac.go (PACRule, PACConfig, Generate,
// PACHandlerFromConfig) — it supports rule types, CIDR matching and
// wildcard subdomains. The earlier flat-rule version that lived here was
// removed to avoid a duplicate PACRule declaration in this package.

// --- helpers ---

// hopHeaders are per-hop headers that should not be forwarded.
var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailers", "Transfer-Encoding", "Upgrade", "Proxy-Connection",
}

func (p *HTTPProxy) stripHopHeaders(r *http.Request) {
	for _, h := range hopHeaders {
		r.Header.Del(h)
	}
}

func (p *HTTPProxy) stripBlockedHeaders(r *http.Request) {
	for _, h := range p.cfg.BlockedHeaders {
		r.Header.Del(h)
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// bufferedConn wraps a net.Conn with a bufio.Reader for peeking.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }
