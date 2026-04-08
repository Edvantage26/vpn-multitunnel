package tunnel

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/system"
)

// WatchGuardTunnel manages a WatchGuard Mobile VPN with SSL client subprocess
// and provides a VPNTunnel interface by binding connections to the VPN adapter's IP.
type WatchGuardTunnel struct {
	ProfileID      string
	Config         *config.WatchGuardConfig
	Process        *exec.Cmd
	Dialer         *OSDialer
	AdapterName    string
	adaptersBefore []system.AdapterInfo // snapshot before client launch

	Stats TunnelStats
	mu    sync.RWMutex

	stopCh chan struct{}
	done   chan struct{}
}

// wellKnownWatchGuardPaths lists common installation paths for the WatchGuard SSL VPN client
var wellKnownWatchGuardPaths = []string{
	`C:\Program Files\WatchGuard\WatchGuard Mobile VPN with SSL\wgsslvpnc.exe`,
	`C:\Program Files (x86)\WatchGuard\WatchGuard Mobile VPN with SSL\wgsslvpnc.exe`,
	`C:\Program Files\WatchGuard\WatchGuard Mobile VPN with SSL\WatchGuard Mobile VPN with SSL client.exe`,
	`C:\Program Files (x86)\WatchGuard\WatchGuard Mobile VPN with SSL\WatchGuard Mobile VPN with SSL client.exe`,
}

// FindWatchGuardBinary locates the WatchGuard SSL VPN client binary.
func FindWatchGuardBinary() (string, error) {
	// Check PATH first
	pathResult, lookErr := exec.LookPath("wgsslvpnc.exe")
	if lookErr == nil {
		return pathResult, nil
	}

	// Check well-known paths
	for _, candidatePath := range wellKnownWatchGuardPaths {
		if _, statErr := os.Stat(candidatePath); statErr == nil {
			return candidatePath, nil
		}
	}

	return "", fmt.Errorf("WatchGuard SSL VPN client not found in PATH or standard installation directories")
}

// NewWatchGuardTunnel creates and starts a WatchGuard SSL VPN tunnel.
// It launches the WatchGuard client, waits for the VPN adapter to appear,
// detects the assigned IP, and creates an OSDialer.
func NewWatchGuardTunnel(profileID string, wgConfig *config.WatchGuardConfig) (*WatchGuardTunnel, error) {
	watchGuardBinaryPath, findErr := FindWatchGuardBinary()
	if findErr != nil {
		return nil, findErr
	}

	wgTunnel := &WatchGuardTunnel{
		ProfileID: profileID,
		Config:    wgConfig,
		Stats: TunnelStats{
			Connected: false,
			Endpoint:  fmt.Sprintf("%s:%s", wgConfig.ServerAddress, wgConfig.ServerPort),
		},
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}

	// Snapshot adapters BEFORE launching the client so we can detect new ones
	adaptersBefore, snapshotErr := system.GetAllAdapters()
	if snapshotErr != nil {
		return nil, fmt.Errorf("failed to snapshot network adapters: %w", snapshotErr)
	}
	wgTunnel.adaptersBefore = adaptersBefore

	// Launch the WatchGuard client GUI — the user will enter credentials in its window.
	// No CLI args — the GUI app handles server/auth on its own.
	wgTunnel.Process = exec.Command(watchGuardBinaryPath)
	// Do NOT hide the window — the user needs to interact with the WatchGuard GUI
	wgTunnel.Process.SysProcAttr = &syscall.SysProcAttr{}

	log.Printf("[WatchGuard/%s] Launching WatchGuard SSL VPN client (user will connect via its GUI)", profileID)

	if startErr := wgTunnel.Process.Start(); startErr != nil {
		return nil, fmt.Errorf("failed to start WatchGuard client: %w", startErr)
	}

	// Wait for the VPN adapter to appear (user connects via WatchGuard GUI)
	// Use a longer timeout since the user needs to interact with the GUI
	if connectErr := wgTunnel.waitForConnection(120 * time.Second); connectErr != nil {
		// Don't kill the process — the user may still be interacting with it
		return nil, fmt.Errorf("WatchGuard connection failed: %w", connectErr)
	}

	// Start background process monitor
	go wgTunnel.monitorProcess()

	return wgTunnel, nil
}

// waitForConnection detects the VPN adapter by comparing adapters before and after
// the client starts. Any new adapter with an IPv4 address that wasn't there before
// is assumed to be the VPN adapter. This is more reliable than matching by name
// since adapter names vary across WatchGuard client versions.
func (wg_tunnel *WatchGuardTunnel) waitForConnection(timeout time.Duration) error {
	existingAdapterNames := make(map[string]bool)
	for _, adapter := range wg_tunnel.adaptersBefore {
		existingAdapterNames[adapter.Name] = true
	}

	deadline := time.Now().Add(timeout)
	pollInterval := 1 * time.Second

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		currentAdapters, listErr := system.GetAllAdapters()
		if listErr != nil {
			continue
		}

		for _, adapter := range currentAdapters {
			// Skip adapters that existed before
			if existingAdapterNames[adapter.Name] {
				continue
			}
			// Skip adapters without IPv4
			if !adapter.IsUp() || len(adapter.IPv4Addrs) == 0 {
				continue
			}
			// Skip loopback
			if strings.Contains(strings.ToLower(adapter.Name), "loopback") {
				continue
			}

			assignedIP := adapter.IPv4Addrs[0]
			dnsServer := ""
			if len(adapter.DNSServers) > 0 {
				dnsServer = adapter.DNSServers[0]
			}

			wg_tunnel.AdapterName = adapter.Name
			wg_tunnel.Dialer = &OSDialer{
				LocalIP:       net.ParseIP(assignedIP),
				DNSServerAddr: dnsServer,
				AssignedAddr:  assignedIP,
			}

			log.Printf("[WatchGuard/%s] Detected new adapter '%s' with IP %s, DNS %s",
				wg_tunnel.ProfileID, adapter.Name, assignedIP, dnsServer)

			wg_tunnel.mu.Lock()
			wg_tunnel.Stats.Connected = true
			wg_tunnel.mu.Unlock()

			return nil
		}
	}

	// List current adapters in error for debugging
	currentAdapters, _ := system.GetAllAdapters()
	var adapterNames []string
	for _, adapter := range currentAdapters {
		status := "down"
		if adapter.IsUp() {
			status = "up"
		}
		adapterNames = append(adapterNames, fmt.Sprintf("%s (%s, IPs: %v)", adapter.Name, status, adapter.IPv4Addrs))
	}
	return fmt.Errorf("no new VPN adapter detected after %v.\nCurrent adapters:\n%s",
		timeout, strings.Join(adapterNames, "\n"))
}

