package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"time"
	"unsafe"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"gvisor.dev/gvisor/pkg/tcpip"

	"vpnmultitunnel/internal/config"
)

// Tunnel represents an active WireGuard tunnel using netstack
type Tunnel struct {
	ProfileID string
	Device    *device.Device
	Net       *netstack.Net
	Config    *config.WireGuardConfig

	// Statistics
	Stats TunnelStats
}

// TunnelStats holds tunnel statistics
type TunnelStats struct {
	BytesSent     uint64
	BytesRecv     uint64
	LastHandshake string
	Endpoint      string
	Connected     bool
}

// NewTunnel creates a new userspace WireGuard tunnel
func NewTunnel(profileID string, wgConfig *config.WireGuardConfig) (*Tunnel, error) {
	// Parse addresses
	var localAddresses []netip.Addr
	for _, addrStr := range wgConfig.Interface.Address {
		// Parse address, stripping CIDR notation
		addrStr = strings.Split(addrStr, "/")[0]
		addr, err := netip.ParseAddr(addrStr)
		if err != nil {
			return nil, fmt.Errorf("invalid address %s: %w", addrStr, err)
		}
		localAddresses = append(localAddresses, addr)
	}

	if len(localAddresses) == 0 {
		return nil, fmt.Errorf("no valid addresses in config")
	}

	// Parse DNS servers
	var dnsAddresses []netip.Addr
	for _, dnsStr := range wgConfig.Interface.DNS {
		dns, err := netip.ParseAddr(dnsStr)
		if err != nil {
			continue // Skip invalid DNS
		}
		dnsAddresses = append(dnsAddresses, dns)
	}

	// Set default MTU
	mtu := wgConfig.Interface.MTU
	if mtu == 0 {
		mtu = 1420 // WireGuard default
	}

	// Create netstack TUN device
	tun, tnet, err := netstack.CreateNetTUN(localAddresses, dnsAddresses, mtu)
	if err != nil {
		return nil, fmt.Errorf("failed to create netstack TUN: %w", err)
	}

	// Create WireGuard device
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))

	// Configure the device
	if err := configureDevice(dev, wgConfig); err != nil {
		dev.Close()
		return nil, fmt.Errorf("failed to configure device: %w", err)
	}

	// Bring up the device
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("failed to bring up device: %w", err)
	}

	tunnel := &Tunnel{
		ProfileID: profileID,
		Device:    dev,
		Net:       tnet,
		Config:    wgConfig,
		Stats: TunnelStats{
			Connected: true,
		},
	}

	// Get endpoint for stats
	if len(wgConfig.Peers) > 0 {
		tunnel.Stats.Endpoint = wgConfig.Peers[0].Endpoint
	}

	return tunnel, nil
}

