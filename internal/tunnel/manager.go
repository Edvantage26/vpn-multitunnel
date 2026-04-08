package tunnel

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/debug"
	"vpnmultitunnel/internal/proxy"
)

// VPNCredentials holds username/password for VPN connections that require auth
type VPNCredentials struct {
	Username string
	Password string
}

// Manager manages all active tunnels and their proxies
type Manager struct {
	config             *config.AppConfig
	tunnels            map[string]VPNTunnel
	proxyManager       *proxy.Manager
	healthChecks       map[string]*HealthChecker
	pendingCredentials map[string]*VPNCredentials // profileID → credentials (consumed on connect)
	mu                 sync.RWMutex
}

// NewManager creates a new tunnel manager
func NewManager(cfg *config.AppConfig) *Manager {
	tunnel_manager := &Manager{
		config:             cfg,
		tunnels:            make(map[string]VPNTunnel),
		healthChecks:       make(map[string]*HealthChecker),
		pendingCredentials: make(map[string]*VPNCredentials),
	}

	// Initialize proxy manager
	tunnel_manager.proxyManager = proxy.NewManager()

	// Build DNS rules from profiles and start DNS proxy if enabled
	cfg.DNSProxy.Rules = config.BuildDNSRulesFromProfiles(cfg.Profiles)
	if cfg.DNSProxy.Enabled {
		tunnel_manager.proxyManager.StartDNSProxy(&cfg.DNSProxy, tunnel_manager.getTunnelForProfile, tunnel_manager.GetDNSServerForProfile)
	}

	// Start TCP proxy if enabled
	if cfg.TCPProxy.IsEnabled() {
		profilePortsMap := buildProfilePortsMap(cfg.Profiles)
		tunnel_manager.proxyManager.StartTCPProxy(&cfg.TCPProxy, tunnel_manager.getTunnelForProfile, profilePortsMap)
	}

	return tunnel_manager
}

// SetCredentials stores credentials for a profile to be used on next connect
func (tunnel_manager *Manager) SetCredentials(profileID string, username string, password string) {
	tunnel_manager.mu.Lock()
	defer tunnel_manager.mu.Unlock()
	tunnel_manager.pendingCredentials[profileID] = &VPNCredentials{
		Username: username,
		Password: password,
	}
}

// SetLoopbackIPCallback sets the callback for configuring new loopback IPs dynamically
func (tunnel_manager *Manager) SetLoopbackIPCallback(callback func(ip string) error) {
	tunnel_manager.proxyManager.SetLoopbackIPCallback(callback)
}

// Start starts a tunnel for the given profile, dispatching by VPN type.
// The tunnel creation (which may take a long time for OpenVPN/WatchGuard)
// happens outside the lock to avoid blocking other operations.
func (tunnel_manager *Manager) Start(profile *config.Profile) error {
	// Quick check under lock: is it already running?
	tunnel_manager.mu.RLock()
	_, alreadyRunning := tunnel_manager.tunnels[profile.ID]
	tunnel_manager.mu.RUnlock()

	if alreadyRunning {
		return fmt.Errorf("tunnel already running for profile: %s", profile.Name)
	}

	// Create tunnel OUTSIDE the lock — this can take 60s+ for OpenVPN/WatchGuard
	debug.GetLogger().InfoProfile("tunnel", profile.ID, fmt.Sprintf("Connecting tunnel (type: %s)...", profile.GetVPNType()), nil)
	vpnTunnel, createErr := tunnel_manager.createTunnel(profile)
	if createErr != nil {
		debug.GetLogger().ErrorProfile("tunnel", profile.ID, fmt.Sprintf("Tunnel connection failed: %v", createErr), nil)
		return createErr
	}

	// Now take the lock briefly to register the tunnel
	tunnel_manager.mu.Lock()
	tunnel_manager.tunnels[profile.ID] = vpnTunnel
	tunnel_manager.mu.Unlock()

	log.Printf("Tunnel started for profile: %s (type: %s)", profile.Name, profile.GetVPNType())
	debug.GetLogger().InfoProfile("tunnel", profile.ID, fmt.Sprintf("Tunnel connected (type: %s)", profile.GetVPNType()), map[string]any{
		"profileName": profile.Name,
		"vpnType":     string(profile.GetVPNType()),
		"configFile":  profile.ConfigFile,
	})

	// Start health check if enabled, using the tunnel's assigned IP
	healthCheckTargetIP := vpnTunnel.GetAssignedIP()
	if profile.HealthCheck.Enabled && healthCheckTargetIP != "" {
		healthChecker := NewHealthChecker(profile.ID, healthCheckTargetIP,
			profile.HealthCheck.IntervalSeconds, vpnTunnel)
		tunnel_manager.mu.Lock()
		tunnel_manager.healthChecks[profile.ID] = healthChecker
		tunnel_manager.mu.Unlock()
		healthChecker.Start()
	}

	return nil
}

