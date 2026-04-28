package app

import (
	"fmt"
	"log"
	"strings"
	"time"

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
		profile_vpn_type := current_profile.GetVPNType()
		client_version := ""
		switch profile_vpn_type {
		case config.VPNTypeWireGuard:
			// Built-in library, no meaningful version to show
		case config.VPNTypeOpenVPN, config.VPNTypeWatchGuard:
			client_version = app.cachedOpenVPNVersion
		case config.VPNTypeExternal:
			client_version = "External"
		}

		status := ProfileStatus{
			ID:            current_profile.ID,
			Name:          current_profile.Name,
			Type:          string(profile_vpn_type),
			ConfigFile:    current_profile.ConfigFile,
			Connected:     app.tunnelManager.IsConnected(current_profile.ID),
			Connecting:    app.connectingProfiles[current_profile.ID],
			TunnelIP:      app.profileService.GetTunnelIP(current_profile.ID),
			LastError:     app.lastConnectErrors[current_profile.ID],
			ClientVersion: client_version,
			AutoConnect:   current_profile.Enabled && current_profile.ShouldAutoConnect(),
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
			// Surface any DNS health issues to the frontend
			if app.dnsHealthIssue != "" {
				status.DNSIssue = app.dnsHealthIssue
			}
		}

		result[idx_profile] = status
	}

	return result
}

// GetProfile returns a single profile by ID, with computed fields populated from the WireGuard .conf
func (app *App) GetProfile(id string) (*config.Profile, error) {
	profile, err := app.profileService.GetByID(id)
	if err != nil {
		return nil, err
	}
	app.populateComputedFields(profile)
	return profile, nil
}

// populateComputedFields fills read-only fields (DNS server, TargetIP) from the WireGuard .conf
func (app *App) populateComputedFields(profile *config.Profile) {
	profile.DNS.Server = app.tunnelManager.GetDNSServerForProfile(profile.ID)
	profile.HealthCheck.TargetIP = app.tunnelManager.GetTargetIPForProfile(profile.ID)
}

// Connect connects a profile by ID (with UAC elevation if needed)
func (app *App) Connect(id string) error {
	app.flipMasterOnIfDisabled()
	return app.connectInternal(id, true)
}

// ConnectWithCredentials connects a profile by ID, providing username/password for auth.
// Saves the credentials to the profile config for future connections.
func (app *App) ConnectWithCredentials(id string, username string, password string) error {
	app.flipMasterOnIfDisabled()
	// Save credentials to profile for future connections
	profile, profileErr := app.profileService.GetByID(id)
	if profileErr == nil {
		profile.Credentials = &config.VPNCredentialConfig{
			Username: username,
			Password: password,
		}
		app.profileService.Update(*profile)
	}

	app.tunnelManager.SetCredentials(id, username, password)
	return app.connectInternal(id, true)
}

// flipMasterOnIfDisabled re-enables the master switch when the user manually
// initiates a connection while it was off. This keeps the UI toggle in sync
// with the user's effective intent.
func (app *App) flipMasterOnIfDisabled() {
	app.mu.Lock()
	if !app.masterEnabled {
		app.masterEnabled = true
	}
	app.mu.Unlock()
}

// ProfileNeedsCredentials returns true if connecting this profile requires username/password
// AND no credentials are saved in the profile config.
func (app *App) ProfileNeedsCredentials(id string) bool {
	profile, err := app.profileService.GetByID(id)
	if err != nil {
		return false
	}
	// If credentials are already saved, don't prompt
	if profile.Credentials != nil && profile.Credentials.Username != "" {
		return false
	}
	if profile.GetVPNType() == config.VPNTypeOpenVPN {
		configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
		if pathErr != nil {
			return false
		}
		ovpnConfig, parseErr := config.ParseOpenVPNConfig(configPath)
		if parseErr != nil {
			return false
		}
		return ovpnConfig.AuthUserPass
	}
	if profile.GetVPNType() == config.VPNTypeWatchGuard {
		return true // WatchGuard always needs credentials
	}
	return false
}

