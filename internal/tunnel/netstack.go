package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

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

		// Endpoint
		if peer.Endpoint != "" {
			configLines = append(configLines, fmt.Sprintf("endpoint=%s", peer.Endpoint))
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
func (t *Tunnel) Close() error {
	t.Stats.Connected = false
	if t.Device != nil {
		t.Device.Close()
	}
	return nil
}

// DialTCP creates a TCP connection through the tunnel
func (t *Tunnel) DialTCP(addr string) (net.Conn, error) {
	return t.Net.DialContextTCPAddrPort(nil, netip.MustParseAddrPort(addr))
}

// DialUDP creates a UDP connection through the tunnel
func (t *Tunnel) DialUDP(addr string) (net.Conn, error) {
	return t.Net.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort(addr))
}

// Dial creates a connection through the tunnel
// DNS resolution happens through the tunnel's configured DNS servers
func (t *Tunnel) Dial(network, addr string) (net.Conn, error) {
	if t.Net == nil {
		return nil, fmt.Errorf("tunnel not initialized")
	}

	// Use the netstack's built-in DialContext which handles DNS resolution
	// through the tunnel's configured DNS servers
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return t.Net.DialContext(ctx, network, addr)
}

// UpdateStats updates tunnel statistics from the device
func (t *Tunnel) UpdateStats() {
	if t.Device == nil {
		return
	}

	// Get device status via IPC
	stats, err := t.Device.IpcGet()
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
			fmt.Sscanf(value, "%d", &t.Stats.BytesSent)
		case "rx_bytes":
			fmt.Sscanf(value, "%d", &t.Stats.BytesRecv)
		case "last_handshake_time_sec":
			var timestamp int64
			fmt.Sscanf(value, "%d", &timestamp)
			if timestamp > 0 {
				t.Stats.LastHandshake = fmt.Sprintf("%d", timestamp)
			}
		}
	}
}

// GetNet returns the netstack.Net for this tunnel
func (t *Tunnel) GetNet() *netstack.Net {
	return t.Net
}

// GetDebugInfo returns detailed debug information about the tunnel
func (t *Tunnel) GetDebugInfo() string {
	if t.Device == nil {
		return "Device is nil"
	}

	stats, err := t.Device.IpcGet()
	if err != nil {
		return fmt.Sprintf("Error getting IPC stats: %v", err)
	}

	return stats
}