// createTunnel creates a VPNTunnel based on the profile's VPN type
func (tunnel_manager *Manager) createTunnel(profile *config.Profile) (VPNTunnel, error) {
	switch profile.GetVPNType() {
	case config.VPNTypeWireGuard:
		return tunnel_manager.createWireGuardTunnel(profile)
	case config.VPNTypeOpenVPN:
		return tunnel_manager.createOpenVPNTunnel(profile)
	case config.VPNTypeWatchGuard:
		return tunnel_manager.createWatchGuardTunnel(profile)
	case config.VPNTypeExternal:
		return tunnel_manager.createExternalTunnel(profile)
	default:
		return nil, fmt.Errorf("unknown VPN type: %s", profile.GetVPNType())
	}
}

// createWireGuardTunnel creates a WireGuard tunnel using netstack (userspace)
func (tunnel_manager *Manager) createWireGuardTunnel(profile *config.Profile) (*Tunnel, error) {
	configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
	if pathErr != nil {
		return nil, fmt.Errorf("failed to get config path: %w", pathErr)
	}

	wgConfig, parseErr := config.ParseWireGuardConfig(configPath)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse WireGuard config: %w", parseErr)
	}

	wireguardTunnel, tunnelErr := NewTunnel(profile.ID, wgConfig)
	if tunnelErr != nil {
		return nil, fmt.Errorf("failed to create tunnel: %w", tunnelErr)
	}

	return wireguardTunnel, nil
}

// createOpenVPNTunnel creates an OpenVPN tunnel by launching openvpn.exe as a subprocess
func (tunnel_manager *Manager) createOpenVPNTunnel(profile *config.Profile) (*OpenVPNTunnel, error) {
	configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
	if pathErr != nil {
		return nil, fmt.Errorf("failed to get config path: %w", pathErr)
	}

	ovpnConfig, parseErr := config.ParseOpenVPNConfig(configPath)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse OpenVPN config: %w", parseErr)
	}

	// Consume stored credentials (if any)
	tunnel_manager.mu.RLock()
	credentials := tunnel_manager.pendingCredentials[profile.ID]
	tunnel_manager.mu.RUnlock()

	var authUsername, authPassword string
	if credentials != nil {
		authUsername = credentials.Username
		authPassword = credentials.Password
		// Clean up after consuming
		tunnel_manager.mu.Lock()
		delete(tunnel_manager.pendingCredentials, profile.ID)
		tunnel_manager.mu.Unlock()
	} else if profile.Credentials != nil {
		// Fallback to saved credentials from profile config
		authUsername = profile.Credentials.Username
		authPassword = profile.Credentials.Password
	}

	openVPNTunnel, tunnelErr := NewOpenVPNTunnel(profile.ID, ovpnConfig, configPath, authUsername, authPassword)
	if tunnelErr != nil {
		return nil, fmt.Errorf("failed to create OpenVPN tunnel: %w", tunnelErr)
	}

	return openVPNTunnel, nil
}