// connectInternal connects a profile, optionally prompting for elevation.
// For OpenVPN and WatchGuard, the connection runs in a background goroutine
// so the UI doesn't freeze (these can take 60s+). WireGuard connects synchronously
// since it's near-instant (userspace).
func (app *App) connectInternal(id string, allowElevation bool) error {
	profile, err := app.profileService.GetByID(id)
	if err != nil {
		return err
	}

	if app.tunnelManager.IsConnected(id) {
		return fmt.Errorf("profile %s is already connected", profile.Name)
	}

	// Ensure loopback IP exists for this profile
	if app.config.Settings.AutoConfigureLoopback && app.config.TCPProxy.IsEnabled() {
		if tunnelIP := app.profileService.GetTunnelIP(id); tunnelIP != "" {
			if !app.networkConfig.LoopbackIPExists(tunnelIP) {
				if allowElevation || app.networkConfig.IsServiceConnected() {
					if loopbackErr := app.networkConfig.AddLoopbackIPElevated(tunnelIP); loopbackErr != nil {
						log.Printf("Warning: Could not add loopback IP %s: %v", tunnelIP, loopbackErr)
					}
				}
			}
		}
	}

	// For slow VPN types (OpenVPN, WatchGuard), run connection in background
	vpnType := profile.GetVPNType()
	if vpnType == config.VPNTypeOpenVPN || vpnType == config.VPNTypeWatchGuard || vpnType == config.VPNTypeExternal {
		app.mu.Lock()
		app.connectingProfiles[id] = true
		delete(app.lastConnectErrors, id)
		app.mu.Unlock()

		go app.connectInBackground(id, profile, allowElevation)
		return nil
	}

	// WireGuard: synchronous (near-instant)
	return app.doConnect(id, profile, allowElevation)
}

// doConnect performs the actual tunnel start and post-connect setup.
func (app *App) doConnect(id string, profile *config.Profile, allowElevation bool) error {
	if startErr := app.tunnelManager.Start(profile); startErr != nil {
		app.mu.Lock()
		app.lastConnectErrors[id] = startErr.Error()
		app.mu.Unlock()
		return startErr
	}

	// Clear any previous error on successful connect
	app.mu.Lock()
	delete(app.lastConnectErrors, id)
	app.mu.Unlock()

	// If the master switch was flipped OFF while tunnelManager.Start was running
	// (typical for OpenVPN which can take 30s+), skip DNS reconfiguration. The
	// caller (connectInBackground) will tear down the just-started tunnel.
	if !app.IsMasterEnabled() {
		log.Printf("doConnect for %s skipping post-connect setup: master is OFF", profile.Name)
		return nil
	}

	// Configure system DNS if not already configured and auto-configure is enabled
	if !app.networkConfig.IsTransparentDNSConfigured() && app.config.Settings.AutoConfigureDNS && app.config.DNSProxy.Enabled {
		if allowElevation || app.networkConfig.IsServiceConnected() {
			app.configureSystemDNS()
		} else {
			log.Printf("DNS not configured (no elevation allowed and service not connected)")
		}
	}

	// When the DNS proxy is in charge, OpenVPN's pushed DNS on its TAP adapter
	// would otherwise win the metric race against our system-DNS proxy entry,
	// and Windows would route every lookup through that pushed server first
	// (often unreachable → 25 s timeouts per query). openvpnserv2 keeps
	// re-applying the pushed DNS asynchronously so a one-shot ResetDNS gets
	// overwritten. We point the TAP at our proxy instead, repeatedly for a
	// short window to outlast openvpnserv2's apply cycle.
	if app.config.DNSProxy.Enabled && profile.GetVPNType() == config.VPNTypeOpenVPN {
		go func() {
			for retry_attempt := 0; retry_attempt < 6; retry_attempt++ {
				app.networkConfig.PointActiveOpenVPNAdaptersAtProxy()
				time.Sleep(2 * time.Second)
			}
		}()
	}

	// Flush DNS cache so apps pick up new tunnel routes
	if app.config.DNSProxy.Enabled {
		system.FlushDNSCache()
	}

	// Update tray status
	app.updateTrayStatus()

	return nil
}

