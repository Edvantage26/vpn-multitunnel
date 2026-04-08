package proxy

import (
	"context"
	"fmt"
	"log"
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
func (host_mapping_cache *HostMappingCache) GetOrAssignIP(hostname string, profileID string) string {
	host_mapping_cache.mu.Lock()
	defer host_mapping_cache.mu.Unlock()

	// Check if hostname already has an IP assigned
	if ip, exists := host_mapping_cache.ipPool[hostname]; exists {
		return ip
	}

	// Find next available IP in the 127.0.x.y range
	// Start from 127.0.100.1 to avoid conflicts with profile-based IPs (127.0.1.1, 127.0.2.1, etc.)
	for idx_octet3 := 100; idx_octet3 < 255; idx_octet3++ {
		for idx_octet4 := 1; idx_octet4 < 255; idx_octet4++ {
			ip := fmt.Sprintf("127.0.%d.%d", idx_octet3, idx_octet4)
			if !host_mapping_cache.usedIPs[ip] {
				host_mapping_cache.ipPool[hostname] = ip
				host_mapping_cache.usedIPs[ip] = true
				return ip
			}
		}
	}

	// Fallback: return a hash-based IP if pool is exhausted (unlikely)
	return "127.0.200.1"
}

// Set stores a new mapping
func (host_mapping_cache *HostMappingCache) Set(mapping *HostMapping) {
	host_mapping_cache.mu.Lock()
	defer host_mapping_cache.mu.Unlock()

	mapping.ResolvedAt = time.Now()
	host_mapping_cache.byTunnelIP[mapping.TunnelIP] = mapping
	host_mapping_cache.byHostname[mapping.Hostname] = mapping
}

// GetByTunnelIP retrieves a mapping by tunnel IP
func (host_mapping_cache *HostMappingCache) GetByTunnelIP(tunnelIP string) *HostMapping {
	host_mapping_cache.mu.RLock()
	defer host_mapping_cache.mu.RUnlock()

	mapping, exists := host_mapping_cache.byTunnelIP[tunnelIP]
	if !exists {
		return nil
	}

	// Check if mapping is still valid
	if time.Since(mapping.ResolvedAt) > host_mapping_cache.mappingTTL {
		return nil
	}

	return mapping
}

// GetByHostname retrieves a mapping by hostname
func (host_mapping_cache *HostMappingCache) GetByHostname(hostname string) *HostMapping {
	host_mapping_cache.mu.RLock()
	defer host_mapping_cache.mu.RUnlock()

	mapping, exists := host_mapping_cache.byHostname[hostname]
	if !exists {
		return nil
	}

	// Check if mapping is still valid
	if time.Since(mapping.ResolvedAt) > host_mapping_cache.mappingTTL {
		return nil
	}

	return mapping
}

// GetAllActive returns all active (non-expired) mappings
func (host_mapping_cache *HostMappingCache) GetAllActive() []*HostMapping {
	host_mapping_cache.mu.RLock()
	defer host_mapping_cache.mu.RUnlock()

	var result []*HostMapping
	now := time.Now()

	for _, mapping := range host_mapping_cache.byHostname {
		if now.Sub(mapping.ResolvedAt) <= host_mapping_cache.mappingTTL {
			result = append(result, mapping)
		}
	}

	return result
}

// Remove removes a mapping by hostname
func (host_mapping_cache *HostMappingCache) Remove(hostname string) {
	host_mapping_cache.mu.Lock()
	defer host_mapping_cache.mu.Unlock()

	if mapping, exists := host_mapping_cache.byHostname[hostname]; exists {
		delete(host_mapping_cache.byTunnelIP, mapping.TunnelIP)
		delete(host_mapping_cache.byHostname, hostname)
	}
}

// Clear removes all mappings
func (host_mapping_cache *HostMappingCache) Clear() {
	host_mapping_cache.mu.Lock()
	defer host_mapping_cache.mu.Unlock()

	host_mapping_cache.byTunnelIP = make(map[string]*HostMapping)
	host_mapping_cache.byHostname = make(map[string]*HostMapping)
}

// Cleanup removes expired mappings and releases their IP allocations
func (host_mapping_cache *HostMappingCache) Cleanup() {
	host_mapping_cache.mu.Lock()
	defer host_mapping_cache.mu.Unlock()

	now := time.Now()
	purged_count := 0
	for hostname, mapping := range host_mapping_cache.byHostname {
		if now.Sub(mapping.ResolvedAt) > host_mapping_cache.mappingTTL {
			delete(host_mapping_cache.byTunnelIP, mapping.TunnelIP)
			delete(host_mapping_cache.byHostname, hostname)
			// Release IP allocation so it can be reused
			if allocated_ip, has_ip := host_mapping_cache.ipPool[hostname]; has_ip {
				delete(host_mapping_cache.usedIPs, allocated_ip)
				delete(host_mapping_cache.ipPool, hostname)
			}
			purged_count++
		}
	}
	if purged_count > 0 {
		log.Printf("HostMapping: cleaned up %d expired entries", purged_count)
	}
}

// StartCleanupLoop runs Cleanup() periodically in the background
func (host_mapping_cache *HostMappingCache) StartCleanupLoop(cleanup_ctx context.Context) {
	cleanup_ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer cleanup_ticker.Stop()
		for {
			select {
			case <-cleanup_ctx.Done():
				return
			case <-cleanup_ticker.C:
				host_mapping_cache.Cleanup()
			}
		}
	}()
}
