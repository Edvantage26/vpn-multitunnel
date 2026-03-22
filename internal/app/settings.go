package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"vpnmultitunnel/internal/config"
)

// UpdateSettingsResult describes the side effects that occurred when updating settings
type UpdateSettingsResult struct {
	DNSProxyRestarted     bool   `json:"dnsProxyRestarted"`
	SystemDNSReconfigured bool   `json:"systemDNSReconfigured"`
	LoopbackIPChanged     bool   `json:"loopbackIPChanged"`
	Warning               string `json:"warning,omitempty"`
}

// GetSettings returns the current settings
func (app *App) GetSettings() config.Settings {
	return app.config.Settings
}

// UpdateSettings updates the settings and handles all DNS side effects
func (app *App) UpdateSettings(settings config.Settings) (UpdateSettingsResult, error) {
	result := UpdateSettingsResult{}

	previous_dns_address := app.config.Settings.DNSListenAddress
	previous_fallback := app.config.Settings.DNSFallbackServer
	dns_was_active := app.networkConfig.IsTransparentDNSConfigured()

	app.config.Settings = settings

	dns_address_changed := settings.DNSListenAddress != previous_dns_address &&
		settings.DNSListenAddress != "" &&
		settings.DNSListenAddress != "127.0.0.1"

	fallback_changed := settings.DNSFallbackServer != previous_fallback

	// Handle DNS listen address change
	if dns_address_changed {
		log.Printf("DNS listen address changing from %s to %s", previous_dns_address, settings.DNSListenAddress)
		result.LoopbackIPChanged = true

		if dns_was_active {
			// Restore old DNS configuration first
			log.Printf("Transparent DNS was active, restoring before address change...")
			app.restoreSystemDNS()

			// Remove old loopback IP
			if previous_dns_address != "" && previous_dns_address != "127.0.0.1" {
				if err := app.networkConfig.RemoveLoopbackIPElevated(previous_dns_address); err != nil {
					log.Printf("Warning: could not remove old loopback IP %s: %v", previous_dns_address, err)
				}
			}
		}

		// Create new loopback IP
		if err := app.networkConfig.AddLoopbackIPElevated(settings.DNSListenAddress); err != nil {
			log.Printf("Warning: could not add loopback IP %s: %v", settings.DNSListenAddress, err)
			result.Warning = fmt.Sprintf("Failed to create loopback IP %s: %v", settings.DNSListenAddress, err)
		}

		// Update DNS proxy config to use new address
		app.config.DNSProxy.ListenAddress = settings.DNSListenAddress
		app.networkConfig.SetDNSProxyAddress(settings.DNSListenAddress)

		// Restart DNS proxy on new address
		app.tunnelManager.RestartDNSProxy(&app.config.DNSProxy)
		result.DNSProxyRestarted = true

		if dns_was_active {
			// Reconfigure system DNS to point to new address
			log.Printf("Reconfiguring system DNS to new address %s...", settings.DNSListenAddress)
			app.configureSystemDNS()
			result.SystemDNSReconfigured = true
		}
	}

	// Handle fallback DNS change
	if fallback_changed {
		log.Printf("Fallback DNS changed from %s to %s", previous_fallback, settings.DNSFallbackServer)
		app.networkConfig.SetDNSFallbackServer(settings.DNSFallbackServer)

		// Restart DNS proxy to pick up new fallback (only if we didn't already restart above)
		if !result.DNSProxyRestarted {
			app.tunnelManager.RestartDNSProxy(&app.config.DNSProxy)
			result.DNSProxyRestarted = true
		}
	}

	if err := config.Save(app.config); err != nil {
		return result, err
	}

	return result, nil
}

// CopyEnvVars generates and copies environment variables to clipboard
func (app *App) CopyEnvVars() error {
	var envVars []string

	profiles := app.profileService.GetAll()
	for _, p := range profiles {
		if !app.tunnelManager.IsConnected(p.ID) {
			continue
		}

	}

	if len(envVars) == 0 {
		return fmt.Errorf("no active connections")
	}

	text := strings.Join(envVars, "\n")
	return runtime.ClipboardSetText(app.ctx, text)
}

// GetConfigDir returns the configuration directory path
func (app *App) GetConfigDir() (string, error) {
	return config.GetConfigDir()
}

// GetAppPath returns the directory where the application is running from
func (app *App) GetAppPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	return filepath.Dir(execPath)
}
