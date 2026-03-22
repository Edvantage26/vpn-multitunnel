package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"vpnmultitunnel/internal/api"
	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/debug"
	"vpnmultitunnel/internal/proxy"
	"vpnmultitunnel/internal/service"
	"vpnmultitunnel/internal/system"
	"vpnmultitunnel/internal/tray"
	"vpnmultitunnel/internal/tunnel"
)

// App struct
type App struct {
	ctx            context.Context
	config         *config.AppConfig
	tunnelManager  *tunnel.Manager
	profileService *service.ProfileService
	systemTray     *tray.SystemTray
	networkConfig  *system.NetworkConfig
	debugServer    *api.Server
	mu             sync.RWMutex

	// Tracks profiles currently in the process of connecting
	connectingProfiles map[string]bool

	// Cache for DNS status checks (to avoid running netstat every 2 seconds)
	dnsStatusMu          sync.RWMutex
	dnsStatusPort53Free  bool
	dnsStatusClientDown  bool
	dnsStatusCacheTime   time.Time
	dnsStatusCacheTTL    time.Duration
}

// ProfileStatus represents the status of a profile for the UI
type ProfileStatus struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ConfigFile    string `json:"configFile"`
	Connected     bool   `json:"connected"`
	Connecting    bool   `json:"connecting"`
	Healthy       bool   `json:"healthy"`
	TunnelIP      string `json:"tunnelIP"`
	BytesSent     uint64 `json:"bytesSent"`
	BytesRecv     uint64 `json:"bytesRecv"`
	LastHandshake string `json:"lastHandshake"`
	Endpoint      string `json:"endpoint"`
}

// WireGuardConfigDisplay represents WireGuard config metadata for UI display
type WireGuardConfigDisplay struct {
	Interface struct {
		Address    string `json:"address"`
		DNS        string `json:"dns"`
		PublicKey  string `json:"publicKey"`
		ListenPort int    `json:"listenPort,omitempty"`
	} `json:"interface"`
	Peer struct {
		Endpoint   string `json:"endpoint"`
		AllowedIPs string `json:"allowedIPs"`
		PublicKey  string `json:"publicKey"`
	} `json:"peer"`
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		connectingProfiles: make(map[string]bool),
		dnsStatusCacheTTL:  10 * time.Second, // Cache DNS status for 10 seconds
	}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Failed to load config: %v", err)
		cfg = config.Default()
	}
	a.config = cfg

	// Propagate DNS listen address from Settings to DNSProxy config
	if cfg.Settings.DNSListenAddress != "" {
		cfg.DNSProxy.ListenAddress = cfg.Settings.DNSListenAddress
	}

	// Initialize network configuration manager
	a.networkConfig = system.GetNetworkConfig()
	a.networkConfig.SetDNSProxyAddress(cfg.DNSProxy.GetListenAddress())
	a.networkConfig.SetDNSFallbackServer(cfg.Settings.DNSFallbackServer)

	// Try to connect to the VPN MultiTunnel service for privileged operations
	if cfg.Settings.UseService {
		if a.networkConfig.ConnectToService() {
			log.Printf("Connected to VPN MultiTunnel service - UAC prompts will be avoided")
		} else {
			log.Printf("VPN MultiTunnel service not available - will use UAC elevation when needed")
		}
	}

	// Initialize services
	a.profileService = service.NewProfileService(cfg)
	a.tunnelManager = tunnel.NewManager(cfg)

	// Set callback for dynamically configuring loopback IPs when DNS assigns new IPs
	a.tunnelManager.SetLoopbackIPCallback(func(ip string) error {
		if a.networkConfig == nil {
			return fmt.Errorf("network config not initialized")
		}
		// Check if IP already exists
		if a.networkConfig.LoopbackIPExists(ip) {
			return nil
		}
		// Try to add the IP via service or UAC elevation
		return a.networkConfig.AddLoopbackIPElevated(ip)
	})

	// Assign tunnel IPs to existing profiles that don't have one
	if err := a.AssignTunnelIPsForExistingProfiles(); err != nil {
		log.Printf("Failed to assign tunnel IPs: %v", err)
	}

	// Note: Loopback IPs are now configured on-demand when connecting a VPN
	// This avoids requiring admin at startup

	// Always use port 53 for DNS proxy when enabled
	if cfg.DNSProxy.Enabled {
		cfg.DNSProxy.ListenPort = 53
	}

	// Initialize system tray
	a.systemTray = tray.GetInstance()
	a.systemTray.Start(a)

	// Set up tray callbacks for window control
	a.systemTray.SetShowWindowFunc(func() {
		runtime.WindowShow(a.ctx)
		runtime.WindowSetAlwaysOnTop(a.ctx, true)
		runtime.WindowSetAlwaysOnTop(a.ctx, false)
	})
	a.systemTray.SetQuitFunc(func() {
		runtime.Quit(a.ctx)
	})

	// Auto-connect profiles that have autoConnect enabled (default: true)
	// Mark them as "connecting" first so the UI shows spinners immediately,
	// then run the actual connections in a goroutine so the window opens without blocking.
	var autoConnectProfiles []*config.Profile
	for idx_profile := range cfg.Profiles {
		profile := &cfg.Profiles[idx_profile]
		if !profile.Enabled || !profile.ShouldAutoConnect() {
			continue
		}
		// Check if loopback IP exists - skip if not and service unavailable
		if cfg.TCPProxy.Enabled && cfg.Settings.AutoConfigureLoopback {
			tunnelIP := a.profileService.GetTunnelIP(profile.ID)
			if tunnelIP != "" && !a.networkConfig.LoopbackIPExists(tunnelIP) && !a.networkConfig.IsServiceConnected() {
				log.Printf("Auto-connect skipped for %s: loopback IP %s not configured and service not available", profile.Name, tunnelIP)
				continue
			}
		}
		a.connectingProfiles[profile.ID] = true
		autoConnectProfiles = append(autoConnectProfiles, profile)
	}

	go func() {
		// Connect profiles sequentially
		for _, profile := range autoConnectProfiles {
			if err := a.connectInternal(profile.ID, false); err != nil {
				log.Printf("Auto-connect failed for %s: %v", profile.Name, err)
			}
			a.mu.Lock()
			delete(a.connectingProfiles, profile.ID)
			a.mu.Unlock()
		}

		// If system DNS is already pointing to our proxy (from a previous session),
		// ensure the DNS proxy is actually listening on port 53.
		// Always restart because the initial bind at startup may have failed
		// (e.g., loopback IP didn't exist yet).
		if cfg.Settings.UsePort53 && cfg.DNSProxy.Enabled && a.networkConfig.IsTransparentDNSConfigured() {
			log.Printf("System DNS already configured to proxy, ensuring DNS proxy on port 53...")
			// Ensure the loopback IP exists first (needed for binding)
			dnsAddr := cfg.DNSProxy.GetListenAddress()
			if dnsAddr != "127.0.0.1" {
				a.networkConfig.AddLoopbackIPElevated(dnsAddr)
			}
			if err := a.tunnelManager.RestartDNSProxyOnPort(53); err != nil {
				log.Printf("Warning: Failed to restart DNS proxy on port 53: %v", err)
			}
			// Ensure IPv6 DNS also points to our proxy (prevents fe80::1 from taking priority)
			if active_network_interface, get_interface_err := a.networkConfig.GetActiveNetworkInterface(); get_interface_err == nil {
				a.networkConfig.SetDNSv6(active_network_interface, []string{"::1"})
			}
			system.FlushDNSCache()
		}

		// Update tray status after auto-connect
		a.updateTrayStatus()
	}()

	// Initialize and start the debug API server
	if cfg.Settings.DebugAPIEnabled {
		port := cfg.Settings.DebugAPIPort
		if port == 0 {
			port = 8765
		}
		a.debugServer = api.NewServer(port, a)
		if err := a.debugServer.Start(); err != nil {
			log.Printf("Warning: Failed to start debug API server: %v", err)
		}
	}

	debug.Info("app", "Application started", map[string]any{
		"debugApiEnabled": cfg.Settings.DebugAPIEnabled,
		"debugApiPort":    cfg.Settings.DebugAPIPort,
	})
}

