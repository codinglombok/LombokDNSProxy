// Package api — LombokDNSProxy · WebSocket Handler
// Real-time bidirectional channel between the OS daemon and browser extension.
// Uses nhooyr.io/websocket (stdlib-friendly, no gorilla dependency).
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// WSMessage is the JSON envelope exchanged between daemon and extension.
type WSMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ExtHub manages all connected browser extension WebSocket clients.
type ExtHub struct {
	mu      sync.RWMutex
	clients map[*extClient]struct{}
	log     *zap.Logger
	stats   StatsProvider
}

// extClient represents one connected browser extension instance.
type extClient struct {
	conn    *websocket.Conn
	send    chan WSMessage
	hub     *ExtHub
	tabData map[int][]DomainEntry // tabId → domains
	mu      sync.Mutex
}

// DomainEntry is one DNS query event from the extension.
type DomainEntry struct {
	Domain    string  `json:"domain"`
	Blocked   bool    `json:"blocked"`
	Cached    bool    `json:"cached"`
	LatencyMs float64 `json:"latencyMs"`
	Upstream  string  `json:"upstream"`
	TabID     int     `json:"tabId"`
	URL       string  `json:"url"`
}

// NewExtHub creates an ExtHub that serves extension WebSocket connections.
func NewExtHub(stats StatsProvider, log *zap.Logger) *ExtHub {
	h := &ExtHub{
		clients: make(map[*extClient]struct{}),
		log:     log,
		stats:   stats,
	}
	go h.broadcastLoop()
	return h
}

// ServeHTTP handles incoming WebSocket upgrade requests.
// Mount at /ext on the API server.
func (h *ExtHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"chrome-extension://*", "moz-extension://*"},
		InsecureSkipVerify: true, // only 127.0.0.1 is reachable anyway
	})
	if err != nil {
		h.log.Debug("ws: accept failed", zap.Error(err))
		return
	}

	client := &extClient{
		conn:    conn,
		send:    make(chan WSMessage, 32),
		hub:     h,
		tabData: make(map[int][]DomainEntry),
	}

	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	h.log.Debug("extension connected", zap.String("remote", r.RemoteAddr))

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go client.writeLoop(ctx)
	client.readLoop(ctx) // blocks until disconnect

	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()

	conn.Close(websocket.StatusNormalClosure, "bye")
	h.log.Debug("extension disconnected")
}

// Broadcast sends a message to all connected extension clients.
func (h *ExtHub) Broadcast(msg WSMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default: // drop if buffer full
		}
	}
}

// PushQueryEvent notifies all clients of a DNS query result.
func (h *ExtHub) PushQueryEvent(entry DomainEntry) {
	payload, _ := json.Marshal(entry)
	h.Broadcast(WSMessage{Type: "query_log", Payload: payload})
}

// broadcastLoop sends periodic stats pushes to all connected clients.
func (h *ExtHub) broadcastLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		stats := h.stats.GlobalStats()
		payload, _ := json.Marshal(stats)
		h.Broadcast(WSMessage{Type: "stats", Payload: payload})
	}
}

// readLoop handles incoming messages from one extension client.
func (c *extClient) readLoop(ctx context.Context) {
	for {
		var msg WSMessage
		if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
			return // disconnect or context cancelled
		}

		switch msg.Type {
		case "ping":
			c.send <- WSMessage{Type: "pong"}

		case "block":
			var body struct {
				Domain string `json:"domain"`
			}
			if json.Unmarshal(msg.Payload, &body) == nil && body.Domain != "" {
				c.hub.stats.(interface{ BlockDomain(string) error }).BlockDomain(body.Domain) //nolint:errcheck
				c.hub.log.Info("domain blocked via extension", zap.String("domain", body.Domain))
			}

		case "allow":
			var body struct {
				Domain string `json:"domain"`
				TTL    int    `json:"ttl"`
			}
			if json.Unmarshal(msg.Payload, &body) == nil && body.Domain != "" {
				c.hub.stats.(interface{ AllowDomain(string) error }).AllowDomain(body.Domain) //nolint:errcheck
				c.hub.log.Info("domain allowed via extension", zap.String("domain", body.Domain))
			}

		case "query_log":
			var entry DomainEntry
			if json.Unmarshal(msg.Payload, &entry) == nil {
				c.mu.Lock()
				tab := c.tabData[entry.TabID]
				tab = append([]DomainEntry{entry}, tab...)
				if len(tab) > 200 {
					tab = tab[:200]
				}
				c.tabData[entry.TabID] = tab
				c.mu.Unlock()
			}

		case "stats":
			stats := c.hub.stats.GlobalStats()
			payload, _ := json.Marshal(stats)
			c.send <- WSMessage{Type: "stats", Payload: payload}
		}
	}
}

// writeLoop drains c.send and writes to the WebSocket connection.
func (c *extClient) writeLoop(ctx context.Context) {
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			if err := wsjson.Write(ctx, c.conn, msg); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
