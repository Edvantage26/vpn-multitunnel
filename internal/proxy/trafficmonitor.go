package proxy

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"vpnmultitunnel/internal/debug"
)

// ConnectionStatus represents the lifecycle state of a TCP connection
type ConnectionStatus string

const (
	ConnectionStatusActive ConnectionStatus = "active"
	ConnectionStatusClosed ConnectionStatus = "closed"
)

// ProtocolHint indicates the detected protocol type of a connection
type ProtocolHint string

const (
	ProtocolHintTLS         ProtocolHint = "tls"
	ProtocolHintPlain       ProtocolHint = "plain"
	ProtocolHintWebSocket   ProtocolHint = "ws"
	ProtocolHintTLSLongLive ProtocolHint = "tls-long-lived"
)

// TrafficEntry captures per-connection metadata (no payload inspection)
type TrafficEntry struct {
	ConnectionID  string           `json:"connectionId"`
	Hostname      string           `json:"hostname"`
	SNIHostname   string           `json:"sniHostname"`
	TunnelIP      string           `json:"tunnelIP"`
	RealIP        string           `json:"realIP"`
	ProfileID     string           `json:"profileId"`
	Port          int              `json:"port"`
	ProtocolHint  ProtocolHint     `json:"protocolHint"`
	BytesSent     int64            `json:"bytesSent"`
	BytesReceived int64            `json:"bytesReceived"`
	Status        ConnectionStatus `json:"status"`
	StartedAt      time.Time        `json:"startedAt"`
	ClosedAt       *time.Time       `json:"closedAt,omitempty"`
	DurationMs     int64            `json:"durationMs"`
	LastByteUpdate time.Time        `json:"-"` // internal: tracks when bytes were last updated
}

// DNSLogEntry captures a single DNS query/response event
type DNSLogEntry struct {
	Timestamp      time.Time `json:"timestamp"`
	Domain         string    `json:"domain"`
	QueryType      string    `json:"queryType"`
	ResolvedIP     string    `json:"resolvedIP"`
	TunnelIP       string    `json:"tunnelIP"`
	ProfileID      string    `json:"profileId"`
	ResponseTimeMs int64     `json:"responseTimeMs"`
	Success        bool      `json:"success"`
	ErrorMessage   string    `json:"errorMessage,omitempty"`
	ViaTunnel      bool      `json:"viaTunnel"`
	SourceProcess  string    `json:"sourceProcess,omitempty"`
}

// ProfileTrafficSummary aggregates traffic stats per profile
type ProfileTrafficSummary struct {
	ProfileID         string `json:"profileId"`
	ActiveConnections int    `json:"activeConnections"`
	TotalConnections  int64  `json:"totalConnections"`
	TotalBytesSent    int64  `json:"totalBytesSent"`
	TotalBytesRecv    int64  `json:"totalBytesRecv"`
	TotalDNSQueries   int64  `json:"totalDnsQueries"`
}

// TrafficMonitor collects and stores traffic metadata from TCP proxy and DNS proxy
type TrafficMonitor struct {
	connectionBuffer  *debug.RingBuffer[TrafficEntry]
	dnsLogBuffer      *debug.RingBuffer[DNSLogEntry]
	activeConnections map[string]*TrafficEntry
	profileSummaries  map[string]*ProfileTrafficSummary
	eventEmitter      func(eventName string, eventData interface{})
	lastUpdateEmit    atomic.Int64 // unix millis of last connection-update emit (throttle)
	mu                sync.RWMutex
}

var (
	trafficMonitorInstance *TrafficMonitor
	trafficMonitorOnce     sync.Once
)

// GetTrafficMonitor returns the singleton TrafficMonitor instance
func GetTrafficMonitor() *TrafficMonitor {
	trafficMonitorOnce.Do(func() {
		trafficMonitorInstance = &TrafficMonitor{
			connectionBuffer:  debug.NewRingBuffer[TrafficEntry](5000),
			dnsLogBuffer:      debug.NewRingBuffer[DNSLogEntry](10000),
			activeConnections: make(map[string]*TrafficEntry),
			profileSummaries:  make(map[string]*ProfileTrafficSummary),
		}
		go trafficMonitorInstance.longLivedHeuristicLoop()
	})
	return trafficMonitorInstance
}

// SetEventEmitter sets the callback used to emit events to the frontend
func (traffic_monitor *TrafficMonitor) SetEventEmitter(emitter func(string, interface{})) {
	traffic_monitor.mu.Lock()
	defer traffic_monitor.mu.Unlock()
	traffic_monitor.eventEmitter = emitter
}

// emitEvent safely emits an event if the emitter is configured
func (traffic_monitor *TrafficMonitor) emitEvent(event_name string, event_data interface{}) {
	if traffic_monitor.eventEmitter != nil {
		traffic_monitor.eventEmitter(event_name, event_data)
	}
}

