package app

import (
	"fmt"
	"strings"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/system"
	"vpnmultitunnel/internal/tray"
)

// IsRunningAsAdmin returns whether the app is running with admin privileges
func (app *App) IsRunningAsAdmin() bool {
	return system.IsAdmin()
}

// GetSystemStatus returns the current system configuration status
func (app *App) GetSystemStatus() map[string]interface{} {
	isAdmin := system.IsAdmin()
	dnsConfigured := false
	if app.networkConfig != nil {
		// Use IsTransparentDNSConfigured which checks actual system DNS state
		dnsConfigured = app.networkConfig.IsTransparentDNSConfigured()
	}

	// Get current system DNS and active interface
	currentDNS := ""
	activeInterface := ""
	if app.networkConfig != nil {
		interfaceName, err := app.networkConfig.GetActiveNetworkInterface()
		if err == nil {
			activeInterface = interfaceName
			dnsServers, err := app.networkConfig.GetCurrentDNS(interfaceName)
			if err == nil && len(dnsServers) > 0 {
				currentDNS = dnsServers[0]
			}
		}
	}

	// Check if service is connected
	serviceConnected := false
	if app.networkConfig != nil {
		serviceConnected = app.networkConfig.IsServiceConnected()
	}

	return map[string]interface{}{
		"isAdmin":               isAdmin,
		"serviceConnected":      serviceConnected,
		"dnsConfigured":         dnsConfigured,
		"currentDNS":            currentDNS,
		"dnsProxyAddress":       app.config.DNSProxy.GetListenAddress(),
		"port53Free":            app.isPort53Free(),
		"dnsClientRunning":      app.isDNSClientRunning(),
		"autoConfigureLoopback": app.config.Settings.AutoConfigureLoopback,
		"autoConfigureDNS":      app.config.Settings.AutoConfigureDNS,
		"usePort53":             app.config.Settings.UsePort53,
		"useService":            app.config.Settings.UseService,
		"tcpProxyEnabled":       app.config.TCPProxy.IsEnabled(),
		"dnsProxyEnabled":       app.config.DNSProxy.Enabled,
		"dnsProxyPort":          app.config.DNSProxy.ListenPort,
		"activeInterface":       activeInterface,
		"dnsIssue":              app.dnsHealthIssue,
		"appVersion":            AppVersion,
	}
}

// IsServiceConnected returns whether the VPN MultiTunnel service is connected
func (app *App) IsServiceConnected() bool {
	if app.networkConfig == nil {
		return false
	}
	return app.networkConfig.IsServiceConnected()
}

// AssignTunnelIPsForExistingProfiles assigns tunnel IPs to profiles that don't have one
func (app *App) AssignTunnelIPsForExistingProfiles() error {
	profiles := app.profileService.GetAll()
	changed := false

	for _, p := range profiles {
		if app.config.TCPProxy.TunnelIPs == nil {
			app.config.TCPProxy.TunnelIPs = make(map[string]string)
		}
		if _, exists := app.config.TCPProxy.TunnelIPs[p.ID]; !exists {
			// Profile doesn't have a tunnel IP, assign one
			tunnelIP := app.findNextTunnelIP()
			if tunnelIP != "" {
				app.config.TCPProxy.TunnelIPs[p.ID] = tunnelIP
				changed = true
			}
		}
	}

	if changed {
		return config.Save(app.config)
	}
	return nil
}

// findNextTunnelIP finds the next available tunnel IP
func (app *App) findNextTunnelIP() string {
	used := make(map[int]bool)
	for _, ip := range app.config.TCPProxy.TunnelIPs {
		parts := strings.Split(ip, ".")
		if len(parts) == 4 && parts[0] == "127" && parts[1] == "0" {
			var octect int
			fmt.Sscanf(parts[2], "%d", &octect)
			used[octect] = true
		}
	}

	for ip_octet := 1; ip_octet < 255; ip_octet++ {
		if !used[ip_octet] {
			return fmt.Sprintf("127.0.%d.1", ip_octet)
		}
	}
	return ""
}

// updateTrayStatus updates the system tray tooltip with current VPN status
func (app *App) updateTrayStatus() {
	if app.systemTray == nil {
		return
	}

	profiles := app.profileService.GetAll()
	statuses := make([]tray.VPNStatus, len(profiles))

	for idx_profile, profile_entry := range profiles {
		statuses[idx_profile] = tray.VPNStatus{
			Name:      profile_entry.Name,
			Connected: app.tunnelManager.IsConnected(profile_entry.ID),
		}
	}

	app.systemTray.UpdateVPNStatus(statuses)
}

// GetLoopbackIPs returns all assigned loopback IPs
func (app *App) GetLoopbackIPs() map[string]string {
	result := make(map[string]string)

	// Get IPs from TCP proxy config
	for profileID, ip := range app.config.TCPProxy.TunnelIPs {
		result[profileID] = ip
	}

	// Also get dynamically assigned IPs from host mappings
	mappings := app.GetHostMappings()
	for _, m := range mappings {
		if !strings.HasPrefix(m.LoopbackIP, "127.0.") {
			continue
		}
		// Use hostname as key for dynamic IPs
		key := fmt.Sprintf("host:%s", m.Hostname)
		result[key] = m.LoopbackIP
	}

	return result
}
