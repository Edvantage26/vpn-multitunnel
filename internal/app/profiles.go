package app

import (
	"fmt"
	"log"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/system"
)

// GetProfiles returns all profiles with their current status
func (app *App) GetProfiles() []ProfileStatus {
	app.mu.RLock()
	defer app.mu.RUnlock()

	profiles := app.profileService.GetAll()
	result := make([]ProfileStatus, len(profiles))

	for idx_profile, current_profile := range profiles {
		status := ProfileStatus{
			ID:         current_profile.ID,
			Name:       current_profile.Name,
			ConfigFile: current_profile.ConfigFile,
			Connected:  app.tunnelManager.IsConnected(current_profile.ID),
			Connecting: app.connectingProfiles[current_profile.ID],
			TunnelIP:   app.profileService.GetTunnelIP(current_profile.ID),
		}

		// Get stats if connected
		if status.Connected {
			if stats := app.tunnelManager.GetStats(current_profile.ID); stats != nil {
				status.BytesSent = stats.BytesSent
				status.BytesRecv = stats.BytesRecv
				status.LastHandshake = stats.LastHandshake
				status.Endpoint = stats.Endpoint
				status.Healthy = stats.Connected
			}
		}

		result[idx_profile] = status
	}

	return result
}

// GetProfile returns a single profile by ID
func (app *App) GetProfile(id string) (*config.Profile, error) {
	return app.profileService.GetByID(id)
}

// Connect connects a profile by ID (with UAC elevation if needed)
func (app *App) Connect(id string) error {
	return app.connectInternal(id, true)
}

// connectInternal connects a profile, optionally prompting for elevation
func (app *App) connectInternal(id string, allowElevation bool) error {
	profile, err := app.profileService.GetByID(id)
	if err != nil {
		return err
	}

	if app.tunnelManager.IsConnected(id) {
		return fmt.Errorf("profile %s is already connected", profile.Name)
	}

	// Ensure loopback IP exists for this profile
	if app.config.Settings.AutoConfigureLoopback && app.config.TCPProxy.Enabled {
		if tunnelIP := app.profileService.GetTunnelIP(id); tunnelIP != "" {
			if !app.networkConfig.LoopbackIPExists(tunnelIP) {
				if allowElevation || app.networkConfig.IsServiceConnected() {
					if err := app.networkConfig.AddLoopbackIPElevated(tunnelIP); err != nil {
						log.Printf("Warning: Could not add loopback IP %s: %v", tunnelIP, err)
						// Continue anyway - transparent proxy will still work
					}
				} else {
					log.Printf("Loopback IP %s not configured, skipping (no elevation and no service)", tunnelIP)
				}
			}
		}
	}

	// Start the tunnel
	if err := app.tunnelManager.Start(profile); err != nil {
		return err
	}

	// Configure system DNS if not already configured and auto-configure is enabled
	if !app.networkConfig.IsTransparentDNSConfigured() && app.config.Settings.AutoConfigureDNS && app.config.DNSProxy.Enabled {
		// Allow DNS configure if elevation is permitted OR if the service is connected
		// (the service handles privileged ops without UAC)
		if allowElevation || app.networkConfig.IsServiceConnected() {
			app.configureSystemDNS()
		} else {
			log.Printf("DNS not configured (no elevation allowed and service not connected)")
		}
	}

	// Flush DNS cache so apps pick up new tunnel routes
	if app.config.DNSProxy.Enabled {
		system.FlushDNSCache()
	}

	// Update tray status
	app.updateTrayStatus()

	return nil
}

// Disconnect disconnects a profile by ID
func (app *App) Disconnect(id string) error {
	if err := app.tunnelManager.Stop(id); err != nil {
		return err
	}

	// Restore DNS if this was the last connection
	if app.tunnelManager.GetConnectedCount() == 0 && app.networkConfig.IsTransparentDNSConfigured() {
		app.restoreSystemDNS()
	}

	// Flush DNS cache so apps stop using stale tunnel routes
	if app.config.DNSProxy.Enabled {
		system.FlushDNSCache()
	}

	// Update tray status
	app.updateTrayStatus()

	return nil
}

// ConnectAll connects all enabled profiles
func (app *App) ConnectAll() error {
	profiles := app.profileService.GetAll()
	var lastErr error

	for _, p := range profiles {
		if p.Enabled && !app.tunnelManager.IsConnected(p.ID) {
			// Use Connect() to trigger UAC elevation for loopback IPs
			if err := app.Connect(p.ID); err != nil {
				log.Printf("Failed to connect %s: %v", p.Name, err)
				lastErr = err
			}
		}
	}

	// Update tray status (in case some connections failed)
	app.updateTrayStatus()

	return lastErr
}

