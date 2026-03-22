package tunnel

import (
	"log"
	"net"
	"sync"
	"time"
)

// HealthChecker performs periodic health checks on a tunnel
type HealthChecker struct {
	ProfileID string
	TargetIP  string
	Interval  time.Duration
	tunnel    *Tunnel

	healthy  bool
	lastPing time.Duration
	mu       sync.RWMutex

	stopCh chan struct{}
	done   chan struct{}
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(profileID, targetIP string, intervalSeconds int, tunnel *Tunnel) *HealthChecker {
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

	// Try to establish a TCP connection to the target
	// We use TCP because ICMP requires elevated privileges
	// Try common ports that are likely open
	ports := []string{"22", "80", "443", "53"}

	var success bool
	for _, port := range ports {
		addr := net.JoinHostPort(hc.TargetIP, port)
		conn, err := hc.tunnel.Dial("tcp", addr)
		if err == nil {
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
		}
	} else {
		if wasHealthy {
			log.Printf("[%s] Health check: Tunnel is now unhealthy", hc.ProfileID)
		}
	}
}

// PingTarget performs a single ping check and returns success and duration
func PingTarget(tunnel *Tunnel, targetIP string) (bool, time.Duration) {
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
