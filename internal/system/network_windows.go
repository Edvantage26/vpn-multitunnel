package system

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

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
func (network_config *NetworkConfig) ConnectToService() bool {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()

	if network_config.serviceClient != nil && network_config.useService {
		return true // Already connected
	}

	client := ipc.NewClient()
	if err := client.Connect(); err != nil {
		log.Printf("Service not available: %v", err)
		network_config.useService = false
		return false
	}

	// Test the connection
	if err := client.Ping(); err != nil {
		log.Printf("Service ping failed: %v", err)
		client.Close()
		network_config.useService = false
		return false
	}

	network_config.serviceClient = client
	network_config.useService = true
	log.Printf("Connected to VPN MultiTunnel service")
	return true
}

// DisconnectFromService closes the service connection
func (network_config *NetworkConfig) DisconnectFromService() {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()

	if network_config.serviceClient != nil {
		network_config.serviceClient.Close()
		network_config.serviceClient = nil
	}
	network_config.useService = false
}

// IsServiceConnected returns whether the service is connected
func (network_config *NetworkConfig) IsServiceConnected() bool {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	return network_config.useService && network_config.serviceClient != nil
}

// InstallMSI installs an MSI package via the Windows service (runs as SYSTEM, no UAC)
func (network_config *NetworkConfig) InstallMSI(msiPath string, components string) error {
	network_config.mu.Lock()
	service_client := network_config.serviceClient
	network_config.mu.Unlock()

	if service_client == nil {
		return fmt.Errorf("service not connected")
	}
	return service_client.InstallMSI(msiPath, components)
}

// UninstallMSI uninstalls an MSI package via the Windows service (runs as SYSTEM, no UAC)
func (network_config *NetworkConfig) UninstallMSI(productCode string) error {
	network_config.mu.Lock()
	service_client := network_config.serviceClient
	network_config.mu.Unlock()

	if service_client == nil {
		return fmt.Errorf("service not connected")
	}
	return service_client.UninstallMSI(productCode)
}

// SetUseService sets whether to use the service for privileged operations
func (network_config *NetworkConfig) SetUseService(use bool) {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	network_config.useService = use
}

// SetDNSProxyAddress sets the DNS proxy listen address
func (network_config *NetworkConfig) SetDNSProxyAddress(address string) {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	network_config.dnsProxyAddress = address
}

// GetDNSProxyAddress returns the DNS proxy listen address (default: 127.0.0.53)
func (network_config *NetworkConfig) GetDNSProxyAddress() string {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	if network_config.dnsProxyAddress == "" {
		return "127.0.0.53"
	}
	return network_config.dnsProxyAddress
}

// SetDNSFallbackServer sets the fallback DNS server address
func (network_config *NetworkConfig) SetDNSFallbackServer(server string) {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	network_config.dnsFallbackServer = server
}

