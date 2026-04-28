package tunnel

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/debug"
	"vpnmultitunnel/internal/system"
)

// OpenVPNTunnel manages an openvpn.exe subprocess and provides a VPNTunnel interface
// by binding outgoing connections to the VPN adapter's assigned IP.
type OpenVPNTunnel struct {
	ProfileID       string
	Config          *config.OpenVPNConfig
	Process         *exec.Cmd
	ManagementPort  int
	Dialer          *OSDialer
	processOutput   []byte    // Captures stderr/stdout for error reporting (protected by mu)
	processExitedCh chan error // Receives process exit result (single writer)
	Username        string    // Auth username (for auth-user-pass configs)
	Password        string    // Auth password (for auth-user-pass configs)

	Stats TunnelStats
	mu    sync.RWMutex

	stopCh chan struct{}
	done   chan struct{}
}

// wellKnownOpenVPNPaths lists common installation paths for openvpn.exe on Windows
var wellKnownOpenVPNPaths = []string{
	`C:\Program Files\OpenVPN\bin\openvpn.exe`,
	`C:\Program Files (x86)\OpenVPN\bin\openvpn.exe`,
	`C:\Program Files\OpenVPN Connect\core\openvpn.exe`,
}

// FindOpenVPNBinary locates the openvpn.exe binary on the system.
// Checks PATH first, then well-known installation directories.
func FindOpenVPNBinary() (string, error) {
	// Check PATH
	pathResult, lookErr := exec.LookPath("openvpn.exe")
	if lookErr == nil {
		return pathResult, nil
	}

	// Check well-known paths
	for _, candidatePath := range wellKnownOpenVPNPaths {
		if _, statErr := os.Stat(candidatePath); statErr == nil {
			return candidatePath, nil
		}
	}

	return "", fmt.Errorf("openvpn.exe not found in PATH or standard installation directories")
}

