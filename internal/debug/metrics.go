package debug

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// DNSQueryMetrics tracks DNS query statistics
type DNSQueryMetrics struct {
	TotalQueries     int64 `json:"totalQueries"`
	SuccessfulQueries int64 `json:"successfulQueries"`
	FailedQueries    int64 `json:"failedQueries"`
	CacheHits        int64 `json:"cacheHits"`
	TunnelQueries    int64 `json:"tunnelQueries"`
	FallbackQueries  int64 `json:"fallbackQueries"`
}

// ProxyMetrics tracks proxy connection statistics
type ProxyMetrics struct {
	TotalConnections   int64 `json:"totalConnections"`
	ActiveConnections  int64 `json:"activeConnections"`
	FailedConnections  int64 `json:"failedConnections"`
	BytesTransferred   int64 `json:"bytesTransferred"`
}

// LatencySample represents a latency measurement
type LatencySample struct {
	Timestamp time.Time     `json:"timestamp"`
	ProfileID string        `json:"profileId"`
	Target    string        `json:"target"`
	Latency   time.Duration `json:"latencyMs"`
	Success   bool          `json:"success"`
}

// TunnelMetrics tracks tunnel statistics per profile
type TunnelMetrics struct {
	ProfileID      string          `json:"profileId"`
	BytesSent      int64           `json:"bytesSent"`
	BytesRecv      int64           `json:"bytesRecv"`
	Handshakes     int64           `json:"handshakes"`
	LastHandshake  time.Time       `json:"lastHandshake"`
	LatencyHistory []LatencySample `json:"latencyHistory"`
	AvgLatencyMs   float64         `json:"avgLatencyMs"`
	mu             sync.RWMutex
}

// MetricsCollector collects and aggregates application metrics
type MetricsCollector struct {
	dns           DNSQueryMetrics
	tcpProxy      ProxyMetrics
	tunnels       map[string]*TunnelMetrics // profileID -> metrics
	startTime     time.Time
	latencyBuffer *RingBuffer[LatencySample]
	mu            sync.RWMutex
}

// Global metrics collector
var (
	globalMetrics *MetricsCollector
	metricsOnce   sync.Once
)

// GetMetricsCollector returns the global metrics collector
func GetMetricsCollector() *MetricsCollector {
	metricsOnce.Do(func() {
		globalMetrics = NewMetricsCollector()
	})
	return globalMetrics
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		tunnels:       make(map[string]*TunnelMetrics),
		startTime:     time.Now(),
		latencyBuffer: NewRingBuffer[LatencySample](1000),
	}
}

// DNS Metrics

// RecordDNSQuery records a DNS query
func (m *MetricsCollector) RecordDNSQuery(success bool, throughTunnel bool, cacheHit bool) {
	atomic.AddInt64(&m.dns.TotalQueries, 1)
	if success {
		atomic.AddInt64(&m.dns.SuccessfulQueries, 1)
	} else {
		atomic.AddInt64(&m.dns.FailedQueries, 1)
	}
	if cacheHit {
		atomic.AddInt64(&m.dns.CacheHits, 1)
	}
	if throughTunnel {
		atomic.AddInt64(&m.dns.TunnelQueries, 1)
	} else if !cacheHit {
		atomic.AddInt64(&m.dns.FallbackQueries, 1)
	}
}

// GetDNSMetrics returns DNS query metrics
func (m *MetricsCollector) GetDNSMetrics() DNSQueryMetrics {
	return DNSQueryMetrics{
		TotalQueries:      atomic.LoadInt64(&m.dns.TotalQueries),
		SuccessfulQueries: atomic.LoadInt64(&m.dns.SuccessfulQueries),
		FailedQueries:     atomic.LoadInt64(&m.dns.FailedQueries),
		CacheHits:         atomic.LoadInt64(&m.dns.CacheHits),
		TunnelQueries:     atomic.LoadInt64(&m.dns.TunnelQueries),
		FallbackQueries:   atomic.LoadInt64(&m.dns.FallbackQueries),
	}
}

// TCP Proxy Metrics

// RecordTCPProxyConnection records a TCP proxy connection event
func (m *MetricsCollector) RecordTCPProxyConnection(success bool) {
	atomic.AddInt64(&m.tcpProxy.TotalConnections, 1)
	if success {
		atomic.AddInt64(&m.tcpProxy.ActiveConnections, 1)
	} else {
		atomic.AddInt64(&m.tcpProxy.FailedConnections, 1)
	}
}

// RecordTCPProxyDisconnect records a TCP proxy disconnection
func (m *MetricsCollector) RecordTCPProxyDisconnect(bytesTransferred int64) {
	atomic.AddInt64(&m.tcpProxy.ActiveConnections, -1)
	atomic.AddInt64(&m.tcpProxy.BytesTransferred, bytesTransferred)
}

// GetTCPProxyMetrics returns TCP proxy metrics
func (m *MetricsCollector) GetTCPProxyMetrics() ProxyMetrics {
	return ProxyMetrics{
		TotalConnections:  atomic.LoadInt64(&m.tcpProxy.TotalConnections),
		ActiveConnections: atomic.LoadInt64(&m.tcpProxy.ActiveConnections),
		FailedConnections: atomic.LoadInt64(&m.tcpProxy.FailedConnections),
		BytesTransferred:  atomic.LoadInt64(&m.tcpProxy.BytesTransferred),
	}
}

