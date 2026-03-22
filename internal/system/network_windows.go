package system

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"vpnmultitunnel/internal/ipc"
)

// NetworkConfig manages Windows network configuration for transparent proxy
type NetworkConfig struct {
	originalDNS          map[string][]string // interface -> original IPv4 DNS servers
	originalDNSv6        map[string][]string // interface -> original IPv6 DNS servers
	configuredIPs        []string            // loopback IPs we've added
	dnsConfigured        bool
	dnsProxyAddress      string              // The DNS proxy listen address (e.g., "127.0.0.53")
	dnsFallbackServer    string              // Fallback DNS server (e.g., "8.8.8.8")
	dnsClientWasRunning  bool                // Was DNS Client running before we stopped it?
	dnsClientStopped     bool                // Did we stop the DNS Client service?
	serviceClient        *ipc.Client         // IPC client for service communication
	useService           bool                // Whether to use the service for privileged ops
	mu                   sync.Mutex
}

var (
	instance *NetworkConfig
	once     sync.Once
)

// GetNetworkConfig returns the singleton NetworkConfig instance
func GetNetworkConfig() *NetworkConfig {
	once.Do(func() {
		instance = &NetworkConfig{
			originalDNS:   make(map[string][]string),
			originalDNSv6: make(map[string][]string),
			configuredIPs: []string{},
		}
	})
	return instance
}

// ConnectToService attempts to connect to the VPN MultiTunnel service
// Returns true if connected successfully
func (n *NetworkConfig) ConnectToService() bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.serviceClient != nil && n.useService {
		return true // Already connected
	}

	client := ipc.NewClient()
	if err := client.Connect(); err != nil {
		log.Printf("Service not available: %v", err)
		n.useService = false
		return false
	}

	// Test the connection
	if err := client.Ping(); err != nil {
		log.Printf("Service ping failed: %v", err)
		client.Close()
		n.useService = false
		return false
	}

	n.serviceClient = client
	n.useService = true
	log.Printf("Connected to VPN MultiTunnel service")
	return true
}

// DisconnectFromService closes the service connection
func (n *NetworkConfig) DisconnectFromService() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.serviceClient != nil {
		n.serviceClient.Close()
		n.serviceClient = nil
	}
	n.useService = false
}

// IsServiceConnected returns whether the service is connected
func (n *NetworkConfig) IsServiceConnected() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.useService && n.serviceClient != nil
}

// SetUseService sets whether to use the service for privileged operations
func (n *NetworkConfig) SetUseService(use bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.useService = use
}

// SetDNSProxyAddress sets the DNS proxy listen address
func (n *NetworkConfig) SetDNSProxyAddress(address string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.dnsProxyAddress = address
}

// GetDNSProxyAddress returns the DNS proxy listen address (default: 127.0.0.53)
func (n *NetworkConfig) GetDNSProxyAddress() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dnsProxyAddress == "" {
		return "127.0.0.53"
	}
	return n.dnsProxyAddress
}

// SetDNSFallbackServer sets the fallback DNS server address
func (n *NetworkConfig) SetDNSFallbackServer(server string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.dnsFallbackServer = server
}

// GetDNSFallbackServer returns the fallback DNS server (default: 8.8.8.8)
func (n *NetworkConfig) GetDNSFallbackServer() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dnsFallbackServer == "" {
		return "8.8.8.8"
	}
	return n.dnsFallbackServer
}

// IsAdmin checks if the application is running with administrator privileges
func IsAdmin() bool {
	// Try to execute a command that requires admin
	cmd := exec.Command("net", "session")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	err := cmd.Run()
	return err == nil
}

// hideWindow sets the command to run without showing a console window
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// EnsureLoopbackIPs ensures the required loopback IPs are configured
func (n *NetworkConfig) EnsureLoopbackIPs(ips []string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !IsAdmin() {
		return fmt.Errorf("administrator privileges required to configure loopback IPs")
	}

	for _, ip := range ips {
		if ip == "127.0.0.1" {
			continue // Skip default loopback
		}

		// Check if IP already exists
		if n.loopbackIPExists(ip) {
			log.Printf("Loopback IP %s already configured", ip)
			continue
		}

		// Add the loopback IP
		if err := n.addLoopbackIP(ip); err != nil {
			log.Printf("Failed to add loopback IP %s: %v", ip, err)
			// Continue with other IPs
		} else {
			n.configuredIPs = append(n.configuredIPs, ip)
			log.Printf("Added loopback IP %s", ip)
		}
	}

	return nil
}

