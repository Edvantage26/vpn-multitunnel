package tunnel

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/system"
)

// ExternalTunnel represents an externally-managed VPN connection.
// It does NOT start or manage any VPN process. Instead, it monitors
// the system's network adapters and creates an OSDialer when the
// expected VPN adapter appears with an IP address.
//
// This supports any VPN that creates a network adapter — WatchGuard,
// Cisco AnyConnect, GlobalProtect, FortiClient, etc.
type ExternalTunnel struct {
	ProfileID      string
	Config         *config.ExternalVPNConfig
	Dialer         *OSDialer
	AdapterName    string   // Detected adapter name
	adaptersBefore []system.AdapterInfo // Snapshot before connect (for auto-detect)

	Stats TunnelStats
	mu    sync.RWMutex

	stopCh chan struct{}
	done   chan struct{}
}

// NewExternalTunnel creates an external tunnel that monitors for a VPN adapter.
// It never blocks — always returns immediately and starts a background monitor.
// If the adapter is already up, it's detected immediately (Connected = true).
// If not, the monitor polls until it appears (Connected = false until then).
func NewExternalTunnel(profileID string, extConfig *config.ExternalVPNConfig) (*ExternalTunnel, error) {
	extTunnel := &ExternalTunnel{
		ProfileID: profileID,
		Config:    extConfig,
		Stats: TunnelStats{
			Connected: false,
		},
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}

	// Snapshot current adapters for auto-detect mode
	adaptersBefore, snapshotErr := system.GetAllAdapters()
	if snapshotErr != nil {
		return nil, fmt.Errorf("failed to snapshot adapters: %w", snapshotErr)
	}
	extTunnel.adaptersBefore = adaptersBefore

	// Try to find the adapter immediately (it may already be connected)
	if extTunnel.tryDetectAdapter() {
		log.Printf("[External/%s] Adapter already connected: '%s' with IP %s",
			profileID, extTunnel.AdapterName, extTunnel.Dialer.AssignedAddr)
		extTunnel.mu.Lock()
		extTunnel.Stats.Connected = true
		extTunnel.mu.Unlock()
	} else {
		log.Printf("[External/%s] Adapter not found yet, monitoring... (name filter: '%s', auto-detect: %v)",
			profileID, extConfig.AdapterName, extConfig.AdapterAutoDetect)
	}

	// Always start the background monitor — it handles connect/disconnect/reconnect
	go extTunnel.monitorAdapter()

	return extTunnel, nil
}

// tryDetectAdapter checks if the expected adapter is present and has an IP.
// Returns true if found and Dialer is set up.
func (ext_tunnel *ExternalTunnel) tryDetectAdapter() bool {
	if ext_tunnel.Config.AdapterName != "" {
		// Match by name substring
		matchedAdapter, findErr := system.FindAdapterByNameSubstring(ext_tunnel.Config.AdapterName)
		if findErr != nil || matchedAdapter == nil || !matchedAdapter.IsUp() || len(matchedAdapter.IPv4Addrs) == 0 {
			return false
		}
		ext_tunnel.setupDialer(matchedAdapter)
		return true
	}

	if ext_tunnel.Config.AdapterAutoDetect {
		// Detect any NEW adapter that appeared since snapshot
		existingNames := make(map[string]bool)
		for _, adapter := range ext_tunnel.adaptersBefore {
			existingNames[adapter.Name] = true
		}

		currentAdapters, listErr := system.GetAllAdapters()
		if listErr != nil {
			return false
		}

		for _, adapter := range currentAdapters {
			if existingNames[adapter.Name] {
				continue
			}
			if !adapter.IsUp() || len(adapter.IPv4Addrs) == 0 {
				continue
			}
			if strings.Contains(strings.ToLower(adapter.Name), "loopback") {
				continue
			}
			ext_tunnel.setupDialer(&adapter)
			return true
		}
	}

	return false
}

// setupDialer creates the OSDialer from a detected adapter
func (ext_tunnel *ExternalTunnel) setupDialer(adapter *system.AdapterInfo) {
	assignedIP := adapter.IPv4Addrs[0]
	dnsServer := ext_tunnel.Config.DNSServer
	if dnsServer == "" && len(adapter.DNSServers) > 0 {
		dnsServer = adapter.DNSServers[0]
	}

	ext_tunnel.AdapterName = adapter.Name
	ext_tunnel.Dialer = &OSDialer{
		LocalIP:       net.ParseIP(assignedIP),
		DNSServerAddr: dnsServer,
		AssignedAddr:  assignedIP,
	}

	ext_tunnel.mu.Lock()
	ext_tunnel.Stats.Endpoint = fmt.Sprintf("adapter: %s", adapter.Name)
	ext_tunnel.mu.Unlock()
}

