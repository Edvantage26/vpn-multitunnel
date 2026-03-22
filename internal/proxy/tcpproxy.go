package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"vpnmultitunnel/internal/config"
)

// TCPProxy is a transparent TCP proxy that listens on multiple IPs/ports
// and routes connections through the appropriate WireGuard tunnel
type TCPProxy struct {
	config       *config.TCPProxy
	tunnelGetter TunnelGetter
	hostMapping  *HostMappingCache
	dnsResolver  DNSResolver

	// listeners maps "ip:port" to listener
	listeners map[string]net.Listener
	// profileForIP maps tunnel IP (e.g., "127.0.1.1") to profile ID
	profileForIP map[string]string
	// portsForProfile maps profileID to its configured TCP proxy ports (nil/empty = use global)
	portsForProfile map[string][]int

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
}

// DNSResolver resolves hostnames via tunnel DNS
type DNSResolver interface {
	ResolveViaTunnel(profileID, hostname, dnsServer string) (string, error)
}

// NewTCPProxy creates a new TCP proxy
func NewTCPProxy(cfg *config.TCPProxy, tunnelGetter TunnelGetter, hostMapping *HostMappingCache, dnsResolver DNSResolver, profilePorts map[string][]int) *TCPProxy {
	ctx, cancel := context.WithCancel(context.Background())

	// Build reverse mapping: tunnel IP -> profile ID
	profileForIP := make(map[string]string)
	for profileID, tunnelIP := range cfg.TunnelIPs {
		profileForIP[tunnelIP] = profileID
	}

	return &TCPProxy{
		config:          cfg,
		tunnelGetter:    tunnelGetter,
		hostMapping:     hostMapping,
		dnsResolver:     dnsResolver,
		listeners:       make(map[string]net.Listener),
		profileForIP:    profileForIP,
		portsForProfile: profilePorts,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// getPortsForProfile returns the TCP proxy ports for a given profile
func (tcpProxy *TCPProxy) getPortsForProfile(profileID string) []int {
	if ports, exists := tcpProxy.portsForProfile[profileID]; exists {
		return ports
	}
	return nil
}

// Start starts the TCP proxy, listening on all configured IPs and ports
func (t *TCPProxy) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.config.Enabled {
		log.Printf("TCP proxy is disabled")
		return nil
	}

	// For each tunnel IP, listen on its configured ports (per-profile or global fallback)
	for profileID, tunnelIP := range t.config.TunnelIPs {
		for _, port := range t.getPortsForProfile(profileID) {
			addr := fmt.Sprintf("%s:%d", tunnelIP, port)

			listener, err := net.Listen("tcp", addr)
			if err != nil {
				// Log the error but continue with other ports
				// This is common when the IP doesn't exist on the system
				if strings.Contains(err.Error(), "cannot assign requested address") {
					log.Printf("TCP proxy: cannot listen on %s (IP may need to be added to loopback)", addr)
				} else {
					log.Printf("TCP proxy: failed to listen on %s: %v", addr, err)
				}
				continue
			}

			t.listeners[addr] = listener
			t.wg.Add(1)
			go t.acceptLoop(listener, profileID, tunnelIP, port)
		}
	}

	if len(t.listeners) > 0 {
		log.Printf("TCP proxy started with %d listeners", len(t.listeners))
	}

	return nil
}

// Stop stops the TCP proxy
func (t *TCPProxy) Stop() {
	t.cancel()

	t.mu.Lock()
	for addr, listener := range t.listeners {
		listener.Close()
		delete(t.listeners, addr)
	}
	t.mu.Unlock()

	t.wg.Wait()
	log.Printf("TCP proxy stopped")
}

func (t *TCPProxy) acceptLoop(listener net.Listener, profileID string, tunnelIP string, port int) {
	defer t.wg.Done()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-t.ctx.Done():
				return
			default:
				if !strings.Contains(err.Error(), "use of closed network connection") {
					log.Printf("TCP proxy accept error on %s:%d: %v", tunnelIP, port, err)
				}
				return
			}
		}

		go t.handleConnection(conn, profileID, tunnelIP, port)
	}
}

