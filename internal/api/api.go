// Package api — LombokDNSProxy · REST API + WebSocket server
// Serves the web dashboard (port 7787) and browser extension sync (ws://127.0.0.1:7788/ext).
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// StatsProvider is implemented by the server package to expose live metrics.
type StatsProvider interface {
	GlobalStats() Stats
	BlockDomain(domain string) error
	AllowDomain(domain string) error
	FlushCache()
}

// Stats is the JSON response shape for GET /api/v1/stats.
type Stats struct {
	TotalQueries    uint64  `json:"totalQueries"`
	BlockedCount    uint64  `json:"blockedCount"`
	CacheHitRate    float64 `json:"cacheHitRate"`
	AvgLatencyMs    float64 `json:"avgLatencyMs"`
	UptimeSeconds   int64   `json:"uptimeSeconds"`
	FilteredDomains int     `json:"filteredDomains"`
	CacheSize       int     `json:"cacheSize"`
}

// Server serves the REST API and WebSocket endpoint.
type Server struct {
	addr     string
	provider StatsProvider
	log      *zap.Logger
	mux      *http.ServeMux
	start    time.Time
}

// New creates a new API server.
func New(addr string, provider StatsProvider, log *zap.Logger) *Server {
	s := &Server{
		addr:     addr,
		provider: provider,
		log:      log,
		mux:      http.NewServeMux(),
		start:    time.Now(),
	}
	s.registerRoutes()
	return s
}

// ListenAndServe starts the HTTP server. Blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	s.log.Info("API server listening", zap.String("addr", s.addr))
	go func() { <-ctx.Done(); srv.Shutdown(context.Background()) }() //nolint:errcheck
	return srv.ListenAndServe()
}

// registerRoutes wires all HTTP endpoints.
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	s.mux.HandleFunc("POST /api/v1/filter/block", s.handleBlock)
	s.mux.HandleFunc("POST /api/v1/filter/allow", s.handleAllow)
	s.mux.HandleFunc("POST /api/v1/cache/flush", s.handleFlushCache)
	s.mux.HandleFunc("GET /proxy.pac", s.handlePAC)
	s.mux.HandleFunc("GET /health", s.handleHealth)

	// Browser extension WebSocket
	s.mux.HandleFunc("/ext", s.handleExtWebSocket)

	// Serve dashboard SPA (everything else)
	s.mux.Handle("/", http.FileServer(http.Dir("./web")))
}

// handleStats returns live global statistics as JSON.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := s.provider.GlobalStats()
	stats.UptimeSeconds = int64(time.Since(s.start).Seconds())
	writeJSON(w, http.StatusOK, stats)
}

// handleBlock adds a domain to the runtime blocklist.
func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Domain == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := s.provider.BlockDomain(body.Domain); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.log.Info("domain blocked via API", zap.String("domain", body.Domain))
	writeJSON(w, http.StatusOK, map[string]string{"status": "blocked", "domain": body.Domain})
}

// handleAllow adds a domain to the allowlist.
func (s *Server) handleAllow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain string `json:"domain"`
		TTL    int    `json:"ttl"` // seconds; 0 = permanent
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Domain == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := s.provider.AllowDomain(body.Domain); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.log.Info("domain allowed via API", zap.String("domain", body.Domain))
	writeJSON(w, http.StatusOK, map[string]string{"status": "allowed", "domain": body.Domain})
}

// handleFlushCache clears the DNS cache.
func (s *Server) handleFlushCache(w http.ResponseWriter, _ *http.Request) {
	s.provider.FlushCache()
	s.log.Info("cache flushed via API")
	writeJSON(w, http.StatusOK, map[string]string{"status": "flushed"})
}

// handlePAC returns the Proxy Auto-Config JavaScript.
func (s *Server) handlePAC(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	w.Write([]byte(`function FindProxyForURL(url, host) {
  if (/\.(local|internal|corp)$/.test(host)) return "DIRECT";
  return "PROXY 127.0.0.1:7788; SOCKS5 127.0.0.1:7789; DIRECT";
}`)) //nolint:errcheck
}

// handleHealth returns 200 OK for health checks.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleExtWebSocket handles browser extension WebSocket connections.
// Each connection receives push stats updates and can send block/allow commands.
func (s *Server) handleExtWebSocket(w http.ResponseWriter, r *http.Request) {
	// TODO: upgrade to WebSocket using nhooyr.io/websocket or gorilla/websocket
	// For now, return 426 Upgrade Required as a clear signal
	http.Error(w, "WebSocket upgrade required", http.StatusUpgradeRequired)
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