// LoopbackIPExists checks if a loopback IP is already configured (public)
func (n *NetworkConfig) LoopbackIPExists(ip string) bool {
	return n.loopbackIPExists(ip)
}

// loopbackIPExists checks if a loopback IP is already configured
func (n *NetworkConfig) loopbackIPExists(ip string) bool {
	// Check cache first
	n.mu.Lock()
	for _, configuredIP := range n.configuredIPs {
		if configuredIP == ip {
			n.mu.Unlock()
			return true
		}
	}
	n.mu.Unlock()

	// Try ping - fastest and most reliable check
	cmd := exec.Command("ping", "-n", "1", "-w", "100", ip)
	hideWindow(cmd)
	err := cmd.Run()
	if err == nil {
		// IP responded, add to cache
		n.mu.Lock()
		n.configuredIPs = append(n.configuredIPs, ip)
		n.mu.Unlock()
		return true
	}

	// Fallback: Use netsh to check if IP exists
	cmd2 := exec.Command("netsh", "interface", "ipv4", "show", "ipaddresses", "interface=Loopback Pseudo-Interface 1")
	hideWindow(cmd2)
	output, err := cmd2.Output()
	if err != nil {
		return false
	}
	exists := strings.Contains(string(output), ip)
	if exists {
		n.mu.Lock()
		n.configuredIPs = append(n.configuredIPs, ip)
		n.mu.Unlock()
	}
	return exists
}

// addLoopbackIP adds a loopback IP address
func (n *NetworkConfig) addLoopbackIP(ip string) error {
	// netsh interface ipv4 add address "Loopback Pseudo-Interface 1" <ip> 255.255.255.0
	cmd := exec.Command("netsh", "interface", "ipv4", "add", "address",
		"Loopback Pseudo-Interface 1", ip, "255.255.255.0")
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh error: %v - %s", err, string(output))
	}
	return nil
}

// RemoveLoopbackIP removes a loopback IP address
func (n *NetworkConfig) RemoveLoopbackIP(ip string) error {
	if ip == "127.0.0.1" {
		return nil // Never remove default loopback
	}

	cmd := exec.Command("netsh", "interface", "ipv4", "delete", "address",
		"Loopback Pseudo-Interface 1", ip)
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh error: %v - %s", err, string(output))
	}
	return nil
}

// CleanupLoopbackIPs removes all loopback IPs we added
func (n *NetworkConfig) CleanupLoopbackIPs() {
	n.mu.Lock()
	defer n.mu.Unlock()

	for _, ip := range n.configuredIPs {
		if err := n.RemoveLoopbackIP(ip); err != nil {
			log.Printf("Failed to remove loopback IP %s: %v", ip, err)
		} else {
			log.Printf("Removed loopback IP %s", ip)
		}
	}
	n.configuredIPs = []string{}
}

// GetActiveNetworkInterface returns the name of the primary network interface
func (n *NetworkConfig) GetActiveNetworkInterface() (string, error) {
	// Use PowerShell to get the active interface
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		"Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Select-Object -ExpandProperty InterfaceAlias | Select-Object -First 1")
	hideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get active interface: %v", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetCurrentDNS gets the current IPv4 DNS servers for an interface
func (n *NetworkConfig) GetCurrentDNS(interfaceName string) ([]string, error) {
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("(Get-DnsClientServerAddress -InterfaceAlias '%s' -AddressFamily IPv4).ServerAddresses -join ','", interfaceName))
	hideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS servers: %v", err)
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return []string{}, nil
	}
	return strings.Split(result, ","), nil
}