// GetDNSFallbackServer returns the fallback DNS server (default: 8.8.8.8)
func (network_config *NetworkConfig) GetDNSFallbackServer() string {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	if network_config.dnsFallbackServer == "" {
		return "8.8.8.8"
	}
	return network_config.dnsFallbackServer
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
func (network_config *NetworkConfig) EnsureLoopbackIPs(ips []string) error {
	if !IsAdmin() {
		return fmt.Errorf("administrator privileges required to configure loopback IPs")
	}

	for _, ip := range ips {
		if ip == "127.0.0.1" {
			continue // Skip default loopback
		}

		// Check if IP already exists
		if network_config.loopbackIPExists(ip) {
			log.Printf("Loopback IP %s already configured", ip)
			continue
		}

		// Add the loopback IP
		if err := network_config.addLoopbackIP(ip); err != nil {
			log.Printf("Failed to add loopback IP %s: %v", ip, err)
			// Continue with other IPs
		} else {
			network_config.mu.Lock()
			network_config.configuredIPs = append(network_config.configuredIPs, ip)
			network_config.mu.Unlock()
			log.Printf("Added loopback IP %s", ip)
		}
	}

	return nil
}

// LoopbackIPExists checks if a loopback IP is already configured (public)
func (network_config *NetworkConfig) LoopbackIPExists(ip string) bool {
	return network_config.loopbackIPExists(ip)
}

// loopbackIPExists checks if a loopback IP is already configured
func (network_config *NetworkConfig) loopbackIPExists(ip string) bool {
	// Check cache first
	network_config.mu.Lock()
	for _, configuredIP := range network_config.configuredIPs {
		if configuredIP == ip {
			network_config.mu.Unlock()
			return true
		}
	}
	network_config.mu.Unlock()

	// Try ping - fastest and most reliable check
	cmd := exec.Command("ping", "-n", "1", "-w", "100", ip)
	hideWindow(cmd)
	pingErr := cmd.Run()
	if pingErr == nil {
		// IP responded, add to cache
		network_config.mu.Lock()
		network_config.configuredIPs = append(network_config.configuredIPs, ip)
		network_config.mu.Unlock()
		return true
	}

	// Fallback: Use netsh to check if IP exists
	netshCmd := exec.Command("netsh", "interface", "ipv4", "show", "ipaddresses", "interface=Loopback Pseudo-Interface 1")
	hideWindow(netshCmd)
	netshOutput, netshErr := netshCmd.Output()
	if netshErr != nil {
		return false
	}
	exists := strings.Contains(string(netshOutput), ip)
	if exists {
		network_config.mu.Lock()
		network_config.configuredIPs = append(network_config.configuredIPs, ip)
		network_config.mu.Unlock()
	}
	return exists
}

// addLoopbackIP adds a loopback IP address
func (network_config *NetworkConfig) addLoopbackIP(ip string) error {
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
func (network_config *NetworkConfig) RemoveLoopbackIP(ip string) error {
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
func (network_config *NetworkConfig) CleanupLoopbackIPs() {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()

	for _, ip := range network_config.configuredIPs {
		if err := network_config.RemoveLoopbackIP(ip); err != nil {
			log.Printf("Failed to remove loopback IP %s: %v", ip, err)
		} else {
			log.Printf("Removed loopback IP %s", ip)
		}
	}
	network_config.configuredIPs = []string{}
}

// GetActiveNetworkInterface returns the name of the primary network interface
// Uses the Win32 IP Helper API (GetBestRoute) instead of PowerShell for instant results.
func (network_config *NetworkConfig) GetActiveNetworkInterface() (string, error) {
	interfaceName, nativeErr := getActiveInterfaceNative()
	if nativeErr == nil && interfaceName != "" {
		return interfaceName, nil
	}

	// Fallback to PowerShell if native API fails
	log.Printf("Native GetActiveNetworkInterface failed (%v), falling back to PowerShell", nativeErr)
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		"Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Select-Object -ExpandProperty InterfaceAlias | Select-Object -First 1")
	hideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get active interface: %v", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// getActiveInterfaceNative uses Win32 GetBestRoute + ConvertInterfaceLuidToAlias to find
// the primary network interface without spawning PowerShell.
func getActiveInterfaceNative() (string, error) {
	iphlpapi := syscall.NewLazyDLL("iphlpapi.dll")
	procGetBestRoute := iphlpapi.NewProc("GetBestRoute")
	procConvertInterfaceIndexToLuid := iphlpapi.NewProc("ConvertInterfaceIndexToLuid")
	procConvertInterfaceLuidToAlias := iphlpapi.NewProc("ConvertInterfaceLuidToAlias")

	// MIB_IPFORWARDROW structure (used by GetBestRoute)
	type mibIpForwardRow struct {
		ForwardDest      uint32
		ForwardMask      uint32
		ForwardPolicy    uint32
		ForwardNextHop   uint32
		ForwardIfIndex   uint32
		ForwardType      uint32
		ForwardProto     uint32
		ForwardAge       uint32
		ForwardNextHopAS uint32
		ForwardMetric1   uint32
		ForwardMetric2   uint32
		ForwardMetric3   uint32
		ForwardMetric4   uint32
		ForwardMetric5   uint32
	}

	var bestRoute mibIpForwardRow
	// GetBestRoute(0.0.0.0, 0.0.0.0) finds the default route
	returnCode, _, _ := procGetBestRoute.Call(0, 0, uintptr(unsafe.Pointer(&bestRoute)))
	if returnCode != 0 {
		return "", fmt.Errorf("GetBestRoute failed with code %d", returnCode)
	}

	// Convert interface index to LUID
	var interfaceLuid uint64
	returnCode, _, _ = procConvertInterfaceIndexToLuid.Call(
		uintptr(bestRoute.ForwardIfIndex),
		uintptr(unsafe.Pointer(&interfaceLuid)),
	)
	if returnCode != 0 {
		return "", fmt.Errorf("ConvertInterfaceIndexToLuid failed with code %d", returnCode)
	}

	// Convert LUID to alias (interface name)
	aliasBuffer := make([]uint16, 256)
	returnCode, _, _ = procConvertInterfaceLuidToAlias.Call(
		uintptr(unsafe.Pointer(&interfaceLuid)),
		uintptr(unsafe.Pointer(&aliasBuffer[0])),
		256,
	)
	if returnCode != 0 {
		return "", fmt.Errorf("ConvertInterfaceLuidToAlias failed with code %d", returnCode)
	}

	return syscall.UTF16ToString(aliasBuffer), nil
}

// getDNSServersNative uses the Win32 GetAdaptersAddresses API to retrieve DNS server
// addresses for a given interface name without spawning PowerShell.
// When ipv6Only is true, returns only IPv6 addresses; otherwise only IPv4.
func getDNSServersNative(interfaceName string, ipv6Only bool) ([]string, error) {
	// GAA_FLAG_SKIP_UNICAST | GAA_FLAG_SKIP_ANYCAST | GAA_FLAG_SKIP_MULTICAST
	const gaaFlags = 0x0001 | 0x0002 | 0x0004

	// First call to get required buffer size
	var bufferSize uint32
	windows.GetAdaptersAddresses(syscall.AF_UNSPEC, gaaFlags, 0, nil, &bufferSize)

	if bufferSize == 0 {
		return nil, fmt.Errorf("GetAdaptersAddresses returned zero buffer size")
	}

	adapterBuffer := make([]byte, bufferSize)
	firstAdapter := (*windows.IpAdapterAddresses)(unsafe.Pointer(&adapterBuffer[0]))

	getAdaptersErr := windows.GetAdaptersAddresses(syscall.AF_UNSPEC, gaaFlags, 0, firstAdapter, &bufferSize)
	if getAdaptersErr != nil {
		return nil, fmt.Errorf("GetAdaptersAddresses failed: %w", getAdaptersErr)
	}

	for currentAdapter := firstAdapter; currentAdapter != nil; currentAdapter = currentAdapter.Next {
		currentFriendlyName := syscall.UTF16ToString((*[256]uint16)(unsafe.Pointer(currentAdapter.FriendlyName))[:])

		if currentFriendlyName != interfaceName {
			continue
		}

		// Found our interface — collect DNS server addresses
		var dnsServerList []string
		for dnsServer := currentAdapter.FirstDnsServerAddress; dnsServer != nil; dnsServer = dnsServer.Next {
			sockaddrPtr := dnsServer.Address.Sockaddr
			if sockaddrPtr == nil {
				continue
			}
			addressFamily := *(*uint16)(unsafe.Pointer(sockaddrPtr))

			if addressFamily == syscall.AF_INET && !ipv6Only {
				ipBytes := (*[4]byte)(unsafe.Pointer(uintptr(unsafe.Pointer(sockaddrPtr)) + 4))
				ipAddress := fmt.Sprintf("%d.%d.%d.%d", ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3])
				dnsServerList = append(dnsServerList, ipAddress)
			} else if addressFamily == syscall.AF_INET6 && ipv6Only {
				ipBytes := (*[16]byte)(unsafe.Pointer(uintptr(unsafe.Pointer(sockaddrPtr)) + 8))
				ipAddress := net.IP(ipBytes[:]).String()
				dnsServerList = append(dnsServerList, ipAddress)
			}
		}
		return dnsServerList, nil
	}

	return []string{}, nil
}

// GetCurrentDNS gets the current IPv4 DNS servers for an interface.
// Uses the Win32 GetAdaptersAddresses API instead of PowerShell for instant results.
func (network_config *NetworkConfig) GetCurrentDNS(interfaceName string) ([]string, error) {
	dnsServers, nativeErr := getDNSServersNative(interfaceName, false)
	if nativeErr == nil {
		return dnsServers, nil
	}

	// Fallback to PowerShell if native API fails
	log.Printf("Native GetCurrentDNS failed (%v), falling back to PowerShell", nativeErr)
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

// GetCurrentDNSv6 gets the current IPv6 DNS servers for an interface.
// Uses the Win32 GetAdaptersAddresses API instead of PowerShell for instant results.
func (network_config *NetworkConfig) GetCurrentDNSv6(interfaceName string) ([]string, error) {
	dnsServers, nativeErr := getDNSServersNative(interfaceName, true)
	if nativeErr == nil {
		return dnsServers, nil
	}

	// Fallback to PowerShell if native API fails
	log.Printf("Native GetCurrentDNSv6 failed (%v), falling back to PowerShell", nativeErr)
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
func (network_config *NetworkConfig) SetDNS(interfaceName string, dnsServers []string) error {
	// Try to use the service first
	network_config.mu.Lock()
	useService := network_config.useService && network_config.serviceClient != nil
	client := network_config.serviceClient
	network_config.mu.Unlock()

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

// SaveOriginalDNS saves the current DNS servers for an interface so they can be restored later
func (network_config *NetworkConfig) SaveOriginalDNS(interfaceName string, dnsServers []string) {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	if _, already_saved := network_config.originalDNS[interfaceName]; !already_saved {
		network_config.originalDNS[interfaceName] = dnsServers
	}
}

// SetDNSForInterface sets the DNS server for a specific interface to the proxy address
func (network_config *NetworkConfig) SetDNSForInterface(interfaceName string, dnsProxyAddress string) error {
	return network_config.SetDNS(interfaceName, []string{dnsProxyAddress})
}

// SetDNSv6 sets the IPv6 DNS servers for an interface
func (network_config *NetworkConfig) SetDNSv6(interfaceName string, dnsServers []string) error {
	// Try to use the service first
	network_config.mu.Lock()
	useService := network_config.useService && network_config.serviceClient != nil
	client := network_config.serviceClient
	network_config.mu.Unlock()

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
func (network_config *NetworkConfig) ResetDNS(interfaceName string) error {
	// Try to use the service first
	network_config.mu.Lock()
	useService := network_config.useService && network_config.serviceClient != nil
	client := network_config.serviceClient
	network_config.mu.Unlock()

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
func (network_config *NetworkConfig) ConfigureSystemDNS(dnsProxyAddress string) error {
	network_config.mu.Lock()

	if network_config.dnsConfigured {
		network_config.mu.Unlock()
		return nil // Already configured
	}

	// Get active interface
	interfaceName, err := network_config.GetActiveNetworkInterface()
	if err != nil {
		network_config.mu.Unlock()
		return fmt.Errorf("failed to get active interface: %w", err)
	}

	// Save original IPv4 DNS
	originalDNS, err := network_config.GetCurrentDNS(interfaceName)
	if err != nil {
		log.Printf("Warning: could not get original DNS: %v", err)
		originalDNS = []string{"8.8.8.8"} // Fallback
	}
	network_config.originalDNS[interfaceName] = originalDNS

	// Save original IPv6 DNS
	originalDNSv6, err := network_config.GetCurrentDNSv6(interfaceName)
	if err != nil {
		log.Printf("Warning: could not get original IPv6 DNS: %v", err)
		originalDNSv6 = []string{} // No fallback for IPv6
	}
	network_config.originalDNSv6[interfaceName] = originalDNSv6

	// Check if we can use the service
	useService := network_config.useService && network_config.serviceClient != nil
	client := network_config.serviceClient

	network_config.mu.Unlock()

	// Try to use the service first (no UAC prompt)
	if useService {
		log.Printf("Configuring system DNS via service: %s", dnsProxyAddress)
		if err := client.ConfigureSystemDNS(dnsProxyAddress); err != nil {
			log.Printf("Warning: service ConfigureSystemDNS failed: %v, falling back to UAC", err)
		} else {
			network_config.mu.Lock()
			network_config.dnsConfigured = true
			network_config.mu.Unlock()
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

	network_config.mu.Lock()
	network_config.dnsConfigured = true
	network_config.mu.Unlock()

	log.Printf("Configured system DNS to %s / ::1 (original IPv4: %v, IPv6: %v)", dnsProxyAddress, originalDNS, originalDNSv6)
	return nil
}

// RestoreSystemDNS restores the original DNS configuration (both IPv4 and IPv6) with UAC elevation
// If no original DNS was saved (e.g., app restarted), it resets to DHCP
func (network_config *NetworkConfig) RestoreSystemDNS() error {
	network_config.mu.Lock()

	// Check if we have original DNS saved
	hasOriginal := network_config.dnsConfigured && (len(network_config.originalDNS) > 0 || len(network_config.originalDNSv6) > 0)
	dnsProxyAddr := network_config.dnsProxyAddress
	if dnsProxyAddr == "" {
		dnsProxyAddr = "127.0.0.53"
	}

	// If no original saved, check if system DNS is currently our proxy address and reset to DHCP
	if !hasOriginal {
		network_config.mu.Unlock()

		// Get active interface
		interfaceName, err := network_config.GetActiveNetworkInterface()
		if err != nil {
			return fmt.Errorf("failed to get active interface: %w", err)
		}

		// Check if DNS is currently our proxy address
		currentDNS, _ := network_config.GetCurrentDNS(interfaceName)
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
	for interfaceName, originalDNS := range network_config.originalDNS {
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
	for interfaceName, originalDNSv6 := range network_config.originalDNSv6 {
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

	network_config.mu.Unlock()

	if len(commands) == 0 {
		network_config.mu.Lock()
		network_config.originalDNS = make(map[string][]string)
		network_config.originalDNSv6 = make(map[string][]string)
		network_config.dnsConfigured = false
		network_config.mu.Unlock()
		return nil
	}

	// Execute all commands with elevation
	fullCommand := strings.Join(commands, "; ")
	args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, fullCommand)

	log.Printf("Restoring DNS with elevation: %s", fullCommand)
	if err := RunElevated("powershell", args); err != nil {
		return fmt.Errorf("failed to restore DNS: %w", err)
	}

	network_config.mu.Lock()
	network_config.originalDNS = make(map[string][]string)
	network_config.originalDNSv6 = make(map[string][]string)
	network_config.dnsConfigured = false
	network_config.mu.Unlock()

	log.Printf("DNS configuration restored")
	return nil
}

// IsDNSConfigured returns whether DNS has been configured by us
func (network_config *NetworkConfig) IsDNSConfigured() bool {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	return network_config.dnsConfigured
}

// IsDNSClientStopped returns whether we stopped the DNS Client service
func (network_config *NetworkConfig) IsDNSClientStopped() bool {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()
	return network_config.dnsClientStopped
}

// IsTransparentDNSConfigured checks if the system DNS is pointing to our DNS proxy
// This checks the actual system state, not just the runtime flag
func (network_config *NetworkConfig) IsTransparentDNSConfigured() bool {
	// First check runtime flag
	network_config.mu.Lock()
	if network_config.dnsConfigured {
		network_config.mu.Unlock()
		return true
	}
	network_config.mu.Unlock()

	// Check actual system DNS
	interfaceName, err := network_config.GetActiveNetworkInterface()
	if err != nil {
		return false
	}

	dnsServers, err := network_config.GetCurrentDNS(interfaceName)
	if err != nil || len(dnsServers) == 0 {
		return false
	}

	// Check if primary DNS is our DNS proxy address
	dnsProxyAddr := network_config.GetDNSProxyAddress()
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
	network_config := GetNetworkConfig()
	network_config.mu.Lock()
	useService := network_config.useService && network_config.serviceClient != nil
	client := network_config.serviceClient
	network_config.mu.Unlock()

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
	network_config := GetNetworkConfig()
	network_config.mu.Lock()
	useService := network_config.useService && network_config.serviceClient != nil
	client := network_config.serviceClient
	network_config.mu.Unlock()

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
func (network_config *NetworkConfig) SetupTransparentDNS() error {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()

	if network_config.dnsConfigured {
		log.Printf("Transparent DNS already configured")
		return nil
	}

	// Get DNS proxy address
	dnsProxyAddr := network_config.dnsProxyAddress
	if dnsProxyAddr == "" {
		dnsProxyAddr = "127.0.0.53"
	}

	// Ensure the loopback IP exists (e.g., 127.0.0.53) so the system resolver can reach it
	if dnsProxyAddr != "127.0.0.1" {
		log.Printf("Ensuring loopback IP %s exists for DNS proxy...", dnsProxyAddr)
		network_config.mu.Unlock()
		if err := network_config.AddLoopbackIPElevated(dnsProxyAddr); err != nil {
			log.Printf("Warning: could not add loopback IP %s: %v (may already exist)", dnsProxyAddr, err)
		}
		network_config.mu.Lock()
	}

	// Get active interface
	interfaceName, err := network_config.GetActiveNetworkInterface()
	if err != nil {
		return fmt.Errorf("failed to get active interface: %w", err)
	}

	// Save original DNS
	network_config.mu.Unlock()
	originalDNS, err := network_config.GetCurrentDNS(interfaceName)
	network_config.mu.Lock()
	if err != nil {
		log.Printf("Warning: could not get original DNS: %v", err)
		fallback_dns_server := network_config.dnsFallbackServer
		if fallback_dns_server == "" {
			fallback_dns_server = "8.8.8.8"
		}
		originalDNS = []string{fallback_dns_server}
	}
	network_config.originalDNS[interfaceName] = originalDNS

	// Save original IPv6 DNS
	network_config.mu.Unlock()
	originalDNSv6, err := network_config.GetCurrentDNSv6(interfaceName)
	network_config.mu.Lock()
	if err != nil {
		log.Printf("Warning: could not get original IPv6 DNS: %v", err)
	}
	network_config.originalDNSv6[interfaceName] = originalDNSv6

	// Set IPv4 DNS to our proxy address with configurable fallback
	fallback_server := network_config.dnsFallbackServer
	if fallback_server == "" {
		fallback_server = "8.8.8.8"
	}
	network_config.mu.Unlock()
	err = network_config.SetDNS(interfaceName, []string{dnsProxyAddr, fallback_server})
	network_config.mu.Lock()
	if err != nil {
		return fmt.Errorf("failed to set DNS: %w", err)
	}

	// Set IPv6 DNS to ::1 (our proxy also listens there)
	// This prevents Windows from preferring IPv6 DNS (e.g., fe80::1) over our proxy
	network_config.mu.Unlock()
	if set_ipv6_err := network_config.SetDNSv6(interfaceName, []string{"::1"}); set_ipv6_err != nil {
		log.Printf("Warning: could not set IPv6 DNS to ::1: %v", set_ipv6_err)
	}
	network_config.mu.Lock()

	network_config.dnsConfigured = true
	log.Printf("Transparent DNS configured: system DNS set to %s,8.8.8.8 + ::1 (original IPv4: %v, IPv6: %v)", dnsProxyAddr, originalDNS, originalDNSv6)
	return nil
}

// setDNSElevated sets DNS with UAC elevation
func (network_config *NetworkConfig) setDNSElevated(interfaceName, dnsAddress string) error {
	psCommand := fmt.Sprintf(`Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s`, interfaceName, dnsAddress)
	args := fmt.Sprintf(`-WindowStyle Hidden -Command "%s"`, psCommand)

	return RunElevated("powershell", args)
}

// RestoreTransparentDNS restores the original DNS configuration and restarts DNS Client
func (network_config *NetworkConfig) RestoreTransparentDNS() error {
	network_config.mu.Lock()
	defer network_config.mu.Unlock()

	if !network_config.dnsConfigured {
		return nil // Nothing to restore
	}

	var lastErr error

	// Restore original DNS for each interface
	// SetDNS and ResetDNS will use the service if available
	for interfaceName, originalDNS := range network_config.originalDNS {
		if len(originalDNS) == 0 {
			// Reset to DHCP
			network_config.mu.Unlock()
			if err := network_config.ResetDNS(interfaceName); err != nil {
				log.Printf("Failed to reset DNS for %s: %v", interfaceName, err)
				lastErr = err
			} else {
				log.Printf("Reset DNS to DHCP for %s", interfaceName)
			}
			network_config.mu.Lock()
		} else {
			// Restore original DNS
			network_config.mu.Unlock()
			if err := network_config.SetDNS(interfaceName, originalDNS); err != nil {
				log.Printf("Failed to restore DNS for %s: %v", interfaceName, err)
				lastErr = err
			} else {
				log.Printf("Restored DNS to %v for %s", originalDNS, interfaceName)
			}
			network_config.mu.Lock()
		}
	}

	// Restore original IPv6 DNS for each interface
	for interfaceName, originalDNSv6 := range network_config.originalDNSv6 {
		network_config.mu.Unlock()
		if len(originalDNSv6) == 0 {
			// Reset IPv6 DNS to automatic (same as ResetDNS but for IPv6)
			if reset_ipv6_err := network_config.ResetDNS(interfaceName); reset_ipv6_err != nil {
				log.Printf("Warning: could not reset IPv6 DNS for %s: %v", interfaceName, reset_ipv6_err)
			}
		} else {
			if restore_ipv6_err := network_config.SetDNSv6(interfaceName, originalDNSv6); restore_ipv6_err != nil {
				log.Printf("Warning: could not restore IPv6 DNS for %s: %v", interfaceName, restore_ipv6_err)
			}
		}
		network_config.mu.Lock()
	}

	// Restart DNS Client if we stopped it
	if network_config.dnsClientStopped && network_config.dnsClientWasRunning {
		network_config.mu.Unlock()
		if err := StartDNSClientService(); err != nil {
			log.Printf("Failed to restart DNS Client service: %v", err)
			lastErr = err
		}
		network_config.mu.Lock()
		network_config.dnsClientStopped = false
	}

	network_config.originalDNS = make(map[string][]string)
	network_config.originalDNSv6 = make(map[string][]string)
	network_config.dnsConfigured = false
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
func (network_config *NetworkConfig) AddLoopbackIPElevated(ip string) error {
	if ip == "127.0.0.1" {
		return nil
	}

	// Check if already exists
	if network_config.loopbackIPExists(ip) {
		log.Printf("Loopback IP %s already exists", ip)
		return nil
	}

	// Try to use the service first
	network_config.mu.Lock()
	useService := network_config.useService && network_config.serviceClient != nil
	client := network_config.serviceClient
	network_config.mu.Unlock()

	if useService {
		log.Printf("Adding loopback IP %s via service...", ip)
		if err := client.AddLoopbackIP(ip); err != nil {
			log.Printf("Service call failed, falling back to UAC: %v", err)
		} else {
			network_config.mu.Lock()
			network_config.configuredIPs = append(network_config.configuredIPs, ip)
			network_config.mu.Unlock()
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

	network_config.mu.Lock()
	network_config.configuredIPs = append(network_config.configuredIPs, ip)
	network_config.mu.Unlock()

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
func (network_config *NetworkConfig) ConfigureSystemDNSElevated(dnsAddress string) error {
	network_config.mu.Lock()
	if network_config.dnsConfigured {
		network_config.mu.Unlock()
		return nil
	}

	useService := network_config.useService && network_config.serviceClient != nil
	client := network_config.serviceClient
	network_config.mu.Unlock()

	// Get active interface
	interfaceName, err := network_config.GetActiveNetworkInterface()
	if err != nil {
		return fmt.Errorf("failed to get active interface: %w", err)
	}

	// Save original DNS
	originalDNS, err := network_config.GetCurrentDNS(interfaceName)
	if err != nil {
		log.Printf("Warning: could not get original DNS: %v", err)
		originalDNS = []string{"8.8.8.8"}
	}

	network_config.mu.Lock()
	network_config.originalDNS[interfaceName] = originalDNS
	network_config.mu.Unlock()

	// Try to use the service first
	if useService {
		log.Printf("Configuring system DNS to %s via service...", dnsAddress)
		if err := client.ConfigureSystemDNS(dnsAddress); err != nil {
			log.Printf("Service call failed, falling back to UAC: %v", err)
		} else {
			network_config.mu.Lock()
			network_config.dnsConfigured = true
			network_config.mu.Unlock()
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

	network_config.mu.Lock()
	network_config.dnsConfigured = true
	network_config.mu.Unlock()
	log.Printf("Configured system DNS to %s,8.8.8.8", dnsAddress)
	return nil
}
