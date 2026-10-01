// Package main — LombokDNSProxy Desktop GUI · App bindings
// All methods exported here become callable from the frontend via Wails.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package main

import (
	"context"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the main Wails application struct.
// Its exported methods are bound to the JavaScript frontend.
type App struct {
	ctx     context.Context
	started bool
}

// NewApp creates a new App instance.
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. Initialises the daemon connection.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// TODO: connect to running daemon via HTTP / start embedded daemon
	a.started = true
}

// domReady is called after the frontend DOM is ready.
func (a *App) domReady(_ context.Context) {
	runtime.WindowSetTitle(a.ctx, "LombokDNSProxy")
}

// shutdown is called on app exit. Restores system DNS if configured.
func (a *App) shutdown(_ context.Context) {
	// TODO: call daemon shutdown endpoint or restore OS DNS
}

// ─── Stats ───────────────────────────────────────────────────────────────────

// GlobalStats is the shape returned to the frontend.
type GlobalStats struct {
	TotalQueries  uint64  `json:"totalQueries"`
	BlockedCount  uint64  `json:"blockedCount"`
	CacheHitRate  float64 `json:"cacheHitRate"`
	AvgLatencyMs  float64 `json:"avgLatencyMs"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
	FilteredDomains int   `json:"filteredDomains"`
}

// GetStats returns current daemon statistics.
func (a *App) GetStats() GlobalStats {
	// TODO: query http://127.0.0.1:7787/api/v1/stats
	return GlobalStats{
		TotalQueries:    48291,
		BlockedCount:    7234,
		CacheHitRate:    71.3,
		AvgLatencyMs:    9.2,
		UptimeSeconds:   8040,
		FilteredDomains: 4_200_000,
	}
}

// ─── Filter control ──────────────────────────────────────────────────────────

// BlockDomain adds domain to the runtime blocklist.
func (a *App) BlockDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	// TODO: POST http://127.0.0.1:7787/api/v1/filter/block
	return nil
}

// AllowDomain adds domain to the allowlist, overriding any blocklist match.
func (a *App) AllowDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	// TODO: POST http://127.0.0.1:7787/api/v1/filter/allow
	return nil
}

// ─── Daemon control ──────────────────────────────────────────────────────────

// StartDaemon starts the embedded DNS daemon.
func (a *App) StartDaemon() error {
	// TODO: start embedded server.Server
	return nil
}

// StopDaemon stops the DNS daemon.
func (a *App) StopDaemon() error {
	// TODO: graceful shutdown
	return nil
}

// IsRunning returns true if the daemon is active.
func (a *App) IsRunning() bool {
	return a.started
}

// ─── System DNS ──────────────────────────────────────────────────────────────

// SetSystemDNS configures the OS to use LombokDNSProxy as the DNS resolver.
func (a *App) SetSystemDNS() error {
	// TODO: platform-specific:
	// Linux  → systemd-resolved / /etc/resolv.conf
	// macOS  → networksetup -setdnsservers
	// Windows→ Set-DnsClientServerAddress via WinDivert
	return nil
}

// RestoreSystemDNS restores the OS to its original DNS configuration.
func (a *App) RestoreSystemDNS() error {
	return nil
}

// ─── UI helpers ──────────────────────────────────────────────────────────────

// OpenDashboard opens the full web dashboard in the default browser.
func (a *App) OpenDashboard() {
	runtime.BrowserOpenURL(a.ctx, "http://127.0.0.1:7787")
}

// ShowNotification sends a system notification.
func (a *App) ShowNotification(title, body string) {
	runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:    runtime.InfoDialog,
		Title:   title,
		Message: body,
	})
}