// configureDevice applies the WireGuard configuration to the device
func configureDevice(dev *device.Device, wgConfig *config.WireGuardConfig) error {
	var configLines []string

	// Add private key
	privateKeyBytes, err := base64.StdEncoding.DecodeString(wgConfig.Interface.PrivateKey)
	if err != nil {
		return fmt.Errorf("invalid private key: %w", err)
	}
	configLines = append(configLines, fmt.Sprintf("private_key=%s", hex.EncodeToString(privateKeyBytes)))

	// Add listen port if specified
	if wgConfig.Interface.ListenPort > 0 {
		configLines = append(configLines, fmt.Sprintf("listen_port=%d", wgConfig.Interface.ListenPort))
	}

	// Add peers
	for _, peer := range wgConfig.Peers {
		// Public key
		publicKeyBytes, err := base64.StdEncoding.DecodeString(peer.PublicKey)
		if err != nil {
			return fmt.Errorf("invalid peer public key: %w", err)
		}
		configLines = append(configLines, fmt.Sprintf("public_key=%s", hex.EncodeToString(publicKeyBytes)))

		// Preshared key (optional)
		if peer.PresharedKey != "" {
			pskBytes, err := base64.StdEncoding.DecodeString(peer.PresharedKey)
			if err != nil {
				return fmt.Errorf("invalid preshared key: %w", err)
			}
			configLines = append(configLines, fmt.Sprintf("preshared_key=%s", hex.EncodeToString(pskBytes)))
		}

		// Endpoint — pre-resolve hostname to IP so WireGuard doesn't depend
		// on its internal resolver (which may fail if system DNS points to
		// our proxy and the proxy isn't fully ready yet).
		if peer.Endpoint != "" {
			resolvedEndpoint, resolveErr := preResolveEndpoint(peer.Endpoint)
			if resolveErr != nil {
				return fmt.Errorf("failed to resolve endpoint %s: %w", peer.Endpoint, resolveErr)
			}
			configLines = append(configLines, fmt.Sprintf("endpoint=%s", resolvedEndpoint))
		}

		// Persistent keepalive
		if peer.PersistentKeepalive > 0 {
			configLines = append(configLines, fmt.Sprintf("persistent_keepalive_interval=%d", peer.PersistentKeepalive))
		}

		// Allowed IPs
		for _, allowedIP := range peer.AllowedIPs {
			configLines = append(configLines, fmt.Sprintf("allowed_ip=%s", allowedIP))
		}
	}

	// Apply configuration
	configStr := strings.Join(configLines, "\n")
	if err := dev.IpcSet(configStr); err != nil {
		return fmt.Errorf("failed to apply config: %w", err)
	}

	return nil
}

// Close shuts down the tunnel
func (tunnel *Tunnel) Close() error {
	tunnel.Stats.Connected = false
	if tunnel.Device != nil {
		tunnel.Device.Close()
	}
	return nil
}

// DialTCP creates a TCP connection through the tunnel
func (tunnel *Tunnel) DialTCP(addr string) (net.Conn, error) {
	return tunnel.Net.DialContextTCPAddrPort(nil, netip.MustParseAddrPort(addr))
}

// DialUDP creates a UDP connection through the tunnel
func (tunnel *Tunnel) DialUDP(addr string) (net.Conn, error) {
	return tunnel.Net.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort(addr))
}

// Dial creates a connection through the tunnel
// DNS resolution happens through the tunnel's configured DNS servers
func (tunnel *Tunnel) Dial(network, addr string) (net.Conn, error) {
	if tunnel.Net == nil {
		return nil, fmt.Errorf("tunnel not initialized")
	}

	// Use the netstack's built-in DialContext which handles DNS resolution
	// through the tunnel's configured DNS servers
	dial_ctx, dial_cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer dial_cancel()

	tunnel_conn, dial_err := tunnel.Net.DialContext(dial_ctx, network, addr)
	if dial_err != nil {
		return nil, dial_err
	}

	// Enable TCP keepalive on gVisor connections to prevent stateful
	// firewalls and NATs from dropping idle TCP sessions.
	// gonet.TCPConn doesn't expose SetKeepAlive, so we access the
	// underlying gVisor endpoint via reflect+unsafe.
	if network == "tcp" || network == "tcp4" || network == "tcp6" {
		enableGvisorTCPKeepalive(tunnel_conn, 30*time.Second, 30*time.Second)
	}

	return tunnel_conn, nil
}

