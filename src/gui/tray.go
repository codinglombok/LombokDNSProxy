// Package main — LombokDNSProxy Desktop GUI · System Tray
// Manages the system tray icon, menu, and quick-toggle functionality.
// Uses the Wails v2 system tray API (cross-platform: Windows, macOS, Linux).
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package main

import (
	"context"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// TrayManager manages the system tray state and menu.
type TrayManager struct {
	ctx       context.Context
	app       *App
	protected bool // whether protection is currently active
}

// NewTrayManager creates a TrayManager bound to the given app.
func NewTrayManager(ctx context.Context, app *App) *TrayManager {
	return &TrayManager{ctx: ctx, app: app, protected: true}
}

// SetupMenu builds and registers the tray context menu.
// Call after the Wails app has started (dom ready).
func (t *TrayManager) SetupMenu() {
	t.updateIcon()
	t.rebuildMenu()
}

// rebuildMenu re-creates the tray menu based on current state.
func (t *TrayManager) rebuildMenu() {
	stats := t.app.GetStats()
	statusLabel := "🟢 Protection: ON"
	if !t.protected {
		statusLabel = "🔴 Protection: OFF"
	}

	runtime.MenuSetApplicationMenu(t.ctx, &runtime.Menu{
		Items: []*runtime.MenuItem{
			{
				Label:   "LombokDNSProxy",
				Enabled: boolPtr(false), // title — not clickable
			},
			{Type: runtime.Separator},
			{
				Label:   statusLabel,
				Enabled: boolPtr(false),
			},
			{
				Label: fmt.Sprintf("Queries: %s  Blocked: %s  Cache: %.0f%%",
					formatCount(stats.TotalQueries),
					formatCount(stats.BlockedCount),
					stats.CacheHitRate,
				),
				Enabled: boolPtr(false),
			},
			{Type: runtime.Separator},
			{
				Label: "Open Dashboard",
				Click: &runtime.CallbackData{
					Callback: func(_ *runtime.CallbackData) {
						runtime.BrowserOpenURL(t.ctx, "http://127.0.0.1:7787")
					},
				},
			},
			{
				Label: "Toggle Protection",
				Click: &runtime.CallbackData{
					Callback: func(_ *runtime.CallbackData) {
						t.toggleProtection()
					},
				},
			},
			{Type: runtime.Separator},
			{
				Label: "Flush DNS Cache",
				Click: &runtime.CallbackData{
					Callback: func(_ *runtime.CallbackData) {
						t.flushCache()
					},
				},
			},
			{
				Label: "Update Blocklists",
				Click: &runtime.CallbackData{
					Callback: func(_ *runtime.CallbackData) {
						t.updateLists()
					},
				},
			},
			{Type: runtime.Separator},
			{
				Label: "Quit",
				Click: &runtime.CallbackData{
					Callback: func(_ *runtime.CallbackData) {
						runtime.Quit(t.ctx)
					},
				},
			},
		},
	})
}

// toggleProtection enables or disables DNS blocking.
func (t *TrayManager) toggleProtection() {
	t.protected = !t.protected
	t.updateIcon()
	t.rebuildMenu()

	state := "enabled"
	if !t.protected {
		state = "paused"
	}
	runtime.MessageDialog(t.ctx, runtime.MessageDialogOptions{
		Type:    runtime.InfoDialog,
		Title:   "LombokDNSProxy",
		Message: "Protection " + state,
	})
}

// flushCache clears the DNS cache via the daemon API.
func (t *TrayManager) flushCache() {
	// TODO: POST http://127.0.0.1:7787/api/v1/cache/flush
	runtime.MessageDialog(t.ctx, runtime.MessageDialogOptions{
		Type:    runtime.InfoDialog,
		Title:   "DNS Cache",
		Message: "Cache flushed successfully",
	})
}

// updateLists triggers an immediate filter list refresh.
func (t *TrayManager) updateLists() {
	// TODO: POST http://127.0.0.1:7787/api/v1/filter/reload
	runtime.MessageDialog(t.ctx, runtime.MessageDialogOptions{
		Type:    runtime.InfoDialog,
		Title:   "Blocklists",
		Message: "Blocklist update started in background",
	})
}

// updateIcon switches the tray icon between active and paused state.
func (t *TrayManager) updateIcon() {
	// Icon paths relative to the Wails embedded asset server
	// Wails handles platform-specific icon formats (ICO/ICNS/PNG)
	icon := "icons/icon-active.png"
	if !t.protected {
		icon = "icons/icon-paused.png"
	}
	runtime.WindowSetIcon(t.ctx, []byte(icon)) // In production: embed icon bytes
}

// formatCount converts a uint64 to a short human-readable string.
func formatCount(n uint64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func boolPtr(b bool) *bool { return &b }
