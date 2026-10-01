// Package server — LombokDNSProxy · Central Server
// Wires together the DNS resolver, proxy engines, API, and filter updater.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package server

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"

	"github.com/codinglombok/lombok-dns-proxy/src/cache"
	"github.com/codinglombok/lombok-dns-proxy/src/core"
	"github.com/codinglombok/lombok-dns-proxy/src/filter"
	"github.com/codinglombok/lombok-dns-proxy/src/proxy"
)

// Config holds all runtime configuration for the server.
type Config struct {
	// DNS
	DNSAddr    string // e.g. "127.0.0.1:53"
	DoTAddr    string // e.g. "0.0.0.0:853"
	CacheSize  int
	Upstreams  []core.Upstream

	// Filter
	FilterEnabled  bool
	FilterSources  []filter.ListSource
	FilterCacheDir string
	FilterInterval time.Duration

	// Proxy
	HTTPProxyAddr  string // e.g. "127.0.0.1:7788"
	SOCKS5Addr     string // e.g. "127.0.0.1:7789"

	// Dashboard API
	DashboardAddr string // e.g. "127.0.0.1:7787"
}

// Server is the top-level runtime object.
type Server struct {
	cfg      Config
	log      *zap.Logger
	resolver *core.RacingResolver
	cache    *cache.Cache
	filter   *filter.Engine
	updater  *filter.Updater
	http     *proxy.HTTPProxy
	socks5   *proxy.SOCKS5Server
}

// New creates a fully configured Server from cfg.
func New(cfg Config, log *zap.Logger) (*Server, error) {
	// ECC provider (real: wraps lombokecc package)
	ecc := &noopECC{}

	// Cache
	c := cache.New(cfg.CacheSize, ecc, 16)

	// Filter engine
	fe := filter.New()
	updater := filter.NewUpdater(fe, cfg.FilterSources, cfg.FilterCacheDir, cfg.FilterInterval, log)

	// DNS resolver
	resolver := core.NewRacingResolver(cfg.Upstreams, c, fe)

	// HTTP proxy
	httpProxy := proxy.NewHTTPProxy(proxy.HTTPProxyConfig{
		ListenAddr:   cfg.HTTPProxyAddr,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  90 * time.Second,
	}, log)

	// SOCKS5 proxy
	s5 := proxy.NewSOCKS5Server(proxy.SOCKS5Config{
		ListenAddr: cfg.SOCKS5Addr,
		// UDP ASSOCIATE hands the client an address to send datagrams to, so
		// it has to be an address the client can actually reach — loopback
		// would break every non-local client.
		UDPRelayIP: localIP(),
	}, log)

	return &Server{
		cfg:      cfg,
		log:      log,
		resolver: resolver,
		cache:    c,
		filter:   fe,
		updater:  updater,
		http:     httpProxy,
		socks5:   s5,
	}, nil
}

// Run starts all subsystems and blocks until ctx is cancelled.
// Every listener is shut down before Run returns, so the process can restart
// the server in place without hitting "address already in use".
func (s *Server) Run(ctx context.Context) error {
	s.log.Info("LombokDNSProxy starting",
		zap.String("dns", s.cfg.DNSAddr),
		zap.String("http-proxy", s.cfg.HTTPProxyAddr),
		zap.String("socks5", s.cfg.SOCKS5Addr),
		zap.String("dashboard", s.cfg.DashboardAddr),
	)

	errCh := make(chan error, 6)

	// Hold references so the listeners can actually be closed later —
	// a dns.Server that is only reachable from inside its own goroutine
	// keeps its port bound for the life of the process.
	udpSrv := &dns.Server{Addr: s.cfg.DNSAddr, Net: "udp", Handler: s}
	tcpSrv := &dns.Server{Addr: s.cfg.DNSAddr, Net: "tcp", Handler: s}

	go func() {
		s.log.Info("DNS UDP listening", zap.String("addr", s.cfg.DNSAddr))
		errCh <- udpSrv.ListenAndServe()
	}()
	go func() {
		s.log.Info("DNS TCP listening", zap.String("addr", s.cfg.DNSAddr))
		errCh <- tcpSrv.ListenAndServe()
	}()

	// HTTP proxy
	go func() { errCh <- s.http.ListenAndServe(ctx) }()

	// SOCKS5 proxy
	go func() { errCh <- s.socks5.ListenAndServe(ctx) }()

	// Filter updater
	go s.updater.Run(ctx)

	// Shut the DNS listeners down on the way out, whether we are leaving
	// because of a cancelled context or because a subsystem failed.
	defer s.shutdownDNS(udpSrv, tcpSrv)

	select {
	case <-ctx.Done():
		s.log.Info("shutting down gracefully")
		return nil
	case err := <-errCh:
		return fmt.Errorf("server fatal error: %w", err)
	}
}

// shutdownDNS closes both DNS listeners, giving in-flight queries a short
// grace period. Errors are logged rather than returned: by the time this
// runs the caller is already on its way out, and a failed close should not
// mask the reason for shutting down.
func (s *Server) shutdownDNS(servers ...*dns.Server) {
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := srv.ShutdownContext(ctx); err != nil {
			s.log.Debug("DNS listener shutdown",
				zap.String("net", srv.Net), zap.Error(err))
		}
		cancel()
	}
}

// queryTimeout bounds a single inbound query end to end. The resolver already
// caps its own upstream race, but this covers the whole pipeline so a stuck
// stage can never pin a handler goroutine for the life of the process.
const queryTimeout = 5 * time.Second

// ServeDNS implements dns.Handler — called for every inbound DNS query.
func (s *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	resp, err := s.resolver.Resolve(ctx, r)
	if err != nil {
		s.log.Debug("resolve error", zap.Error(err))
	}
	if resp != nil {
		w.WriteMsg(resp) //nolint:errcheck
	}
}

// noopECC satisfies cache.ECCProvider with no-op operations (for testing/dev).
// Production: replace with real lombokecc.Provider.
type noopECC struct{}

func (n *noopECC) Encode(data []byte) ([]byte, error) { return make([]byte, 16), nil }
func (n *noopECC) Verify(data, _ []byte) ([]byte, error) { return data, nil }

// localIP returns the first non-loopback IPv4 address for UDP relay binding.
func localIP() net.IP {
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4
			}
		}
	}
	return net.IPv4(127, 0, 0, 1)
}
