package proxy

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"vpnmultitunnel/internal/config"
)

// TunnelDialer is an interface for tunnels that can dial connections
type TunnelDialer interface {
	Dial(network, addr string) (net.Conn, error)
}

// TunnelGetter is a function that returns a tunnel for a profile ID
type TunnelGetter func(profileID string) TunnelDialer

// Manager manages all proxy servers
type Manager struct {
	dnsProxy       *DNSProxy
	tcpProxy       *TCPProxy
	hostMapping    *HostMappingCache
	tunnelGetter   TunnelGetter
	// Callback for configuring new loopback IPs (set by app)
	onConfigureLoopbackIP func(ip string) error
	mu             sync.RWMutex
}

// NewManager creates a new proxy manager
func NewManager() *Manager {
	return &Manager{
		hostMapping:   NewHostMappingCache(2 * time.Hour),
	}
}

// SetLoopbackIPCallback sets the callback for configuring new loopback IPs
func (m *Manager) SetLoopbackIPCallback(callback func(ip string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onConfigureLoopbackIP = callback
}

// StopAllForProfile stops all proxies for a profile
func (m *Manager) StopAllForProfile(profileID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// No per-profile proxies to stop currently (SOCKS5 was removed)
	_ = profileID
}

// StartDNSProxy starts the global DNS proxy
func (m *Manager) StartDNSProxy(dnsConfig *config.DNSProxy, tunnelGetter TunnelGetter) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dnsProxy != nil {
		m.dnsProxy.Stop()
	}

	m.tunnelGetter = tunnelGetter

	proxy, err := NewDNSProxy(dnsConfig, m.getDNSDialer)
	if err != nil {
		return err
	}

	if err := proxy.Start(); err != nil {
		return err
	}

	m.dnsProxy = proxy
	log.Printf("DNS proxy started on port %d", dnsConfig.ListenPort)
	return nil
}

// StopDNSProxy stops the DNS proxy
func (m *Manager) StopDNSProxy() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dnsProxy != nil {
		m.dnsProxy.Stop()
		m.dnsProxy = nil
	}
}

// RestartDNSProxyOnPort restarts the DNS proxy on a new port
func (m *Manager) RestartDNSProxyOnPort(newPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dnsProxy == nil {
		return fmt.Errorf("DNS proxy not running")
	}

	if err := m.dnsProxy.Restart(newPort); err != nil {
		return fmt.Errorf("failed to restart DNS proxy on port %d: %w", newPort, err)
	}

	log.Printf("DNS proxy restarted on port %d", newPort)
	return nil
}

// GetDNSProxyPort returns the current DNS proxy port
func (m *Manager) GetDNSProxyPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.dnsProxy != nil {
		return m.dnsProxy.GetPort()
	}
	return 0
}

// StartTCPProxy starts the transparent TCP proxy
func (m *Manager) StartTCPProxy(tcpConfig *config.TCPProxy, tunnelGetter TunnelGetter, profilePorts map[string][]int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tcpProxy != nil {
		m.tcpProxy.Stop()
	}

	m.tunnelGetter = tunnelGetter

	// Create and start TCP proxy first (so we can reference it in the callback)
	m.tcpProxy = NewTCPProxy(tcpConfig, m.getTCPDialer, m.hostMapping, m.dnsProxy, profilePorts)
	if err := m.tcpProxy.Start(); err != nil {
		return err
	}

	// Create the callback for when DNS assigns a new unique IP to a hostname
	onNewIP := func(ip string, profileID string) error {
		// Configure loopback IP on the system if callback is set
		if m.onConfigureLoopbackIP != nil {
			if err := m.onConfigureLoopbackIP(ip); err != nil {
				log.Printf("Failed to configure loopback IP %s: %v", ip, err)
				// Continue anyway - IP might already exist
			}
		}

		// Add TCP proxy listeners for this new IP
		if m.tcpProxy != nil {
			if err := m.tcpProxy.AddListenerForIP(ip, profileID); err != nil {
				log.Printf("Failed to add TCP listener for %s: %v", ip, err)
				return err
			}
		}

		return nil
	}

	// Configure DNS proxy for transparent proxy mode with the callback
	if m.dnsProxy != nil {
		m.dnsProxy.SetTransparentProxyConfig(tcpConfig.TunnelIPs, m.hostMapping, tcpConfig.Enabled, onNewIP)
	}

	log.Printf("TCP proxy started with %d listeners", m.tcpProxy.GetListenerCount())
	return nil
}

// StopTCPProxy stops the transparent TCP proxy
func (m *Manager) StopTCPProxy() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tcpProxy != nil {
		m.tcpProxy.Stop()
		m.tcpProxy = nil
	}

	// Disable transparent proxy mode in DNS proxy
	if m.dnsProxy != nil {
		m.dnsProxy.SetTransparentProxyConfig(nil, nil, false, nil)
	}
}

// GetActiveConnections returns active transparent proxy connections
func (m *Manager) GetActiveConnections() []ActiveConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.tcpProxy != nil {
		return m.tcpProxy.GetActiveConnections()
	}
	return nil
}

// GetHostMapping returns the host mapping cache (for external use)
func (m *Manager) GetHostMapping() *HostMappingCache {
	return m.hostMapping
}

// getDNSDialer returns a dialer function for a profile ID
func (m *Manager) getDNSDialer(profileID string) TunnelDialer {
	if m.tunnelGetter == nil {
		return nil
	}
	return m.tunnelGetter(profileID)
}

// getTCPDialer returns a dialer function for a profile ID (used by TCP proxy)
func (m *Manager) getTCPDialer(profileID string) TunnelDialer {
	if m.tunnelGetter == nil {
		return nil
	}
	return m.tunnelGetter(profileID)
}

// IsTCPProxyEnabled returns whether the TCP proxy is enabled
func (m *Manager) IsTCPProxyEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tcpProxy != nil
}

// GetTCPProxyListenerCount returns the number of TCP proxy listeners
func (m *Manager) GetTCPProxyListenerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.tcpProxy != nil {
		return m.tcpProxy.GetListenerCount()
	}
	return 0
}

// ResolveViaTunnel resolves a hostname using a specific tunnel's DNS server
func (m *Manager) ResolveViaTunnel(profileID, hostname, dnsServer string) (string, error) {
	m.mu.RLock()
	dnsProxy := m.dnsProxy
	m.mu.RUnlock()

	if dnsProxy == nil {
		return "", fmt.Errorf("DNS proxy not running")
	}

	return dnsProxy.ResolveViaTunnel(profileID, hostname, dnsServer)
}