// GetCurrentDNSv6 gets the current IPv6 DNS servers for an interface
func (n *NetworkConfig) GetCurrentDNSv6(interfaceName string) ([]string, error) {
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("(Get-DnsClientServerAddress -InterfaceAlias '%s' -AddressFamily IPv6).ServerAddresses -join ','", interfaceName))
	hideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get IPv6 DNS servers: %v", err)
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return []string{}, nil
	}
	return strings.Split(result, ","), nil
}

// SetDNS sets the IPv4 DNS servers for an interface
func (n *NetworkConfig) SetDNS(interfaceName string, dnsServers []string) error {
	// Try to use the service first
	n.mu.Lock()
	useService := n.useService && n.serviceClient != nil
	client := n.serviceClient
	n.mu.Unlock()

	if useService {
		log.Printf("Setting DNS for %s via service: %v", interfaceName, dnsServers)
		if err := client.SetDNS(interfaceName, dnsServers); err != nil {
			log.Printf("Service call failed: %v", err)
			return fmt.Errorf("failed to set DNS via service: %w", err)
		}
		return nil
	}

	// Fallback to local PowerShell (requires admin)
	if !IsAdmin() {
		return fmt.Errorf("administrator privileges required to configure DNS")
	}

	dnsString := strings.Join(dnsServers, ",")
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s", interfaceName, dnsString))
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set DNS: %v - %s", err, string(output))
	}
	return nil
}

// SetDNSv6 sets the IPv6 DNS servers for an interface
func (n *NetworkConfig) SetDNSv6(interfaceName string, dnsServers []string) error {
	// Try to use the service first
	n.mu.Lock()
	useService := n.useService && n.serviceClient != nil
	client := n.serviceClient
	n.mu.Unlock()

	if useService {
		log.Printf("Setting IPv6 DNS for %s via service: %v", interfaceName, dnsServers)
		if err := client.SetDNSv6(interfaceName, dnsServers); err != nil {
			log.Printf("Service call failed: %v", err)
			return fmt.Errorf("failed to set IPv6 DNS via service: %w", err)
		}
		return nil
	}

	// Fallback to local PowerShell (requires admin)
	if !IsAdmin() {
		return fmt.Errorf("administrator privileges required to configure DNS")
	}

	dnsString := strings.Join(dnsServers, ",")
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s", interfaceName, dnsString))
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set IPv6 DNS: %v - %s", err, string(output))
	}
	return nil
}

// ResetDNS resets DNS to automatic (DHCP)
func (n *NetworkConfig) ResetDNS(interfaceName string) error {
	// Try to use the service first
	n.mu.Lock()
	useService := n.useService && n.serviceClient != nil
	client := n.serviceClient
	n.mu.Unlock()

	if useService {
		log.Printf("Resetting DNS for %s via service", interfaceName)
		if err := client.ResetDNS(interfaceName); err != nil {
			log.Printf("Service call failed: %v", err)
			return fmt.Errorf("failed to reset DNS via service: %w", err)
		}
		return nil
	}

	// Fallback to local PowerShell (requires admin)
	if !IsAdmin() {
		return fmt.Errorf("administrator privileges required to configure DNS")
	}

	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ResetServerAddresses", interfaceName))
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reset DNS: %v - %s", err, string(output))
	}
	return nil
}