func (t *TCPProxy) handleConnection(conn net.Conn, profileID string, tunnelIP string, port int) {
	defer conn.Close()

	// Get the tunnel for this profile
	tunnel := t.tunnelGetter(profileID)
	if tunnel == nil {
		log.Printf("TCP proxy: tunnel not connected for profile %s", profileID)
		return
	}

	// Look up the host mapping to find the real destination
	mapping := t.hostMapping.GetByTunnelIP(tunnelIP)
	if mapping == nil {
		log.Printf("TCP proxy: no host mapping found for tunnel IP %s", tunnelIP)
		return
	}

	// Connect to the real destination through the tunnel
	realAddr := fmt.Sprintf("%s:%d", mapping.RealIP, port)
	remote, err := tunnel.Dial("tcp", realAddr)
	if err != nil {
		log.Printf("TCP proxy: failed to dial %s via tunnel %s: %v", realAddr, profileID, err)
		return
	}
	defer remote.Close()

	log.Printf("TCP proxy: %s -> %s:%d via %s (real: %s)",
		conn.RemoteAddr(), tunnelIP, port, profileID, realAddr)

	// Bidirectional relay
	t.relay(conn, remote)
}

func (t *TCPProxy) relay(local, remote net.Conn) {
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()

	// Local to remote
	go func() {
		defer cancel()
		io.Copy(remote, local)
	}()

	// Remote to local
	go func() {
		defer cancel()
		io.Copy(local, remote)
	}()

	<-ctx.Done()
}

// UpdateConfig updates the TCP proxy configuration
func (tcpProxy *TCPProxy) UpdateConfig(cfg *config.TCPProxy, profilePorts map[string][]int) {
	tcpProxy.Stop()

	tcpProxy.mu.Lock()
	tcpProxy.config = cfg
	tcpProxy.portsForProfile = profilePorts

	// Rebuild reverse mapping
	tcpProxy.profileForIP = make(map[string]string)
	for profileID, tunnelIP := range cfg.TunnelIPs {
		tcpProxy.profileForIP[tunnelIP] = profileID
	}

	tcpProxy.ctx, tcpProxy.cancel = context.WithCancel(context.Background())
	tcpProxy.mu.Unlock()

	tcpProxy.Start()
}

// GetActiveConnections returns information about current mappings
func (t *TCPProxy) GetActiveConnections() []ActiveConnection {
	mappings := t.hostMapping.GetAllActive()
	result := make([]ActiveConnection, len(mappings))

	for idx_mapping, host_mapping := range mappings {
		result[idx_mapping] = ActiveConnection{
			Hostname:  host_mapping.Hostname,
			TunnelIP:  host_mapping.TunnelIP,
			RealIP:    host_mapping.RealIP,
			ProfileID: host_mapping.ProfileID,
			Age:       time.Since(host_mapping.ResolvedAt).String(),
		}
	}

	return result
}

// ActiveConnection represents an active transparent proxy connection
type ActiveConnection struct {
	Hostname  string `json:"hostname"`
	TunnelIP  string `json:"tunnelIP"`
	RealIP    string `json:"realIP"`
	ProfileID string `json:"profileId"`
	Age       string `json:"age"`
}

// GetListenerCount returns the number of active listeners
func (t *TCPProxy) GetListenerCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.listeners)
}

// AddListenerForIP dynamically adds listeners for a new IP address
// This is called by DNS proxy when a new hostname gets a unique IP assigned
func (t *TCPProxy) AddListenerForIP(ip string, profileID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Register this IP for the profile
	t.profileForIP[ip] = profileID

	// Start listeners for profile-specific ports on this IP (or global fallback)
	for _, port := range t.getPortsForProfile(profileID) {
		addr := fmt.Sprintf("%s:%d", ip, port)

		// Skip if already listening
		if _, exists := t.listeners[addr]; exists {
			continue
		}

		listener, err := net.Listen("tcp", addr)
		if err != nil {
			if strings.Contains(err.Error(), "cannot assign requested address") {
				// IP doesn't exist on system yet - this is expected
				// The caller should configure the loopback IP first
				return fmt.Errorf("IP %s not configured on system", ip)
			}
			log.Printf("TCP proxy: failed to listen on %s: %v", addr, err)
			continue
		}

		t.listeners[addr] = listener
		t.wg.Add(1)
		go t.acceptLoop(listener, profileID, ip, port)
		log.Printf("TCP proxy: added listener on %s", addr)
	}

	return nil
}

// IsListening checks if a specific address is being listened on
func (t *TCPProxy) IsListening(ip string, port int) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	addr := fmt.Sprintf("%s:%d", ip, port)
	_, exists := t.listeners[addr]
	return exists
}