// createWatchGuardTunnel creates a WatchGuard SSL VPN tunnel by launching the client subprocess
func (tunnel_manager *Manager) createWatchGuardTunnel(profile *config.Profile) (*WatchGuardTunnel, error) {
	// WatchGuard config is stored as a JSON file in the configs directory
	configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
	if pathErr != nil {
		return nil, fmt.Errorf("failed to get config path: %w", pathErr)
	}

	wgConfig, parseErr := config.ParseWatchGuardConfig(configPath)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse WatchGuard config: %w", parseErr)
	}

	watchGuardTunnel, tunnelErr := NewWatchGuardTunnel(profile.ID, wgConfig)
	if tunnelErr != nil {
		return nil, fmt.Errorf("failed to create WatchGuard tunnel: %w", tunnelErr)
	}

	return watchGuardTunnel, nil
}

// createExternalTunnel creates an external tunnel that monitors a VPN adapter
func (tunnel_manager *Manager) createExternalTunnel(profile *config.Profile) (*ExternalTunnel, error) {
	configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
	if pathErr != nil {
		return nil, fmt.Errorf("failed to get config path: %w", pathErr)
	}

	extConfig, parseErr := config.ParseExternalVPNConfig(configPath)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse external VPN config: %w", parseErr)
	}

	externalTunnel, tunnelErr := NewExternalTunnel(profile.ID, extConfig)
	if tunnelErr != nil {
		return nil, fmt.Errorf("failed to create external tunnel: %w", tunnelErr)
	}

	return externalTunnel, nil
}

// Stop stops a tunnel for the given profile ID
func (tunnel_manager *Manager) Stop(profileID string) error {
	tunnel_manager.mu.Lock()
	defer tunnel_manager.mu.Unlock()

	tunnel, exists := tunnel_manager.tunnels[profileID]
	if !exists {
		return fmt.Errorf("no tunnel running for profile: %s", profileID)
	}

	// Stop health check
	if hc, exists := tunnel_manager.healthChecks[profileID]; exists {
		hc.Stop()
		delete(tunnel_manager.healthChecks, profileID)
	}

	// Stop proxies
	tunnel_manager.proxyManager.StopAllForProfile(profileID)

	// Close tunnel
	if err := tunnel.Close(); err != nil {
		log.Printf("Error closing tunnel: %v", err)
	}

	delete(tunnel_manager.tunnels, profileID)
	log.Printf("Tunnel stopped for profile: %s", profileID)
	debug.GetLogger().InfoProfile("tunnel", profileID, "Tunnel disconnected", nil)

	return nil
}

// StopAll stops all tunnels
func (tunnel_manager *Manager) StopAll() {
	tunnel_manager.mu.Lock()
	defer tunnel_manager.mu.Unlock()

	// Stop DNS proxy
	tunnel_manager.proxyManager.StopDNSProxy()

	for profileID, tunnel := range tunnel_manager.tunnels {
		// Stop health check
		if hc, exists := tunnel_manager.healthChecks[profileID]; exists {
			hc.Stop()
			delete(tunnel_manager.healthChecks, profileID)
		}

		// Stop proxies
		tunnel_manager.proxyManager.StopAllForProfile(profileID)

		// Close tunnel
		tunnel.Close()
		log.Printf("Tunnel stopped for profile: %s", profileID)
	}

	tunnel_manager.tunnels = make(map[string]VPNTunnel)
}

// IsConnected returns whether a tunnel is running for the given profile
func (tunnel_manager *Manager) IsConnected(profileID string) bool {
	tunnel_manager.mu.RLock()
	defer tunnel_manager.mu.RUnlock()
	_, exists := tunnel_manager.tunnels[profileID]
	return exists
}

// GetConnectedCount returns the number of connected tunnels
func (tunnel_manager *Manager) GetConnectedCount() int {
	tunnel_manager.mu.RLock()
	defer tunnel_manager.mu.RUnlock()
	return len(tunnel_manager.tunnels)
}