// ConfigureSystemDNS configures the system to use our DNS proxy (both IPv4 and IPv6) with UAC elevation
func (n *NetworkConfig) ConfigureSystemDNS(dnsProxyAddress string) error {
	n.mu.Lock()

	if n.dnsConfigured {
		n.mu.Unlock()
		return nil // Already configured
	}

	// Get active interface
	interfaceName, err := n.GetActiveNetworkInterface()
	if err != nil {
		n.mu.Unlock()
		return fmt.Errorf("failed to get active interface: %w", err)
	}

	// Save original IPv4 DNS
	originalDNS, err := n.GetCurrentDNS(interfaceName)
	if err != nil {
		log.Printf("Warning: could not get original DNS: %v", err)
		originalDNS = []string{"8.8.8.8"} // Fallback
	}
	n.originalDNS[interfaceName] = originalDNS

	// Save original IPv6 DNS
	originalDNSv6, err := n.GetCurrentDNSv6(interfaceName)
	if err != nil {
		log.Printf("Warning: could not get original IPv6 DNS: %v", err)
		originalDNSv6 = []string{} // No fallback for IPv6
	}
	n.originalDNSv6[interfaceName] = originalDNSv6

	// Check if we can use the service
	useService := n.useService && n.serviceClient != nil
	client := n.serviceClient

	n.mu.Unlock()

	// Try to use the service first (no UAC prompt)
	if useService {
		log.Printf("Configuring system DNS via service: %s", dnsProxyAddress)
		if err := client.ConfigureSystemDNS(dnsProxyAddress); err != nil {
			log.Printf("Warning: service ConfigureSystemDNS failed: %v, falling back to UAC", err)
		} else {
			n.mu.Lock()
			n.dnsConfigured = true
			n.mu.Unlock()
			log.Printf("Configured system DNS to %s via service (original IPv4: %v, IPv6: %v)", dnsProxyAddress, originalDNS, originalDNSv6)
			return nil
		}
	}

	// Fallback: Build PowerShell command to set both IPv4 and IPv6 DNS (with fallback DNS)
	cmd := fmt.Sprintf(
		"Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s,8.8.8.8; Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses ::1,2001:4860:4860::8888",
		interfaceName, dnsProxyAddress, interfaceName)
	args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, cmd)

	log.Printf("Configuring DNS with elevation (fallback): %s", cmd)
	if err := RunElevated("powershell", args); err != nil {
		return fmt.Errorf("failed to configure DNS: %w", err)
	}

	n.mu.Lock()
	n.dnsConfigured = true
	n.mu.Unlock()

	log.Printf("Configured system DNS to %s / ::1 (original IPv4: %v, IPv6: %v)", dnsProxyAddress, originalDNS, originalDNSv6)
	return nil
}

// RestoreSystemDNS restores the original DNS configuration (both IPv4 and IPv6) with UAC elevation
// If no original DNS was saved (e.g., app restarted), it resets to DHCP
func (n *NetworkConfig) RestoreSystemDNS() error {
	n.mu.Lock()

	// Check if we have original DNS saved
	hasOriginal := n.dnsConfigured && (len(n.originalDNS) > 0 || len(n.originalDNSv6) > 0)
	dnsProxyAddr := n.dnsProxyAddress
	if dnsProxyAddr == "" {
		dnsProxyAddr = "127.0.0.53"
	}

	// If no original saved, check if system DNS is currently our proxy address and reset to DHCP
	if !hasOriginal {
		n.mu.Unlock()

		// Get active interface
		interfaceName, err := n.GetActiveNetworkInterface()
		if err != nil {
			return fmt.Errorf("failed to get active interface: %w", err)
		}

		// Check if DNS is currently our proxy address
		currentDNS, _ := n.GetCurrentDNS(interfaceName)
		if len(currentDNS) == 0 || currentDNS[0] != dnsProxyAddr {
			return nil // DNS is not configured by us, nothing to restore
		}

		// Reset to DHCP
		cmd := fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ResetServerAddresses", interfaceName)
		args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, cmd)

		log.Printf("Resetting DNS to DHCP for %s", interfaceName)
		if err := RunElevated("powershell", args); err != nil {
			return fmt.Errorf("failed to reset DNS: %w", err)
		}

		log.Printf("DNS reset to DHCP")
		return nil
	}

	// Build PowerShell commands for all interfaces
	var commands []string

	// Restore IPv4 DNS
	for interfaceName, originalDNS := range n.originalDNS {
		if len(originalDNS) == 0 {
			// Reset to DHCP
			commands = append(commands, fmt.Sprintf(
				"Set-DnsClientServerAddress -InterfaceAlias '%s' -ResetServerAddresses",
				interfaceName))
		} else {
			// Restore original DNS
			dnsString := strings.Join(originalDNS, ",")
			commands = append(commands, fmt.Sprintf(
				"Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s",
				interfaceName, dnsString))
		}
	}

	// Restore IPv6 DNS
	for interfaceName, originalDNSv6 := range n.originalDNSv6 {
		if len(originalDNSv6) == 0 {
			// Reset IPv6 to DHCP - use a different approach
			commands = append(commands, fmt.Sprintf(
				"Set-DnsClientServerAddress -InterfaceAlias '%s' -ResetServerAddresses",
				interfaceName))
		} else {
			// Restore original IPv6 DNS
			dnsString := strings.Join(originalDNSv6, ",")
			commands = append(commands, fmt.Sprintf(
				"Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s",
				interfaceName, dnsString))
		}
	}

	n.mu.Unlock()

	if len(commands) == 0 {
		n.mu.Lock()
		n.originalDNS = make(map[string][]string)
		n.originalDNSv6 = make(map[string][]string)
		n.dnsConfigured = false
		n.mu.Unlock()
		return nil
	}

	// Execute all commands with elevation
	fullCommand := strings.Join(commands, "; ")
	args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, fullCommand)

	log.Printf("Restoring DNS with elevation: %s", fullCommand)
	if err := RunElevated("powershell", args); err != nil {
		return fmt.Errorf("failed to restore DNS: %w", err)
	}

	n.mu.Lock()
	n.originalDNS = make(map[string][]string)
	n.originalDNSv6 = make(map[string][]string)
	n.dnsConfigured = false
	n.mu.Unlock()

	log.Printf("DNS configuration restored")
	return nil
}

