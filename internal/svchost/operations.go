package svchost

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Operations implements the privileged operations for the Windows service
type Operations struct {
	originalDNS      map[string][]string // interface -> original IPv4 DNS servers
	originalDNSv6    map[string][]string // interface -> original IPv6 DNS servers
	dnsConfigured    bool
	dnsClientStopped bool
	configuredIPs    []string
	mu               sync.Mutex
}

// NewOperations creates a new operations handler
func NewOperations() *Operations {
	return &Operations{
		originalDNS:   make(map[string][]string),
		originalDNSv6: make(map[string][]string),
		configuredIPs: []string{},
	}
}

// hideWindow sets the command to run without showing a console window
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// validateIP validates that the string is a valid IP address in the 127.x.x.x range
func validateIP(ip string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}
	// Only allow loopback range 127.x.x.x
	if !strings.HasPrefix(ip, "127.") {
		return fmt.Errorf("only loopback IPs (127.x.x.x) are allowed: %s", ip)
	}
	return nil
}

// validateInterfaceName validates that the interface name is safe
func validateInterfaceName(name string) error {
	// Allow alphanumeric, spaces, hyphens, and parentheses
	validPattern := regexp.MustCompile(`^[a-zA-Z0-9\s\-\(\)]+$`)
	if !validPattern.MatchString(name) {
		return fmt.Errorf("invalid interface name: %s", name)
	}
	if len(name) > 256 {
		return fmt.Errorf("interface name too long: %s", name)
	}
	return nil
}

// validateDNSServer validates a DNS server address
func validateDNSServer(server string) error {
	parsed := net.ParseIP(server)
	if parsed == nil {
		return fmt.Errorf("invalid DNS server: %s", server)
	}
	return nil
}

// IsDNSConfigured returns whether DNS has been configured
func (operations *Operations) IsDNSConfigured() bool {
	operations.mu.Lock()
	defer operations.mu.Unlock()
	return operations.dnsConfigured
}

// AddLoopbackIP adds a loopback IP address
func (operations *Operations) AddLoopbackIP(ip string) error {
	if ip == "127.0.0.1" {
		return nil // Skip default loopback
	}

	if err := validateIP(ip); err != nil {
		return err
	}

	operations.mu.Lock()
	defer operations.mu.Unlock()

	// Check if already configured
	for _, configured := range operations.configuredIPs {
		if configured == ip {
			log.Printf("Loopback IP %s already configured", ip)
			return nil
		}
	}

	// Check if IP already exists using ping
	cmd := exec.Command("ping", "-n", "1", "-w", "100", ip)
	hideWindow(cmd)
	if err := cmd.Run(); err == nil {
		log.Printf("Loopback IP %s already exists", ip)
		operations.configuredIPs = append(operations.configuredIPs, ip)
		return nil
	}

	// Add the loopback IP
	cmd = exec.Command("netsh", "interface", "ipv4", "add", "address",
		"Loopback Pseudo-Interface 1", ip, "255.255.255.0")
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh error: %v - %s", err, string(output))
	}

	operations.configuredIPs = append(operations.configuredIPs, ip)
	log.Printf("Added loopback IP %s", ip)
	return nil
}

// RemoveLoopbackIP removes a loopback IP address
func (operations *Operations) RemoveLoopbackIP(ip string) error {
	if ip == "127.0.0.1" {
		return nil // Never remove default loopback
	}

	if err := validateIP(ip); err != nil {
		return err
	}

	cmd := exec.Command("netsh", "interface", "ipv4", "delete", "address",
		"Loopback Pseudo-Interface 1", ip)
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh error: %v - %s", err, string(output))
	}

	// Remove from configured list
	operations.mu.Lock()
	var newIPs []string
	for _, configured := range operations.configuredIPs {
		if configured != ip {
			newIPs = append(newIPs, configured)
		}
	}
	operations.configuredIPs = newIPs
	operations.mu.Unlock()

	log.Printf("Removed loopback IP %s", ip)
	return nil
}

// EnsureLoopbackIPs ensures multiple loopback IPs exist
func (operations *Operations) EnsureLoopbackIPs(ips []string) error {
	var lastErr error
	for _, ip := range ips {
		if err := operations.AddLoopbackIP(ip); err != nil {
			log.Printf("Failed to add loopback IP %s: %v", ip, err)
			lastErr = err
		}
	}
	return lastErr
}