// Latency Metrics

// RecordLatency records a latency measurement
func (m *MetricsCollector) RecordLatency(profileID, target string, latency time.Duration, success bool) {
	sample := LatencySample{
		Timestamp: time.Now(),
		ProfileID: profileID,
		Target:    target,
		Latency:   latency,
		Success:   success,
	}
	m.latencyBuffer.Add(sample)

	// Update tunnel metrics
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tunnels[profileID]; !exists {
		m.tunnels[profileID] = &TunnelMetrics{
			ProfileID:      profileID,
			LatencyHistory: make([]LatencySample, 0, 100),
		}
	}

	tm := m.tunnels[profileID]
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Keep last 100 samples
	tm.LatencyHistory = append(tm.LatencyHistory, sample)
	if len(tm.LatencyHistory) > 100 {
		tm.LatencyHistory = tm.LatencyHistory[1:]
	}

	// Calculate average latency
	var totalLatency time.Duration
	var successCount int
	for _, s := range tm.LatencyHistory {
		if s.Success {
			totalLatency += s.Latency
			successCount++
		}
	}
	if successCount > 0 {
		tm.AvgLatencyMs = float64(totalLatency.Milliseconds()) / float64(successCount)
	}
}

// GetLatencyHistory returns recent latency samples
func (m *MetricsCollector) GetLatencyHistory(limit int) []LatencySample {
	return m.latencyBuffer.GetLast(limit)
}

// GetLatencyHistoryForProfile returns latency samples for a specific profile
func (m *MetricsCollector) GetLatencyHistoryForProfile(profileID string, limit int) []LatencySample {
	filter := func(sample LatencySample) bool {
		return sample.ProfileID == profileID
	}
	return m.latencyBuffer.GetFiltered(filter, limit)
}

// Tunnel Metrics

// UpdateTunnelStats updates tunnel statistics
func (m *MetricsCollector) UpdateTunnelStats(profileID string, bytesSent, bytesRecv int64, lastHandshake time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tunnels[profileID]; !exists {
		m.tunnels[profileID] = &TunnelMetrics{
			ProfileID:      profileID,
			LatencyHistory: make([]LatencySample, 0, 100),
		}
	}

	tm := m.tunnels[profileID]
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.BytesSent = bytesSent
	tm.BytesRecv = bytesRecv
	tm.LastHandshake = lastHandshake
}

// GetTunnelMetrics returns metrics for a specific tunnel
func (m *MetricsCollector) GetTunnelMetrics(profileID string) *TunnelMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if tm, exists := m.tunnels[profileID]; exists {
		tm.mu.RLock()
		defer tm.mu.RUnlock()
		return &TunnelMetrics{
			ProfileID:      tm.ProfileID,
			BytesSent:      tm.BytesSent,
			BytesRecv:      tm.BytesRecv,
			Handshakes:     tm.Handshakes,
			LastHandshake:  tm.LastHandshake,
			LatencyHistory: append([]LatencySample{}, tm.LatencyHistory...),
			AvgLatencyMs:   tm.AvgLatencyMs,
		}
	}
	return nil
}

// GetAllMetrics returns all metrics as a map
func (m *MetricsCollector) GetAllMetrics() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tunnelMetrics := make(map[string]*TunnelMetrics)
	for id, tm := range m.tunnels {
		tm.mu.RLock()
		tunnelMetrics[id] = &TunnelMetrics{
			ProfileID:     tm.ProfileID,
			BytesSent:     tm.BytesSent,
			BytesRecv:     tm.BytesRecv,
			Handshakes:    tm.Handshakes,
			LastHandshake: tm.LastHandshake,
			AvgLatencyMs:  tm.AvgLatencyMs,
		}
		tm.mu.RUnlock()
	}

	return map[string]any{
		"uptime":     time.Since(m.startTime).String(),
		"uptimeMs":   time.Since(m.startTime).Milliseconds(),
		"dns":        m.GetDNSMetrics(),
		"tcpProxy":   m.GetTCPProxyMetrics(),
		"tunnels":    tunnelMetrics,
	}
}

// GetMetricsJSON returns all metrics as JSON
func (m *MetricsCollector) GetMetricsJSON() (string, error) {
	metrics := m.GetAllMetrics()
	data, err := json.Marshal(metrics)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Reset resets all metrics
func (m *MetricsCollector) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.dns = DNSQueryMetrics{}
	m.tcpProxy = ProxyMetrics{}
	m.tunnels = make(map[string]*TunnelMetrics)
	m.latencyBuffer.Clear()
	m.startTime = time.Now()
}

// Convenience functions

// RecordDNS records a DNS query to the global collector
func RecordDNS(success, throughTunnel, cacheHit bool) {
	GetMetricsCollector().RecordDNSQuery(success, throughTunnel, cacheHit)
}

// RecordLatencySample records a latency sample to the global collector
func RecordLatencySample(profileID, target string, latency time.Duration, success bool) {
	GetMetricsCollector().RecordLatency(profileID, target, latency, success)
}
