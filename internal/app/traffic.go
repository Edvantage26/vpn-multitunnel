package app

import (
	"vpnmultitunnel/internal/proxy"
)

// GetTrafficConnections returns the most recent closed TCP connection entries
func (app *App) GetTrafficConnections(entry_limit int) []proxy.TrafficEntry {
	return app.tunnelManager.GetTrafficMonitor().GetRecentConnections(entry_limit)
}

// GetActiveTrafficConnections returns all currently active TCP connections
func (app *App) GetActiveTrafficConnections() []proxy.TrafficEntry {
	return app.tunnelManager.GetTrafficMonitor().GetActiveConnectionsList()
}

// GetDNSQueryLog returns the most recent DNS query log entries
func (app *App) GetDNSQueryLog(entry_limit int) []proxy.DNSLogEntry {
	return app.tunnelManager.GetTrafficMonitor().GetRecentDNSQueries(entry_limit)
}

// GetProfileTrafficSummaries returns per-profile traffic summaries
func (app *App) GetProfileTrafficSummaries() []proxy.ProfileTrafficSummary {
	return app.tunnelManager.GetTrafficMonitor().GetProfileSummaries()
}

// ClearTrafficLog clears all traffic monitor data
func (app *App) ClearTrafficLog() {
	app.tunnelManager.GetTrafficMonitor().Clear()
}