// GetStats returns statistics for a tunnel
func (tunnel_manager *Manager) GetStats(profileID string) *TunnelStats {
	tunnel_manager.mu.RLock()
	defer tunnel_manager.mu.RUnlock()

	activeTunnel, exists := tunnel_manager.tunnels[profileID]
	if !exists {
		return nil
	}

	activeTunnel.UpdateStats()
	tunnelStats := activeTunnel.GetStats()
	return &tunnelStats
}

// RestartDNSProxy restarts the DNS proxy with new configuration
func (tunnel_manager *Manager) RestartDNSProxy(dnsConfig *config.DNSProxy) {
	tunnel_manager.proxyManager.StopDNSProxy()
	// Rebuild rules from profiles
	dnsConfig.Rules = config.BuildDNSRulesFromProfiles(tunnel_manager.config.Profiles)
	if dnsConfig.Enabled {
		tunnel_manager.proxyManager.StartDNSProxy(dnsConfig, tunnel_manager.getTunnelForProfile, tunnel_manager.GetDNSServerForProfile)
	}
}

// RestartDNSProxyOnPort restarts the DNS proxy on a specific port
func (tunnel_manager *Manager) RestartDNSProxyOnPort(port int) error {
	return tunnel_manager.proxyManager.RestartDNSProxyOnPort(port)
}

// GetDNSProxyPort returns the current DNS proxy port
func (tunnel_manager *Manager) GetDNSProxyPort() int {
	return tunnel_manager.proxyManager.GetDNSProxyPort()
}

// RestartTCPProxy restarts the TCP proxy with new configuration
func (tunnel_manager *Manager) RestartTCPProxy(tcpConfig *config.TCPProxy) {
	tunnel_manager.proxyManager.StopTCPProxy()
	if tcpConfig.IsEnabled() {
		profilePortsMap := buildProfilePortsMap(tunnel_manager.config.Profiles)
		tunnel_manager.proxyManager.StartTCPProxy(tcpConfig, tunnel_manager.getTunnelForProfile, profilePortsMap)
	}
}

// buildProfilePortsMap creates a map of profileID -> TCP proxy ports from profile configs
func buildProfilePortsMap(profiles []config.Profile) map[string][]int {
	profilePortsMap := make(map[string][]int, len(profiles))
	for _, profile := range profiles {
		profilePortsMap[profile.ID] = profile.GetTCPProxyPorts()
	}
	return profilePortsMap
}

// GetActiveConnections returns active transparent proxy connections
func (tunnel_manager *Manager) GetActiveConnections() []proxy.ActiveConnection {
	return tunnel_manager.proxyManager.GetActiveConnections()
}

// IsTCPProxyEnabled returns whether the TCP proxy is enabled
func (tunnel_manager *Manager) IsTCPProxyEnabled() bool {
	return tunnel_manager.proxyManager.IsTCPProxyEnabled()
}

// GetTCPProxyListenerCount returns the number of TCP proxy listeners
func (tunnel_manager *Manager) GetTCPProxyListenerCount() int {
	return tunnel_manager.proxyManager.GetTCPProxyListenerCount()
}

// GetTrafficMonitor returns the traffic monitor singleton
func (tunnel_manager *Manager) GetTrafficMonitor() *proxy.TrafficMonitor {
	return tunnel_manager.proxyManager.GetTrafficMonitor()
}

// getTunnelForProfile returns a tunnel getter function for the proxy manager
func (tunnel_manager *Manager) getTunnelForProfile(profileID string) proxy.TunnelDialer {
	tunnel_manager.mu.RLock()
	defer tunnel_manager.mu.RUnlock()
	active_tunnel := tunnel_manager.tunnels[profileID]
	if active_tunnel == nil {
		return nil
	}
	return active_tunnel
}

// GetTunnel returns the tunnel for a profile
func (tunnel_manager *Manager) GetTunnel(profileID string) VPNTunnel {
	tunnel_manager.mu.RLock()
	defer tunnel_manager.mu.RUnlock()
	return tunnel_manager.tunnels[profileID]
}

