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
func (proxy_manager *Manager) SetLoopbackIPCallback(callback func(ip string) error) {
	proxy_manager.mu.Lock()
	defer proxy_manager.mu.Unlock()
	proxy_manager.onConfigureLoopbackIP = callback
}

// StopAllForProfile stops all proxies for a profile
func (proxy_manager *Manager) StopAllForProfile(profileID string) {
	proxy_manager.mu.Lock()
	defer proxy_manager.mu.Unlock()
	// No per-profile proxies to stop currently (SOCKS5 was removed)
	_ = profileID
}

// StartDNSProxy starts the global DNS proxy
func (proxy_manager *Manager) StartDNSProxy(dnsConfig *config.DNSProxy, tunnelGetter TunnelGetter) error {
	proxy_manager.mu.Lock()
	defer proxy_manager.mu.Unlock()

	if proxy_manager.dnsProxy != nil {
		proxy_manager.dnsProxy.Stop()
	}

	proxy_manager.tunnelGetter = tunnelGetter

	proxy, err := NewDNSProxy(dnsConfig, proxy_manager.getDNSDialer)
	if err != nil {
		return err
	}

	if err := proxy.Start(); err != nil {
		return err
	}

	proxy_manager.dnsProxy = proxy
	log.Printf("DNS proxy started on port %d", dnsConfig.ListenPort)
	return nil
}

// StopDNSProxy stops the DNS proxy
func (proxy_manager *Manager) StopDNSProxy() {
	proxy_manager.mu.Lock()
	defer proxy_manager.mu.Unlock()

	if proxy_manager.dnsProxy != nil {
		proxy_manager.dnsProxy.Stop()
		proxy_manager.dnsProxy = nil
	}
}

// RestartDNSProxyOnPort restarts the DNS proxy on a new port
func (proxy_manager *Manager) RestartDNSProxyOnPort(newPort int) error {
	proxy_manager.mu.Lock()
	defer proxy_manager.mu.Unlock()

	if proxy_manager.dnsProxy == nil {
		return fmt.Errorf("DNS proxy not running")
	}

	if err := proxy_manager.dnsProxy.Restart(newPort); err != nil {
		return fmt.Errorf("failed to restart DNS proxy on port %d: %w", newPort, err)
	}

	log.Printf("DNS proxy restarted on port %d", newPort)
	return nil
}

// GetDNSProxyPort returns the current DNS proxy port
func (proxy_manager *Manager) GetDNSProxyPort() int {
	proxy_manager.mu.RLock()
	defer proxy_manager.mu.RUnlock()

	if proxy_manager.dnsProxy != nil {
		return proxy_manager.dnsProxy.GetPort()
	}
	return 0
}

// StartTCPProxy starts the transparent TCP proxy
func (proxy_manager *Manager) StartTCPProxy(tcpConfig *config.TCPProxy, tunnelGetter TunnelGetter, profilePorts map[string][]int) error {
	proxy_manager.mu.Lock()
	defer proxy_manager.mu.Unlock()

	if proxy_manager.tcpProxy != nil {
		proxy_manager.tcpProxy.Stop()
	}

	proxy_manager.tunnelGetter = tunnelGetter

	// Create and start TCP proxy first (so we can reference it in the callback)
	proxy_manager.tcpProxy = NewTCPProxy(tcpConfig, proxy_manager.getTCPDialer, proxy_manager.hostMapping, proxy_manager.dnsProxy, profilePorts)
	if err := proxy_manager.tcpProxy.Start(); err != nil {
		return err
	}

	// Create the callback for when DNS assigns a new unique IP to a hostname
	onNewIP := func(ip string, profileID string) error {
		// Configure loopback IP on the system if callback is set
		if proxy_manager.onConfigureLoopbackIP != nil {
			if err := proxy_manager.onConfigureLoopbackIP(ip); err != nil {
				log.Printf("Failed to configure loopback IP %s: %v", ip, err)
				// Continue anyway - IP might already exist
			}
		}

		// Add TCP proxy listeners for this new IP
		if proxy_manager.tcpProxy != nil {
			if err := proxy_manager.tcpProxy.AddListenerForIP(ip, profileID); err != nil {
				log.Printf("Failed to add TCP listener for %s: %v", ip, err)
				return err
			}
		}

		return nil
	}

	// Configure DNS proxy for transparent proxy mode with the callback
	if proxy_manager.dnsProxy != nil {
		proxy_manager.dnsProxy.SetTransparentProxyConfig(tcpConfig.TunnelIPs, proxy_manager.hostMapping, tcpConfig.Enabled, onNewIP)
	}

	log.Printf("TCP proxy started with %d listeners", proxy_manager.tcpProxy.GetListenerCount())
	return nil
}

// StopTCPProxy stops the transparent TCP proxy
func (proxy_manager *Manager) StopTCPProxy() {
	proxy_manager.mu.Lock()
	defer proxy_manager.mu.Unlock()

	if proxy_manager.tcpProxy != nil {
		proxy_manager.tcpProxy.Stop()
		proxy_manager.tcpProxy = nil
	}

	// Disable transparent proxy mode in DNS proxy
	if proxy_manager.dnsProxy != nil {
		proxy_manager.dnsProxy.SetTransparentProxyConfig(nil, nil, false, nil)
	}
}

// GetActiveConnections returns active transparent proxy connections
func (proxy_manager *Manager) GetActiveConnections() []ActiveConnection {
	proxy_manager.mu.RLock()
	defer proxy_manager.mu.RUnlock()

	if proxy_manager.tcpProxy != nil {
		return proxy_manager.tcpProxy.GetActiveConnections()
	}
	return nil
}

// GetHostMapping returns the host mapping cache (for external use)
func (proxy_manager *Manager) GetHostMapping() *HostMappingCache {
	return proxy_manager.hostMapping
}

// getDNSDialer returns a dialer function for a profile ID
func (proxy_manager *Manager) getDNSDialer(profileID string) TunnelDialer {
	if proxy_manager.tunnelGetter == nil {
		return nil
	}
	return proxy_manager.tunnelGetter(profileID)
}

// getTCPDialer returns a dialer function for a profile ID (used by TCP proxy)
func (proxy_manager *Manager) getTCPDialer(profileID string) TunnelDialer {
	if proxy_manager.tunnelGetter == nil {
		return nil
	}
	return proxy_manager.tunnelGetter(profileID)
}

// IsTCPProxyEnabled returns whether the TCP proxy is enabled
func (proxy_manager *Manager) IsTCPProxyEnabled() bool {
	proxy_manager.mu.RLock()
	defer proxy_manager.mu.RUnlock()
	return proxy_manager.tcpProxy != nil
}

// GetTCPProxyListenerCount returns the number of TCP proxy listeners
func (proxy_manager *Manager) GetTCPProxyListenerCount() int {
	proxy_manager.mu.RLock()
	defer proxy_manager.mu.RUnlock()

	if proxy_manager.tcpProxy != nil {
		return proxy_manager.tcpProxy.GetListenerCount()
	}
	return 0
}

// ResolveViaTunnel resolves a hostname using a specific tunnel's DNS server
func (proxy_manager *Manager) ResolveViaTunnel(profileID, hostname, dnsServer string) (string, error) {
	proxy_manager.mu.RLock()
	dnsProxy := proxy_manager.dnsProxy
	proxy_manager.mu.RUnlock()

	if dnsProxy == nil {
		return "", fmt.Errorf("DNS proxy not running")
	}

	return dnsProxy.ResolveViaTunnel(profileID, hostname, dnsServer)
}
