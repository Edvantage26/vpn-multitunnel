package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

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
	// Set up crash log: capture panics to a file so we can diagnose crashes
	setupCrashLog()
	defer func() {
		if panicValue := recover(); panicValue != nil {
			crashMessage := fmt.Sprintf("FATAL PANIC at %s: %v\n\nStack trace:\n%s",
				time.Now().Format(time.RFC3339), panicValue, debug.Stack())
			log.Printf(crashMessage)
			writeCrashLog(crashMessage)
		}
	}()
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

// setupCrashLog configures logging to also write to a crash log file
func setupCrashLog() {
	crashLogPath := getCrashLogPath()
	crashLogFile, fileErr := os.OpenFile(crashLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if fileErr != nil {
		return // Can't create crash log, continue without it
	}
	// Write startup marker
	fmt.Fprintf(crashLogFile, "\n=== App started at %s ===\n", time.Now().Format(time.RFC3339))
	crashLogFile.Close()
}

// writeCrashLog writes a crash message to the crash log file
func writeCrashLog(crashMessage string) {
	crashLogPath := getCrashLogPath()
	crashLogFile, fileErr := os.OpenFile(crashLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if fileErr != nil {
		return
	}
	defer crashLogFile.Close()
	fmt.Fprintf(crashLogFile, "%s\n", crashMessage)
}

// getCrashLogPath returns the path to the crash log file in the data directory
func getCrashLogPath() string {
	dataDir, dataDirErr := config.GetDataDir()
	if dataDirErr != nil {
		// Fallback to temp directory
		return filepath.Join(os.TempDir(), "vpnmultitunnel-crash.log")
	}
	return filepath.Join(dataDir, "crash.log")
}