// configureLoopbackIPs sets up the required loopback IP addresses
func (a *App) configureLoopbackIPs() {
	if !system.IsAdmin() {
		log.Println("Warning: Not running as admin, cannot configure loopback IPs automatically")
		log.Println("Please run as administrator or manually add loopback IPs:")
		for _, ip := range a.config.TCPProxy.TunnelIPs {
			log.Printf("  netsh interface ipv4 add address \"Loopback Pseudo-Interface 1\" %s 255.255.255.0", ip)
		}
		return
	}

	// Collect all tunnel IPs
	var ips []string
	for _, ip := range a.config.TCPProxy.TunnelIPs {
		ips = append(ips, ip)
	}

	if len(ips) > 0 {
		if err := a.networkConfig.EnsureLoopbackIPs(ips); err != nil {
			log.Printf("Warning: Failed to configure loopback IPs: %v", err)
		}
	}
}

// shutdown is called when the app is closing
func (a *App) shutdown(ctx context.Context) {
	debug.Info("app", "Application shutting down", nil)

	// Stop debug server
	if a.debugServer != nil {
		a.debugServer.Stop()
	}

	// Restore transparent DNS if we configured it
	if a.networkConfig != nil && a.networkConfig.IsTransparentDNSConfigured() {
		if err := a.networkConfig.RestoreTransparentDNS(); err != nil {
			log.Printf("Failed to restore DNS: %v", err)
		}
		system.FlushDNSCache()
	}

	// Stop system tray
	if a.systemTray != nil {
		a.systemTray.Stop()
	}

	// Stop all tunnels
	if a.tunnelManager != nil {
		a.tunnelManager.StopAll()
	}

	// Disconnect from service
	if a.networkConfig != nil {
		a.networkConfig.DisconnectFromService()
	}
}