// DisconnectAll disconnects all profiles
func (app *App) DisconnectAll() error {
	app.tunnelManager.StopAll()

	// Restore DNS since all VPNs are now disconnected
	if app.networkConfig.IsTransparentDNSConfigured() {
		app.restoreSystemDNS()
	}

	// Update tray status
	app.updateTrayStatus()

	return nil
}

// DeleteProfile deletes a profile by ID
func (app *App) DeleteProfile(id string) error {
	// Disconnect first if connected
	if app.tunnelManager.IsConnected(id) {
		app.tunnelManager.Stop(id)
	}

	// Get tunnel IP before deleting
	tunnelIP := app.profileService.GetTunnelIP(id)

	// Delete the profile
	if err := app.profileService.Delete(id); err != nil {
		return err
	}

	// Remove loopback IP if it was assigned
	if tunnelIP != "" && app.config.Settings.AutoConfigureLoopback {
		// Remove from config
		delete(app.config.TCPProxy.TunnelIPs, id)
		config.Save(app.config)

		// Remove from system if admin
		if system.IsAdmin() {
			if err := app.networkConfig.RemoveLoopbackIP(tunnelIP); err != nil {
				log.Printf("Warning: Failed to remove loopback IP %s: %v", tunnelIP, err)
			} else {
				log.Printf("Removed loopback IP %s", tunnelIP)
			}
		}
	}

	return nil
}

// ImportConfig imports a WireGuard configuration file
func (app *App) ImportConfig() (*config.Profile, error) {
	// Open file dialog
	selection, err := runtime.OpenFileDialog(app.ctx, runtime.OpenDialogOptions{
		Title: "Import WireGuard Configuration",
		Filters: []runtime.FileFilter{
			{DisplayName: "WireGuard Config", Pattern: "*.conf"},
		},
	})
	if err != nil {
		return nil, err
	}
	if selection == "" {
		return nil, fmt.Errorf("no file selected")
	}

	// Import the config
	profile, err := app.profileService.Import(selection)
	if err != nil {
		return nil, err
	}

	// Add loopback IP for the new profile if running as admin
	if app.config.Settings.AutoConfigureLoopback && app.config.TCPProxy.Enabled {
		if tunnelIP := app.profileService.GetTunnelIP(profile.ID); tunnelIP != "" {
			if system.IsAdmin() {
				if err := app.networkConfig.EnsureLoopbackIPs([]string{tunnelIP}); err != nil {
					log.Printf("Warning: Failed to add loopback IP %s: %v", tunnelIP, err)
				} else {
					log.Printf("Added loopback IP %s for profile %s", tunnelIP, profile.Name)
				}
			}
		}
	}

	return profile, nil
}

// UpdateProfile updates a profile
func (app *App) UpdateProfile(profile config.Profile) error {
	if err := app.profileService.Update(profile); err != nil {
		return err
	}

	// Sync DNS settings to global DNS rules
	app.syncProfileDNSToRules(profile)

	// Restart TCP proxy to pick up per-profile port changes
	if app.config.TCPProxy.Enabled {
		app.tunnelManager.RestartTCPProxy(&app.config.TCPProxy)
	}

	return nil
}

// ReorderProfiles persists a new display order for profiles
func (app *App) ReorderProfiles(orderedIDs []string) error {
	return app.profileService.Reorder(orderedIDs)
}

// syncProfileDNSToRules syncs a profile's DNS settings to the global DNS proxy rules
func (app *App) syncProfileDNSToRules(profile config.Profile) {
	if profile.DNS.Server == "" || len(profile.DNS.Domains) == 0 {
		return
	}

	// Find or create rules for each domain suffix
	for _, domain := range profile.DNS.Domains {
		suffix := domain
		if !strings.HasPrefix(suffix, ".") {
			suffix = "." + suffix
		}

		// Find existing rule for this suffix
		found := false
		for i := range app.config.DNSProxy.Rules {
			if app.config.DNSProxy.Rules[i].Suffix == suffix && app.config.DNSProxy.Rules[i].ProfileID == profile.ID {
				// Update existing rule
				app.config.DNSProxy.Rules[i].DNSServer = profile.DNS.Server
				app.config.DNSProxy.Rules[i].Hosts = profile.DNS.Hosts
				found = true
				break
			}
		}

		// Create new rule if not found
		if !found {
			newRule := config.DNSRule{
				Suffix:    suffix,
				ProfileID: profile.ID,
				DNSServer: profile.DNS.Server,
				Hosts:     profile.DNS.Hosts,
			}
			app.config.DNSProxy.Rules = append(app.config.DNSProxy.Rules, newRule)
		}
	}

	// Enable DNS proxy if not already
	if !app.config.DNSProxy.Enabled && len(app.config.DNSProxy.Rules) > 0 {
		app.config.DNSProxy.Enabled = true
	}

	// Save and restart DNS proxy
	config.Save(app.config)
	app.tunnelManager.RestartDNSProxy(&app.config.DNSProxy)
}
