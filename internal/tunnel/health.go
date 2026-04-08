package tunnel

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"vpnmultitunnel/internal/debug"
)

// HealthChecker performs periodic health checks on a tunnel
type HealthChecker struct {
	ProfileID string
	TargetIP  string
	Interval  time.Duration
	tunnel    VPNTunnel

	healthy              bool
	lastPing             time.Duration
	lastHandshakeStale   bool
	previousBytesSent    uint64
	previousBytesRecv    uint64
	mu                   sync.RWMutex

	stopCh chan struct{}
	done   chan struct{}
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(profileID, targetIP string, intervalSeconds int, tunnel VPNTunnel) *HealthChecker {
	return &HealthChecker{
		ProfileID: profileID,
		TargetIP:  targetIP,
		Interval:  time.Duration(intervalSeconds) * time.Second,
		tunnel:    tunnel,
		healthy:   false,
		stopCh:    make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Start begins the health check loop
func (hc *HealthChecker) Start() {
	go hc.run()
}

// Stop stops the health check loop
func (hc *HealthChecker) Stop() {
	close(hc.stopCh)
	<-hc.done
}

// IsHealthy returns whether the tunnel is healthy
func (hc *HealthChecker) IsHealthy() bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.healthy
}

// LastPing returns the last ping duration
func (hc *HealthChecker) LastPing() time.Duration {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.lastPing
}

func (hc *HealthChecker) run() {
	defer close(hc.done)

	// Do an initial check
	hc.check()

	ticker := time.NewTicker(hc.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-hc.stopCh:
			return
		case <-ticker.C:
			hc.check()
		}
	}
}

func (hc *HealthChecker) check() {
	start := time.Now()
	diagnosticLogger := debug.GetLogger()

	// Log tunnel stats (handshake age, bytes tx/rx) before health check
	hc.logTunnelDiagnostics(diagnosticLogger)

	// Try to establish a TCP connection to the target
	// We use TCP because ICMP requires elevated privileges
	// Try common ports that are likely open
	ports := []string{"22", "80", "443", "53"}

	var success bool
	for _, port := range ports {
		addr := net.JoinHostPort(hc.TargetIP, port)
		conn, dialErr := hc.tunnel.Dial("tcp", addr)
		if dialErr == nil {
			conn.Close()
			success = true
			break
		}
	}

	pingDuration := time.Since(start)

	hc.mu.Lock()
	wasHealthy := hc.healthy
	hc.healthy = success
	hc.lastPing = pingDuration
	hc.mu.Unlock()

	if success {
		if !wasHealthy {
			log.Printf("[%s] Health check: Tunnel is now healthy (ping: %v)", hc.ProfileID, pingDuration)
			diagnosticLogger.InfoProfile("health", hc.ProfileID, fmt.Sprintf("Tunnel recovered - healthy (ping: %v)", pingDuration), nil)
		}
	} else {
		if wasHealthy {
			log.Printf("[%s] Health check: Tunnel is now unhealthy", hc.ProfileID)
			diagnosticLogger.WarnProfile("health", hc.ProfileID, "Tunnel became unhealthy - all TCP health check ports failed", map[string]any{
				"targetIP": hc.TargetIP,
				"ping":     pingDuration.String(),
			})
		}
	}
}

// logTunnelDiagnostics logs handshake age, bytes tx/rx, and keepalive status
func (hc *HealthChecker) logTunnelDiagnostics(diagnosticLogger *debug.Logger) {
	hc.tunnel.UpdateStats()
	tunnelStats := hc.tunnel.GetStats()

	// Calculate handshake age
	handshakeAgeSeconds := int64(-1)
	if tunnelStats.LastHandshake != "" {
		var handshakeTimestamp int64
		fmt.Sscanf(tunnelStats.LastHandshake, "%d", &handshakeTimestamp)
		if handshakeTimestamp > 0 {
			handshakeAgeSeconds = time.Now().Unix() - handshakeTimestamp
		}
	}

	// Check for traffic changes since last check
	hc.mu.RLock()
	prevBytesSent := hc.previousBytesSent
	prevBytesRecv := hc.previousBytesRecv
	wasHandshakeStale := hc.lastHandshakeStale
	hc.mu.RUnlock()

	deltaSentBytes := tunnelStats.BytesSent - prevBytesSent
	deltaRecvBytes := tunnelStats.BytesRecv - prevBytesRecv

	// Detect handshake staleness (>180s is WireGuard's internal threshold)
	isHandshakeStale := handshakeAgeSeconds > 180

	// Log handshake state transitions
	if isHandshakeStale && !wasHandshakeStale {
		diagnosticLogger.WarnProfile("tunnel", hc.ProfileID, fmt.Sprintf("WireGuard handshake is stale (age: %ds) - NAT mapping may have expired", handshakeAgeSeconds), map[string]any{
			"handshakeAgeSeconds": handshakeAgeSeconds,
			"bytesSent":          tunnelStats.BytesSent,
			"bytesRecv":          tunnelStats.BytesRecv,
		})
	} else if !isHandshakeStale && wasHandshakeStale {
		diagnosticLogger.InfoProfile("tunnel", hc.ProfileID, fmt.Sprintf("WireGuard handshake recovered (age: %ds)", handshakeAgeSeconds), map[string]any{
			"handshakeAgeSeconds": handshakeAgeSeconds,
		})
	}

	// Log periodic diagnostics (every check, at debug level)
	diagnosticLogger.DebugProfile("tunnel", hc.ProfileID, "Tunnel diagnostics", map[string]any{
		"handshakeAgeSeconds": handshakeAgeSeconds,
		"bytesSent":          tunnelStats.BytesSent,
		"bytesRecv":          tunnelStats.BytesRecv,
		"deltaSentBytes":     deltaSentBytes,
		"deltaRecvBytes":     deltaRecvBytes,
		"healthy":            hc.healthy,
	})

	// Warn if no traffic in either direction (possible stall)
	if prevBytesSent > 0 && deltaSentBytes == 0 && deltaRecvBytes == 0 {
		diagnosticLogger.WarnProfile("tunnel", hc.ProfileID, "No traffic detected since last health check", map[string]any{
			"handshakeAgeSeconds": handshakeAgeSeconds,
			"interval":           hc.Interval.String(),
		})
	}

	// Update previous values
	hc.mu.Lock()
	hc.previousBytesSent = tunnelStats.BytesSent
	hc.previousBytesRecv = tunnelStats.BytesRecv
	hc.lastHandshakeStale = isHandshakeStale
	hc.mu.Unlock()
}

// PingTarget performs a single ping check and returns success and duration
func PingTarget(tunnel VPNTunnel, targetIP string) (bool, time.Duration) {
	start := time.Now()

	// Try common ports
	ports := []string{"22", "80", "443", "53"}
	for _, port := range ports {
		addr := net.JoinHostPort(targetIP, port)
		conn, err := tunnel.Dial("tcp", addr)
		if err == nil {
			conn.Close()
			return true, time.Since(start)
		}
	}

	return false, time.Since(start)
}
