package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"vpnmultitunnel/internal/app"
	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/system"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Enforce single instance: if another instance is running, request graceful
	// shutdown so it cleans up its tray icon, then take over the mutex.
	singleInstance, singleInstanceErr := system.NewSingleInstance()
	if singleInstanceErr != nil {
		log.Printf("Warning: single-instance check failed: %v", singleInstanceErr)
	} else {
		defer singleInstance.Release()
	}

	// Load configuration early to use settings for Wails options
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Failed to load config: %v, using defaults", err)
		cfg = config.Default()
	}

	// Create an instance of the app structure
	application := app.New()

	// Wire up single-instance shutdown listener so future instances can ask us to quit.
	// When a new instance starts, it sends a shutdown message via named pipe.
	// We clean up the tray icon and exit so the new instance can take over cleanly.
	if singleInstance != nil {
		singleInstance.SetShutdownCallback(func() {
			log.Printf("Graceful shutdown requested by new instance")
			application.Shutdown(nil)
			singleInstance.Release()
			os.Exit(0)
		})
		singleInstance.ListenForShutdown()
		singleInstance.ListenForSignals()
	}

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
		HideWindowOnClose: true,
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