// RecordConnectionOpen records a new TCP connection being established
func (traffic_monitor *TrafficMonitor) RecordConnectionOpen(traffic_entry TrafficEntry) {
	traffic_monitor.mu.Lock()
	defer traffic_monitor.mu.Unlock()

	traffic_entry.Status = ConnectionStatusActive
	traffic_entry.StartedAt = time.Now()
	traffic_entry.LastByteUpdate = time.Now()
	traffic_monitor.activeConnections[traffic_entry.ConnectionID] = &traffic_entry

	// Update profile summary
	summary := traffic_monitor.getOrCreateProfileSummary(traffic_entry.ProfileID)
	summary.ActiveConnections++
	summary.TotalConnections++

	log.Printf("Traffic: connection opened %s -> %s:%d [%s] profile=%s",
		traffic_entry.Hostname, traffic_entry.RealIP, traffic_entry.Port,
		traffic_entry.ProtocolHint, traffic_entry.ProfileID)

	traffic_monitor.emitEvent("traffic-connection-open", traffic_entry)
}

// UpdateConnectionBytes updates the byte counters for an active connection
func (traffic_monitor *TrafficMonitor) UpdateConnectionBytes(connection_id string, bytes_sent int64, bytes_received int64) {
	traffic_monitor.mu.Lock()
	defer traffic_monitor.mu.Unlock()

	active_entry, exists := traffic_monitor.activeConnections[connection_id]
	if !exists {
		return
	}

	active_entry.BytesSent = bytes_sent
	active_entry.BytesReceived = bytes_received
	active_entry.DurationMs = time.Since(active_entry.StartedAt).Milliseconds()
	active_entry.LastByteUpdate = time.Now()

	// Throttle event emission to at most once per 500ms globally
	now_millis := time.Now().UnixMilli()
	last_emit_millis := traffic_monitor.lastUpdateEmit.Load()
	if now_millis-last_emit_millis >= 500 {
		traffic_monitor.lastUpdateEmit.Store(now_millis)
		traffic_monitor.emitEvent("traffic-connection-update", map[string]interface{}{
			"connectionId":  connection_id,
			"bytesSent":     bytes_sent,
			"bytesReceived": bytes_received,
			"durationMs":    active_entry.DurationMs,
		})
	}
}

// UpdateProtocolHint upgrades the protocol hint for an active connection
func (traffic_monitor *TrafficMonitor) UpdateProtocolHint(connection_id string, new_hint ProtocolHint) {
	traffic_monitor.mu.Lock()
	defer traffic_monitor.mu.Unlock()

	active_entry, exists := traffic_monitor.activeConnections[connection_id]
	if !exists {
		return
	}

	active_entry.ProtocolHint = new_hint
	traffic_monitor.emitEvent("traffic-connection-update", map[string]interface{}{
		"connectionId": connection_id,
		"protocolHint": string(new_hint),
	})
}

// CloseConnection records a TCP connection being closed
func (traffic_monitor *TrafficMonitor) CloseConnection(connection_id string, final_bytes_sent int64, final_bytes_received int64) {
	traffic_monitor.mu.Lock()
	defer traffic_monitor.mu.Unlock()

	active_entry, exists := traffic_monitor.activeConnections[connection_id]
	if !exists {
		return
	}

	close_time := time.Now()
	active_entry.BytesSent = final_bytes_sent
	active_entry.BytesReceived = final_bytes_received
	active_entry.Status = ConnectionStatusClosed
	active_entry.ClosedAt = &close_time
	active_entry.DurationMs = close_time.Sub(active_entry.StartedAt).Milliseconds()

	// Update profile summary
	summary := traffic_monitor.getOrCreateProfileSummary(active_entry.ProfileID)
	summary.ActiveConnections--
	if summary.ActiveConnections < 0 {
		summary.ActiveConnections = 0
	}
	summary.TotalBytesSent += final_bytes_sent
	summary.TotalBytesRecv += final_bytes_received

	// Move to ring buffer and remove from active map
	closed_entry := *active_entry
	traffic_monitor.connectionBuffer.Add(closed_entry)
	delete(traffic_monitor.activeConnections, connection_id)

	log.Printf("Traffic: connection closed %s -> %s:%d [%s] sent=%d recv=%d duration=%dms",
		closed_entry.Hostname, closed_entry.RealIP, closed_entry.Port,
		closed_entry.ProtocolHint, final_bytes_sent, final_bytes_received, closed_entry.DurationMs)

	traffic_monitor.emitEvent("traffic-connection-close", closed_entry)
}

// RecordDNSQuery records a DNS query event
func (traffic_monitor *TrafficMonitor) RecordDNSQuery(dns_entry DNSLogEntry) {
	traffic_monitor.mu.Lock()
	defer traffic_monitor.mu.Unlock()

	traffic_monitor.dnsLogBuffer.Add(dns_entry)

	if dns_entry.ProfileID != "" {
		summary := traffic_monitor.getOrCreateProfileSummary(dns_entry.ProfileID)
		summary.TotalDNSQueries++
	}

	traffic_monitor.emitEvent("traffic-dns-query", dns_entry)
}