// IsDNSConfigured returns whether DNS has been configured by us
func (n *NetworkConfig) IsDNSConfigured() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.dnsConfigured
}

// IsDNSClientStopped returns whether we stopped the DNS Client service
func (n *NetworkConfig) IsDNSClientStopped() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.dnsClientStopped
}

// IsTransparentDNSConfigured checks if the system DNS is pointing to our DNS proxy
// This checks the actual system state, not just the runtime flag
func (n *NetworkConfig) IsTransparentDNSConfigured() bool {
	// First check runtime flag
	n.mu.Lock()
	if n.dnsConfigured {
		n.mu.Unlock()
		return true
	}
	n.mu.Unlock()

	// Check actual system DNS
	interfaceName, err := n.GetActiveNetworkInterface()
	if err != nil {
		return false
	}

	dnsServers, err := n.GetCurrentDNS(interfaceName)
	if err != nil || len(dnsServers) == 0 {
		return false
	}

	// Check if primary DNS is our DNS proxy address
	dnsProxyAddr := n.GetDNSProxyAddress()
	return dnsServers[0] == dnsProxyAddr
}

// FlushDNSCache flushes the Windows DNS cache
func FlushDNSCache() error {
	cmd := exec.Command("ipconfig", "/flushdns")
	hideWindow(cmd)
	return cmd.Run()
}

// StopDNSClientService stops and disables the Windows DNS Client service (requires elevation or service)
func StopDNSClientService() error {
	// Try to use the service first
	n := GetNetworkConfig()
	n.mu.Lock()
	useService := n.useService && n.serviceClient != nil
	client := n.serviceClient
	n.mu.Unlock()

	if useService {
		log.Printf("Stopping DNS Client service via VPN MultiTunnel service...")
		if err := client.StopDNSClient(); err != nil {
			log.Printf("Service call failed: %v", err)
			return fmt.Errorf("failed to stop DNS Client via service: %w", err)
		}
		log.Printf("DNS Client service stopped via service")
		return nil
	}

	// No service available - this shouldn't happen in normal operation
	log.Printf("Warning: VPN MultiTunnel service not available, DNS Client cannot be stopped without UAC")
	return fmt.Errorf("service not available to stop DNS Client")
}

// StartDNSClientService re-enables and starts the Windows DNS Client service (requires elevation or service)
func StartDNSClientService() error {
	// Try to use the service first
	n := GetNetworkConfig()
	n.mu.Lock()
	useService := n.useService && n.serviceClient != nil
	client := n.serviceClient
	n.mu.Unlock()

	if useService {
		log.Printf("Starting DNS Client service via VPN MultiTunnel service...")
		if err := client.StartDNSClient(); err != nil {
			log.Printf("Service call failed: %v", err)
			return fmt.Errorf("failed to start DNS Client via service: %w", err)
		}
		log.Printf("DNS Client service started via service")
		return nil
	}

	// No service available - this shouldn't happen in normal operation
	log.Printf("Warning: VPN MultiTunnel service not available, DNS Client cannot be started without UAC")
	return fmt.Errorf("service not available to start DNS Client")
}