// GetActiveNetworkInterface returns the name of the primary network interface
func (operations *Operations) GetActiveNetworkInterface() (string, error) {
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
func (operations *Operations) GetCurrentDNS(interfaceName string) ([]string, error) {
	if err := validateInterfaceName(interfaceName); err != nil {
		return nil, err
	}

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
func (operations *Operations) GetCurrentDNSv6(interfaceName string) ([]string, error) {
	if err := validateInterfaceName(interfaceName); err != nil {
		return nil, err
	}

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
func (operations *Operations) SetDNS(interfaceName string, dnsServers []string) error {
	if err := validateInterfaceName(interfaceName); err != nil {
		return err
	}
	for _, server := range dnsServers {
		if err := validateDNSServer(server); err != nil {
			return err
		}
	}

	dnsString := strings.Join(dnsServers, ",")
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s", interfaceName, dnsString))
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set DNS: %v - %s", err, string(output))
	}
	log.Printf("Set DNS for %s to %v", interfaceName, dnsServers)
	return nil
}

// SetDNSv6 sets the IPv6 DNS servers for an interface
func (operations *Operations) SetDNSv6(interfaceName string, dnsServers []string) error {
	if err := validateInterfaceName(interfaceName); err != nil {
		return err
	}
	for _, server := range dnsServers {
		if err := validateDNSServer(server); err != nil {
			return err
		}
	}

	dnsString := strings.Join(dnsServers, ",")
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses %s", interfaceName, dnsString))
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set IPv6 DNS: %v - %s", err, string(output))
	}
	log.Printf("Set IPv6 DNS for %s to %v", interfaceName, dnsServers)
	return nil
}

// ResetDNS resets DNS to automatic (DHCP)
func (operations *Operations) ResetDNS(interfaceName string) error {
	if err := validateInterfaceName(interfaceName); err != nil {
		return err
	}

	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command",
		fmt.Sprintf("Set-DnsClientServerAddress -InterfaceAlias '%s' -ResetServerAddresses", interfaceName))
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reset DNS: %v - %s", err, string(output))
	}
	log.Printf("Reset DNS for %s to DHCP", interfaceName)
	return nil
}

// ConfigureSystemDNS configures the system to use our DNS proxy
// Note: Using a loopback IP like 127.0.0.53 avoids conflicts with Windows DNS Client
func (operations *Operations) ConfigureSystemDNS(dnsAddress string) error {
	if err := validateDNSServer(dnsAddress); err != nil {
		return err
	}

	// Always try to stop DNS Client service first - it holds port 53
	// Do this even if we think DNS is already configured, because the DNS Client
	// might have been restarted by Windows since the last call
	log.Printf("Stopping Windows DNS Client service to free port 53...")
	if err := operations.StopDNSClient(); err != nil {
		log.Printf("Warning: could not stop DNS Client service: %v", err)
		// Continue anyway - maybe it's not running
	}

	operations.mu.Lock()
	if operations.dnsConfigured {
		operations.mu.Unlock()
		log.Printf("DNS already configured, but DNS Client stop was attempted")
		return nil // DNS settings already applied
	}
	operations.mu.Unlock()

	// Ensure the loopback IP exists for the DNS proxy (e.g., 127.0.0.53)
	// This is needed so the DNS proxy can bind to this address
	if dnsAddress != "127.0.0.1" {
		log.Printf("Ensuring loopback IP %s exists...", dnsAddress)
		if err := operations.AddLoopbackIP(dnsAddress); err != nil {
			log.Printf("Warning: could not add loopback IP %s: %v (may already exist)", dnsAddress, err)
			// Continue anyway - the IP might already exist
		}
	}

	// Get active interface
	interfaceName, err := operations.GetActiveNetworkInterface()
	if err != nil {
		return fmt.Errorf("failed to get active interface: %w", err)
	}

	// Save original IPv4 DNS
	originalDNS, err := operations.GetCurrentDNS(interfaceName)
	if err != nil {
		log.Printf("Warning: could not get original DNS: %v", err)
		originalDNS = []string{"8.8.8.8"} // Fallback
	}

	// Save original IPv6 DNS
	originalDNSv6, err := operations.GetCurrentDNSv6(interfaceName)
	if err != nil {
		log.Printf("Warning: could not get original IPv6 DNS: %v", err)
		originalDNSv6 = []string{} // No fallback for IPv6
	}

	operations.mu.Lock()
	operations.originalDNS[interfaceName] = originalDNS
	operations.originalDNSv6[interfaceName] = originalDNSv6
	operations.mu.Unlock()

	// Set DNS to the proxy address and ::1 for IPv6
	if err := operations.SetDNS(interfaceName, []string{dnsAddress}); err != nil {
		return err
	}
	if err := operations.SetDNSv6(interfaceName, []string{"::1"}); err != nil {
		log.Printf("Warning: could not set IPv6 DNS: %v", err)
	}

	operations.mu.Lock()
	operations.dnsConfigured = true
	operations.mu.Unlock()

	log.Printf("Configured system DNS to %s / ::1 (original IPv4: %v, IPv6: %v)", dnsAddress, originalDNS, originalDNSv6)
	return nil
}

