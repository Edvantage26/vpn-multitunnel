package ipc

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

// Client represents an IPC client that connects to the Windows service
type Client struct {
	conn net.Conn
	mu   sync.Mutex
}

// NewClient creates a new IPC client
func NewClient() *Client {
	return &Client{}
}

// Connect connects to the service
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil // Already connected
	}

	timeout := 5 * time.Second
	conn, err := winio.DialPipe(PipeName, &timeout)
	if err != nil {
		return fmt.Errorf("failed to connect to service: %w", err)
	}

	c.conn = conn
	return nil
}

// Close closes the connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// IsConnected returns true if connected to the service
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Send sends a request and waits for a response
func (c *Client) Send(req *Request) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("not connected to service")
	}

	// Set deadline for the entire operation
	c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer c.conn.SetDeadline(time.Time{})

	// Encode request
	data, err := req.Encode()
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	// Write message length
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := c.conn.Write(lenBuf); err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("failed to write message length: %w", err)
	}

	// Write message body
	if _, err := c.conn.Write(data); err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("failed to write message body: %w", err)
	}

	// Read response length
	_, err = io.ReadFull(c.conn, lenBuf)
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("failed to read response length: %w", err)
	}

	respLen := binary.BigEndian.Uint32(lenBuf)
	if respLen > 1024*1024 { // Max 1MB message
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("response too large: %d bytes", respLen)
	}

	// Read response body
	respBuf := make([]byte, respLen)
	_, err = io.ReadFull(c.conn, respBuf)
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Decode response
	resp, err := DecodeResponse(respBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp, nil
}

// Ping checks if the service is responding
func (c *Client) Ping() error {
	req := NewRequest(OpPing)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("ping failed: %s", resp.Error)
	}
	return nil
}

// AddLoopbackIP requests the service to add a loopback IP
func (c *Client) AddLoopbackIP(ip string) error {
	req := NewRequest(OpAddLoopbackIP).SetParam("ip", ip)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("add loopback IP failed: %s", resp.Error)
	}
	return nil
}

// RemoveLoopbackIP requests the service to remove a loopback IP
func (c *Client) RemoveLoopbackIP(ip string) error {
	req := NewRequest(OpRemoveLoopbackIP).SetParam("ip", ip)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("remove loopback IP failed: %s", resp.Error)
	}
	return nil
}

// EnsureLoopbackIPs requests the service to ensure multiple loopback IPs exist
func (c *Client) EnsureLoopbackIPs(ips []string) error {
	req := NewRequest(OpEnsureLoopbackIPs).SetParam("ips", ips)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("ensure loopback IPs failed: %s", resp.Error)
	}
	return nil
}

// SetDNS requests the service to set DNS servers for an interface
func (c *Client) SetDNS(interfaceName string, dnsServers []string) error {
	req := NewRequest(OpSetDNS).
		SetParam("interface", interfaceName).
		SetParam("servers", dnsServers)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("set DNS failed: %s", resp.Error)
	}
	return nil
}

// SetDNSv6 requests the service to set IPv6 DNS servers for an interface
func (c *Client) SetDNSv6(interfaceName string, dnsServers []string) error {
	req := NewRequest(OpSetDNSv6).
		SetParam("interface", interfaceName).
		SetParam("servers", dnsServers)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("set DNS v6 failed: %s", resp.Error)
	}
	return nil
}

// ResetDNS requests the service to reset DNS to DHCP
func (c *Client) ResetDNS(interfaceName string) error {
	req := NewRequest(OpResetDNS).SetParam("interface", interfaceName)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("reset DNS failed: %s", resp.Error)
	}
	return nil
}

// ConfigureSystemDNS requests the service to configure system DNS for transparent proxy
func (c *Client) ConfigureSystemDNS(dnsAddress string) error {
	req := NewRequest(OpConfigureSystemDNS).SetParam("address", dnsAddress)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("configure system DNS failed: %s", resp.Error)
	}
	return nil
}

// RestoreSystemDNS requests the service to restore original DNS configuration
func (c *Client) RestoreSystemDNS() error {
	req := NewRequest(OpRestoreSystemDNS)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("restore system DNS failed: %s", resp.Error)
	}
	return nil
}

// StopDNSClient requests the service to stop the DNS Client service
func (c *Client) StopDNSClient() error {
	req := NewRequest(OpStopDNSClient)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("stop DNS client failed: %s", resp.Error)
	}
	return nil
}

// StartDNSClient requests the service to start the DNS Client service
func (c *Client) StartDNSClient() error {
	req := NewRequest(OpStartDNSClient)
	resp, err := c.Send(req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("start DNS client failed: %s", resp.Error)
	}
	return nil
}

// IsServiceRunning checks if the service is available
func IsServiceRunning() bool {
	client := NewClient()
	if err := client.Connect(); err != nil {
		return false
	}
	defer client.Close()

	if err := client.Ping(); err != nil {
		return false
	}
	return true
}
