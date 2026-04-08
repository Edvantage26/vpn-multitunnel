package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/debug"
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

// getPortsForProfile returns the TCP proxy ports for a given profile.
// For the internet profile (__internet__), returns the union of all profile ports
// so that any proxied internet traffic can be captured regardless of destination port.
func (tcp_proxy *TCPProxy) getPortsForProfile(profileID string) []int {
	if ports, exists := tcp_proxy.portsForProfile[profileID]; exists {
		return ports
	}
	if profileID == InternetProfileID {
		return tcp_proxy.getAllUniquePorts()
	}
	return nil
}

// getAllUniquePorts returns the deduplicated union of ports from all profiles
func (tcp_proxy *TCPProxy) getAllUniquePorts() []int {
	seen_ports := make(map[int]bool)
	var unique_ports []int
	for _, profile_ports := range tcp_proxy.portsForProfile {
		for _, port_value := range profile_ports {
			if !seen_ports[port_value] {
				seen_ports[port_value] = true
				unique_ports = append(unique_ports, port_value)
			}
		}
	}
	return unique_ports
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

// InternetProfileID is the special profile ID used for non-tunnel internet traffic
const InternetProfileID = "__internet__"

func (tcp_proxy *TCPProxy) handleConnection(conn net.Conn, profileID string, tunnelIP string, port int) {
	defer conn.Close()

	debug.Debug("proxy", fmt.Sprintf("Connection from %s to %s:%d (profile: %s)", conn.RemoteAddr(), tunnelIP, port, profileID), map[string]any{
		"profileId": profileID,
		"tunnelIP":  tunnelIP,
		"port":      port,
	})

	// Look up the host mapping to find the real destination
	mapping := tcp_proxy.hostMapping.GetByTunnelIP(tunnelIP)
	if mapping == nil {
		debug.Warn("proxy", fmt.Sprintf("No host mapping for tunnel IP %s — mapping may have expired (TTL 30m)", tunnelIP), map[string]any{
			"profileId": profileID,
			"tunnelIP":  tunnelIP,
			"port":      port,
		})
		return
	}

	// Sniff the first bytes to detect TLS SNI or WebSocket upgrade
	protocol_result, sniff_error := ParseConnectionProtocol(conn)
	if sniff_error != nil && protocol_result == nil {
		debug.Warn("proxy", fmt.Sprintf("Protocol sniff failed for %s:%d: %v", mapping.Hostname, port, sniff_error), map[string]any{
			"profileId": profileID,
			"hostname":  mapping.Hostname,
		})
		return
	}

	// Wrap connection to replay prefetched bytes
	var proxied_conn net.Conn = conn
	if protocol_result != nil && len(protocol_result.PrefetchBytes) > 0 {
		proxied_conn = NewPrefixedConn(conn, protocol_result.PrefetchBytes)
	}

	// Build traffic entry for monitoring
	connection_id := uuid.New().String()
	sni_hostname := ""
	detected_hint := ProtocolHintPlain
	if protocol_result != nil {
		sni_hostname = protocol_result.ServerName
		detected_hint = protocol_result.ProtocolHint
	}

	traffic_entry := TrafficEntry{
		ConnectionID: connection_id,
		Hostname:     mapping.Hostname,
		SNIHostname:  sni_hostname,
		TunnelIP:     tunnelIP,
		RealIP:       mapping.RealIP,
		ProfileID:    profileID,
		Port:         port,
		ProtocolHint: detected_hint,
	}
	GetTrafficMonitor().RecordConnectionOpen(traffic_entry)

	// Enable TCP keepalive on the client-side connection to prevent NAT timeouts
	if tcp_conn, is_tcp := conn.(*net.TCPConn); is_tcp {
		tcp_conn.SetKeepAlive(true)
		tcp_conn.SetKeepAlivePeriod(60 * time.Second)
	}

	// Connect to the real destination
	realAddr := fmt.Sprintf("%s:%d", mapping.RealIP, port)
	var remote net.Conn
	var err error
	if profileID == InternetProfileID {
		// Direct internet connection — no tunnel needed
		remote, err = net.DialTimeout("tcp", realAddr, 10*time.Second)
	} else {
		// Route through VPN tunnel
		tunnel := tcp_proxy.tunnelGetter(profileID)
		if tunnel == nil {
			debug.Error("proxy", fmt.Sprintf("Tunnel not connected for profile %s — cannot relay %s:%d", profileID, mapping.Hostname, port), map[string]any{
				"profileId": profileID,
				"hostname":  mapping.Hostname,
				"realAddr":  realAddr,
			})
			GetTrafficMonitor().CloseConnection(connection_id, 0, 0)
			return
		}
		remote, err = tunnel.Dial("tcp", realAddr)
	}
	if err != nil {
		debug.Error("proxy", fmt.Sprintf("Failed to dial %s via %s: %v", realAddr, profileID, err), map[string]any{
			"profileId": profileID,
			"hostname":  mapping.Hostname,
			"realAddr":  realAddr,
		})
		GetTrafficMonitor().CloseConnection(connection_id, 0, 0)
		return
	}
	defer remote.Close()

	// Enable TCP keepalive on the remote-side connection to prevent NAT timeouts
	if tcp_remote, is_tcp := remote.(*net.TCPConn); is_tcp {
		tcp_remote.SetKeepAlive(true)
		tcp_remote.SetKeepAlivePeriod(60 * time.Second)
	}

	route_label := profileID
	if profileID == InternetProfileID {
		route_label = "internet"
	}
	debug.Info("proxy", fmt.Sprintf("Relay %s -> %s:%d via %s [%s]", mapping.Hostname, mapping.RealIP, port, route_label, detected_hint), map[string]any{
		"profileId": profileID,
		"hostname":  mapping.Hostname,
		"realAddr":  realAddr,
		"protocol":  string(detected_hint),
	})

	// Bidirectional relay with byte counting
	tcp_proxy.relay(proxied_conn, remote, connection_id, mapping.Hostname, profileID)
}

func (tcp_proxy *TCPProxy) relay(local, remote net.Conn, connection_id string, hostname string, profileID string) {
	ctx, cancel := context.WithCancel(tcp_proxy.ctx)
	defer cancel()

	bytes_sent_counter := &atomic.Int64{}
	bytes_received_counter := &atomic.Int64{}

	// Track which side closed first
	var close_reason atomic.Value
	close_reason.Store("unknown")

	// Local to remote (upload)
	go func() {
		defer cancel()
		counting_remote := NewCountingWriter(remote, bytes_sent_counter)
		_, copy_err := io.Copy(counting_remote, local)
		if copy_err != nil {
			close_reason.Store(fmt.Sprintf("upload error: %v", copy_err))
		} else {
			close_reason.Store("client closed")
		}
	}()

	// Remote to local (download)
	go func() {
		defer cancel()
		counting_local := NewCountingWriter(local, bytes_received_counter)
		_, copy_err := io.Copy(counting_local, remote)
		if copy_err != nil {
			close_reason.Store(fmt.Sprintf("download error: %v", copy_err))
		} else {
			close_reason.Store("server closed")
		}
	}()

	// Periodic byte count updates: fast initial update (200ms), then every 1 second
	go func() {
		// Fast first update so the UI shows non-zero data quickly
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
			GetTrafficMonitor().UpdateConnectionBytes(
				connection_id,
				bytes_sent_counter.Load(),
				bytes_received_counter.Load(),
			)
		}

		update_ticker := time.NewTicker(1 * time.Second)
		defer update_ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-update_ticker.C:
				GetTrafficMonitor().UpdateConnectionBytes(
					connection_id,
					bytes_sent_counter.Load(),
					bytes_received_counter.Load(),
				)
			}
		}
	}()

	<-ctx.Done()

	final_sent := bytes_sent_counter.Load()
	final_recv := bytes_received_counter.Load()
	reason := close_reason.Load().(string)

	debug.Info("proxy", fmt.Sprintf("Relay closed %s — %s (sent=%d, recv=%d)", hostname, reason, final_sent, final_recv), map[string]any{
		"profileId":    profileID,
		"hostname":     hostname,
		"closeReason":  reason,
		"bytesSent":    final_sent,
		"bytesReceived": final_recv,
	})

	// Final update with exact counts
	GetTrafficMonitor().CloseConnection(
		connection_id,
		final_sent,
		final_recv,
	)
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
