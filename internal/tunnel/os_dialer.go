package tunnel

import (
	"context"
	"fmt"
	"net"
	"time"
)

// OSDialer implements TunnelDialer by binding outgoing connections to a specific
// local IP address (the VPN adapter's assigned IP). The OS routing table then
// directs the traffic through the corresponding VPN tunnel.
//
// This is used by OpenVPN and WatchGuard SSL tunnels, which create OS-level
// network adapters rather than userspace tunnels.
type OSDialer struct {
	LocalIP       net.IP
	DNSServerAddr string
	AssignedAddr  string // The VPN-assigned IP address (may include CIDR)
}

// Dial creates a connection through the VPN tunnel by binding to the VPN adapter's IP.
// The OS routes traffic through the VPN because the source IP matches the VPN adapter.
func (os_dialer *OSDialer) Dial(network, addr string) (net.Conn, error) {
	if os_dialer.LocalIP == nil {
		return nil, fmt.Errorf("OS dialer has no local IP configured")
	}

	var localAddr net.Addr
	switch network {
	case "tcp", "tcp4", "tcp6":
		localAddr = &net.TCPAddr{IP: os_dialer.LocalIP}
	case "udp", "udp4", "udp6":
		localAddr = &net.UDPAddr{IP: os_dialer.LocalIP}
	default:
		localAddr = &net.TCPAddr{IP: os_dialer.LocalIP}
	}

	dialer := &net.Dialer{
		LocalAddr: localAddr,
		Timeout:   15 * time.Second,
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer dialCancel()

	return dialer.DialContext(dialCtx, network, addr)
}

// GetDNSServer returns the DNS server address for this tunnel
func (os_dialer *OSDialer) GetDNSServer() string {
	return os_dialer.DNSServerAddr
}

// GetAssignedIP returns the VPN-assigned IP address
func (os_dialer *OSDialer) GetAssignedIP() string {
	return os_dialer.AssignedAddr
}
