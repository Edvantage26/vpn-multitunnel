package tunnel

import (
	"fmt"
	"log"
	"sync"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/proxy"
)

// Manager manages all active tunnels and their proxies
type Manager struct {
	config       *config.AppConfig
	tunnels      map[string]*Tunnel
	proxyManager *proxy.Manager
	healthChecks map[string]*HealthChecker
	mu           sync.RWMutex
}

// NewManager creates a new tunnel manager
func NewManager(cfg *config.AppConfig) *Manager {
	m := &Manager{
		config:       cfg,
		tunnels:      make(map[string]*Tunnel),
		healthChecks: make(map[string]*HealthChecker),
	}

	// Initialize proxy manager
	m.proxyManager = proxy.NewManager()

	// Start DNS proxy if enabled
	if cfg.DNSProxy.Enabled {
		m.proxyManager.StartDNSProxy(&cfg.DNSProxy, m.getTunnelForProfile)
	}

	// Start TCP proxy if enabled
	if cfg.TCPProxy.Enabled {
		profilePortsMap := buildProfilePortsMap(cfg.Profiles)
		m.proxyManager.StartTCPProxy(&cfg.TCPProxy, m.getTunnelForProfile, profilePortsMap)
	}

	return m
}

// SetLoopbackIPCallback sets the callback for configuring new loopback IPs dynamically
func (m *Manager) SetLoopbackIPCallback(callback func(ip string) error) {
	m.proxyManager.SetLoopbackIPCallback(callback)
}

// Start starts a tunnel for the given profile
func (m *Manager) Start(profile *config.Profile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already running
	if _, exists := m.tunnels[profile.ID]; exists {
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

	m.tunnels[profile.ID] = tunnel
	log.Printf("Tunnel started for profile: %s", profile.Name)

	// Start health check if enabled
	if profile.HealthCheck.Enabled && profile.HealthCheck.TargetIP != "" {
		hc := NewHealthChecker(profile.ID, profile.HealthCheck.TargetIP,
			profile.HealthCheck.IntervalSeconds, tunnel)
		m.healthChecks[profile.ID] = hc
		hc.Start()
	}

	return nil
}

// Stop stops a tunnel for the given profile ID
func (m *Manager) Stop(profileID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tunnel, exists := m.tunnels[profileID]
	if !exists {
		return fmt.Errorf("no tunnel running for profile: %s", profileID)
	}

	// Stop health check
	if hc, exists := m.healthChecks[profileID]; exists {
		hc.Stop()
		delete(m.healthChecks, profileID)
	}

	// Stop proxies
	m.proxyManager.StopAllForProfile(profileID)

	// Close tunnel
	if err := tunnel.Close(); err != nil {
		log.Printf("Error closing tunnel: %v", err)
	}

	delete(m.tunnels, profileID)
	log.Printf("Tunnel stopped for profile: %s", profileID)

	return nil
}

// StopAll stops all tunnels
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop DNS proxy
	m.proxyManager.StopDNSProxy()

	for profileID, tunnel := range m.tunnels {
		// Stop health check
		if hc, exists := m.healthChecks[profileID]; exists {
			hc.Stop()
			delete(m.healthChecks, profileID)
		}

		// Stop proxies
		m.proxyManager.StopAllForProfile(profileID)

		// Close tunnel
		tunnel.Close()
		log.Printf("Tunnel stopped for profile: %s", profileID)
	}

	m.tunnels = make(map[string]*Tunnel)
}

// IsConnected returns whether a tunnel is running for the given profile
func (m *Manager) IsConnected(profileID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.tunnels[profileID]
	return exists
}

// GetConnectedCount returns the number of connected tunnels
func (m *Manager) GetConnectedCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tunnels)
}

// GetStats returns statistics for a tunnel
func (m *Manager) GetStats(profileID string) *TunnelStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tunnel, exists := m.tunnels[profileID]
	if !exists {
		return nil
	}

	tunnel.UpdateStats()
	return &tunnel.Stats
}

// RestartDNSProxy restarts the DNS proxy with new configuration
func (m *Manager) RestartDNSProxy(dnsConfig *config.DNSProxy) {
	m.proxyManager.StopDNSProxy()
	if dnsConfig.Enabled {
		m.proxyManager.StartDNSProxy(dnsConfig, m.getTunnelForProfile)
	}
}

// RestartDNSProxyOnPort restarts the DNS proxy on a specific port
func (m *Manager) RestartDNSProxyOnPort(port int) error {
	return m.proxyManager.RestartDNSProxyOnPort(port)
}

// GetDNSProxyPort returns the current DNS proxy port
func (m *Manager) GetDNSProxyPort() int {
	return m.proxyManager.GetDNSProxyPort()
}

// RestartTCPProxy restarts the TCP proxy with new configuration
func (m *Manager) RestartTCPProxy(tcpConfig *config.TCPProxy) {
	m.proxyManager.StopTCPProxy()
	if tcpConfig.Enabled {
		profilePortsMap := buildProfilePortsMap(m.config.Profiles)
		m.proxyManager.StartTCPProxy(tcpConfig, m.getTunnelForProfile, profilePortsMap)
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
func (m *Manager) GetActiveConnections() []proxy.ActiveConnection {
	return m.proxyManager.GetActiveConnections()
}

// IsTCPProxyEnabled returns whether the TCP proxy is enabled
func (m *Manager) IsTCPProxyEnabled() bool {
	return m.proxyManager.IsTCPProxyEnabled()
}

// GetTCPProxyListenerCount returns the number of TCP proxy listeners
func (m *Manager) GetTCPProxyListenerCount() int {
	return m.proxyManager.GetTCPProxyListenerCount()
}

// getTunnelForProfile returns a tunnel getter function for the proxy manager
func (m *Manager) getTunnelForProfile(profileID string) proxy.TunnelDialer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t := m.tunnels[profileID]
	if t == nil {
		return nil
	}
	return t
}

// GetTunnel returns the tunnel for a profile (for testing)
func (m *Manager) GetTunnel(profileID string) *Tunnel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tunnels[profileID]
}

// GetHostMappings returns all active host mappings from the proxy manager
func (m *Manager) GetHostMappings() []*proxy.HostMapping {
	cache := m.proxyManager.GetHostMapping()
	if cache == nil {
		return nil
	}
	return cache.GetAllActive()
}

// ResolveViaTunnel resolves a hostname using a specific tunnel's DNS server
func (m *Manager) ResolveViaTunnel(profileID, hostname, dnsServer string) (string, error) {
	return m.proxyManager.ResolveViaTunnel(profileID, hostname, dnsServer)
}