// IsDNSClientRunning checks if the DNS Client service is running
func IsDNSClientRunning() bool {
	// Use sc query instead of PowerShell (no console window)
	cmd := exec.Command("sc", "query", "Dnscache")
	hideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "RUNNING")
}

// SetupTransparentDNS configures the system for transparent DNS proxy
// Sets system DNS to our proxy address (default: 127.0.0.53)
// Using a different loopback IP avoids conflicts with Windows DNS Client on 127.0.0.1:53
func (n *NetworkConfig) SetupTransparentDNS() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.dnsConfigured {
		log.Printf("Transparent DNS already configured")
		return nil
	}

	// Get DNS proxy address
	dnsProxyAddr := n.dnsProxyAddress
	if dnsProxyAddr == "" {
		dnsProxyAddr = "127.0.0.53"
	}

	// Ensure the loopback IP exists (e.g., 127.0.0.53) so the system resolver can reach it
	if dnsProxyAddr != "127.0.0.1" {
		log.Printf("Ensuring loopback IP %s exists for DNS proxy...", dnsProxyAddr)
		n.mu.Unlock()
		if err := n.AddLoopbackIPElevated(dnsProxyAddr); err != nil {
			log.Printf("Warning: could not add loopback IP %s: %v (may already exist)", dnsProxyAddr, err)
		}
		n.mu.Lock()
	}

	// Get active interface
	interfaceName, err := n.GetActiveNetworkInterface()
	if err != nil {
		return fmt.Errorf("failed to get active interface: %w", err)
	}

	// Save original DNS
	n.mu.Unlock()
	originalDNS, err := n.GetCurrentDNS(interfaceName)
	n.mu.Lock()
	if err != nil {
		log.Printf("Warning: could not get original DNS: %v", err)
		fallback_dns_server := n.dnsFallbackServer
		if fallback_dns_server == "" {
			fallback_dns_server = "8.8.8.8"
		}
		originalDNS = []string{fallback_dns_server}
	}
	n.originalDNS[interfaceName] = originalDNS

	// Save original IPv6 DNS
	n.mu.Unlock()
	originalDNSv6, err := n.GetCurrentDNSv6(interfaceName)
	n.mu.Lock()
	if err != nil {
		log.Printf("Warning: could not get original IPv6 DNS: %v", err)
	}
	n.originalDNSv6[interfaceName] = originalDNSv6

	// Set IPv4 DNS to our proxy address with configurable fallback
	fallback_server := n.dnsFallbackServer
	if fallback_server == "" {
		fallback_server = "8.8.8.8"
	}
	n.mu.Unlock()
	err = n.SetDNS(interfaceName, []string{dnsProxyAddr, fallback_server})
	n.mu.Lock()
	if err != nil {
		return fmt.Errorf("failed to set DNS: %w", err)
	}

	// Set IPv6 DNS to ::1 (our proxy also listens there)
	// This prevents Windows from preferring IPv6 DNS (e.g., fe80::1) over our proxy
	n.mu.Unlock()
	if set_ipv6_err := n.SetDNSv6(interfaceName, []string{"::1"}); set_ipv6_err != nil {
		log.Printf("Warning: could not set IPv6 DNS to ::1: %v", set_ipv6_err)
	}
	n.mu.Lock()

	n.dnsConfigured = true
	log.Printf("Transparent DNS configured: system DNS set to %s,8.8.8.8 + ::1 (original IPv4: %v, IPv6: %v)", dnsProxyAddr, originalDNS, originalDNSv6)
	return nil
}

// setDNSElevated sets DNS with UAC elevation
func (n *NetworkConfig) setDNSElevated(interfaceName, dnsAddress string) error {
	psCommand := fmt.Sprintf(`Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s`, interfaceName, dnsAddress)
	args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, psCommand)

	return RunElevated("powershell", args)
}