// RestoreSystemDNS restores the original DNS configuration
func (operations *Operations) RestoreSystemDNS() error {
	operations.mu.Lock()
	if !operations.dnsConfigured {
		operations.mu.Unlock()
		return nil // Nothing to restore
	}
	operations.mu.Unlock()

	var lastErr error

	// Build and execute PowerShell commands for all interfaces
	operations.mu.Lock()
	for interfaceName, originalDNS := range operations.originalDNS {
		operations.mu.Unlock()
		if len(originalDNS) == 0 {
			if err := operations.ResetDNS(interfaceName); err != nil {
				log.Printf("Failed to reset DNS for %s: %v", interfaceName, err)
				lastErr = err
			}
		} else {
			if err := operations.SetDNS(interfaceName, originalDNS); err != nil {
				log.Printf("Failed to restore DNS for %s: %v", interfaceName, err)
				lastErr = err
			}
		}
		operations.mu.Lock()
	}

	// Restore IPv6 DNS
	for interfaceName, originalDNSv6 := range operations.originalDNSv6 {
		operations.mu.Unlock()
		if len(originalDNSv6) == 0 {
			if err := operations.ResetDNS(interfaceName); err != nil {
				log.Printf("Failed to reset IPv6 DNS for %s: %v", interfaceName, err)
				lastErr = err
			}
		} else {
			if err := operations.SetDNSv6(interfaceName, originalDNSv6); err != nil {
				log.Printf("Failed to restore IPv6 DNS for %s: %v", interfaceName, err)
				lastErr = err
			}
		}
		operations.mu.Lock()
	}

	// Restart DNS Client if we stopped it
	if operations.dnsClientStopped {
		operations.mu.Unlock()
		if err := operations.StartDNSClient(); err != nil {
			log.Printf("Failed to restart DNS Client: %v", err)
			lastErr = err
		}
		operations.mu.Lock()
	}

	operations.originalDNS = make(map[string][]string)
	operations.originalDNSv6 = make(map[string][]string)
	operations.dnsConfigured = false
	operations.dnsClientStopped = false
	operations.mu.Unlock()

	log.Printf("DNS configuration restored")
	return lastErr
}

// StopDNSClient stops and disables the Windows DNS Client service
// This is aggressive to ensure port 53 is freed for our DNS proxy
func (operations *Operations) StopDNSClient() error {
	log.Printf("Stopping DNS Client service (Dnscache)...")

	// Use PowerShell to stop the service - it has better privileges for protected services
	// The script: 1) Stops the service, 2) Sets startup to disabled, 3) Verifies it stopped
	psScript := `
$ErrorActionPreference = 'SilentlyContinue'
# Try to stop the service
Stop-Service -Name Dnscache -Force -ErrorAction SilentlyContinue
# Set to disabled to prevent auto-restart
Set-Service -Name Dnscache -StartupType Disabled -ErrorAction SilentlyContinue
# Wait for service to stop
$maxWait = 10
for ($i = 0; $i -lt $maxWait; $i++) {
    $svc = Get-Service -Name Dnscache -ErrorAction SilentlyContinue
    if ($svc.Status -eq 'Stopped') {
        Write-Output "SERVICE_STOPPED"
        exit 0
    }
    Start-Sleep -Milliseconds 500
}
Write-Output "SERVICE_NOT_STOPPED"
exit 1
`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psScript)
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))
	log.Printf("PowerShell Stop-Service Dnscache: %s (err: %v)", outputStr, err)

	// Check if service stopped
	if strings.Contains(outputStr, "SERVICE_STOPPED") {
		log.Printf("DNS Client service stopped successfully via PowerShell")
	} else {
		log.Printf("Warning: PowerShell could not stop DNS Client service")

		// Fallback: Try sc command anyway
		scCmd := exec.Command("sc", "stop", "Dnscache")
		hideWindow(scCmd)
		scOutput, _ := scCmd.CombinedOutput()
		log.Printf("sc stop Dnscache fallback: %s", string(scOutput))
	}

	// Wait a moment for the service to release resources
	time.Sleep(1 * time.Second)

	// Kill any process still using port 53 (as a last resort)
	if pid := operations.getProcessOnPort53(); pid != 0 {
		log.Printf("Found process %d using port 53, killing it...", pid)
		cmd = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid))
		hideWindow(cmd)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("Warning: Could not kill process %d: %v - %s", pid, err, string(output))
		} else {
			log.Printf("Killed process %d that was using port 53", pid)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Verify port 53 is free
	if pid := operations.getProcessOnPort53(); pid != 0 {
		return fmt.Errorf("port 53 still in use by process %d after stopping DNS Client", pid)
	}

	operations.mu.Lock()
	operations.dnsClientStopped = true
	operations.mu.Unlock()

	log.Printf("DNS Client service stopped and port 53 is free")
	return nil
}