// GetRecentConnections returns the most recent closed connections
func (traffic_monitor *TrafficMonitor) GetRecentConnections(entry_limit int) []TrafficEntry {
	return traffic_monitor.connectionBuffer.GetLast(entry_limit)
}

// GetActiveConnectionsList returns all currently active connections
func (traffic_monitor *TrafficMonitor) GetActiveConnectionsList() []TrafficEntry {
	traffic_monitor.mu.RLock()
	defer traffic_monitor.mu.RUnlock()

	active_list := make([]TrafficEntry, 0, len(traffic_monitor.activeConnections))
	for _, active_entry := range traffic_monitor.activeConnections {
		entry_copy := *active_entry
		entry_copy.DurationMs = time.Since(entry_copy.StartedAt).Milliseconds()
		active_list = append(active_list, entry_copy)
	}
	return active_list
}

// GetRecentDNSQueries returns the most recent DNS query log entries
func (traffic_monitor *TrafficMonitor) GetRecentDNSQueries(entry_limit int) []DNSLogEntry {
	return traffic_monitor.dnsLogBuffer.GetLast(entry_limit)
}

// GetProfileSummaries returns traffic summaries for all profiles
func (traffic_monitor *TrafficMonitor) GetProfileSummaries() []ProfileTrafficSummary {
	traffic_monitor.mu.RLock()
	defer traffic_monitor.mu.RUnlock()

	summaries := make([]ProfileTrafficSummary, 0, len(traffic_monitor.profileSummaries))
	for _, summary := range traffic_monitor.profileSummaries {
		summaries = append(summaries, *summary)
	}
	return summaries
}

// Clear resets all traffic monitor data
func (traffic_monitor *TrafficMonitor) Clear() {
	traffic_monitor.mu.Lock()
	defer traffic_monitor.mu.Unlock()

	traffic_monitor.connectionBuffer.Clear()
	traffic_monitor.dnsLogBuffer.Clear()
	traffic_monitor.activeConnections = make(map[string]*TrafficEntry)
	traffic_monitor.profileSummaries = make(map[string]*ProfileTrafficSummary)
}

// getOrCreateProfileSummary returns an existing summary or creates a new one
// Must be called with mu held
func (traffic_monitor *TrafficMonitor) getOrCreateProfileSummary(profile_id string) *ProfileTrafficSummary {
	summary, exists := traffic_monitor.profileSummaries[profile_id]
	if !exists {
		summary = &ProfileTrafficSummary{ProfileID: profile_id}
		traffic_monitor.profileSummaries[profile_id] = summary
	}
	return summary
}

// longLivedHeuristicLoop periodically scans active connections for:
// 1. TLS connections open >30s → upgrade to "tls-long-lived" hint
// 2. Stale connections with no byte updates in >10 minutes → force close (leak cleanup)
func (traffic_monitor *TrafficMonitor) longLivedHeuristicLoop() {
	heuristic_ticker := time.NewTicker(10 * time.Second)
	defer heuristic_ticker.Stop()

	for range heuristic_ticker.C {
		traffic_monitor.mu.Lock()
		long_lived_threshold := 30 * time.Second
		stale_threshold := 10 * time.Minute
		now := time.Now()

		var stale_connection_ids []string

		for connection_id, active_entry := range traffic_monitor.activeConnections {
			// Upgrade TLS → long-lived after 30s
			if active_entry.ProtocolHint == ProtocolHintTLS && now.Sub(active_entry.StartedAt) > long_lived_threshold {
				active_entry.ProtocolHint = ProtocolHintTLSLongLive
				traffic_monitor.emitEvent("traffic-connection-update", map[string]interface{}{
					"connectionId": connection_id,
					"protocolHint": string(ProtocolHintTLSLongLive),
				})
			}

			// Detect stale connections — no byte update in 10 minutes
			if now.Sub(active_entry.LastByteUpdate) > stale_threshold {
				stale_connection_ids = append(stale_connection_ids, connection_id)
			}
		}
		traffic_monitor.mu.Unlock()

		// Force-close stale connections outside the lock (CloseConnection takes the lock)
		for _, stale_id := range stale_connection_ids {
			traffic_monitor.mu.RLock()
			stale_entry, exists := traffic_monitor.activeConnections[stale_id]
			var final_sent, final_recv int64
			if exists {
				final_sent = stale_entry.BytesSent
				final_recv = stale_entry.BytesReceived
			}
			traffic_monitor.mu.RUnlock()

			if exists {
				log.Printf("Traffic: reaping stale connection %s (%s) — no updates in 10 minutes",
					stale_id, stale_entry.Hostname)
				traffic_monitor.CloseConnection(stale_id, final_sent, final_recv)
			}
		}
	}
}