// RestoreTransparentDNS restores the original DNS configuration and restarts DNS Client
func (n *NetworkConfig) RestoreTransparentDNS() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.dnsConfigured {
		return nil // Nothing to restore
	}

	var lastErr error

	// Restore original DNS for each interface
	// SetDNS and ResetDNS will use the service if available
	for interfaceName, originalDNS := range n.originalDNS {
		if len(originalDNS) == 0 {
			// Reset to DHCP
			n.mu.Unlock()
			if err := n.ResetDNS(interfaceName); err != nil {
				log.Printf("Failed to reset DNS for %s: %v", interfaceName, err)
				lastErr = err
			} else {
				log.Printf("Reset DNS to DHCP for %s", interfaceName)
			}
			n.mu.Lock()
		} else {
			// Restore original DNS
			n.mu.Unlock()
			if err := n.SetDNS(interfaceName, originalDNS); err != nil {
				log.Printf("Failed to restore DNS for %s: %v", interfaceName, err)
				lastErr = err
			} else {
				log.Printf("Restored DNS to %v for %s", originalDNS, interfaceName)
			}
			n.mu.Lock()
		}
	}

	// Restore original IPv6 DNS for each interface
	for interfaceName, originalDNSv6 := range n.originalDNSv6 {
		n.mu.Unlock()
		if len(originalDNSv6) == 0 {
			// Reset IPv6 DNS to automatic (same as ResetDNS but for IPv6)
			if reset_ipv6_err := n.ResetDNS(interfaceName); reset_ipv6_err != nil {
				log.Printf("Warning: could not reset IPv6 DNS for %s: %v", interfaceName, reset_ipv6_err)
			}
		} else {
			if restore_ipv6_err := n.SetDNSv6(interfaceName, originalDNSv6); restore_ipv6_err != nil {
				log.Printf("Warning: could not restore IPv6 DNS for %s: %v", interfaceName, restore_ipv6_err)
			}
		}
		n.mu.Lock()
	}

	// Restart DNS Client if we stopped it
	if n.dnsClientStopped && n.dnsClientWasRunning {
		n.mu.Unlock()
		if err := StartDNSClientService(); err != nil {
			log.Printf("Failed to restart DNS Client service: %v", err)
			lastErr = err
		}
		n.mu.Lock()
		n.dnsClientStopped = false
	}

	n.originalDNS = make(map[string][]string)
	n.originalDNSv6 = make(map[string][]string)
	n.dnsConfigured = false
	log.Printf("Transparent DNS restored")
	return lastErr
}

// RunElevated runs a command with UAC elevation (prompts user)
func RunElevated(program string, args string) error {
	verb := "runas"

	verbPtr, _ := syscall.UTF16PtrFromString(verb)
	programPtr, _ := syscall.UTF16PtrFromString(program)
	argsPtr, _ := syscall.UTF16PtrFromString(args)
	workDirPtr, _ := syscall.UTF16PtrFromString("")

	// Load shell32.dll
	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecute := shell32.NewProc("ShellExecuteW")

	const SW_SHOWNORMAL = 1 // Show window normally for UAC prompt

	ret, _, err := shellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(programPtr)),
		uintptr(unsafe.Pointer(argsPtr)),
		uintptr(unsafe.Pointer(workDirPtr)),
		SW_SHOWNORMAL,
	)

	if ret <= 32 {
		return fmt.Errorf("ShellExecute failed: %v", err)
	}
	return nil
}

// AddLoopbackIPElevated adds a loopback IP with UAC elevation or service
func (n *NetworkConfig) AddLoopbackIPElevated(ip string) error {
	if ip == "127.0.0.1" {
		return nil
	}

	// Check if already exists
	if n.loopbackIPExists(ip) {
		log.Printf("Loopback IP %s already exists", ip)
		return nil
	}

	// Try to use the service first
	n.mu.Lock()
	useService := n.useService && n.serviceClient != nil
	client := n.serviceClient
	n.mu.Unlock()

	if useService {
		log.Printf("Adding loopback IP %s via service...", ip)
		if err := client.AddLoopbackIP(ip); err != nil {
			log.Printf("Service call failed, falling back to UAC: %v", err)
		} else {
			n.mu.Lock()
			n.configuredIPs = append(n.configuredIPs, ip)
			n.mu.Unlock()
			log.Printf("Added loopback IP %s via service", ip)
			return nil
		}
	}

	// Fallback to UAC elevation
	netshCmd := fmt.Sprintf(`netsh interface ipv4 add address "Loopback Pseudo-Interface 1" %s 255.255.255.0`, ip)
	args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, netshCmd)

	log.Printf("Requesting elevation to add loopback IP %s...", ip)
	if err := RunElevated("powershell", args); err != nil {
		return fmt.Errorf("failed to add loopback IP: %w", err)
	}

	n.mu.Lock()
	n.configuredIPs = append(n.configuredIPs, ip)
	n.mu.Unlock()

	log.Printf("Added loopback IP %s", ip)
	return nil
}