// monitorAdapter periodically checks if the adapter is still up.
// If it goes down, marks the tunnel as disconnected.
// If it comes back up, re-establishes the dialer.
func (ext_tunnel *ExternalTunnel) monitorAdapter() {
	defer close(ext_tunnel.done)

	pollInterval := time.Duration(ext_tunnel.Config.PollIntervalSec) * time.Second
	if pollInterval < time.Second {
		pollInterval = 2 * time.Second
	}

	for {
		select {
		case <-ext_tunnel.stopCh:
			return
		case <-time.After(pollInterval):
		}

		wasConnected := ext_tunnel.IsConnected()
		adapterFound := ext_tunnel.tryDetectAdapter()

		if wasConnected && !adapterFound {
			log.Printf("[External/%s] Adapter '%s' went down", ext_tunnel.ProfileID, ext_tunnel.AdapterName)
			ext_tunnel.mu.Lock()
			ext_tunnel.Stats.Connected = false
			ext_tunnel.Dialer = nil
			ext_tunnel.mu.Unlock()
		} else if !wasConnected && adapterFound {
			log.Printf("[External/%s] Adapter '%s' came back with IP %s",
				ext_tunnel.ProfileID, ext_tunnel.AdapterName, ext_tunnel.Dialer.AssignedAddr)
			ext_tunnel.mu.Lock()
			ext_tunnel.Stats.Connected = true
			ext_tunnel.mu.Unlock()
		}
	}
}

// IsConnected returns whether the external adapter is currently up
func (ext_tunnel *ExternalTunnel) IsConnected() bool {
	ext_tunnel.mu.RLock()
	defer ext_tunnel.mu.RUnlock()
	return ext_tunnel.Stats.Connected
}

// --- VPNTunnel interface implementation ---

func (ext_tunnel *ExternalTunnel) Dial(network, addr string) (net.Conn, error) {
	ext_tunnel.mu.RLock()
	dialer := ext_tunnel.Dialer
	ext_tunnel.mu.RUnlock()

	if dialer == nil {
		return nil, fmt.Errorf("external VPN adapter not connected")
	}
	return dialer.Dial(network, addr)
}

func (ext_tunnel *ExternalTunnel) Close() error {
	close(ext_tunnel.stopCh)
	<-ext_tunnel.done
	return nil
}

func (ext_tunnel *ExternalTunnel) UpdateStats() {
	// Check if adapter is still up — monitorAdapter handles this
}

func (ext_tunnel *ExternalTunnel) GetStats() TunnelStats {
	ext_tunnel.mu.RLock()
	defer ext_tunnel.mu.RUnlock()
	return ext_tunnel.Stats
}

func (ext_tunnel *ExternalTunnel) GetDebugInfo() string {
	ext_tunnel.mu.RLock()
	defer ext_tunnel.mu.RUnlock()

	var debugLines []string
	debugLines = append(debugLines, "Type: External VPN")
	debugLines = append(debugLines, fmt.Sprintf("Adapter Filter: %s", ext_tunnel.Config.AdapterName))
	debugLines = append(debugLines, fmt.Sprintf("Auto-Detect: %v", ext_tunnel.Config.AdapterAutoDetect))
	debugLines = append(debugLines, fmt.Sprintf("Detected Adapter: %s", ext_tunnel.AdapterName))
	debugLines = append(debugLines, fmt.Sprintf("Connected: %v", ext_tunnel.Stats.Connected))

	if ext_tunnel.Dialer != nil {
		debugLines = append(debugLines, fmt.Sprintf("VPN IP: %s", ext_tunnel.Dialer.AssignedAddr))
		debugLines = append(debugLines, fmt.Sprintf("DNS Server: %s", ext_tunnel.Dialer.DNSServerAddr))
	}

	return strings.Join(debugLines, "\n")
}

func (ext_tunnel *ExternalTunnel) GetDNSServer() string {
	ext_tunnel.mu.RLock()
	defer ext_tunnel.mu.RUnlock()
	if ext_tunnel.Dialer != nil {
		return ext_tunnel.Dialer.GetDNSServer()
	}
	return ext_tunnel.Config.DNSServer
}

func (ext_tunnel *ExternalTunnel) GetAssignedIP() string {
	ext_tunnel.mu.RLock()
	defer ext_tunnel.mu.RUnlock()
	if ext_tunnel.Dialer != nil {
		return ext_tunnel.Dialer.GetAssignedIP()
	}
	return ""
}