// GetHostMappings returns all active host mappings from the proxy manager
func (tunnel_manager *Manager) GetHostMappings() []*proxy.HostMapping {
	cache := tunnel_manager.proxyManager.GetHostMapping()
	if cache == nil {
		return nil
	}
	return cache.GetAllActive()
}

// ResolveViaTunnel resolves a hostname using the tunnel's DNS server (from .conf cache)
func (tunnel_manager *Manager) ResolveViaTunnel(profileID, hostname string) (string, error) {
	dnsServer := tunnel_manager.GetDNSServerForProfile(profileID)
	if dnsServer == "" {
		return "", fmt.Errorf("no DNS server configured for profile %s", profileID)
	}
	return tunnel_manager.proxyManager.ResolveViaTunnel(profileID, hostname, dnsServer)
}

// GetDNSServerForProfile returns the DNS server for a profile.
// If the tunnel is connected, reads from the VPNTunnel interface.
// Otherwise, falls back to parsing the config file from disk (WireGuard only).
func (tunnel_manager *Manager) GetDNSServerForProfile(profileID string) string {
	tunnel_manager.mu.RLock()
	activeTunnel, hasTunnel := tunnel_manager.tunnels[profileID]
	tunnel_manager.mu.RUnlock()

	if hasTunnel {
		return activeTunnel.GetDNSServer()
	}

	// Fallback: parse config from disk for disconnected profiles
	return tunnel_manager.getDNSServerFromDisk(profileID)
}

// GetTargetIPForProfile returns the health check target IP for a profile.
// If the tunnel is connected, reads from the VPNTunnel interface.
// Otherwise, falls back to parsing the config file from disk (WireGuard only).
func (tunnel_manager *Manager) GetTargetIPForProfile(profileID string) string {
	tunnel_manager.mu.RLock()
	activeTunnel, hasTunnel := tunnel_manager.tunnels[profileID]
	tunnel_manager.mu.RUnlock()

	if hasTunnel {
		return activeTunnel.GetAssignedIP()
	}

	// Fallback: parse config from disk for disconnected profiles
	return tunnel_manager.getAssignedIPFromDisk(profileID)
}

// getDNSServerFromDisk parses the config file from disk to get the DNS server (fallback for disconnected tunnels)
func (tunnel_manager *Manager) getDNSServerFromDisk(profileID string) string {
	for _, profile := range tunnel_manager.config.Profiles {
		if profile.ID != profileID {
			continue
		}
		switch profile.GetVPNType() {
		case config.VPNTypeWireGuard:
			configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
			if pathErr != nil {
				return ""
			}
			wgConfig, parseErr := config.ParseWireGuardConfig(configPath)
			if parseErr != nil || len(wgConfig.Interface.DNS) == 0 {
				return ""
			}
			return wgConfig.Interface.DNS[0]
		case config.VPNTypeOpenVPN:
			configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
			if pathErr != nil {
				return ""
			}
			ovpnConfig, parseErr := config.ParseOpenVPNConfig(configPath)
			if parseErr != nil || len(ovpnConfig.DNSServers) == 0 {
				return ""
			}
			return ovpnConfig.DNSServers[0]
		default:
			// WatchGuard: DNS server only available when connected
			return ""
		}
	}
	return ""
}

// getAssignedIPFromDisk parses the config file from disk to get the assigned IP (fallback for disconnected tunnels)
func (tunnel_manager *Manager) getAssignedIPFromDisk(profileID string) string {
	for _, profile := range tunnel_manager.config.Profiles {
		if profile.ID != profileID {
			continue
		}
		switch profile.GetVPNType() {
		case config.VPNTypeWireGuard:
			configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
			if pathErr != nil {
				return ""
			}
			wgConfig, parseErr := config.ParseWireGuardConfig(configPath)
			if parseErr != nil || len(wgConfig.Interface.Address) == 0 {
				return ""
			}
			return strings.Split(wgConfig.Interface.Address[0], "/")[0]
		default:
			// OpenVPN and WatchGuard: assigned IP only available when connected
			return ""
		}
	}
	return ""
}