// RemoveLoopbackIPElevated removes a loopback IP with UAC elevation or service
func (network_config *NetworkConfig) RemoveLoopbackIPElevated(ip string) error {
	if ip == "127.0.0.1" {
		return nil
	}

	// Check if it actually exists before trying to remove
	if !network_config.loopbackIPExists(ip) {
		log.Printf("Loopback IP %s does not exist, nothing to remove", ip)
		return nil
	}

	// Try to use the service first
	network_config.mu.Lock()
	use_service := network_config.useService && network_config.serviceClient != nil
	service_client := network_config.serviceClient
	network_config.mu.Unlock()

	if use_service {
		log.Printf("Removing loopback IP %s via service...", ip)
		if err := service_client.RemoveLoopbackIP(ip); err != nil {
			log.Printf("Service call failed, falling back to UAC: %v", err)
		} else {
			log.Printf("Removed loopback IP %s via service", ip)
			return nil
		}
	}

	// Fallback to UAC elevation
	netsh_cmd := fmt.Sprintf(`netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" %s`, ip)
	powershell_args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, netsh_cmd)

	log.Printf("Requesting elevation to remove loopback IP %s...", ip)
	if err := RunElevated("powershell", powershell_args); err != nil {
		return fmt.Errorf("failed to remove loopback IP: %w", err)
	}

	log.Printf("Removed loopback IP %s", ip)
	return nil
}

// ConfigureSystemDNSElevated configures DNS with UAC elevation or service
func (n *NetworkConfig) ConfigureSystemDNSElevated(dnsAddress string) error {
	n.mu.Lock()
	if n.dnsConfigured {
		n.mu.Unlock()
		return nil
	}

	useService := n.useService && n.serviceClient != nil
	client := n.serviceClient
	n.mu.Unlock()

	// Get active interface
	interfaceName, err := n.GetActiveNetworkInterface()
	if err != nil {
		return fmt.Errorf("failed to get active interface: %w", err)
	}

	// Save original DNS
	originalDNS, err := n.GetCurrentDNS(interfaceName)
	if err != nil {
		log.Printf("Warning: could not get original DNS: %v", err)
		originalDNS = []string{"8.8.8.8"}
	}

	n.mu.Lock()
	n.originalDNS[interfaceName] = originalDNS
	n.mu.Unlock()

	// Try to use the service first
	if useService {
		log.Printf("Configuring system DNS to %s via service...", dnsAddress)
		if err := client.ConfigureSystemDNS(dnsAddress); err != nil {
			log.Printf("Service call failed, falling back to UAC: %v", err)
		} else {
			n.mu.Lock()
			n.dnsConfigured = true
			n.mu.Unlock()
			log.Printf("Configured system DNS to %s via service", dnsAddress)
			return nil
		}
	}

	// Fallback to UAC elevation (with fallback DNS)
	psCommand := fmt.Sprintf(`Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s,8.8.8.8`, interfaceName, dnsAddress)
	args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, psCommand)

	log.Printf("Requesting elevation to configure DNS...")
	if err := RunElevated("powershell", args); err != nil {
		return fmt.Errorf("failed to set DNS: %w", err)
	}

	n.mu.Lock()
	n.dnsConfigured = true
	n.mu.Unlock()
	log.Printf("Configured system DNS to %s,8.8.8.8", dnsAddress)
	return nil
}
