package tunnel

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/proxy"
)

// Manager manages all active tunnels and their proxies
type Manager struct {
	config              *config.AppConfig
	tunnels             map[string]*Tunnel
	proxyManager        *proxy.Manager
	healthChecks        map[string]*HealthChecker
	wireguardConfigCache map[string]*config.WireGuardConfig // profileID → parsed .conf
	mu                  sync.RWMutex
}

// NewManager creates a new tunnel manager
func NewManager(cfg *config.AppConfig) *Manager {
	tunnel_manager := &Manager{
		config:              cfg,
		tunnels:             make(map[string]*Tunnel),
		healthChecks:        make(map[string]*HealthChecker),
		wireguardConfigCache: make(map[string]*config.WireGuardConfig),
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

// SetLoopbackIPCallback sets the callback for configuring new loopback IPs dynamically
func (tunnel_manager *Manager) SetLoopbackIPCallback(callback func(ip string) error) {
	tunnel_manager.proxyManager.SetLoopbackIPCallback(callback)
}

// Start starts a tunnel for the given profile
func (tunnel_manager *Manager) Start(profile *config.Profile) error {
	tunnel_manager.mu.Lock()
	defer tunnel_manager.mu.Unlock()

	// Check if already running
	if _, exists := tunnel_manager.tunnels[profile.ID]; exists {
		return fmt.Errorf("tunnel already running for profile: %s", profile.Name)
	}

	// Load WireGuard config
	configPath, err := config.GetConfigFilePath(profile.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to get config path: %w", err)
	}

	wgConfig, err := config.ParseWireGuardConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to parse WireGuard config: %w", err)
	}

	// Create tunnel
	tunnel, err := NewTunnel(profile.ID, wgConfig)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}

	tunnel_manager.tunnels[profile.ID] = tunnel
	tunnel_manager.wireguardConfigCache[profile.ID] = wgConfig
	log.Printf("Tunnel started for profile: %s", profile.Name)

	// Start health check if enabled, using TargetIP from the WireGuard .conf Address field
	// Read directly from cache (already under lock, can't call GetTargetIPForProfile which RLocks)
	var healthCheckTargetIP string
	if len(wgConfig.Interface.Address) > 0 {
		healthCheckTargetIP = strings.Split(wgConfig.Interface.Address[0], "/")[0]
	}
	if profile.HealthCheck.Enabled && healthCheckTargetIP != "" {
		healthChecker := NewHealthChecker(profile.ID, healthCheckTargetIP,
			profile.HealthCheck.IntervalSeconds, tunnel)
		tunnel_manager.healthChecks[profile.ID] = healthChecker
		healthChecker.Start()
	}

	return nil
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
	delete(tunnel_manager.wireguardConfigCache, profileID)
	log.Printf("Tunnel stopped for profile: %s", profileID)

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

	tunnel_manager.tunnels = make(map[string]*Tunnel)
	tunnel_manager.wireguardConfigCache = make(map[string]*config.WireGuardConfig)
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

	tunnel, exists := tunnel_manager.tunnels[profileID]
	if !exists {
		return nil
	}

	tunnel.UpdateStats()
	return &tunnel.Stats
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

// GetTunnel returns the tunnel for a profile (for testing)
func (tunnel_manager *Manager) GetTunnel(profileID string) *Tunnel {
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

// GetDNSServerForProfile returns the DNS server from the cached WireGuard .conf for a profile.
// Falls back to parsing the .conf file if not cached (e.g., tunnel not connected).
func (tunnel_manager *Manager) GetDNSServerForProfile(profileID string) string {
	tunnel_manager.mu.RLock()
	cachedConfig, hasCached := tunnel_manager.wireguardConfigCache[profileID]
	tunnel_manager.mu.RUnlock()

	if hasCached && len(cachedConfig.Interface.DNS) > 0 {
		return cachedConfig.Interface.DNS[0]
	}

	// Fallback: parse .conf from disk
	for _, profile := range tunnel_manager.config.Profiles {
		if profile.ID == profileID {
			configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
			if pathErr != nil {
				return ""
			}
			wgConfig, parseErr := config.ParseWireGuardConfig(configPath)
			if parseErr != nil || len(wgConfig.Interface.DNS) == 0 {
				return ""
			}
			return wgConfig.Interface.DNS[0]
		}
	}
	return ""
}

// GetTargetIPForProfile returns the health check target IP from the cached WireGuard .conf
// (first Address field without CIDR notation). Falls back to parsing .conf from disk.
func (tunnel_manager *Manager) GetTargetIPForProfile(profileID string) string {
	tunnel_manager.mu.RLock()
	cachedConfig, hasCached := tunnel_manager.wireguardConfigCache[profileID]
	tunnel_manager.mu.RUnlock()

	if hasCached && len(cachedConfig.Interface.Address) > 0 {
		return strings.Split(cachedConfig.Interface.Address[0], "/")[0]
	}

	// Fallback: parse .conf from disk
	for _, profile := range tunnel_manager.config.Profiles {
		if profile.ID == profileID {
			configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
			if pathErr != nil {
				return ""
			}
			wgConfig, parseErr := config.ParseWireGuardConfig(configPath)
			if parseErr != nil || len(wgConfig.Interface.Address) == 0 {
				return ""
			}
			return strings.Split(wgConfig.Interface.Address[0], "/")[0]
		}
	}
	return ""
}
