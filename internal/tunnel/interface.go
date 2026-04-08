package tunnel

import "net"

// VPNTunnel is the protocol-agnostic interface that all tunnel implementations must satisfy.
// WireGuard (netstack), OpenVPN (subprocess), and WatchGuard SSL (subprocess) all implement this.
type VPNTunnel interface {
	// Dial creates a connection through the tunnel (satisfies proxy.TunnelDialer)
	Dial(network, addr string) (net.Conn, error)

	// Close shuts down the tunnel and releases all resources
	Close() error

	// UpdateStats refreshes tunnel statistics (bytes sent/received, handshake, etc.)
	UpdateStats()

	// GetStats returns the current tunnel statistics
	GetStats() TunnelStats

	// GetDebugInfo returns protocol-specific debug information
	GetDebugInfo() string

	// GetDNSServer returns the DNS server IP configured for this tunnel
	GetDNSServer() string

	// GetAssignedIP returns the IP address assigned to this tunnel endpoint
	GetAssignedIP() string
}
