package app

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/miekg/dns"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/debug"
	"vpnmultitunnel/internal/system"
)

// DNSTestResult contains the result of a DNS connectivity test
type DNSTestResult struct {
	ProxyListening      bool   `json:"proxyListening"`
	SystemDNSConfigured bool   `json:"systemDNSConfigured"`
	QuerySuccess        bool   `json:"querySuccess"`
	ResolvedIP          string `json:"resolvedIP"`
	Error               string `json:"error,omitempty"`
}

// configureSystemDNS sets up the system to use our DNS proxy with transparent DNS
func (app *App) configureSystemDNS() {
	// Setup transparent DNS: set system DNS to our proxy address (e.g., 127.0.0.53)
	// Using a different loopback IP avoids conflicts with Windows DNS Client
	if err := app.networkConfig.SetupTransparentDNS(); err != nil {
		log.Printf("Warning: Failed to setup transparent DNS: %v", err)
		return
	}

	// Restart DNS proxy on port 53
	if err := app.tunnelManager.RestartDNSProxyOnPort(53); err != nil {
		log.Printf("Warning: Failed to restart DNS proxy on port 53: %v", err)
		// Try to continue anyway
	}

	system.FlushDNSCache()
	dnsAddr := app.config.DNSProxy.GetListenAddress()
	log.Printf("Transparent DNS configured: DNS proxy on %s:53, system DNS = %s", dnsAddr, dnsAddr)
}

// restoreSystemDNS restores the original DNS configuration
func (app *App) restoreSystemDNS() {
	// Restart DNS proxy on original port (10053) before restoring DNS Client
	originalPort := 10053
	if app.config.Settings.UsePort53 {
		originalPort = 53 // Keep on 53 if that was the original setting
	} else {
		originalPort = app.config.DNSProxy.ListenPort
	}

	// Only restart on different port if we're not already on original port
	currentPort := app.tunnelManager.GetDNSProxyPort()
	if currentPort == 53 && originalPort != 53 {
		if err := app.tunnelManager.RestartDNSProxyOnPort(originalPort); err != nil {
			log.Printf("Warning: Failed to restart DNS proxy on port %d: %v", originalPort, err)
		}
	}

	// Restore transparent DNS (restores original DNS settings and restarts DNS Client)
	if err := app.networkConfig.RestoreTransparentDNS(); err != nil {
		log.Printf("Failed to restore transparent DNS: %v", err)
	} else {
		log.Println("Restored original DNS configuration and restarted DNS Client")
		system.FlushDNSCache()
	}
}

// TestDNSConnectivity tests DNS proxy connectivity on the given address
func (app *App) TestDNSConnectivity(dns_address string) DNSTestResult {
	result := DNSTestResult{}

	// Check if proxy is listening on the address:53
	test_addr := fmt.Sprintf("%s:53", dns_address)
	test_conn, dial_err := net.DialTimeout("udp", test_addr, 1*time.Second)
	if dial_err == nil {
		test_conn.Close()
	}

	// Send a real DNS query to check if proxy responds
	dns_client := &dns.Client{
		Net:     "udp",
		Timeout: 3 * time.Second,
	}
	dns_query := new(dns.Msg)
	dns_query.SetQuestion(dns.Fqdn("google.com"), dns.TypeA)
	dns_response, _, query_err := dns_client.Exchange(dns_query, test_addr)
	if query_err == nil && dns_response != nil && dns_response.Rcode == dns.RcodeSuccess {
		result.ProxyListening = true
		result.QuerySuccess = true
		// Extract first A record
		for _, answer_record := range dns_response.Answer {
			if a_record, is_a_record := answer_record.(*dns.A); is_a_record {
				result.ResolvedIP = a_record.A.String()
				break
			}
		}
	} else if query_err != nil {
		result.Error = query_err.Error()
	} else if dns_response != nil {
		result.ProxyListening = true
		result.Error = fmt.Sprintf("DNS response code: %d", dns_response.Rcode)
	}

	// Check if system DNS is configured to use this address
	result.SystemDNSConfigured = app.networkConfig.IsTransparentDNSConfigured()

	return result
}

// GetDNSProxyConfig returns the DNS proxy configuration
func (app *App) GetDNSProxyConfig() config.DNSProxy {
	return app.config.DNSProxy
}

// UpdateDNSProxyConfig updates the DNS proxy configuration
func (app *App) UpdateDNSProxyConfig(dnsConfig config.DNSProxy) error {
	app.config.DNSProxy = dnsConfig
	if err := config.Save(app.config); err != nil {
		return err
	}

	// Restart DNS proxy with new config
	app.tunnelManager.RestartDNSProxy(&dnsConfig)
	return nil
}

