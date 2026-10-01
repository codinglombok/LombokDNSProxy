// Package core — LombokDNSProxy · Upstream Pool Manager
// Manages DoH, DoT, DoQ, and plain DNS upstream clients.
// Tracks EWMA latency per upstream, sorts by speed, handles failover.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
)

// Protocol enumerates supported DNS transport protocols.
type Protocol int

const (
	ProtocolPlain Protocol = iota // UDP/TCP port 53
	ProtocolDoH                   // DNS-over-HTTPS (RFC 8484)
	ProtocolDoT                   // DNS-over-TLS  (RFC 7858)
	ProtocolDoQ                   // DNS-over-QUIC (draft-ietf-dprive-dnsoquic)
)

func (p Protocol) String() string {
	switch p {
	case ProtocolDoH:
		return "DoH"
	case ProtocolDoT:
		return "DoT"
	case ProtocolDoQ:
		return "DoQ"
	default:
		return "Plain"
	}
}

// UpstreamClient abstracts a single DNS transport (DoH, DoT, DoQ, plain).
type UpstreamClient interface {
	Exchange(ctx context.Context, req *dns.Msg) (*dns.Msg, error)
}

// Upstream is one DNS upstream server with its transport client and EWMA stats.
type Upstream struct {
	Name     string
	Address  string
	Protocol Protocol
	Latency  time.Duration // EWMA latency; updated by probes
	Errors   uint64        // total failed exchanges
	Healthy  bool
	client   UpstreamClient
}

// Pool manages a set of Upstream servers, auto-sorts by latency, and probes health.
type Pool struct {
	mu        sync.RWMutex
	upstreams []*Upstream
	probeQ    string // domain used for health probes
}

// NewPool creates a Pool from the given upstream configs and initialises transport clients.
func NewPool(upstreams []*Upstream) (*Pool, error) {
	p := &Pool{probeQ: "health.check."}
	for _, u := range upstreams {
		if err := p.initClient(u); err != nil {
			return nil, fmt.Errorf("upstream %s: %w", u.Name, err)
		}
		u.Healthy = true
		p.upstreams = append(p.upstreams, u)
	}
	return p, nil
}

// initClient creates the appropriate UpstreamClient for an Upstream.
func (p *Pool) initClient(u *Upstream) error {
	switch u.Protocol {
	case ProtocolDoH:
		u.client = newDoHClient(u.Address)
	case ProtocolDoT:
		u.client = newDoTClient(u.Address)
	case ProtocolDoQ:
		u.client = newDoQClient(u.Address)
	default:
		u.client = newPlainClient(u.Address)
	}
	return nil
}

// TopN returns the N healthy upstreams with the lowest EWMA latency.
// Result is sorted fastest-first. Caller must not modify the slice.
func (p *Pool) TopN(n int) []*Upstream {
	p.mu.RLock()
	defer p.mu.RUnlock()

	healthy := make([]*Upstream, 0, len(p.upstreams))
	for _, u := range p.upstreams {
		if u.Healthy {
			healthy = append(healthy, u)
		}
	}
	// Insertion sort — N is small (≤8), zero-alloc
	for i := 1; i < len(healthy); i++ {
		for j := i; j > 0 && healthy[j].Latency < healthy[j-1].Latency; j-- {
			healthy[j], healthy[j-1] = healthy[j-1], healthy[j]
		}
	}
	if n > len(healthy) {
		n = len(healthy)
	}
	return healthy[:n]
}

// Exchange sends req to upstream u and returns the response.
func (p *Pool) Exchange(ctx context.Context, u *Upstream, req *dns.Msg) (*dns.Msg, error) {
	start := time.Now()
	resp, err := u.client.Exchange(ctx, req)
	elapsed := time.Since(start)

	p.mu.Lock()
	if err != nil {
		u.Errors++
		if u.Errors > 5 {
			u.Healthy = false
		}
	} else {
		u.Errors = 0
		u.Healthy = true
		// EWMA alpha=0.3: new = 0.7*old + 0.3*sample
		u.Latency = time.Duration(float64(u.Latency)*0.7 + float64(elapsed)*0.3)
	}
	p.mu.Unlock()

	return resp, err
}

// ProbeLoop runs health + latency probes every interval. Blocks until ctx is cancelled.
func (p *Pool) ProbeLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.probeAll(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (p *Pool) probeAll(ctx context.Context) {
	req := new(dns.Msg)
	req.SetQuestion(p.probeQ, dns.TypeA)
	req.RecursionDesired = true

	p.mu.RLock()
	upstreams := make([]*Upstream, len(p.upstreams))
	copy(upstreams, p.upstreams)
	p.mu.RUnlock()

	var wg sync.WaitGroup
	for _, u := range upstreams {
		wg.Add(1)
		go func(u *Upstream) {
			defer wg.Done()
			pCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			p.Exchange(pCtx, u, req.Copy()) //nolint:errcheck
		}(u)
	}
	wg.Wait()
}

// Stats returns a snapshot of all upstreams for the API.
func (p *Pool) Stats() []UpstreamStat {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]UpstreamStat, len(p.upstreams))
	for i, u := range p.upstreams {
		out[i] = UpstreamStat{
			Name:      u.Name,
			Address:   u.Address,
			Protocol:  u.Protocol.String(),
			LatencyMs: float64(u.Latency.Milliseconds()),
			Healthy:   u.Healthy,
			Errors:    u.Errors,
		}
	}
	return out
}