// GetProfiles returns all profiles with their current status
func (a *App) GetProfiles() []ProfileStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()

	profiles := a.profileService.GetAll()
	result := make([]ProfileStatus, len(profiles))

	for idx_profile, current_profile := range profiles {
		status := ProfileStatus{
			ID:         current_profile.ID,
			Name:       current_profile.Name,
			ConfigFile: current_profile.ConfigFile,
			Connected:  a.tunnelManager.IsConnected(current_profile.ID),
			Connecting: a.connectingProfiles[current_profile.ID],
			TunnelIP:   a.profileService.GetTunnelIP(current_profile.ID),
		}

		// Get stats if connected
		if status.Connected {
			if stats := a.tunnelManager.GetStats(current_profile.ID); stats != nil {
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
func (a *App) GetProfile(id string) (*config.Profile, error) {
	return a.profileService.GetByID(id)
}

// Connect connects a profile by ID (with UAC elevation if needed)
func (a *App) Connect(id string) error {
	return a.connectInternal(id, true)
}

// connectInternal connects a profile, optionally prompting for elevation
func (a *App) connectInternal(id string, allowElevation bool) error {
	profile, err := a.profileService.GetByID(id)
	if err != nil {
		return err
	}

	if a.tunnelManager.IsConnected(id) {
		return fmt.Errorf("profile %s is already connected", profile.Name)
	}

	// Ensure loopback IP exists for this profile
	if a.config.Settings.AutoConfigureLoopback && a.config.TCPProxy.Enabled {
		if tunnelIP := a.profileService.GetTunnelIP(id); tunnelIP != "" {
			if !a.networkConfig.LoopbackIPExists(tunnelIP) {
				if allowElevation || a.networkConfig.IsServiceConnected() {
					if err := a.networkConfig.AddLoopbackIPElevated(tunnelIP); err != nil {
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
	if err := a.tunnelManager.Start(profile); err != nil {
		return err
	}

	// Configure system DNS if not already configured and auto-configure is enabled
	if !a.networkConfig.IsTransparentDNSConfigured() && a.config.Settings.AutoConfigureDNS && a.config.DNSProxy.Enabled {
		// Allow DNS configure if elevation is permitted OR if the service is connected
		// (the service handles privileged ops without UAC)
		if allowElevation || a.networkConfig.IsServiceConnected() {
			a.configureSystemDNS()
		} else {
			log.Printf("DNS not configured (no elevation allowed and service not connected)")
		}
	}

	// Flush DNS cache so apps pick up new tunnel routes
	if a.config.DNSProxy.Enabled {
		system.FlushDNSCache()
	}

	// Update tray status
	a.updateTrayStatus()

	return nil
}

// configureSystemDNS sets up the system to use our DNS proxy with transparent DNS
func (a *App) configureSystemDNS() {
	// Setup transparent DNS: set system DNS to our proxy address (e.g., 127.0.0.53)
	// Using a different loopback IP avoids conflicts with Windows DNS Client
	if err := a.networkConfig.SetupTransparentDNS(); err != nil {
		log.Printf("Warning: Failed to setup transparent DNS: %v", err)
		return
	}

	// Restart DNS proxy on port 53
	if err := a.tunnelManager.RestartDNSProxyOnPort(53); err != nil {
		log.Printf("Warning: Failed to restart DNS proxy on port 53: %v", err)
		// Try to continue anyway
	}

	system.FlushDNSCache()
	dnsAddr := a.config.DNSProxy.GetListenAddress()
	log.Printf("Transparent DNS configured: DNS proxy on %s:53, system DNS = %s", dnsAddr, dnsAddr)
}

// Disconnect disconnects a profile by ID
func (a *App) Disconnect(id string) error {
	if err := a.tunnelManager.Stop(id); err != nil {
		return err
	}

	// Restore DNS if this was the last connection
	if a.tunnelManager.GetConnectedCount() == 0 && a.networkConfig.IsTransparentDNSConfigured() {
		a.restoreSystemDNS()
	}

	// Flush DNS cache so apps stop using stale tunnel routes
	if a.config.DNSProxy.Enabled {
		system.FlushDNSCache()
	}

	// Update tray status
	a.updateTrayStatus()

	return nil
}

// restoreSystemDNS restores the original DNS configuration
func (a *App) restoreSystemDNS() {
	// Restart DNS proxy on original port (10053) before restoring DNS Client
	originalPort := 10053
	if a.config.Settings.UsePort53 {
		originalPort = 53 // Keep on 53 if that was the original setting
	} else {
		originalPort = a.config.DNSProxy.ListenPort
	}

	// Only restart on different port if we're not already on original port
	currentPort := a.tunnelManager.GetDNSProxyPort()
	if currentPort == 53 && originalPort != 53 {
		if err := a.tunnelManager.RestartDNSProxyOnPort(originalPort); err != nil {
			log.Printf("Warning: Failed to restart DNS proxy on port %d: %v", originalPort, err)
		}
	}

	// Restore transparent DNS (restores original DNS settings and restarts DNS Client)
	if err := a.networkConfig.RestoreTransparentDNS(); err != nil {
		log.Printf("Failed to restore transparent DNS: %v", err)
	} else {
		log.Println("Restored original DNS configuration and restarted DNS Client")
		system.FlushDNSCache()
	}
}

// ConnectAll connects all enabled profiles
func (a *App) ConnectAll() error {
	profiles := a.profileService.GetAll()
	var lastErr error

	for _, p := range profiles {
		if p.Enabled && !a.tunnelManager.IsConnected(p.ID) {
			// Use Connect() to trigger UAC elevation for loopback IPs
			if err := a.Connect(p.ID); err != nil {
				log.Printf("Failed to connect %s: %v", p.Name, err)
				lastErr = err
			}
		}
	}

	// Update tray status (in case some connections failed)
	a.updateTrayStatus()

	return lastErr
}

// DisconnectAll disconnects all profiles
func (a *App) DisconnectAll() error {
	a.tunnelManager.StopAll()

	// Restore DNS since all VPNs are now disconnected
	if a.networkConfig.IsTransparentDNSConfigured() {
		a.restoreSystemDNS()
	}

	// Update tray status
	a.updateTrayStatus()

	return nil
}

// DeleteProfile deletes a profile by ID
func (a *App) DeleteProfile(id string) error {
	// Disconnect first if connected
	if a.tunnelManager.IsConnected(id) {
		a.tunnelManager.Stop(id)
	}

	// Get tunnel IP before deleting
	tunnelIP := a.profileService.GetTunnelIP(id)

	// Delete the profile
	if err := a.profileService.Delete(id); err != nil {
		return err
	}

	// Remove loopback IP if it was assigned
	if tunnelIP != "" && a.config.Settings.AutoConfigureLoopback {
		// Remove from config
		delete(a.config.TCPProxy.TunnelIPs, id)
		config.Save(a.config)

		// Remove from system if admin
		if system.IsAdmin() {
			if err := a.networkConfig.RemoveLoopbackIP(tunnelIP); err != nil {
				log.Printf("Warning: Failed to remove loopback IP %s: %v", tunnelIP, err)
			} else {
				log.Printf("Removed loopback IP %s", tunnelIP)
			}
		}
	}

	return nil
}

// ImportConfig imports a WireGuard configuration file
func (a *App) ImportConfig() (*config.Profile, error) {
	// Open file dialog
	selection, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
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
	profile, err := a.profileService.Import(selection)
	if err != nil {
		return nil, err
	}

	// Add loopback IP for the new profile if running as admin
	if a.config.Settings.AutoConfigureLoopback && a.config.TCPProxy.Enabled {
		if tunnelIP := a.profileService.GetTunnelIP(profile.ID); tunnelIP != "" {
			if system.IsAdmin() {
				if err := a.networkConfig.EnsureLoopbackIPs([]string{tunnelIP}); err != nil {
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
func (a *App) UpdateProfile(profile config.Profile) error {
	if err := a.profileService.Update(profile); err != nil {
		return err
	}

	// Sync DNS settings to global DNS rules
	a.syncProfileDNSToRules(profile)

	// Restart TCP proxy to pick up per-profile port changes
	if a.config.TCPProxy.Enabled {
		a.tunnelManager.RestartTCPProxy(&a.config.TCPProxy)
	}

	return nil
}

// ReorderProfiles persists a new display order for profiles
func (a *App) ReorderProfiles(orderedIDs []string) error {
	return a.profileService.Reorder(orderedIDs)
}


// syncProfileDNSToRules syncs a profile's DNS settings to the global DNS proxy rules
func (a *App) syncProfileDNSToRules(profile config.Profile) {
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
		for i := range a.config.DNSProxy.Rules {
			if a.config.DNSProxy.Rules[i].Suffix == suffix && a.config.DNSProxy.Rules[i].ProfileID == profile.ID {
				// Update existing rule
				a.config.DNSProxy.Rules[i].DNSServer = profile.DNS.Server
				a.config.DNSProxy.Rules[i].Hosts = profile.DNS.Hosts
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
			a.config.DNSProxy.Rules = append(a.config.DNSProxy.Rules, newRule)
		}
	}

	// Enable DNS proxy if not already
	if !a.config.DNSProxy.Enabled && len(a.config.DNSProxy.Rules) > 0 {
		a.config.DNSProxy.Enabled = true
	}

	// Save and restart DNS proxy
	config.Save(a.config)
	a.tunnelManager.RestartDNSProxy(&a.config.DNSProxy)
}

// GetSettings returns the current settings
func (a *App) GetSettings() config.Settings {
	return a.config.Settings
}

// UpdateSettingsResult describes the side effects that occurred when updating settings
type UpdateSettingsResult struct {
	DNSProxyRestarted     bool   `json:"dnsProxyRestarted"`
	SystemDNSReconfigured bool   `json:"systemDNSReconfigured"`
	LoopbackIPChanged     bool   `json:"loopbackIPChanged"`
	Warning               string `json:"warning,omitempty"`
}

// UpdateSettings updates the settings and handles all DNS side effects
func (a *App) UpdateSettings(settings config.Settings) (UpdateSettingsResult, error) {
	result := UpdateSettingsResult{}

	previous_dns_address := a.config.Settings.DNSListenAddress
	previous_fallback := a.config.Settings.DNSFallbackServer
	dns_was_active := a.networkConfig.IsTransparentDNSConfigured()

	a.config.Settings = settings

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
			a.restoreSystemDNS()

			// Remove old loopback IP
			if previous_dns_address != "" && previous_dns_address != "127.0.0.1" {
				if err := a.networkConfig.RemoveLoopbackIPElevated(previous_dns_address); err != nil {
					log.Printf("Warning: could not remove old loopback IP %s: %v", previous_dns_address, err)
				}
			}
		}

		// Create new loopback IP
		if err := a.networkConfig.AddLoopbackIPElevated(settings.DNSListenAddress); err != nil {
			log.Printf("Warning: could not add loopback IP %s: %v", settings.DNSListenAddress, err)
			result.Warning = fmt.Sprintf("Failed to create loopback IP %s: %v", settings.DNSListenAddress, err)
		}

		// Update DNS proxy config to use new address
		a.config.DNSProxy.ListenAddress = settings.DNSListenAddress
		a.networkConfig.SetDNSProxyAddress(settings.DNSListenAddress)

		// Restart DNS proxy on new address
		a.tunnelManager.RestartDNSProxy(&a.config.DNSProxy)
		result.DNSProxyRestarted = true

		if dns_was_active {
			// Reconfigure system DNS to point to new address
			log.Printf("Reconfiguring system DNS to new address %s...", settings.DNSListenAddress)
			a.configureSystemDNS()
			result.SystemDNSReconfigured = true
		}
	}

	// Handle fallback DNS change
	if fallback_changed {
		log.Printf("Fallback DNS changed from %s to %s", previous_fallback, settings.DNSFallbackServer)
		a.networkConfig.SetDNSFallbackServer(settings.DNSFallbackServer)

		// Restart DNS proxy to pick up new fallback (only if we didn't already restart above)
		if !result.DNSProxyRestarted {
			a.tunnelManager.RestartDNSProxy(&a.config.DNSProxy)
			result.DNSProxyRestarted = true
		}
	}

	if err := config.Save(a.config); err != nil {
		return result, err
	}

	return result, nil
}

// DNSTestResult contains the result of a DNS connectivity test
type DNSTestResult struct {
	ProxyListening     bool   `json:"proxyListening"`
	SystemDNSConfigured bool  `json:"systemDNSConfigured"`
	QuerySuccess       bool   `json:"querySuccess"`
	ResolvedIP         string `json:"resolvedIP"`
	Error              string `json:"error,omitempty"`
}

// TestDNSConnectivity tests DNS proxy connectivity on the given address
func (a *App) TestDNSConnectivity(dns_address string) DNSTestResult {
	result := DNSTestResult{}

	// Check if proxy is listening on the address:53
	test_addr := fmt.Sprintf("%s:53", dns_address)
	test_conn, dial_err := net.DialTimeout("udp", test_addr, 1*time.Second)
	if dial_err == nil {
		test_conn.Close()
	}

	// Send a real DNS query to check if proxy responds
	dns_client := &dns.Client{
		Net:     "udp",
		Timeout: 3 * time.Second,
	}
	dns_query := new(dns.Msg)
	dns_query.SetQuestion(dns.Fqdn("google.com"), dns.TypeA)
	dns_response, _, query_err := dns_client.Exchange(dns_query, test_addr)
	if query_err == nil && dns_response != nil && dns_response.Rcode == dns.RcodeSuccess {
		result.ProxyListening = true
		result.QuerySuccess = true
		// Extract first A record
		for _, answer_record := range dns_response.Answer {
			if a_record, is_a_record := answer_record.(*dns.A); is_a_record {
				result.ResolvedIP = a_record.A.String()
				break
			}
		}
	} else if query_err != nil {
		result.Error = query_err.Error()
	} else if dns_response != nil {
		result.ProxyListening = true
		result.Error = fmt.Sprintf("DNS response code: %d", dns_response.Rcode)
	}

	// Check if system DNS is configured to use this address
	result.SystemDNSConfigured = a.networkConfig.IsTransparentDNSConfigured()

	return result
}

// GetDNSProxyConfig returns the DNS proxy configuration
func (a *App) GetDNSProxyConfig() config.DNSProxy {
	return a.config.DNSProxy
}

// UpdateDNSProxyConfig updates the DNS proxy configuration
func (a *App) UpdateDNSProxyConfig(dnsConfig config.DNSProxy) error {
	a.config.DNSProxy = dnsConfig
	if err := config.Save(a.config); err != nil {
		return err
	}

	// Restart DNS proxy with new config
	a.tunnelManager.RestartDNSProxy(&dnsConfig)
	return nil
}

// CopyEnvVars generates and copies environment variables to clipboard
func (a *App) CopyEnvVars() error {
	var envVars []string

	profiles := a.profileService.GetAll()
	for _, p := range profiles {
		if !a.tunnelManager.IsConnected(p.ID) {
			continue
		}

	}

	if len(envVars) == 0 {
		return fmt.Errorf("no active connections")
	}

	text := strings.Join(envVars, "\n")
	return runtime.ClipboardSetText(a.ctx, text)
}

// GetConfigDir returns the configuration directory path
func (a *App) GetConfigDir() (string, error) {
	return config.GetConfigDir()
}

// GetAppPath returns the directory where the application is running from
func (a *App) GetAppPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	return filepath.Dir(execPath)
}

// TestConnection tests connectivity through a tunnel
func (a *App) TestConnection(profileID, targetHost string, targetPort int) (bool, string) {
	t := a.tunnelManager.GetTunnel(profileID)
	if t == nil {
		return false, "Tunnel not connected"
	}

	addr := fmt.Sprintf("%s:%d", targetHost, targetPort)

	// Measure connection time
	start := time.Now()
	conn, err := t.Dial("tcp", addr)
	elapsed := time.Since(start)

	if err != nil {
		return false, fmt.Sprintf("Connection failed: %v", err)
	}
	conn.Close()

	return true, fmt.Sprintf("Connected to %s in %dms", addr, elapsed.Milliseconds())
}

// GetTunnelDebugInfo returns debug information for a tunnel
func (a *App) GetTunnelDebugInfo(profileID string) string {
	t := a.tunnelManager.GetTunnel(profileID)
	if t == nil {
		return "Tunnel not found"
	}
	return t.GetDebugInfo()
}

// GetTCPProxyConfig returns the TCP proxy configuration
func (a *App) GetTCPProxyConfig() config.TCPProxy {
	return a.config.TCPProxy
}

// UpdateTCPProxyConfig updates the TCP proxy configuration
func (a *App) UpdateTCPProxyConfig(tcpConfig config.TCPProxy) error {
	a.config.TCPProxy = tcpConfig
	if err := config.Save(a.config); err != nil {
		return err
	}

	// Restart TCP proxy with new config
	a.tunnelManager.RestartTCPProxy(&tcpConfig)
	return nil
}

// GetActiveConnections returns active transparent proxy connections
func (a *App) GetActiveConnections() []proxy.ActiveConnection {
	return a.tunnelManager.GetActiveConnections()
}

// GetTunnelIPs returns the tunnel IPs for all profiles
func (a *App) GetTunnelIPs() map[string]string {
	return a.config.TCPProxy.TunnelIPs
}

// IsTCPProxyEnabled returns whether the TCP proxy is enabled
func (a *App) IsTCPProxyEnabled() bool {
	return a.tunnelManager.IsTCPProxyEnabled()
}

// GetTCPProxyListenerCount returns the number of TCP proxy listeners
func (a *App) GetTCPProxyListenerCount() int {
	return a.tunnelManager.GetTCPProxyListenerCount()
}

// IsRunningAsAdmin returns whether the app is running with admin privileges
func (a *App) IsRunningAsAdmin() bool {
	return system.IsAdmin()
}

// IsDNSConfigured returns whether the system DNS has been configured by us
func (a *App) IsDNSConfigured() bool {
	if a.networkConfig == nil {
		return false
	}
	return a.networkConfig.IsDNSConfigured()
}

// RestoreDNS restores the original DNS configuration
func (a *App) RestoreDNS() error {
	if a.networkConfig == nil {
		return fmt.Errorf("network config not initialized")
	}

	log.Printf("Restoring DNS configuration...")
	if err := a.networkConfig.RestoreSystemDNS(); err != nil {
		return fmt.Errorf("failed to restore DNS: %w", err)
	}

	// Also restart DNS Client service if we stopped it
	if a.networkConfig.IsDNSClientStopped() {
		if err := system.StartDNSClientService(); err != nil {
			log.Printf("Warning: failed to restart DNS Client service: %v", err)
		}
	}

	return nil
}

// ConfigureDNS manually configures DNS to use our proxy
func (a *App) ConfigureDNS() debug.DNSConfigResult {
	result := debug.DNSConfigResult{}

	if a.networkConfig == nil {
		result.Error = "network config not initialized"
		return result
	}

	// Get the DNS proxy listen address (default: 127.0.0.53)
	dnsAddress := a.config.DNSProxy.GetListenAddress()
	result.DNSAddress = dnsAddress

	log.Printf("Manually configuring DNS to %s...", dnsAddress)
	if err := a.networkConfig.ConfigureSystemDNS(dnsAddress); err != nil {
		result.Error = fmt.Sprintf("failed to configure DNS: %v", err)
		return result
	}

	// Wait a moment for the DNS Client service to stop
	time.Sleep(500 * time.Millisecond)

	// Restart DNS proxy on port 53 so it actually listens
	if err := a.tunnelManager.RestartDNSProxyOnPort(53); err != nil {
		log.Printf("Warning: Failed to restart DNS proxy on port 53: %v", err)
	}

	system.FlushDNSCache()

	// Force refresh the cache and check status
	a.refreshDNSStatusCache()
	a.dnsStatusMu.RLock()
	result.Port53Free = a.dnsStatusPort53Free
	result.DNSClientDown = a.dnsStatusClientDown
	a.dnsStatusMu.RUnlock()
	result.Success = true

	return result
}

// refreshDNSStatusCache updates the cached DNS status values
func (a *App) refreshDNSStatusCache() {
	a.dnsStatusMu.Lock()
	defer a.dnsStatusMu.Unlock()

	// Check port 53
	cmd := exec.Command("netstat", "-ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.Output()
	if err != nil {
		a.dnsStatusPort53Free = true
	} else {
		a.dnsStatusPort53Free = true
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "UDP") && strings.Contains(line, "0.0.0.0:53 ") {
				a.dnsStatusPort53Free = false
				break
			}
		}
	}

	// Check DNS Client service
	cmd = exec.Command("sc", "query", "Dnscache")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err = cmd.Output()
	if err != nil {
		a.dnsStatusClientDown = true
	} else {
		a.dnsStatusClientDown = !strings.Contains(string(output), "RUNNING")
	}

	a.dnsStatusCacheTime = time.Now()
}

// invalidateDNSStatusCache forces the next status check to refresh
func (a *App) invalidateDNSStatusCache() {
	a.dnsStatusMu.Lock()
	defer a.dnsStatusMu.Unlock()
	a.dnsStatusCacheTime = time.Time{} // Zero time forces refresh
}

// isPort53Free checks if UDP port 53 is available (cached)
func (a *App) isPort53Free() bool {
	a.dnsStatusMu.RLock()
	if time.Since(a.dnsStatusCacheTime) < a.dnsStatusCacheTTL {
		result := a.dnsStatusPort53Free
		a.dnsStatusMu.RUnlock()
		return result
	}
	a.dnsStatusMu.RUnlock()

	// Cache expired, refresh
	a.refreshDNSStatusCache()

	a.dnsStatusMu.RLock()
	defer a.dnsStatusMu.RUnlock()
	return a.dnsStatusPort53Free
}

// isDNSClientRunning checks if Windows DNS Client service is running (cached)
func (a *App) isDNSClientRunning() bool {
	a.dnsStatusMu.RLock()
	if time.Since(a.dnsStatusCacheTime) < a.dnsStatusCacheTTL {
		result := !a.dnsStatusClientDown
		a.dnsStatusMu.RUnlock()
		return result
	}
	a.dnsStatusMu.RUnlock()

	// Cache expired, refresh
	a.refreshDNSStatusCache()

	a.dnsStatusMu.RLock()
	defer a.dnsStatusMu.RUnlock()
	return !a.dnsStatusClientDown
}

// GetSystemStatus returns the current system configuration status
func (a *App) GetSystemStatus() map[string]interface{} {
	isAdmin := system.IsAdmin()
	dnsConfigured := false
	if a.networkConfig != nil {
		// Use IsTransparentDNSConfigured which checks actual system DNS state
		dnsConfigured = a.networkConfig.IsTransparentDNSConfigured()
	}

	// Get current system DNS
	currentDNS := ""
	if a.networkConfig != nil {
		interfaceName, err := a.networkConfig.GetActiveNetworkInterface()
		if err == nil {
			dnsServers, err := a.networkConfig.GetCurrentDNS(interfaceName)
			if err == nil && len(dnsServers) > 0 {
				currentDNS = dnsServers[0]
			}
		}
	}

	// Check if service is connected
	serviceConnected := false
	if a.networkConfig != nil {
		serviceConnected = a.networkConfig.IsServiceConnected()
	}

	return map[string]interface{}{
		"isAdmin":               isAdmin,
		"serviceConnected":      serviceConnected,
		"dnsConfigured":         dnsConfigured,
		"currentDNS":            currentDNS,
		"dnsProxyAddress":       a.config.DNSProxy.GetListenAddress(),
		"port53Free":            a.isPort53Free(),
		"dnsClientRunning":      a.isDNSClientRunning(),
		"autoConfigureLoopback": a.config.Settings.AutoConfigureLoopback,
		"autoConfigureDNS":      a.config.Settings.AutoConfigureDNS,
		"usePort53":             a.config.Settings.UsePort53,
		"useService":            a.config.Settings.UseService,
		"tcpProxyEnabled":       a.config.TCPProxy.Enabled,
		"dnsProxyEnabled":       a.config.DNSProxy.Enabled,
		"dnsProxyPort":          a.config.DNSProxy.ListenPort,
	}
}

// IsServiceConnected returns whether the VPN MultiTunnel service is connected
func (a *App) IsServiceConnected() bool {
	if a.networkConfig == nil {
		return false
	}
	return a.networkConfig.IsServiceConnected()
}

// AssignTunnelIPsForExistingProfiles assigns tunnel IPs to profiles that don't have one
func (a *App) AssignTunnelIPsForExistingProfiles() error {
	profiles := a.profileService.GetAll()
	changed := false

	for _, p := range profiles {
		if a.config.TCPProxy.TunnelIPs == nil {
			a.config.TCPProxy.TunnelIPs = make(map[string]string)
		}
		if _, exists := a.config.TCPProxy.TunnelIPs[p.ID]; !exists {
			// Profile doesn't have a tunnel IP, assign one
			tunnelIP := a.findNextTunnelIP()
			if tunnelIP != "" {
				a.config.TCPProxy.TunnelIPs[p.ID] = tunnelIP
				changed = true
			}
		}
	}

	if changed {
		return config.Save(a.config)
	}
	return nil
}

// findNextTunnelIP finds the next available tunnel IP
func (a *App) findNextTunnelIP() string {
	used := make(map[int]bool)
	for _, ip := range a.config.TCPProxy.TunnelIPs {
		parts := strings.Split(ip, ".")
		if len(parts) == 4 && parts[0] == "127" && parts[1] == "0" {
			var octect int
			fmt.Sscanf(parts[2], "%d", &octect)
			used[octect] = true
		}
	}

	for i := 1; i < 255; i++ {
		if !used[i] {
			return fmt.Sprintf("127.0.%d.1", i)
		}
	}
	return ""
}

// updateTrayStatus updates the system tray tooltip with current VPN status
func (a *App) updateTrayStatus() {
	if a.systemTray == nil {
		return
	}

	profiles := a.profileService.GetAll()
	statuses := make([]tray.VPNStatus, len(profiles))

	for i, p := range profiles {
		statuses[i] = tray.VPNStatus{
			Name:      p.Name,
			Connected: a.tunnelManager.IsConnected(p.ID),
		}
	}

	a.systemTray.UpdateVPNStatus(statuses)
}

// GetWireGuardConfig returns parsed WireGuard config metadata for UI display
func (a *App) GetWireGuardConfig(profileID string) (*WireGuardConfigDisplay, error) {
	profile, err := a.profileService.GetByID(profileID)
	if err != nil {
		return nil, err
	}

	// Get the config file path
	configPath, err := config.GetConfigFilePath(profile.ConfigFile)
	if err != nil {
		return nil, err
	}

	// Parse the WireGuard config
	wgConfig, err := config.ParseWireGuardConfig(configPath)
	if err != nil {
		return nil, err
	}

	// Build the display struct
	display := &WireGuardConfigDisplay{}

	// Interface section
	if len(wgConfig.Interface.Address) > 0 {
		display.Interface.Address = strings.Join(wgConfig.Interface.Address, ", ")
	}
	if len(wgConfig.Interface.DNS) > 0 {
		display.Interface.DNS = strings.Join(wgConfig.Interface.DNS, ", ")
	}
	display.Interface.ListenPort = wgConfig.Interface.ListenPort
	// Note: We don't expose the private key, but we could derive the public key
	// For now, we'll leave publicKey empty as deriving it requires crypto operations

	// Peer section (first peer only for display)
	if len(wgConfig.Peers) > 0 {
		peer := wgConfig.Peers[0]
		display.Peer.Endpoint = peer.Endpoint
		display.Peer.AllowedIPs = strings.Join(peer.AllowedIPs, ", ")
		display.Peer.PublicKey = peer.PublicKey
	}

	return display, nil
}

// GetConfigFileContent returns the raw content of a WireGuard config file
func (a *App) GetConfigFileContent(profileID string) (string, error) {
	profile, err := a.profileService.GetByID(profileID)
	if err != nil {
		return "", err
	}

	configPath, err := config.GetConfigFilePath(profile.ConfigFile)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read config file: %w", err)
	}

	return string(data), nil
}

// SaveConfigFileContent saves the raw content to a WireGuard config file
func (a *App) SaveConfigFileContent(profileID string, content string) error {
	profile, err := a.profileService.GetByID(profileID)
	if err != nil {
		return err
	}

	configPath, err := config.GetConfigFilePath(profile.ConfigFile)
	if err != nil {
		return err
	}

	// Validate the config by parsing it
	// Create a temp file with the content
	tmpFile, err := os.CreateTemp("", "wg-*.conf")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Try to parse it to validate
	if _, err := config.ParseWireGuardConfig(tmpFile.Name()); err != nil {
		return fmt.Errorf("invalid WireGuard config: %w", err)
	}

	// Write the content to the actual file
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}

	return nil
}

// =============================================================================
// DebugProvider Interface Implementation
// =============================================================================

// GetVPNStatusList returns detailed status for all VPN tunnels
func (a *App) GetVPNStatusList() []debug.VPNStatusInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()

	profiles := a.profileService.GetAll()
	result := make([]debug.VPNStatusInfo, 0, len(profiles))

	for _, p := range profiles {
		status := debug.VPNStatusInfo{
			ProfileID:   p.ID,
			ProfileName: p.Name,
			Connected:   a.tunnelManager.IsConnected(p.ID),
			TunnelIP:    a.profileService.GetTunnelIP(p.ID),
		}

		if status.Connected {
			if stats := a.tunnelManager.GetStats(p.ID); stats != nil {
				status.BytesSent = stats.BytesSent
				status.BytesRecv = stats.BytesRecv
				status.Endpoint = stats.Endpoint
				status.Healthy = stats.Connected
			}

			// Get latency metrics
			if tm := debug.GetMetricsCollector().GetTunnelMetrics(p.ID); tm != nil {
				status.AvgLatencyMs = tm.AvgLatencyMs
			}
		}

		result = append(result, status)
	}

	return result
}

// GetProfileNames returns a map of profile IDs to names
func (a *App) GetProfileNames() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	profiles := a.profileService.GetAll()
	result := make(map[string]string, len(profiles))
	for _, p := range profiles {
		result[p.ID] = p.Name
	}
	return result
}

// GetHostMappings returns all active host mappings
func (a *App) GetHostMappings() []debug.HostMappingInfo {
	mappings := a.tunnelManager.GetHostMappings()
	profileNames := a.GetProfileNames()

	result := make([]debug.HostMappingInfo, 0, len(mappings))
	for _, m := range mappings {
		info := debug.HostMappingInfo{
			Hostname:    m.Hostname,
			RealIP:      m.RealIP,
			LoopbackIP:  m.TunnelIP,
			ProfileID:   m.ProfileID,
			ProfileName: profileNames[m.ProfileID],
			ResolvedAt:  m.ResolvedAt,
			ExpiresAt:   m.ResolvedAt.Add(30 * time.Minute), // Default TTL
		}
		result = append(result, info)
	}
	return result
}

// GetDNSConfig returns the DNS proxy configuration
func (a *App) GetDNSConfig() debug.DNSConfigInfo {
	profileNames := a.GetProfileNames()

	rules := make([]debug.DNSRuleInfo, 0, len(a.config.DNSProxy.Rules))
	for _, r := range a.config.DNSProxy.Rules {
		stripSuffix := true
		if r.StripSuffix != nil {
			stripSuffix = *r.StripSuffix
		}
		rules = append(rules, debug.DNSRuleInfo{
			Suffix:      r.Suffix,
			ProfileID:   r.ProfileID,
			ProfileName: profileNames[r.ProfileID],
			DNSServer:   r.DNSServer,
			StripSuffix: stripSuffix,
			Hosts:       r.Hosts,
		})
	}

	return debug.DNSConfigInfo{
		Enabled:    a.config.DNSProxy.Enabled,
		ListenPort: a.config.DNSProxy.ListenPort,
		Rules:      rules,
		Fallback:   a.config.DNSProxy.Fallback,
	}
}

// DiagnoseDNS diagnoses why a hostname might not resolve correctly
func (a *App) DiagnoseDNS(hostname string) debug.DNSDiagnostic {
	hostname = strings.ToLower(hostname)
	dnsConfig := a.GetDNSConfig()

	diagnostic := debug.DNSDiagnostic{
		Hostname: hostname,
		AllRules: dnsConfig.Rules,
	}

	// Check if DNS proxy is enabled
	if !dnsConfig.Enabled {
		diagnostic.WouldResolve = false
		diagnostic.Reason = "DNS proxy is disabled"
		diagnostic.SuggestedFix = "Enable DNS proxy in settings"
		return diagnostic
	}

	// Find matching rule
	matchedRule := a.GetMatchingRule(hostname)
	diagnostic.MatchedRule = matchedRule

	if matchedRule == nil {
		diagnostic.WouldResolve = true
		diagnostic.Reason = fmt.Sprintf("No rule matches '%s', will use fallback DNS (%s)", hostname, dnsConfig.Fallback)
		return diagnostic
	}

	// Check if the profile is connected
	if !a.tunnelManager.IsConnected(matchedRule.ProfileID) {
		diagnostic.WouldResolve = false
		diagnostic.Reason = fmt.Sprintf("Rule '%s' matches, but profile '%s' is not connected", matchedRule.Suffix, matchedRule.ProfileName)
		diagnostic.SuggestedFix = fmt.Sprintf("Connect the '%s' VPN profile", matchedRule.ProfileName)
		return diagnostic
	}

	// Check static hosts
	if matchedRule.Hosts != nil {
		queryDomain := hostname
		if matchedRule.StripSuffix {
			queryDomain = strings.TrimSuffix(hostname, matchedRule.Suffix)
			queryDomain = strings.TrimSuffix(queryDomain, ".")
		}
		if ip, exists := matchedRule.Hosts[queryDomain]; exists {
			diagnostic.WouldResolve = true
			diagnostic.Reason = fmt.Sprintf("Static host mapping: %s -> %s", queryDomain, ip)
			return diagnostic
		}
	}

	diagnostic.WouldResolve = true
	diagnostic.Reason = fmt.Sprintf("Will resolve via tunnel DNS server %s (profile: %s)", matchedRule.DNSServer, matchedRule.ProfileName)
	return diagnostic
}

// QueryDNS performs a DNS query through a VPN tunnel
func (a *App) QueryDNS(hostname string, queryType string, dnsServer string, profileID string) debug.DNSQueryResult {
	start := time.Now()
	profileNames := a.GetProfileNames()

	result := debug.DNSQueryResult{
		Hostname:    hostname,
		QueryType:   queryType,
		DNSServer:   dnsServer,
		ProfileID:   profileID,
		ProfileName: profileNames[profileID],
	}

	// Get tunnel
	tunnel := a.tunnelManager.GetTunnel(profileID)
	if tunnel == nil {
		result.Error = fmt.Sprintf("tunnel not connected for profile: %s", profileID)
		return result
	}

	// If no DNS server specified, try to find from config
	if dnsServer == "" {
		for _, p := range a.config.Profiles {
			if p.ID == profileID && p.DNS.Server != "" {
				dnsServer = p.DNS.Server
				break
			}
		}
		if dnsServer == "" {
			result.Error = "no DNS server specified and none configured for profile"
			return result
		}
		result.DNSServer = dnsServer
	}

	// Parse query type
	var qtype uint16
	switch strings.ToUpper(queryType) {
	case "A":
		qtype = dns.TypeA
	case "AAAA":
		qtype = dns.TypeAAAA
	case "CNAME":
		qtype = dns.TypeCNAME
	case "MX":
		qtype = dns.TypeMX
	case "TXT":
		qtype = dns.TypeTXT
	case "NS":
		qtype = dns.TypeNS
	case "SOA":
		qtype = dns.TypeSOA
	case "PTR":
		qtype = dns.TypePTR
	case "ANY":
		qtype = dns.TypeANY
	default:
		qtype = dns.TypeA
		result.QueryType = "A"
	}

	// Create DNS query
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(hostname), qtype)
	m.RecursionDesired = true

	// Connect through tunnel
	conn, err := tunnel.Dial("udp", dnsServer+":53")
	if err != nil {
		result.Error = fmt.Sprintf("failed to connect to DNS server: %v", err)
		return result
	}
	defer conn.Close()

	// Create DNS connection and send query
	dnsConn := &dns.Conn{Conn: conn}
	if err := dnsConn.WriteMsg(m); err != nil {
		result.Error = fmt.Sprintf("failed to send DNS query: %v", err)
		return result
	}

	// Set read deadline
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// Read response
	response, err := dnsConn.ReadMsg()
	if err != nil {
		result.Error = fmt.Sprintf("failed to read DNS response: %v", err)
		return result
	}

	result.LatencyMs = time.Since(start).Milliseconds()
	result.Rcode = response.Rcode
	result.RcodeName = dns.RcodeToString[response.Rcode]
	result.Success = response.Rcode == dns.RcodeSuccess

	// Parse records
	result.Records = make([]debug.DNSRecord, 0)
	for _, answer := range response.Answer {
		record := debug.DNSRecord{
			Name: answer.Header().Name,
			TTL:  answer.Header().Ttl,
		}

		switch rr := answer.(type) {
		case *dns.A:
			record.Type = "A"
			record.Value = rr.A.String()
		case *dns.AAAA:
			record.Type = "AAAA"
			record.Value = rr.AAAA.String()
		case *dns.CNAME:
			record.Type = "CNAME"
			record.Value = rr.Target
		case *dns.MX:
			record.Type = "MX"
			record.Value = fmt.Sprintf("%d %s", rr.Preference, rr.Mx)
		case *dns.TXT:
			record.Type = "TXT"
			record.Value = strings.Join(rr.Txt, " ")
		case *dns.NS:
			record.Type = "NS"
			record.Value = rr.Ns
		case *dns.SOA:
			record.Type = "SOA"
			record.Value = fmt.Sprintf("%s %s", rr.Ns, rr.Mbox)
		case *dns.PTR:
			record.Type = "PTR"
			record.Value = rr.Ptr
		default:
			record.Type = dns.TypeToString[answer.Header().Rrtype]
			record.Value = answer.String()
		}

		result.Records = append(result.Records, record)
	}

	return result
}

// GetMatchingRule finds the DNS rule that matches a hostname
func (a *App) GetMatchingRule(hostname string) *debug.DNSRuleInfo {
	hostname = strings.ToLower(hostname)
	profileNames := a.GetProfileNames()

	for _, r := range a.config.DNSProxy.Rules {
		suffix := strings.ToLower(r.Suffix)
		if strings.HasSuffix(hostname, suffix) || hostname == strings.TrimPrefix(suffix, ".") {
			stripSuffix := true
			if r.StripSuffix != nil {
				stripSuffix = *r.StripSuffix
			}
			return &debug.DNSRuleInfo{
				Suffix:      r.Suffix,
				ProfileID:   r.ProfileID,
				ProfileName: profileNames[r.ProfileID],
				DNSServer:   r.DNSServer,
				StripSuffix: stripSuffix,
				Hosts:       r.Hosts,
			}
		}
	}
	return nil
}

// GetTCPProxyInfo returns TCP proxy configuration and status
func (a *App) GetTCPProxyInfo() debug.TCPProxyInfo {
	return debug.TCPProxyInfo{
		Enabled:       a.config.TCPProxy.Enabled,
		ListenerCount: a.tunnelManager.GetTCPProxyListenerCount(),
		TunnelIPs:     a.config.TCPProxy.TunnelIPs,
	}
}

// TestHost performs a complete test of a host (DNS + TCP connectivity)
// If useSystemDNS is true, it resolves via the system DNS (same path as real apps like DBeaver)
func (a *App) TestHost(hostname string, port int, profileID string, useSystemDNS bool) debug.HostTestResult {
	result := debug.HostTestResult{
		Hostname:      hostname,
		TCPPort:       port,
		UsedSystemDNS: useSystemDNS,
	}

	// If using system DNS, resolve through the OS (which should use our DNS proxy)
	if useSystemDNS {
		ips, err := net.LookupHost(hostname)
		if err != nil {
			result.DNSError = fmt.Sprintf("System DNS resolution failed: %v", err)
			return result
		}
		if len(ips) > 0 {
			result.RealIP = ips[0]
			result.DNSResolved = true
			result.DNSServer = "system"

			// Try TCP connection through system (normal network stack)
			addr := net.JoinHostPort(result.RealIP, fmt.Sprintf("%d", port))
			start := time.Now()
			conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
			elapsed := time.Since(start)

			if err != nil {
				result.TCPError = err.Error()
			} else {
				conn.Close()
				result.TCPConnected = true
				result.TCPLatencyMs = elapsed.Milliseconds()
			}
		}
		return result
	}

	// Original behavior: resolve via tunnel directly
	// Find the matching rule if profileID not specified
	if profileID == "" {
		rule := a.GetMatchingRule(hostname)
		if rule != nil {
			profileID = rule.ProfileID
			result.DNSRule = rule.Suffix
		}
	}

	if profileID == "" {
		result.DNSError = "No DNS rule matches this hostname and no profileId specified"
		return result
	}

	// Get profile info
	profile, err := a.profileService.GetByID(profileID)
	if err != nil {
		result.DNSError = fmt.Sprintf("Profile not found: %s", profileID)
		return result
	}
	result.ProfileID = profileID
	result.ProfileName = profile.Name

	// Check if tunnel is connected
	if !a.tunnelManager.IsConnected(profileID) {
		result.DNSError = "Tunnel not connected"
		return result
	}

	// Get the tunnel
	t := a.tunnelManager.GetTunnel(profileID)
	if t == nil {
		result.DNSError = "Tunnel not available"
		return result
	}

	// Find the DNS rule for this profile
	var dnsServer string
	var staticHosts map[string]string
	var stripSuffix bool = true
	var ruleSuffix string
	for _, r := range a.config.DNSProxy.Rules {
		if r.ProfileID == profileID {
			dnsServer = r.DNSServer
			staticHosts = r.Hosts
			if r.StripSuffix != nil {
				stripSuffix = *r.StripSuffix
			}
			ruleSuffix = r.Suffix
			result.DNSServer = dnsServer
			result.DNSRule = r.Suffix
			break
		}
	}

	// Check host mappings cache first
	mappings := a.tunnelManager.GetHostMappings()
	for _, m := range mappings {
		if m.Hostname == hostname {
			result.RealIP = m.RealIP
			result.LoopbackIP = m.TunnelIP
			result.DNSResolved = true
			break
		}
	}

	// Check static hosts mapping if not found in cache
	if !result.DNSResolved && staticHosts != nil {
		queryDomain := strings.ToLower(hostname)
		if stripSuffix && ruleSuffix != "" {
			suffix := strings.ToLower(ruleSuffix)
			if !strings.HasPrefix(suffix, ".") {
				suffix = "." + suffix
			}
			if strings.HasSuffix(queryDomain, suffix) {
				queryDomain = queryDomain[:len(queryDomain)-len(suffix)]
			}
		}
		if staticIP, exists := staticHosts[queryDomain]; exists {
			result.RealIP = staticIP
			result.DNSResolved = true
			result.DNSServer = "static"
		}
	}

	// If not in cache/static and we have a DNS server, try to resolve
	if !result.DNSResolved && dnsServer != "" {
		// Try to resolve via tunnel DNS
		ip, err := a.tunnelManager.ResolveViaTunnel(profileID, hostname, dnsServer)
		if err != nil {
			result.DNSError = err.Error()
		} else {
			result.RealIP = ip
			result.DNSResolved = true
		}
	}

	// Test TCP connectivity
	if result.RealIP != "" || hostname != "" {
		targetHost := result.RealIP
		if targetHost == "" {
			targetHost = hostname
		}

		addr := fmt.Sprintf("%s:%d", targetHost, port)
		start := time.Now()
		conn, err := t.Dial("tcp", addr)
		elapsed := time.Since(start)

		if err != nil {
			result.TCPError = err.Error()
		} else {
			conn.Close()
			result.TCPConnected = true
			result.TCPLatencyMs = elapsed.Milliseconds()
		}

		// Record latency metric
		debug.RecordLatencySample(profileID, addr, elapsed, result.TCPConnected)
	}

	return result
}

// GetSystemInfo returns system information
func (a *App) GetSystemInfo() debug.SystemInfo {
	info := debug.SystemInfo{
		IsAdmin:  system.IsAdmin(),
		Platform: "windows",
	}

	if a.networkConfig != nil {
		info.ServiceConnected = a.networkConfig.IsServiceConnected()
		info.DNSConfigured = a.networkConfig.IsTransparentDNSConfigured()

		interfaceName, err := a.networkConfig.GetActiveNetworkInterface()
		if err == nil {
			dnsServers, err := a.networkConfig.GetCurrentDNS(interfaceName)
			if err == nil && len(dnsServers) > 0 {
				info.CurrentDNS = dnsServers[0]
			}
		}
	}

	return info
}

// GenerateDiagnosticReport generates a complete diagnostic report
func (a *App) GenerateDiagnosticReport() debug.DiagnosticReport {
	return debug.DiagnosticReport{
		GeneratedAt:  time.Now(),
		AppVersion:   "1.2.0", // TODO: Get from build info
		SystemInfo:   a.GetSystemInfo(),
		VPNStatus:    a.GetVPNStatusList(),
		DNSConfig:    a.GetDNSConfig(),
		TCPProxyInfo: a.GetTCPProxyInfo(),
		HostMappings: a.GetHostMappings(),
		RecentErrors: debug.GetErrorCollector().GetRecent(50),
		RecentLogs:   debug.GetLogger().GetLogs(100),
		Metrics:      debug.GetMetricsCollector().GetAllMetrics(),
	}
}

// =============================================================================
// Frontend Logging - Exposed to JavaScript via Wails
// =============================================================================

// LogFrontend receives log entries from the frontend (React)
func (a *App) LogFrontend(level, component, message string, fields map[string]any) {
	logger := debug.GetLogger()
	switch debug.LogLevel(level) {
	case debug.LevelDebug:
		logger.Debug("frontend:"+component, message, fields)
	case debug.LevelInfo:
		logger.Info("frontend:"+component, message, fields)
	case debug.LevelWarn:
		logger.Warn("frontend:"+component, message, fields)
	case debug.LevelError:
		logger.Error("frontend:"+component, message, fields)
	default:
		logger.Info("frontend:"+component, message, fields)
	}
}

// LogFrontendError receives error entries from the frontend
func (a *App) LogFrontendError(component, operation, errorMsg string, context map[string]any) {
	debug.RecordError("frontend:"+component, operation, fmt.Errorf("%s", errorMsg), context)
}

// GetDebugLogs returns logs for display (exposed to frontend)
func (a *App) GetDebugLogs(level, component string, limit int) []debug.LogEntry {
	if limit <= 0 {
		limit = 100
	}
	return debug.GetLogger().GetLogsFiltered(debug.LogLevel(level), component, "", limit)
}

// GetDebugErrors returns recent errors (exposed to frontend)
func (a *App) GetDebugErrors(limit int) []debug.ErrorEntry {
	if limit <= 0 {
		limit = 50
	}
	return debug.GetErrorCollector().GetRecent(limit)
}

// GetDebugMetrics returns metrics (exposed to frontend)
func (a *App) GetDebugMetrics() map[string]any {
	return debug.GetMetricsCollector().GetAllMetrics()
}

// TestHostConnectivity tests connectivity to a host (exposed to frontend)
// Uses system DNS by default to match real app behavior (DBeaver, etc.)
func (a *App) TestHostConnectivity(hostname string, port int) debug.HostTestResult {
	return a.TestHost(hostname, port, "", true)
}

// DiagnoseHostDNS diagnoses DNS for a hostname (exposed to frontend)
func (a *App) DiagnoseHostDNS(hostname string) debug.DNSDiagnostic {
	return a.DiagnoseDNS(hostname)
}

// GetAllHostMappings returns all host mappings (exposed to frontend)
func (a *App) GetAllHostMappings() []debug.HostMappingInfo {
	return a.GetHostMappings()
}

// GetLoopbackIPs returns all assigned loopback IPs
func (a *App) GetLoopbackIPs() map[string]string {
	result := make(map[string]string)

	// Get IPs from TCP proxy config
	for profileID, ip := range a.config.TCPProxy.TunnelIPs {
		result[profileID] = ip
	}

	// Also get dynamically assigned IPs from host mappings
	mappings := a.GetHostMappings()
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

// PingHost performs a simple connectivity test
func (a *App) PingHost(profileID, host string, port int) (bool, int64, string) {
	if !a.tunnelManager.IsConnected(profileID) {
		return false, 0, "Tunnel not connected"
	}

	t := a.tunnelManager.GetTunnel(profileID)
	if t == nil {
		return false, 0, "Tunnel not available"
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	start := time.Now()
	conn, err := t.Dial("tcp", addr)
	elapsed := time.Since(start)

	if err != nil {
		return false, elapsed.Milliseconds(), err.Error()
	}
	conn.Close()

	debug.RecordLatencySample(profileID, addr, elapsed, true)
	return true, elapsed.Milliseconds(), fmt.Sprintf("Connected in %dms", elapsed.Milliseconds())
}
