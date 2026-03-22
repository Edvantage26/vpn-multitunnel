package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"vpnmultitunnel/internal/app"
	"vpnmultitunnel/internal/config"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Load configuration early to use settings for Wails options
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Failed to load config: %v, using defaults", err)
		cfg = config.Default()
	}

	// Create an instance of the app structure
	application := app.New()

	// Create application with options
	err = wails.Run(&options.App{
		Title:     "VPN MultiTunnel",
		Width:     1200,
		Height:    800,
		MinWidth:  800,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour:  &options.RGBA{R: 24, G: 24, B: 27, A: 1},
		OnStartup:         application.Startup,
		OnShutdown:        application.Shutdown,
		Frameless:         false,
		StartHidden:       cfg.Settings.StartMinimized,
		HideWindowOnClose: cfg.Settings.MinimizeToTray,
		Windows: &windows.Options{
			WebviewIsTransparent:              false,
			WindowIsTranslucent:               false,
			DisableWindowIcon:                 false,
			DisableFramelessWindowDecorations: false,
			WebviewUserDataPath:               "",
			WebviewBrowserPath:                "",
			Theme:                             windows.Dark,
		},
		Bind: []interface{}{
			application,
		},
	})

	if err != nil {
		log.Fatal("Error:", err)
	}
}