// findFreeManagementPort finds a free TCP port for the OpenVPN management interface
func findFreeManagementPort() (int, error) {
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		return 0, fmt.Errorf("failed to find free port: %w", listenErr)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// NewOpenVPNTunnel creates and starts an OpenVPN tunnel for the given profile.
// It launches openvpn.exe as a subprocess, connects to its management interface,
// waits for connection, detects the assigned IP, and creates an OSDialer.
func NewOpenVPNTunnel(profileID string, ovpnConfig *config.OpenVPNConfig, configFilePath string, username string, password string) (*OpenVPNTunnel, error) {
	openVPNBinaryPath, findErr := FindOpenVPNBinary()
	if findErr != nil {
		return nil, findErr
	}

	managementPort, portErr := findFreeManagementPort()
	if portErr != nil {
		return nil, portErr
	}

	ovpnTunnel := &OpenVPNTunnel{
		ProfileID:      profileID,
		Config:         ovpnConfig,
		ManagementPort: managementPort,
		Username:       username,
		Password:       password,
		Stats: TunnelStats{
			Connected: false,
			Endpoint:  fmt.Sprintf("%s:%s", ovpnConfig.RemoteHost, ovpnConfig.RemotePort),
		},
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}

	// Build command arguments
	// --management-query-passwords: credentials are provided via management interface, not stdin
	// --auth-retry interact: on auth failure, ask again via management instead of exiting
	// --pull-filter ignore "dhcp-option DNS|DNS6|DOMAIN": discard DNS pushed by the
	//   OpenVPN server. Our DNS proxy on 127.0.0.53 is the single source of truth
	//   for DNS routing; letting OpenVPN write the pushed server onto the TAP
	//   adapter causes Windows to query that adapter first (lowest metric wins),
	//   bypassing the proxy and stalling lookups for ~25 s when that pushed
	//   server can only be reached through a route the user hasn't installed.
	commandArgs := []string{
		"--config", configFilePath,
		"--management", "127.0.0.1", fmt.Sprintf("%d", managementPort),
		"--management-query-passwords",
		"--auth-retry", "interact",
		"--disable-dco",
		"--pull-filter", "ignore", "dhcp-option DNS",
		"--pull-filter", "ignore", "dhcp-option DNS6",
		"--pull-filter", "ignore", "dhcp-option DOMAIN",
		"--pull-filter", "ignore", "dhcp-option DOMAIN-SEARCH",
	}

	ovpnTunnel.Process = exec.Command(openVPNBinaryPath, commandArgs...)
	ovpnTunnel.Process.SysProcAttr = getProcessHideWindowAttr()

	// Use a thread-safe writer that drains stdout+stderr without blocking the process.
	// We wrap ProcessOutput writes in a safeWriter to avoid data races.
	safeOutputWriter := &lockedWriter{tunnel: ovpnTunnel, profileID: profileID}
	ovpnTunnel.Process.Stdout = safeOutputWriter
	ovpnTunnel.Process.Stderr = safeOutputWriter

	debug.Info("openvpn", fmt.Sprintf("Running: %s %s", openVPNBinaryPath, strings.Join(commandArgs, " ")), map[string]any{"profileId": profileID})

	if startErr := ovpnTunnel.Process.Start(); startErr != nil {
		return nil, fmt.Errorf("failed to start openvpn.exe: %w", startErr)
	}

	// Start goroutine to capture process exit (used by both waitForConnection and monitorProcess)
	ovpnTunnel.processExitedCh = make(chan error, 1)
	go func() {
		ovpnTunnel.processExitedCh <- ovpnTunnel.Process.Wait()
	}()

	// Wait for the management interface to become available and the tunnel to connect
	if connectErr := ovpnTunnel.waitForConnection(60 * time.Second); connectErr != nil {
		// Include process output in the error for debugging
		outputSnippet := ovpnTunnel.getProcessOutput()
		if len(outputSnippet) > 500 {
			outputSnippet = outputSnippet[len(outputSnippet)-500:]
		}
		ovpnTunnel.killProcess()
		if outputSnippet != "" {
			return nil, fmt.Errorf("OpenVPN connection failed: %w\nProcess output:\n%s", connectErr, outputSnippet)
		}
		return nil, fmt.Errorf("OpenVPN connection failed: %w", connectErr)
	}

	// Start background process monitor
	go ovpnTunnel.monitorProcess()

	return ovpnTunnel, nil
}

// waitForConnection connects to the management interface and waits for CONNECTED state.
// OpenVPN management protocol:
//  1. Connect to TCP socket → receive banner (">INFO:...")
//  2. Send "state on\n" to enable real-time state notifications
//  3. Respond to >PASSWORD: prompts with stored credentials
//  4. Read lines until >STATE:...,CONNECTED,... appears
//  5. Detect adapter IP from the STATE line or by scanning adapters
func (ovpn_tunnel *OpenVPNTunnel) waitForConnection(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	managementAddr := fmt.Sprintf("127.0.0.1:%d", ovpn_tunnel.ManagementPort)

	// Wait for management interface to be ready
	var managementConn net.Conn
	for time.Now().Before(deadline) {
		// Check if process died while we're waiting
		select {
		case exitErr := <-ovpn_tunnel.processExitedCh:
			outputSnippet := ovpn_tunnel.getProcessOutput()
			if len(outputSnippet) > 800 {
				outputSnippet = outputSnippet[len(outputSnippet)-800:]
			}
			return fmt.Errorf("openvpn.exe exited (code: %v) before management interface was ready.\nOutput:\n%s", exitErr, outputSnippet)
		default:
		}

		var dialErr error
		managementConn, dialErr = net.DialTimeout("tcp", managementAddr, 2*time.Second)
		if dialErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if managementConn == nil {
		return fmt.Errorf("timeout connecting to management interface on port %d", ovpn_tunnel.ManagementPort)
	}
	defer managementConn.Close()

	debug.Info("openvpn", "Connected to management interface", map[string]any{"profileId": ovpn_tunnel.ProfileID})

	lineReader := bufio.NewReader(managementConn)
	var detectedVPNIP string
	connected := false
	stateOnSent := false
	readAttempts := 0

	for time.Now().Before(deadline) && !connected {
		// Set a short read deadline so we can check process exit periodically
		managementConn.SetReadDeadline(time.Now().Add(3 * time.Second))

		managementLine, readErr := lineReader.ReadString('\n')

		// Handle timeout — not an error, just retry
		if readErr != nil {
			if netErr, isNetErr := readErr.(net.Error); isNetErr && netErr.Timeout() {
				// Check if process died
				select {
				case exitErr := <-ovpn_tunnel.processExitedCh:
					outputSnippet := ovpn_tunnel.getProcessOutput()
					if len(outputSnippet) > 800 {
						outputSnippet = outputSnippet[len(outputSnippet)-800:]
					}
					return fmt.Errorf("openvpn.exe exited (code: %v) during connection.\nOutput:\n%s", exitErr, outputSnippet)
				default:
				}

				readAttempts++
				// After a few silent timeouts, try sending commands to wake up the interface
				if !stateOnSent && readAttempts >= 2 {
					debug.Debug("openvpn", fmt.Sprintf("No data from management after %d attempts, sending state on", readAttempts), map[string]any{"profileId": ovpn_tunnel.ProfileID})
					managementConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					fmt.Fprintf(managementConn, "state on\n")
					fmt.Fprintf(managementConn, "state\n")
					stateOnSent = true
				}
				continue
			}
			return fmt.Errorf("management interface read error: %w", readErr)
		}

		trimmedLine := strings.TrimSpace(managementLine)
		if trimmedLine == "" {
			continue
		}
		debug.Debug("openvpn", fmt.Sprintf("mgmt: %s", trimmedLine), map[string]any{"profileId": ovpn_tunnel.ProfileID})

		switch {
		// Banner or info messages — now send state on
		case strings.HasPrefix(trimmedLine, ">INFO:"):
			if !stateOnSent {
				managementConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				fmt.Fprintf(managementConn, "state on\n")
				debug.Debug("openvpn", "mgmt >> state on", map[string]any{"profileId": ovpn_tunnel.ProfileID})
				stateOnSent = true
			}

		// Hold prompt (in case --management-hold was in the config file)
		case strings.HasPrefix(trimmedLine, ">HOLD:"):
			managementConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprintf(managementConn, "hold release\n")
			debug.Debug("openvpn", "mgmt >> hold release", map[string]any{"profileId": ovpn_tunnel.ProfileID})

		// Real-time state change: >STATE:<timestamp>,<state>,<detail>,<local_ip>,...
		case strings.HasPrefix(trimmedLine, ">STATE:"):
			stateParts := strings.Split(trimmedLine, ",")
			if len(stateParts) >= 2 {
				stateValue := stateParts[1]
				debug.Info("openvpn", fmt.Sprintf("State: %s", stateValue), map[string]any{"profileId": ovpn_tunnel.ProfileID})

				if stateValue == "CONNECTED" {
					connected = true
					// Extract local VPN IP from state line (field index 3)
					if len(stateParts) >= 4 && stateParts[3] != "" {
						detectedVPNIP = stateParts[3]
						debug.Info("openvpn", fmt.Sprintf("VPN IP from state: %s", detectedVPNIP), map[string]any{"profileId": ovpn_tunnel.ProfileID})
					}
				}
			}

		// Password prompt — respond with stored credentials
		// Format: >PASSWORD:Need 'Auth' username/password
		case strings.HasPrefix(trimmedLine, ">PASSWORD:"):
			managementConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if strings.Contains(trimmedLine, "Need 'Auth'") {
				if ovpn_tunnel.Username == "" {
					return fmt.Errorf("OpenVPN requires username/password but no credentials were provided")
				}
				debug.Info("openvpn", fmt.Sprintf("Providing Auth credentials for user '%s'", ovpn_tunnel.Username), map[string]any{"profileId": ovpn_tunnel.ProfileID})
				fmt.Fprintf(managementConn, "username \"Auth\" %s\n", ovpn_tunnel.Username)
				fmt.Fprintf(managementConn, "password \"Auth\" %s\n", ovpn_tunnel.Password)
			} else if strings.Contains(trimmedLine, "Need 'Private Key'") {
				if ovpn_tunnel.Password == "" {
					return fmt.Errorf("OpenVPN requires a private key password but none was provided")
				}
				fmt.Fprintf(managementConn, "password \"Private Key\" %s\n", ovpn_tunnel.Password)
			} else if strings.Contains(trimmedLine, "Verification Failed") {
				return fmt.Errorf("OpenVPN authentication failed: invalid username or password")
			} else {
				debug.Warn("openvpn", fmt.Sprintf("Unhandled password prompt: %s", trimmedLine), map[string]any{"profileId": ovpn_tunnel.ProfileID})
			}

		// Fatal error from OpenVPN
		case strings.HasPrefix(trimmedLine, ">FATAL:"):
			return fmt.Errorf("OpenVPN fatal error: %s", trimmedLine)

		// Log lines from OpenVPN
		case strings.HasPrefix(trimmedLine, ">LOG:"):
			// Already logged above

		// SUCCESS/ERROR responses to our commands
		case strings.HasPrefix(trimmedLine, "SUCCESS:"), strings.HasPrefix(trimmedLine, "ERROR:"):
			// Already logged above
		}
	}

	if !connected {
		mgmtStatus := "no management messages received"
		if stateOnSent {
			mgmtStatus = "state on sent but no CONNECTED state received"
		}
		return fmt.Errorf("timeout waiting for OpenVPN to connect (waited %v, %s, %d read attempts)", timeout, mgmtStatus, readAttempts)
	}

	// Detect the VPN adapter and its assigned IP
	if detectedVPNIP != "" {
		dnsServer := ""
		if len(ovpn_tunnel.Config.DNSServers) > 0 {
			dnsServer = ovpn_tunnel.Config.DNSServers[0]
		}
		ovpn_tunnel.Dialer = &OSDialer{
			LocalIP:       net.ParseIP(detectedVPNIP),
			DNSServerAddr: dnsServer,
			AssignedAddr:  detectedVPNIP,
		}
		debug.Info("openvpn", fmt.Sprintf("Using VPN IP %s from management interface, DNS %s", detectedVPNIP, dnsServer), map[string]any{"profileId": ovpn_tunnel.ProfileID})
	} else {
		// Fallback: scan for the adapter
		if detectErr := ovpn_tunnel.detectAdapterIP(10 * time.Second); detectErr != nil {
			return fmt.Errorf("failed to detect VPN adapter IP: %w", detectErr)
		}
	}

	ovpn_tunnel.mu.Lock()
	ovpn_tunnel.Stats.Connected = true
	ovpn_tunnel.mu.Unlock()

	return nil
}

// detectAdapterIP finds the OpenVPN adapter and its assigned IP address.
// Looks for TAP-Windows or Wintun adapters that appeared after OpenVPN started.
func (ovpn_tunnel *OpenVPNTunnel) detectAdapterIP(timeout time.Duration) error {
	// Try known OpenVPN adapter name patterns
	adapterNamePatterns := []string{
		"OpenVPN",   // OpenVPN Wintun adapter
		"TAP-Windows", // TAP-Windows6 adapter
		"Wintun",    // Generic Wintun
	}

	for _, adapterPattern := range adapterNamePatterns {
		matchedAdapter, waitErr := system.WaitForAdapterIP(adapterPattern, 500*time.Millisecond, timeout)
		if waitErr == nil && matchedAdapter != nil && len(matchedAdapter.IPv4Addrs) > 0 {
			assignedIP := matchedAdapter.IPv4Addrs[0]
			dnsServer := ""
			if len(matchedAdapter.DNSServers) > 0 {
				dnsServer = matchedAdapter.DNSServers[0]
			}
			// Fallback: use DNS from .ovpn config if adapter doesn't report DNS
			if dnsServer == "" && len(ovpn_tunnel.Config.DNSServers) > 0 {
				dnsServer = ovpn_tunnel.Config.DNSServers[0]
			}

			ovpn_tunnel.Dialer = &OSDialer{
				LocalIP:       net.ParseIP(assignedIP),
				DNSServerAddr: dnsServer,
				AssignedAddr:  assignedIP,
			}

			debug.Info("openvpn", fmt.Sprintf("Detected adapter '%s' with IP %s, DNS %s", matchedAdapter.Name, assignedIP, dnsServer), map[string]any{"profileId": ovpn_tunnel.ProfileID})
			return nil
		}
	}

	return fmt.Errorf("no OpenVPN adapter found with any of the expected name patterns")
}

// monitorProcess watches the openvpn.exe subprocess and marks the tunnel as disconnected if it exits.
// Uses the shared processExitedCh which was already set up before waitForConnection.
func (ovpn_tunnel *OpenVPNTunnel) monitorProcess() {
	defer close(ovpn_tunnel.done)

	select {
	case <-ovpn_tunnel.stopCh:
		// Graceful shutdown requested
		ovpn_tunnel.sendManagementCommand("signal SIGTERM")
		// Give it a few seconds to exit gracefully
		select {
		case <-ovpn_tunnel.processExitedCh:
		case <-time.After(5 * time.Second):
			ovpn_tunnel.killProcess()
		}
	case exitErr := <-ovpn_tunnel.processExitedCh:
		// Process exited on its own
		if exitErr != nil {
			debug.Warn("openvpn", fmt.Sprintf("Process exited with error: %v", exitErr), map[string]any{"profileId": ovpn_tunnel.ProfileID})
		} else {
			debug.Info("openvpn", "Process exited normally", map[string]any{"profileId": ovpn_tunnel.ProfileID})
		}
	}

	ovpn_tunnel.mu.Lock()
	ovpn_tunnel.Stats.Connected = false
	ovpn_tunnel.mu.Unlock()
}

// sendManagementCommand sends a command to the OpenVPN management interface
func (ovpn_tunnel *OpenVPNTunnel) sendManagementCommand(command string) error {
	managementAddr := fmt.Sprintf("127.0.0.1:%d", ovpn_tunnel.ManagementPort)
	managementConn, dialErr := net.DialTimeout("tcp", managementAddr, 2*time.Second)
	if dialErr != nil {
		return dialErr
	}
	defer managementConn.Close()

	managementConn.SetDeadline(time.Now().Add(5 * time.Second))
	_, writeErr := fmt.Fprintf(managementConn, "%s\r\n", command)
	return writeErr
}

// killProcess forcefully terminates the openvpn.exe process
func (ovpn_tunnel *OpenVPNTunnel) killProcess() {
	if ovpn_tunnel.Process != nil && ovpn_tunnel.Process.Process != nil {
		ovpn_tunnel.Process.Process.Kill()
	}
}

// lockedWriter wraps ProcessOutput writes with a mutex and logs each chunk.
// Implements io.Writer so it can be used as Process.Stdout/Stderr.
type lockedWriter struct {
	tunnel    *OpenVPNTunnel
	profileID string
}

func (locked_writer *lockedWriter) Write(data []byte) (int, error) {
	locked_writer.tunnel.mu.Lock()
	// Keep only last 2KB of output to prevent unbounded growth
	locked_writer.tunnel.processOutput = append(locked_writer.tunnel.processOutput, data...)
	if len(locked_writer.tunnel.processOutput) > 2048 {
		locked_writer.tunnel.processOutput = locked_writer.tunnel.processOutput[len(locked_writer.tunnel.processOutput)-2048:]
	}
	locked_writer.tunnel.mu.Unlock()
	trimmedData := strings.TrimSpace(string(data))
	if trimmedData != "" {
		debug.Debug("openvpn", fmt.Sprintf("stdout: %s", trimmedData), map[string]any{"profileId": locked_writer.profileID})
	}
	return len(data), nil
}

// getProcessOutput returns the captured process output (thread-safe)
func (ovpn_tunnel *OpenVPNTunnel) getProcessOutput() string {
	ovpn_tunnel.mu.RLock()
	defer ovpn_tunnel.mu.RUnlock()
	return string(ovpn_tunnel.processOutput)
}

// --- VPNTunnel interface implementation ---

// Dial creates a connection through the OpenVPN tunnel via source IP binding
func (ovpn_tunnel *OpenVPNTunnel) Dial(network, addr string) (net.Conn, error) {
	if ovpn_tunnel.Dialer == nil {
		return nil, fmt.Errorf("OpenVPN tunnel not ready: no adapter IP detected")
	}
	return ovpn_tunnel.Dialer.Dial(network, addr)
}

// Close shuts down the OpenVPN tunnel
func (ovpn_tunnel *OpenVPNTunnel) Close() error {
	close(ovpn_tunnel.stopCh)
	<-ovpn_tunnel.done
	return nil
}

// UpdateStats updates tunnel statistics
func (ovpn_tunnel *OpenVPNTunnel) UpdateStats() {
	// For OpenVPN, stats could be fetched from management interface (future enhancement)
	// For now, just maintain the connected state
}

// GetStats returns the current tunnel statistics
func (ovpn_tunnel *OpenVPNTunnel) GetStats() TunnelStats {
	ovpn_tunnel.mu.RLock()
	defer ovpn_tunnel.mu.RUnlock()
	return ovpn_tunnel.Stats
}

// GetDebugInfo returns OpenVPN-specific debug information
func (ovpn_tunnel *OpenVPNTunnel) GetDebugInfo() string {
	ovpn_tunnel.mu.RLock()
	defer ovpn_tunnel.mu.RUnlock()

	var debugLines []string
	debugLines = append(debugLines, fmt.Sprintf("Type: OpenVPN"))
	debugLines = append(debugLines, fmt.Sprintf("Remote: %s:%s", ovpn_tunnel.Config.RemoteHost, ovpn_tunnel.Config.RemotePort))
	debugLines = append(debugLines, fmt.Sprintf("Protocol: %s", ovpn_tunnel.Config.Protocol))
	debugLines = append(debugLines, fmt.Sprintf("Device: %s", ovpn_tunnel.Config.DeviceType))
	debugLines = append(debugLines, fmt.Sprintf("Management Port: %d", ovpn_tunnel.ManagementPort))
	debugLines = append(debugLines, fmt.Sprintf("Connected: %v", ovpn_tunnel.Stats.Connected))

	if ovpn_tunnel.Dialer != nil {
		debugLines = append(debugLines, fmt.Sprintf("VPN IP: %s", ovpn_tunnel.Dialer.AssignedAddr))
		debugLines = append(debugLines, fmt.Sprintf("DNS Server: %s", ovpn_tunnel.Dialer.DNSServerAddr))
	}

	return strings.Join(debugLines, "\n")
}

// GetDNSServer returns the DNS server for this OpenVPN tunnel
func (ovpn_tunnel *OpenVPNTunnel) GetDNSServer() string {
	if ovpn_tunnel.Dialer != nil {
		return ovpn_tunnel.Dialer.GetDNSServer()
	}
	if len(ovpn_tunnel.Config.DNSServers) > 0 {
		return ovpn_tunnel.Config.DNSServers[0]
	}
	return ""
}

// GetAssignedIP returns the VPN-assigned IP for this OpenVPN tunnel
func (ovpn_tunnel *OpenVPNTunnel) GetAssignedIP() string {
	if ovpn_tunnel.Dialer != nil {
		return ovpn_tunnel.Dialer.GetAssignedIP()
	}
	return ""
}

// getProcessHideWindowAttr returns SysProcAttr to hide the console window on Windows
func getProcessHideWindowAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}