// monitorProcess watches the WatchGuard client process and marks the tunnel as disconnected if it exits
func (wg_tunnel *WatchGuardTunnel) monitorProcess() {
	defer close(wg_tunnel.done)

	processExitCh := make(chan error, 1)
	go func() {
		processExitCh <- wg_tunnel.Process.Wait()
	}()

	select {
	case <-wg_tunnel.stopCh:
		// Graceful shutdown: try to disconnect cleanly, then kill
		wg_tunnel.killProcess()
		select {
		case <-processExitCh:
		case <-time.After(10 * time.Second):
			log.Printf("[WatchGuard/%s] Force-killing process after timeout", wg_tunnel.ProfileID)
		}
	case exitErr := <-processExitCh:
		if exitErr != nil {
			log.Printf("[WatchGuard/%s] Process exited with error: %v", wg_tunnel.ProfileID, exitErr)
		} else {
			log.Printf("[WatchGuard/%s] Process exited normally", wg_tunnel.ProfileID)
		}
	}

	wg_tunnel.mu.Lock()
	wg_tunnel.Stats.Connected = false
	wg_tunnel.mu.Unlock()
}

// killProcess forcefully terminates the WatchGuard client process
func (wg_tunnel *WatchGuardTunnel) killProcess() {
	if wg_tunnel.Process != nil && wg_tunnel.Process.Process != nil {
		wg_tunnel.Process.Process.Kill()
	}
}

// --- VPNTunnel interface implementation ---

// Dial creates a connection through the WatchGuard tunnel via source IP binding
func (wg_tunnel *WatchGuardTunnel) Dial(network, addr string) (net.Conn, error) {
	if wg_tunnel.Dialer == nil {
		return nil, fmt.Errorf("WatchGuard tunnel not ready: no adapter IP detected")
	}
	return wg_tunnel.Dialer.Dial(network, addr)
}

// Close shuts down the WatchGuard tunnel
func (wg_tunnel *WatchGuardTunnel) Close() error {
	close(wg_tunnel.stopCh)
	<-wg_tunnel.done
	return nil
}

// UpdateStats updates tunnel statistics (limited for WatchGuard — no management interface)
func (wg_tunnel *WatchGuardTunnel) UpdateStats() {
	// Check if the adapter is still up
	if wg_tunnel.AdapterName != "" {
		matchedAdapter, findErr := system.FindAdapterByNameSubstring(wg_tunnel.AdapterName)
		if findErr != nil || matchedAdapter == nil || !matchedAdapter.IsUp() {
			wg_tunnel.mu.Lock()
			wg_tunnel.Stats.Connected = false
			wg_tunnel.mu.Unlock()
		}
	}
}

// GetStats returns the current tunnel statistics
func (wg_tunnel *WatchGuardTunnel) GetStats() TunnelStats {
	wg_tunnel.mu.RLock()
	defer wg_tunnel.mu.RUnlock()
	return wg_tunnel.Stats
}

// GetDebugInfo returns WatchGuard-specific debug information
func (wg_tunnel *WatchGuardTunnel) GetDebugInfo() string {
	wg_tunnel.mu.RLock()
	defer wg_tunnel.mu.RUnlock()

	var debugLines []string
	debugLines = append(debugLines, "Type: WatchGuard Mobile VPN with SSL")
	debugLines = append(debugLines, fmt.Sprintf("Server: %s:%s", wg_tunnel.Config.ServerAddress, wg_tunnel.Config.ServerPort))
	debugLines = append(debugLines, fmt.Sprintf("Adapter: %s", wg_tunnel.AdapterName))
	debugLines = append(debugLines, fmt.Sprintf("Connected: %v", wg_tunnel.Stats.Connected))

	if wg_tunnel.Dialer != nil {
		debugLines = append(debugLines, fmt.Sprintf("VPN IP: %s", wg_tunnel.Dialer.AssignedAddr))
		debugLines = append(debugLines, fmt.Sprintf("DNS Server: %s", wg_tunnel.Dialer.DNSServerAddr))
	}

	return strings.Join(debugLines, "\n")
}

// GetDNSServer returns the DNS server for this WatchGuard tunnel
func (wg_tunnel *WatchGuardTunnel) GetDNSServer() string {
	if wg_tunnel.Dialer != nil {
		return wg_tunnel.Dialer.GetDNSServer()
	}
	return ""
}

// GetAssignedIP returns the VPN-assigned IP for this WatchGuard tunnel
func (wg_tunnel *WatchGuardTunnel) GetAssignedIP() string {
	if wg_tunnel.Dialer != nil {
		return wg_tunnel.Dialer.GetAssignedIP()
	}
	return ""
}
