package app

import (
	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/proxy"
)

// GetTCPProxyConfig returns the TCP proxy configuration
func (app *App) GetTCPProxyConfig() config.TCPProxy {
	return app.config.TCPProxy
}

// UpdateTCPProxyConfig updates the TCP proxy configuration
func (app *App) UpdateTCPProxyConfig(tcpConfig config.TCPProxy) error {
	app.config.TCPProxy = tcpConfig
	if err := config.Save(app.config); err != nil {
		return err
	}

	// Restart TCP proxy with new config
	app.tunnelManager.RestartTCPProxy(&tcpConfig)
	return nil
}

// GetActiveConnections returns active transparent proxy connections
func (app *App) GetActiveConnections() []proxy.ActiveConnection {
	return app.tunnelManager.GetActiveConnections()
}

// GetTunnelIPs returns the tunnel IPs for all profiles
func (app *App) GetTunnelIPs() map[string]string {
	return app.config.TCPProxy.TunnelIPs
}

// IsTCPProxyEnabled returns whether the TCP proxy is enabled
func (app *App) IsTCPProxyEnabled() bool {
	return app.tunnelManager.IsTCPProxyEnabled()
}

// GetTCPProxyListenerCount returns the number of TCP proxy listeners
func (app *App) GetTCPProxyListenerCount() int {
	return app.tunnelManager.GetTCPProxyListenerCount()
}