// UpstreamStat is the JSON-serialisable shape for the API.
type UpstreamStat struct {
	Name      string  `json:"name"`
	Address   string  `json:"address"`
	Protocol  string  `json:"protocol"`
	LatencyMs float64 `json:"latencyMs"`
	Healthy   bool    `json:"healthy"`
	Errors    uint64  `json:"errors"`
}

// ─── DoH Client ──────────────────────────────────────────────────────────────

type dohClient struct {
	url    string
	client *http.Client
}

func newDoHClient(url string) *dohClient {
	return &dohClient{
		url: url,
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				ForceAttemptHTTP2:   true,
			},
		},
	}
}

func (c *dohClient) Exchange(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	wire, err := req.Pack()
	if err != nil {
		return nil, err
	}

	// RFC 8484: POST with application/dns-message
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/dns-message")
	httpReq.Header.Set("Accept", "application/dns-message")
	httpReq.Header.Set("User-Agent", "LombokDNSProxy/1.0")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
	if err != nil {
		return nil, err
	}

	m := new(dns.Msg)
	if err := m.Unpack(body); err != nil {
		return nil, fmt.Errorf("DoH: unpack: %w", err)
	}
	return m, nil
}

// dohGetClient uses RFC 8484 GET with base64url-encoded wire format.
type dohGetClient struct {
	url    string
	client *http.Client
}

func (c *dohGetClient) Exchange(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	wire, err := req.Pack()
	if err != nil {
		return nil, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(wire)
	url := c.url + "?dns=" + encoded

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/dns-message")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
	if err != nil {
		return nil, err
	}
	m := new(dns.Msg)
	if err := m.Unpack(body); err != nil {
		return nil, err
	}
	return m, nil
}

// ─── DoT Client ──────────────────────────────────────────────────────────────

type dotClient struct {
	addr      string
	tlsConfig *tls.Config
}

func newDoTClient(addr string) *dotClient {
	host, _, _ := net.SplitHostPort(addr)
	return &dotClient{
		addr: addr,
		tlsConfig: &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS13,
		},
	}
}

func (c *dotClient) Exchange(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", c.addr, c.tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("DoT: dial: %w", err)
	}
	defer conn.Close()

	// Use dns.Conn for length-prefixed TCP DNS framing
	dnsConn := &dns.Conn{Conn: conn}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline) //nolint:errcheck
	}
	if err := dnsConn.WriteMsg(req); err != nil {
		return nil, fmt.Errorf("DoT: write: %w", err)
	}
	resp, err := dnsConn.ReadMsg()
	if err != nil {
		return nil, fmt.Errorf("DoT: read: %w", err)
	}
	return resp, nil
}

// ─── DoQ Client ──────────────────────────────────────────────────────────────

type doqClient struct {
	addr      string
	tlsConfig *tls.Config
}

func newDoQClient(addr string) *doqClient {
	host, _, _ := net.SplitHostPort(addr)
	return &doqClient{
		addr: addr,
		tlsConfig: &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS13,
			NextProtos: []string{"doq"},
		},
	}
}

func (c *doqClient) Exchange(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	conn, err := quic.DialAddr(ctx, c.addr, c.tlsConfig, &quic.Config{
		MaxIdleTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("DoQ: dial: %w", err)
	}
	defer conn.CloseWithError(0, "") //nolint:errcheck

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("DoQ: open stream: %w", err)
	}
	defer stream.Close()

	wire, err := req.Pack()
	if err != nil {
		return nil, err
	}
	// DoQ prepends a 2-byte length field (same as DNS-over-TCP)
	buf := make([]byte, 2+len(wire))
	buf[0] = byte(len(wire) >> 8)
	buf[1] = byte(len(wire))
	copy(buf[2:], wire)

	if _, err := stream.Write(buf); err != nil {
		return nil, fmt.Errorf("DoQ: write: %w", err)
	}

	// Read response length prefix
	lenbuf := make([]byte, 2)
	if _, err := io.ReadFull(stream, lenbuf); err != nil {
		return nil, fmt.Errorf("DoQ: read len: %w", err)
	}
	respLen := int(lenbuf[0])<<8 | int(lenbuf[1])
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(stream, respBuf); err != nil {
		return nil, fmt.Errorf("DoQ: read body: %w", err)
	}

	m := new(dns.Msg)
	if err := m.Unpack(respBuf); err != nil {
		return nil, fmt.Errorf("DoQ: unpack: %w", err)
	}
	return m, nil
}

// ─── Plain DNS Client ─────────────────────────────────────────────────────────

type plainClient struct {
	addr   string
	client *dns.Client
}

func newPlainClient(addr string) *plainClient {
	return &plainClient{
		addr: addr,
		client: &dns.Client{
			Timeout: 5 * time.Second,
			Net:     "udp",
		},
	}
}

func (c *plainClient) Exchange(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	resp, _, err := c.client.ExchangeContext(ctx, req, c.addr)
	if err != nil {
		// Fallback to TCP on truncation
		if resp != nil && resp.Truncated {
			tc := &dns.Client{Timeout: 5 * time.Second, Net: "tcp"}
			resp, _, err = tc.ExchangeContext(ctx, req, c.addr)
		}
	}
	return resp, err
}