// connectInBackground runs the (slow) connection in a goroutine and updates state when done.
// Uses recover to ensure a panic never crashes the entire application.
func (app *App) connectInBackground(id string, profile *config.Profile, allowElevation bool) {
	defer func() {
		if panicValue := recover(); panicValue != nil {
			log.Printf("PANIC in connectInBackground for %s: %v", profile.Name, panicValue)
			app.mu.Lock()
			delete(app.connectingProfiles, id)
			app.lastConnectErrors[id] = fmt.Sprintf("internal error (panic): %v", panicValue)
			app.mu.Unlock()
		}
	}()

	// If the master switch was turned OFF while this background connect was queued,
	// abort before doing anything privileged or DNS-touching.
	if !app.IsMasterEnabled() {
		log.Printf("Background connect for %s aborted: master switch is OFF", profile.Name)
		app.mu.Lock()
		delete(app.connectingProfiles, id)
		app.mu.Unlock()
		return
	}

	connectErr := app.doConnect(id, profile, allowElevation)

	// Auto-retry: if OpenVPN failed because all TAP adapters are in use,
	// create an additional TAP adapter and retry once
	if connectErr != nil && strings.Contains(connectErr.Error(), "tap-windows") && strings.Contains(connectErr.Error(), "in use") {
		log.Printf("[%s] TAP adapters exhausted, creating additional adapter and retrying...", profile.Name)
		if tapErr := app.EnsureTAPAdapter(); tapErr != nil {
			log.Printf("[%s] Failed to create TAP adapter: %v", profile.Name, tapErr)
		} else {
			// Retry the connection with the new adapter
			connectErr = app.doConnect(id, profile, allowElevation)
		}
	}

	app.mu.Lock()
	delete(app.connectingProfiles, id)
	if connectErr != nil {
		app.lastConnectErrors[id] = connectErr.Error()
		log.Printf("Background connect failed for %s: %v", profile.Name, connectErr)
	}
	app.mu.Unlock()

	// Race-guard: if the master switch was flipped OFF while doConnect was running,
	// undo this connection so we honor the user's intent rather than leaving a stale tunnel.
	if connectErr == nil && !app.IsMasterEnabled() {
		log.Printf("Background connect for %s succeeded but master is OFF — disconnecting", profile.Name)
		if disconnect_err := app.Disconnect(id); disconnect_err != nil {
			log.Printf("[%s] Post-connect cleanup failed: %v", profile.Name, disconnect_err)
		}
	}
}

