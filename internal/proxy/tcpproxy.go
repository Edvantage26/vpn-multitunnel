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
func (tcp_proxy *TCPProxy) getPortsForProfile(profileID string) []int {
	if ports, exists := tcp_proxy.portsForProfile[profileID]; exists {
		return ports
	}
	return nil
}

// Start starts the TCP proxy, listening on all configured IPs and ports
func (tcp_proxy *TCPProxy) Start() error {
	tcp_proxy.mu.Lock()
	defer tcp_proxy.mu.Unlock()

	if !tcp_proxy.config.Enabled {
		log.Printf("TCP proxy is disabled")
		return nil
	}

	// For each tunnel IP, listen on its configured ports (per-profile or global fallback)
	for profileID, tunnelIP := range tcp_proxy.config.TunnelIPs {
		for _, port := range tcp_proxy.getPortsForProfile(profileID) {
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

			tcp_proxy.listeners[addr] = listener
			tcp_proxy.wg.Add(1)
			go tcp_proxy.acceptLoop(listener, profileID, tunnelIP, port)
		}
	}

	if len(tcp_proxy.listeners) > 0 {
		log.Printf("TCP proxy started with %d listeners", len(tcp_proxy.listeners))
	}

	return nil
}

// Stop stops the TCP proxy
func (tcp_proxy *TCPProxy) Stop() {
	tcp_proxy.cancel()

	tcp_proxy.mu.Lock()
	for addr, listener := range tcp_proxy.listeners {
		listener.Close()
		delete(tcp_proxy.listeners, addr)
	}
	tcp_proxy.mu.Unlock()

	tcp_proxy.wg.Wait()
	log.Printf("TCP proxy stopped")
}

func (tcp_proxy *TCPProxy) acceptLoop(listener net.Listener, profileID string, tunnelIP string, port int) {
	defer tcp_proxy.wg.Done()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-tcp_proxy.ctx.Done():
				return
			default:
				if !strings.Contains(err.Error(), "use of closed network connection") {
					log.Printf("TCP proxy accept error on %s:%d: %v", tunnelIP, port, err)
				}
				return
			}
		}

		go tcp_proxy.handleConnection(conn, profileID, tunnelIP, port)
	}
}

func (tcp_proxy *TCPProxy) handleConnection(conn net.Conn, profileID string, tunnelIP string, port int) {
	defer conn.Close()

	// Get the tunnel for this profile
	tunnel := tcp_proxy.tunnelGetter(profileID)
	if tunnel == nil {
		log.Printf("TCP proxy: tunnel not connected for profile %s", profileID)
		return
	}

	// Look up the host mapping to find the real destination
	mapping := tcp_proxy.hostMapping.GetByTunnelIP(tunnelIP)
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
	tcp_proxy.relay(conn, remote)
}

func (tcp_proxy *TCPProxy) relay(local, remote net.Conn) {
	ctx, cancel := context.WithCancel(tcp_proxy.ctx)
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
func (tcp_proxy *TCPProxy) UpdateConfig(cfg *config.TCPProxy, profilePorts map[string][]int) {
	tcp_proxy.Stop()

	tcp_proxy.mu.Lock()
	tcp_proxy.config = cfg
	tcp_proxy.portsForProfile = profilePorts

	// Rebuild reverse mapping
	tcp_proxy.profileForIP = make(map[string]string)
	for profileID, tunnelIP := range cfg.TunnelIPs {
		tcp_proxy.profileForIP[tunnelIP] = profileID
	}

	tcp_proxy.ctx, tcp_proxy.cancel = context.WithCancel(context.Background())
	tcp_proxy.mu.Unlock()

	tcp_proxy.Start()
}

// GetActiveConnections returns information about current mappings
func (tcp_proxy *TCPProxy) GetActiveConnections() []ActiveConnection {
	mappings := tcp_proxy.hostMapping.GetAllActive()
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
func (tcp_proxy *TCPProxy) GetListenerCount() int {
	tcp_proxy.mu.RLock()
	defer tcp_proxy.mu.RUnlock()
	return len(tcp_proxy.listeners)
}

// AddListenerForIP dynamically adds listeners for a new IP address
// This is called by DNS proxy when a new hostname gets a unique IP assigned
func (tcp_proxy *TCPProxy) AddListenerForIP(ip string, profileID string) error {
	tcp_proxy.mu.Lock()
	defer tcp_proxy.mu.Unlock()

	// Register this IP for the profile
	tcp_proxy.profileForIP[ip] = profileID

	// Start listeners for profile-specific ports on this IP (or global fallback)
	for _, port := range tcp_proxy.getPortsForProfile(profileID) {
		addr := fmt.Sprintf("%s:%d", ip, port)

		// Skip if already listening
		if _, exists := tcp_proxy.listeners[addr]; exists {
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

		tcp_proxy.listeners[addr] = listener
		tcp_proxy.wg.Add(1)
		go tcp_proxy.acceptLoop(listener, profileID, ip, port)
		log.Printf("TCP proxy: added listener on %s", addr)
	}

	return nil
}

// IsListening checks if a specific address is being listened on
func (tcp_proxy *TCPProxy) IsListening(ip string, port int) bool {
	tcp_proxy.mu.RLock()
	defer tcp_proxy.mu.RUnlock()
	addr := fmt.Sprintf("%s:%d", ip, port)
	_, exists := tcp_proxy.listeners[addr]
	return exists
}