// enableGvisorTCPKeepalive enables TCP keepalive on a gVisor gonet.TCPConn
// by accessing the unexported "ep" field (tcpip.Endpoint) via reflect+unsafe.
// This is necessary because gonet.TCPConn does not implement net.TCPConn's
// SetKeepAlive method, causing the standard type assertion to fail silently.
func enableGvisorTCPKeepalive(tunnel_conn net.Conn, keepalive_idle_duration time.Duration, keepalive_interval_duration time.Duration) {
	conn_value := reflect.ValueOf(tunnel_conn)
	if conn_value.Kind() != reflect.Ptr {
		return
	}
	struct_value := conn_value.Elem()
	endpoint_field := struct_value.FieldByName("ep")
	if !endpoint_field.IsValid() || !endpoint_field.CanAddr() {
		return
	}

	// Access the unexported interface field via unsafe to bypass reflect's
	// read-only restriction on unexported fields
	endpoint_pointer := unsafe.Pointer(endpoint_field.UnsafeAddr())
	gvisor_endpoint := *(*tcpip.Endpoint)(endpoint_pointer)
	if gvisor_endpoint == nil {
		return
	}

	// Enable SO_KEEPALIVE and configure idle/interval timers
	gvisor_endpoint.SocketOptions().SetKeepAlive(true)
	keepalive_idle_option := tcpip.KeepaliveIdleOption(keepalive_idle_duration)
	gvisor_endpoint.SetSockOpt(&keepalive_idle_option)
	keepalive_interval_option := tcpip.KeepaliveIntervalOption(keepalive_interval_duration)
	gvisor_endpoint.SetSockOpt(&keepalive_interval_option)

	log.Printf("[tunnel] TCP keepalive enabled on gVisor connection (idle=%s, interval=%s)", keepalive_idle_duration, keepalive_interval_duration)
}

// UpdateStats updates tunnel statistics from the device
func (tunnel *Tunnel) UpdateStats() {
	if tunnel.Device == nil {
		return
	}

	// Get device status via IPC
	stats, err := tunnel.Device.IpcGet()
	if err != nil {
		return
	}

	// Parse stats
	lines := strings.Split(stats, "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]

		switch key {
		case "tx_bytes":
			fmt.Sscanf(value, "%d", &tunnel.Stats.BytesSent)
		case "rx_bytes":
			fmt.Sscanf(value, "%d", &tunnel.Stats.BytesRecv)
		case "last_handshake_time_sec":
			var timestamp int64
			fmt.Sscanf(value, "%d", &timestamp)
			if timestamp > 0 {
				tunnel.Stats.LastHandshake = fmt.Sprintf("%d", timestamp)
			}
		}
	}
}

// GetStats returns the current tunnel statistics
func (tunnel *Tunnel) GetStats() TunnelStats {
	return tunnel.Stats
}

// GetDNSServer returns the DNS server from the WireGuard config
func (tunnel *Tunnel) GetDNSServer() string {
	if tunnel.Config != nil && len(tunnel.Config.Interface.DNS) > 0 {
		return tunnel.Config.Interface.DNS[0]
	}
	return ""
}

// GetAssignedIP returns the tunnel's assigned IP (first Address without CIDR)
func (tunnel *Tunnel) GetAssignedIP() string {
	if tunnel.Config != nil && len(tunnel.Config.Interface.Address) > 0 {
		addrWithCIDR := tunnel.Config.Interface.Address[0]
		if idx := strings.Index(addrWithCIDR, "/"); idx >= 0 {
			return addrWithCIDR[:idx]
		}
		return addrWithCIDR
	}
	return ""
}

// GetNet returns the netstack.Net for this tunnel
func (tunnel *Tunnel) GetNet() *netstack.Net {
	return tunnel.Net
}

// preResolveEndpoint resolves a hostname:port endpoint to ip:port.
// If the host part is already an IP address, it is returned unchanged.
func preResolveEndpoint(endpoint string) (string, error) {
	host, port, splitErr := net.SplitHostPort(endpoint)
	if splitErr != nil {
		return endpoint, nil // not host:port format, pass through
	}

	// If already an IP, no resolution needed
	if net.ParseIP(host) != nil {
		return endpoint, nil
	}

	resolved_ips, lookupErr := net.LookupHost(host)
	if lookupErr != nil {
		return "", fmt.Errorf("DNS lookup failed for %s: %w", host, lookupErr)
	}
	if len(resolved_ips) == 0 {
		return "", fmt.Errorf("no IP addresses found for %s", host)
	}

	return net.JoinHostPort(resolved_ips[0], port), nil
}

// GetDebugInfo returns detailed debug information about the tunnel
func (tunnel *Tunnel) GetDebugInfo() string {
	if tunnel.Device == nil {
		return "Device is nil"
	}

	stats, err := tunnel.Device.IpcGet()
	if err != nil {
		return fmt.Sprintf("Error getting IPC stats: %v", err)
	}

	return stats
}
