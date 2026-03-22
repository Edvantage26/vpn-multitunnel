package proxy

import (
	"fmt"
	"sync"
	"time"
)

// HostMapping stores the DNS resolution information for transparent proxy routing
type HostMapping struct {
	Hostname   string    // Original hostname (e.g., "db.svi")
	RealIP     string    // Resolved IP via tunnel DNS (e.g., "10.0.1.14")
	TunnelIP   string    // Loopback IP for this tunnel (e.g., "127.0.1.1")
	ProfileID  string    // Profile/tunnel ID to route through
	ResolvedAt time.Time // When the resolution happened
}

// HostMappingCache is a thread-safe cache of DNS resolutions for the transparent proxy
type HostMappingCache struct {
	// byTunnelIP maps tunnel IP to the most recent mapping
	byTunnelIP map[string]*HostMapping
	// byHostname maps hostname to mapping (for lookup by hostname)
	byHostname map[string]*HostMapping
	// ipPool tracks which IPs are assigned to which hostnames (for unique IP per hostname)
	ipPool map[string]string // hostname -> assigned IP
	// usedIPs tracks which IPs are in use
	usedIPs map[string]bool
	// baseIP is the base for IP allocation (e.g., "127.0")
	baseIP string
	// mappingTTL is how long mappings are valid
	mappingTTL time.Duration
	mu         sync.RWMutex
}

// NewHostMappingCache creates a new host mapping cache
func NewHostMappingCache(ttl time.Duration) *HostMappingCache {
	if ttl == 0 {
		ttl = 30 * time.Minute // Increased TTL for better stability
	}
	return &HostMappingCache{
		byTunnelIP: make(map[string]*HostMapping),
		byHostname: make(map[string]*HostMapping),
		ipPool:     make(map[string]string),
		usedIPs:    make(map[string]bool),
		baseIP:     "127.0",
		mappingTTL: ttl,
	}
}

// GetOrAssignIP returns an existing IP for the hostname or assigns a new unique one
func (c *HostMappingCache) GetOrAssignIP(hostname string, profileID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if hostname already has an IP assigned
	if ip, exists := c.ipPool[hostname]; exists {
		return ip
	}

	// Find next available IP in the 127.0.x.y range
	// Start from 127.0.100.1 to avoid conflicts with profile-based IPs (127.0.1.1, 127.0.2.1, etc.)
	for x := 100; x < 255; x++ {
		for y := 1; y < 255; y++ {
			ip := fmt.Sprintf("127.0.%d.%d", x, y)
			if !c.usedIPs[ip] {
				c.ipPool[hostname] = ip
				c.usedIPs[ip] = true
				return ip
			}
		}
	}

	// Fallback: return a hash-based IP if pool is exhausted (unlikely)
	return "127.0.200.1"
}

// Set stores a new mapping
func (c *HostMappingCache) Set(mapping *HostMapping) {
	c.mu.Lock()
	defer c.mu.Unlock()

	mapping.ResolvedAt = time.Now()
	c.byTunnelIP[mapping.TunnelIP] = mapping
	c.byHostname[mapping.Hostname] = mapping
}

// GetByTunnelIP retrieves a mapping by tunnel IP
func (c *HostMappingCache) GetByTunnelIP(tunnelIP string) *HostMapping {
	c.mu.RLock()
	defer c.mu.RUnlock()

	mapping, exists := c.byTunnelIP[tunnelIP]
	if !exists {
		return nil
	}

	// Check if mapping is still valid
	if time.Since(mapping.ResolvedAt) > c.mappingTTL {
		return nil
	}

	return mapping
}

// GetByHostname retrieves a mapping by hostname
func (c *HostMappingCache) GetByHostname(hostname string) *HostMapping {
	c.mu.RLock()
	defer c.mu.RUnlock()

	mapping, exists := c.byHostname[hostname]
	if !exists {
		return nil
	}

	// Check if mapping is still valid
	if time.Since(mapping.ResolvedAt) > c.mappingTTL {
		return nil
	}

	return mapping
}

// GetAllActive returns all active (non-expired) mappings
func (c *HostMappingCache) GetAllActive() []*HostMapping {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []*HostMapping
	now := time.Now()

	for _, mapping := range c.byHostname {
		if now.Sub(mapping.ResolvedAt) <= c.mappingTTL {
			result = append(result, mapping)
		}
	}

	return result
}

// Remove removes a mapping by hostname
func (c *HostMappingCache) Remove(hostname string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if mapping, exists := c.byHostname[hostname]; exists {
		delete(c.byTunnelIP, mapping.TunnelIP)
		delete(c.byHostname, hostname)
	}
}

// Clear removes all mappings
func (c *HostMappingCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byTunnelIP = make(map[string]*HostMapping)
	c.byHostname = make(map[string]*HostMapping)
}

// Cleanup removes expired mappings
func (c *HostMappingCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for hostname, mapping := range c.byHostname {
		if now.Sub(mapping.ResolvedAt) > c.mappingTTL {
			delete(c.byTunnelIP, mapping.TunnelIP)
			delete(c.byHostname, hostname)
		}
	}
}