// IsDNSConfigured returns whether the system DNS has been configured by us
func (app *App) IsDNSConfigured() bool {
	if app.networkConfig == nil {
		return false
	}
	return app.networkConfig.IsDNSConfigured()
}

// RestoreDNS restores the original DNS configuration
func (app *App) RestoreDNS() error {
	if app.networkConfig == nil {
		return fmt.Errorf("network config not initialized")
	}

	log.Printf("Restoring DNS configuration...")
	if err := app.networkConfig.RestoreSystemDNS(); err != nil {
		return fmt.Errorf("failed to restore DNS: %w", err)
	}

	// Also restart DNS Client service if we stopped it
	if app.networkConfig.IsDNSClientStopped() {
		if err := system.StartDNSClientService(); err != nil {
			log.Printf("Warning: failed to restart DNS Client service: %v", err)
		}
	}

	return nil
}

// ConfigureDNS manually configures DNS to use our proxy
func (app *App) ConfigureDNS() debug.DNSConfigResult {
	result := debug.DNSConfigResult{}

	if app.networkConfig == nil {
		result.Error = "network config not initialized"
		return result
	}

	// Get the DNS proxy listen address (default: 127.0.0.53)
	dnsAddress := app.config.DNSProxy.GetListenAddress()
	result.DNSAddress = dnsAddress

	log.Printf("Manually configuring DNS to %s...", dnsAddress)
	if err := app.networkConfig.ConfigureSystemDNS(dnsAddress); err != nil {
		result.Error = fmt.Sprintf("failed to configure DNS: %v", err)
		return result
	}

	// Wait a moment for the DNS Client service to stop
	time.Sleep(500 * time.Millisecond)

	// Restart DNS proxy on port 53 so it actually listens
	if err := app.tunnelManager.RestartDNSProxyOnPort(53); err != nil {
		log.Printf("Warning: Failed to restart DNS proxy on port 53: %v", err)
	}

	system.FlushDNSCache()

	// Force refresh the cache and check status
	app.refreshDNSStatusCache()
	app.dnsStatusMu.RLock()
	result.Port53Free = app.dnsStatusPort53Free
	result.DNSClientDown = app.dnsStatusClientDown
	app.dnsStatusMu.RUnlock()
	result.Success = true

	return result
}

// refreshDNSStatusCache updates the cached DNS status values
func (app *App) refreshDNSStatusCache() {
	app.dnsStatusMu.Lock()
	defer app.dnsStatusMu.Unlock()

	// Check port 53
	cmd := exec.Command("netstat", "-ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.Output()
	if err != nil {
		app.dnsStatusPort53Free = true
	} else {
		app.dnsStatusPort53Free = true
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "UDP") && strings.Contains(line, "0.0.0.0:53 ") {
				app.dnsStatusPort53Free = false
				break
			}
		}
	}

	// Check DNS Client service
	cmd = exec.Command("sc", "query", "Dnscache")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err = cmd.Output()
	if err != nil {
		app.dnsStatusClientDown = true
	} else {
		app.dnsStatusClientDown = !strings.Contains(string(output), "RUNNING")
	}

	app.dnsStatusCacheTime = time.Now()
}

// invalidateDNSStatusCache forces the next status check to refresh
func (app *App) invalidateDNSStatusCache() {
	app.dnsStatusMu.Lock()
	defer app.dnsStatusMu.Unlock()
	app.dnsStatusCacheTime = time.Time{} // Zero time forces refresh
}

// isPort53Free checks if UDP port 53 is available (cached)
func (app *App) isPort53Free() bool {
	app.dnsStatusMu.RLock()
	if time.Since(app.dnsStatusCacheTime) < app.dnsStatusCacheTTL {
		result := app.dnsStatusPort53Free
		app.dnsStatusMu.RUnlock()
		return result
	}
	app.dnsStatusMu.RUnlock()

	// Cache expired, refresh
	app.refreshDNSStatusCache()

	app.dnsStatusMu.RLock()
	defer app.dnsStatusMu.RUnlock()
	return app.dnsStatusPort53Free
}

// isDNSClientRunning checks if Windows DNS Client service is running (cached)
func (app *App) isDNSClientRunning() bool {
	app.dnsStatusMu.RLock()
	if time.Since(app.dnsStatusCacheTime) < app.dnsStatusCacheTTL {
		result := !app.dnsStatusClientDown
		app.dnsStatusMu.RUnlock()
		return result
	}
	app.dnsStatusMu.RUnlock()

	// Cache expired, refresh
	app.refreshDNSStatusCache()

	app.dnsStatusMu.RLock()
	defer app.dnsStatusMu.RUnlock()
	return !app.dnsStatusClientDown
}