// getProcessOnPort53 returns the PID of the process using UDP port 53, or 0 if none
func (operations *Operations) getProcessOnPort53() int {
	cmd := exec.Command("netstat", "-ano")
	hideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		// Look for UDP 0.0.0.0:53 or UDP 127.0.0.1:53
		if strings.Contains(line, "UDP") && strings.Contains(line, ":53 ") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				pid := 0
				fmt.Sscanf(fields[len(fields)-1], "%d", &pid)
				if pid != 0 {
					return pid
				}
			}
		}
	}
	return 0
}

// StartDNSClient re-enables and starts the Windows DNS Client service
func (operations *Operations) StartDNSClient() error {
	log.Printf("Starting DNS Client service (Dnscache)...")

	// Step 1: Re-enable the service
	cmd := exec.Command("sc", "config", "Dnscache", "start=", "auto")
	hideWindow(cmd)
	output, _ := cmd.CombinedOutput()
	log.Printf("sc config Dnscache auto: %s", string(output))

	// Step 2: Start the service
	cmd = exec.Command("sc", "start", "Dnscache")
	hideWindow(cmd)
	output, _ = cmd.CombinedOutput()
	log.Printf("sc start Dnscache: %s", string(output))

	// Step 3: Wait for service to start
	maxAttempts := 10
	for attempt_count := 0; attempt_count < maxAttempts; attempt_count++ {
		time.Sleep(500 * time.Millisecond)

		cmd = exec.Command("sc", "query", "Dnscache")
		hideWindow(cmd)
		output, _ = cmd.CombinedOutput()
		outputStr := string(output)

		if strings.Contains(outputStr, "RUNNING") {
			log.Printf("DNS Client service started successfully")
			break
		}

		if attempt_count == maxAttempts-1 {
			log.Printf("Warning: DNS Client service may not have started")
		}
	}

	operations.mu.Lock()
	operations.dnsClientStopped = false
	operations.mu.Unlock()

	log.Printf("DNS Client service started")
	return nil
}

// InstallMSI installs an MSI package silently. Runs as SYSTEM so no UAC needed.
func (operations *Operations) InstallMSI(msi_path string, components string) error {
	log.Printf("Installing MSI: %s (components: %s)", msi_path, components)

	// Stop any running OpenVPN processes first
	for _, process_name := range []string{"openvpn", "openvpn-gui"} {
		kill_cmd := exec.Command("taskkill", "/F", "/IM", process_name+".exe")
		hideWindow(kill_cmd)
		kill_cmd.Run() // Ignore errors — process may not be running
	}
	time.Sleep(1 * time.Second)

	// Build msiexec arguments with verbose logging for diagnostics
	msi_log_path := msi_path + ".log"
	msi_args := []string{"/i", msi_path, "/quiet", "/norestart", "/l*v", msi_log_path}
	if components != "" {
		msi_args = append(msi_args, "ADDLOCAL="+components)
	}

	install_cmd := exec.Command("msiexec", msi_args...)
	hideWindow(install_cmd)
	install_output, install_err := install_cmd.CombinedOutput()
	if install_err != nil {
		// Read MSI log for diagnostic details
		msi_log_content, _ := os.ReadFile(msi_log_path)
		msi_log_tail := string(msi_log_content)
		if len(msi_log_tail) > 2000 {
			msi_log_tail = msi_log_tail[len(msi_log_tail)-2000:]
		}
		log.Printf("MSI install failed: %v\nOutput: %s\nMSI Log (tail):\n%s", install_err, string(install_output), msi_log_tail)
		return fmt.Errorf("msiexec failed: %w (output: %s)", install_err, string(install_output))
	}

	log.Printf("MSI install completed successfully")
	return nil
}

// UninstallMSI uninstalls an MSI package by product code. Runs as SYSTEM so no UAC needed.
func (operations *Operations) UninstallMSI(product_code string) error {
	log.Printf("Uninstalling MSI product: %s", product_code)

	uninstall_cmd := exec.Command("msiexec", "/x", product_code, "/quiet", "/norestart")
	hideWindow(uninstall_cmd)
	uninstall_output, uninstall_err := uninstall_cmd.CombinedOutput()
	if uninstall_err != nil {
		log.Printf("MSI uninstall failed: %v\nOutput: %s", uninstall_err, string(uninstall_output))
		return fmt.Errorf("msiexec uninstall failed: %w (output: %s)", uninstall_err, string(uninstall_output))
	}

	log.Printf("MSI uninstall completed successfully")
	return nil
}
