// Package main — LombokDNSProxy Desktop GUI
// Wails v2 cross-platform desktop app (Windows, macOS, Linux).
// Provides: system tray, web dashboard, OS DNS integration.
// Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "LombokDNSProxy",
		Width:            1024,
		Height:           720,
		MinWidth:         800,
		MinHeight:        600,
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 1},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
		},
		OnDomReady: func(ctx context.Context) {
			app.domReady(ctx)
		},
		OnShutdown: func(ctx context.Context) {
			app.shutdown(ctx)
		},
		Bind: []interface{}{
			app,
		},
		// macOS options
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			Appearance:           mac.NSAppearanceNameAqua,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			About: &mac.AboutInfo{
				Title:   "LombokDNSProxy",
				Message: "Privacy-first DNS proxy\nPart of the Lombok Ecosystem\nApache 2.0 · @codinglombok",
			},
		},
		// Windows options
		Windows: &windows.Options{
			WebviewIsTransparent:              false,
			WindowIsTranslucent:               false,
			DisableFramelessWindowDecorations: false,
		},
		// System tray
		SystemTray: &options.SystemTray{
			Title:   "LombokDNSProxy",
			Tooltip: "LombokDNSProxy — click to open dashboard",
		},
	})
	if err != nil {
		panic(err)
	}
}