// Disconnect disconnects a profile by ID
func (app *App) Disconnect(id string) error {
	if err := app.tunnelManager.Stop(id); err != nil {
		return err
	}

	// Clear stale error so the UI status dot returns to its neutral (gray) state
	app.mu.Lock()
	delete(app.lastConnectErrors, id)
	app.mu.Unlock()

	// Restore DNS if this was the last connection.
	// Also clear DNS health flags here so we don't carry over a stale "yellow" state
	// to the next reconnect — the next health check (15 s tick) re-evaluates from scratch.
	if app.tunnelManager.GetConnectedCount() == 0 {
		app.mu.Lock()
		app.dnsHealthIssue = ""
		app.consecutiveDNSFailures = 0
		app.mu.Unlock()
		if app.networkConfig.IsTransparentDNSConfigured() {
			app.restoreSystemDNS()
		}
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

	// Clear stale errors and DNS health flags so UI status dots return to neutral
	// (gray) and the next reconnect doesn't inherit a yellow "DNS issue" indicator
	// from a previous session — the network-monitor 15 s tick will re-evaluate.
	app.mu.Lock()
	for profile_id := range app.lastConnectErrors {
		delete(app.lastConnectErrors, profile_id)
	}
	app.dnsHealthIssue = ""
	app.consecutiveDNSFailures = 0
	app.mu.Unlock()

	// Restore DNS since all VPNs are now disconnected
	if app.networkConfig.IsTransparentDNSConfigured() {
		app.restoreSystemDNS()
	}

	// Update tray status
	app.updateTrayStatus()

	return nil
}

// GetProfileConfigPath returns the full filesystem path to a profile's WireGuard config file.
func (app *App) GetProfileConfigPath(id string) string {
	return app.profileService.GetConfigFilePath(id)
}

// DeleteProfile deletes a profile by ID. If deleteConfigFile is true, the
// associated WireGuard .conf file is also removed from disk.
func (app *App) DeleteProfile(id string, deleteConfigFile bool) error {
	// Disconnect first if connected
	if app.tunnelManager.IsConnected(id) {
		app.tunnelManager.Stop(id)
	}

	// Get tunnel IP before deleting
	tunnelIP := app.profileService.GetTunnelIP(id)

	// Delete the profile
	if err := app.profileService.Delete(id, deleteConfigFile); err != nil {
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

// ImportConfig imports a VPN configuration file (WireGuard .conf or OpenVPN .ovpn)
func (app *App) ImportConfig() (*config.Profile, error) {
	return app.ImportConfigByType("")
}

// ImportConfigByType imports a VPN config file, filtering the file dialog by VPN type.
// If vpnType is empty, shows all supported formats.
func (app *App) ImportConfigByType(vpnType string) (*config.Profile, error) {
	// Build file filters based on VPN type
	var dialogTitle string
	var fileFilters []runtime.FileFilter

	switch config.VPNType(vpnType) {
	case config.VPNTypeWireGuard:
		dialogTitle = "Import WireGuard Configuration"
		fileFilters = []runtime.FileFilter{
			{DisplayName: "WireGuard Config (*.conf)", Pattern: "*.conf"},
		}
	case config.VPNTypeOpenVPN:
		dialogTitle = "Import OpenVPN Configuration"
		fileFilters = []runtime.FileFilter{
			{DisplayName: "OpenVPN Config (*.ovpn)", Pattern: "*.ovpn"},
		}
	default:
		dialogTitle = "Import VPN Configuration"
		fileFilters = []runtime.FileFilter{
			{DisplayName: "VPN Configs (*.conf, *.ovpn)", Pattern: "*.conf;*.ovpn"},
			{DisplayName: "WireGuard Config", Pattern: "*.conf"},
			{DisplayName: "OpenVPN Config", Pattern: "*.ovpn"},
		}
	}

	// Open file dialog
	selection, err := runtime.OpenFileDialog(app.ctx, runtime.OpenDialogOptions{
		Title:   dialogTitle,
		Filters: fileFilters,
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
	if app.config.Settings.AutoConfigureLoopback && app.config.TCPProxy.IsEnabled() {
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

// CreateConfigFromText creates a new profile from raw VPN config text (WireGuard format).
// For backward compatibility, defaults to WireGuard parsing.
func (app *App) CreateConfigFromText(config_name string, config_content string) (*config.Profile, error) {
	return app.CreateConfigFromTextWithType(config_name, config_content, string(config.VPNTypeWireGuard))
}

// CreateConfigFromTextWithType creates a new profile from raw VPN config text.
// vpnType should be "wireguard" or "openvpn".
func (app *App) CreateConfigFromTextWithType(config_name string, config_content string, vpnType string) (*config.Profile, error) {
	if strings.TrimSpace(config_name) == "" {
		return nil, fmt.Errorf("configuration name is required")
	}
	if strings.TrimSpace(config_content) == "" {
		return nil, fmt.Errorf("configuration content is required")
	}

	var profile *config.Profile
	var import_error error

	switch config.VPNType(vpnType) {
	case config.VPNTypeOpenVPN:
		profile, import_error = app.profileService.ImportOpenVPNFromText(config_name, config_content)
	default:
		profile, import_error = app.profileService.ImportFromText(config_name, config_content)
	}

	if import_error != nil {
		return nil, import_error
	}

	// Add loopback IP for the new profile if running as admin
	if app.config.Settings.AutoConfigureLoopback && app.config.TCPProxy.IsEnabled() {
		if tunnelIP := app.profileService.GetTunnelIP(profile.ID); tunnelIP != "" {
			if system.IsAdmin() {
				if loopback_error := app.networkConfig.EnsureLoopbackIPs([]string{tunnelIP}); loopback_error != nil {
					log.Printf("Warning: Failed to add loopback IP %s: %v", tunnelIP, loopback_error)
				} else {
					log.Printf("Added loopback IP %s for profile %s", tunnelIP, profile.Name)
				}
			}
		}
	}

	return profile, nil
}

// CreateWatchGuardProfile creates a new WatchGuard Mobile VPN with SSL profile
func (app *App) CreateWatchGuardProfile(profileName string, serverAddress string, serverPort string, username string) (*config.Profile, error) {
	if strings.TrimSpace(profileName) == "" {
		return nil, fmt.Errorf("profile name is required")
	}
	if strings.TrimSpace(serverAddress) == "" {
		return nil, fmt.Errorf("server address is required")
	}
	if serverPort == "" {
		serverPort = "443"
	}

	// Save WatchGuard config as a .wgjson file
	wgConfig := &config.WatchGuardConfig{
		ServerAddress: serverAddress,
		ServerPort:    serverPort,
		Username:      username,
	}

	configFilename, saveErr := config.SaveWatchGuardConfig(wgConfig, profileName)
	if saveErr != nil {
		return nil, saveErr
	}

	// Create profile
	profile := config.ProfileFromWatchGuardConfig(serverAddress, profileName)
	profile.ConfigFile = configFilename

	// Assign tunnel IP for transparent proxy
	tunnelIP := app.profileService.AssignTunnelIP(profile.ID)
	if tunnelIP != "" {
		if app.config.TCPProxy.TunnelIPs == nil {
			app.config.TCPProxy.TunnelIPs = make(map[string]string)
		}
		app.config.TCPProxy.TunnelIPs[profile.ID] = tunnelIP
	}

	// Add to config
	if createErr := app.profileService.Create(*profile); createErr != nil {
		return nil, createErr
	}

	// Add loopback IP for the new profile
	if app.config.Settings.AutoConfigureLoopback && app.config.TCPProxy.IsEnabled() && tunnelIP != "" {
		if system.IsAdmin() || app.networkConfig.IsServiceConnected() {
			if loopbackErr := app.networkConfig.EnsureLoopbackIPs([]string{tunnelIP}); loopbackErr != nil {
				log.Printf("Warning: Failed to add loopback IP %s: %v", tunnelIP, loopbackErr)
			}
		}
	}

	return profile, nil
}

// CreateExternalProfile creates a new External VPN profile that monitors for a network adapter
func (app *App) CreateExternalProfile(profileName string, adapterName string, adapterAutoDetect bool, dnsServer string) (*config.Profile, error) {
	if strings.TrimSpace(profileName) == "" {
		return nil, fmt.Errorf("profile name is required")
	}
	if strings.TrimSpace(adapterName) == "" && !adapterAutoDetect {
		return nil, fmt.Errorf("adapter name is required when auto-detect is disabled")
	}

	// Save external VPN config
	extConfig := &config.ExternalVPNConfig{
		AdapterName:       adapterName,
		AdapterAutoDetect: adapterAutoDetect,
		DNSServer:         dnsServer,
		PollIntervalSec:   2,
	}

	configFilename, saveErr := config.SaveExternalVPNConfig(extConfig, profileName)
	if saveErr != nil {
		return nil, saveErr
	}

	profile := config.ProfileFromExternalVPNConfig(profileName, adapterName)
	profile.ConfigFile = configFilename

	// Assign tunnel IP
	tunnelIP := app.profileService.AssignTunnelIP(profile.ID)
	if tunnelIP != "" {
		if app.config.TCPProxy.TunnelIPs == nil {
			app.config.TCPProxy.TunnelIPs = make(map[string]string)
		}
		app.config.TCPProxy.TunnelIPs[profile.ID] = tunnelIP
	}

	if createErr := app.profileService.Create(*profile); createErr != nil {
		return nil, createErr
	}

	if app.config.Settings.AutoConfigureLoopback && app.config.TCPProxy.IsEnabled() && tunnelIP != "" {
		if system.IsAdmin() || app.networkConfig.IsServiceConnected() {
			if loopbackErr := app.networkConfig.EnsureLoopbackIPs([]string{tunnelIP}); loopbackErr != nil {
				log.Printf("Warning: Failed to add loopback IP %s: %v", tunnelIP, loopbackErr)
			}
		}
	}

	return profile, nil
}

// vpnAdapterKeywords are substrings that identify likely VPN adapters
var vpnAdapterKeywords = []string{
	"tap", "tun", "wireguard", "wg", "tailscale", "cisco", "anyconnect",
	"globalprotect", "forticlient", "fortinet", "sonicwall", "watchguard",
	"openvpn", "vpn", "ipsec", "juniper", "pulse", "f5", "zscaler",
	"cloudflare warp", "mullvad", "nordlynx", "proton",
}

// GetNetworkAdapters returns all network adapters with VPN heuristic flags
func (app *App) GetNetworkAdapters() []AdapterSummary {
	adapters, fetch_err := system.GetAllAdapters()
	if fetch_err != nil {
		log.Printf("Failed to get network adapters: %v", fetch_err)
		return []AdapterSummary{}
	}

	result := make([]AdapterSummary, 0, len(adapters))
	for _, adapter := range adapters {
		lower_name := strings.ToLower(adapter.Name)
		lower_desc := strings.ToLower(adapter.Description)

		// Check if this looks like a VPN adapter
		is_vpn := false
		for _, keyword := range vpnAdapterKeywords {
			if strings.Contains(lower_name, keyword) || strings.Contains(lower_desc, keyword) {
				is_vpn = true
				break
			}
		}

		// Only show VPN adapters
		if !is_vpn {
			continue
		}

		result = append(result, AdapterSummary{
			Name:        adapter.Name,
			Description: adapter.Description,
			IPv4Addrs:   adapter.IPv4Addrs,
			DNSServers:  adapter.DNSServers,
			IsUp:        adapter.IsUp(),
			IsVPN:       true,
		})
	}

	return result
}

// UpdateProfile updates a profile
func (app *App) UpdateProfile(profile config.Profile) error {
	// Strip computed fields before saving (these are resolved from .conf at runtime)
	profile.DNS.Server = ""
	profile.HealthCheck.TargetIP = ""

	if err := app.profileService.Update(profile); err != nil {
		return err
	}

	// Rebuild DNS rules from profiles and restart DNS proxy
	app.tunnelManager.RestartDNSProxy(&app.config.DNSProxy)

	// Restart TCP proxy to pick up per-profile port changes
	if app.config.TCPProxy.IsEnabled() {
		app.tunnelManager.RestartTCPProxy(&app.config.TCPProxy)
	}

	return nil
}

// ReorderProfiles persists a new display order for profiles
func (app *App) ReorderProfiles(orderedIDs []string) error {
	return app.profileService.Reorder(orderedIDs)
}

