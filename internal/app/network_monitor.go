package app

import (
	"fmt"
	"log"
	"time"

	"github.com/miekg/dns"

	"vpnmultitunnel/internal/system"
)

const (
	networkMonitorInterval          = 15 * time.Second
	dnsHealthCheckTimeout           = 3 * time.Second
	consecutiveFailuresBeforeAlert  = 2
)

// startNetworkMonitor begins periodic monitoring of the active network interface
// and DNS health. Detects interface changes and reconfigures DNS automatically.
func (app *App) startNetworkMonitor() {
	app.mu.Lock()
	if app.networkMonitorStop != nil {
		app.mu.Unlock()
		return // already running
	}
	app.networkMonitorStop = make(chan struct{})
	app.mu.Unlock()

	// Initialize cached active interface
	if active_interface, get_err := app.networkConfig.GetActiveNetworkInterface(); get_err == nil {
		app.mu.Lock()
		app.lastActiveInterface = active_interface
		app.mu.Unlock()
	}

	go func() {
		monitor_ticker := time.NewTicker(networkMonitorInterval)
		defer monitor_ticker.Stop()

		log.Printf("[network-monitor] Started (interval: %s)", networkMonitorInterval)

		for {
			select {
			case <-app.networkMonitorStop:
				log.Printf("[network-monitor] Stopped")
				return
			case <-monitor_ticker.C:
				app.networkMonitorTick()
			}
		}
	}()
}

// stopNetworkMonitor signals the monitor goroutine to stop and waits for it to exit.
func (app *App) stopNetworkMonitor() {
	app.mu.Lock()
	stop_channel := app.networkMonitorStop
	app.networkMonitorStop = nil
	app.mu.Unlock()

	if stop_channel != nil {
		close(stop_channel)
	}
}

// networkMonitorTick performs one cycle of interface change detection and DNS health checking.
func (app *App) networkMonitorTick() {
	// Skip if no tunnels are connected
	connected_count := app.tunnelManager.GetConnectedCount()
	if connected_count == 0 {
		// Clear any stale DNS issue when nothing is connected
		app.mu.Lock()
		app.dnsHealthIssue = ""
		app.consecutiveDNSFailures = 0
		app.mu.Unlock()
		return
	}

	// Skip if DNS proxy is not enabled
	if !app.config.DNSProxy.Enabled {
		return
	}

	// --- Interface change detection ---
	app.checkActiveInterfaceChange()

	// --- Auto-repair: DNS should be configured but isn't ---
	app.ensureSystemDNSConfigured()

	// --- DNS health check ---
	app.checkDNSHealth()
}

// checkActiveInterfaceChange detects if the active network interface has changed
// and reconfigures DNS on the new interface automatically.
func (app *App) checkActiveInterfaceChange() {
	current_interface, get_err := app.networkConfig.GetActiveNetworkInterface()
	if get_err != nil {
		log.Printf("[network-monitor] Failed to get active interface: %v", get_err)
		return
	}

	app.mu.RLock()
	previous_interface := app.lastActiveInterface
	app.mu.RUnlock()

	if current_interface == previous_interface || previous_interface == "" {
		// No change, or first run (already initialized in startNetworkMonitor)
		if previous_interface == "" {
			app.mu.Lock()
			app.lastActiveInterface = current_interface
			app.mu.Unlock()
		}
		return
	}

	// Interface changed!
	log.Printf("[network-monitor] Active interface changed: %q -> %q", previous_interface, current_interface)

	app.mu.Lock()
	app.lastActiveInterface = current_interface
	app.mu.Unlock()

	// Always flush DNS cache on network change
	system.FlushDNSCache()

	// Only reconfigure if DNS was configured by us
	if !app.networkConfig.IsTransparentDNSConfigured() {
		log.Printf("[network-monitor] DNS not configured by us, skipping reconfiguration")
		return
	}

	app.reconfigureDNSForNewInterface()
}

// reconfigureDNSForNewInterface restores DNS on the old interface and configures it on the new one.
func (app *App) reconfigureDNSForNewInterface() {
	log.Printf("[network-monitor] Reconfiguring DNS for new active interface...")

	// Restore DNS on the old interface (clears dnsConfigured flag)
	app.restoreSystemDNS()

	// Configure DNS on the new active interface
	app.configureSystemDNS()

	// Flush DNS cache so apps pick up the change
	system.FlushDNSCache()

	log.Printf("[network-monitor] DNS reconfigured successfully")
}

// ensureSystemDNSConfigured detects when system DNS should be pointing to the proxy
// but isn't (e.g., after external DNS reset, app restart, or Windows reconfiguration)
// and automatically reconfigures it.
func (app *App) ensureSystemDNSConfigured() {
	// Only auto-repair if autoConfigureDNS is enabled
	if !app.config.Settings.AutoConfigureDNS {
		return
	}

	// Already configured — nothing to repair
	if app.networkConfig.IsTransparentDNSConfigured() {
		// Even when configured, check that all connected adapters have our DNS
		app.ensureAllAdaptersUseDNSProxy()
		return
	}

	// Need either the service or elevation capability to reconfigure
	if !app.networkConfig.IsServiceConnected() {
		return
	}

	log.Printf("[network-monitor] DNS proxy should be active but system DNS is not configured — auto-repairing...")

	app.configureSystemDNS()

	if app.networkConfig.IsTransparentDNSConfigured() {
		log.Printf("[network-monitor] DNS auto-repair successful")
		app.mu.Lock()
		app.dnsHealthIssue = ""
		app.consecutiveDNSFailures = 0
		app.mu.Unlock()
	} else {
		log.Printf("[network-monitor] DNS auto-repair failed — system DNS still not configured")
		app.mu.Lock()
		app.dnsHealthIssue = "DNS auto-configuration failed: system DNS not pointing to proxy"
		app.mu.Unlock()
	}
}

// ensureAllAdaptersUseDNSProxy checks all UP adapters with DNS configured and ensures
// they point to our DNS proxy. This handles the case where multiple adapters are active
// (e.g., Ethernet + WiFi) and some may have router DNS instead of proxy DNS.
func (app *App) ensureAllAdaptersUseDNSProxy() {
	dns_proxy_address := app.config.DNSProxy.GetListenAddress()

	adapters, get_err := system.GetAllAdapters()
	if get_err != nil {
		return
	}

	// Skip these adapter types — they're internal/virtual and shouldn't have DNS reconfigured
	skip_prefixes := []string{
		"Loopback", "vEthernet", "VirtualBox", "VMware", "Tailscale",
		"OpenVPN", "Local Area Connection 2", "Local Area Connection 3",
		"Local Area Connection 4", "Local Area Connection 5",
		"Local Area Connection 6", "Local Area Connection 7",
		"Local Area Connection 8",
	}

	for _, adapter := range adapters {
		if !adapter.IsUp() {
			continue
		}
		if len(adapter.DNSServers) == 0 {
			continue
		}
		if len(adapter.IPv4Addrs) == 0 {
			continue
		}

		// Skip virtual/internal adapters
		should_skip := false
		for _, prefix := range skip_prefixes {
			if len(adapter.Name) >= len(prefix) && adapter.Name[:len(prefix)] == prefix {
				should_skip = true
				break
			}
		}
		if should_skip {
			continue
		}

		// Check if this adapter already has our DNS proxy
		already_configured := false
		for _, dns_server := range adapter.DNSServers {
			if dns_server == dns_proxy_address {
				already_configured = true
				break
			}
		}

		if already_configured {
			continue
		}

		// This adapter is UP, has DNS servers, but doesn't point to our proxy
		log.Printf("[network-monitor] Adapter %q has DNS %v but not proxy %s — configuring...",
			adapter.Name, adapter.DNSServers, dns_proxy_address)

		// Save original DNS before overwriting
		app.networkConfig.SaveOriginalDNS(adapter.Name, adapter.DNSServers)

		// Set DNS to proxy
		if set_err := app.networkConfig.SetDNSForInterface(adapter.Name, dns_proxy_address); set_err != nil {
			log.Printf("[network-monitor] Failed to set DNS on %q: %v", adapter.Name, set_err)
		} else {
			log.Printf("[network-monitor] Configured DNS proxy on adapter %q", adapter.Name)
			system.FlushDNSCache()
		}
	}
}

// checkDNSHealth verifies that the DNS proxy is reachable and responding correctly.
// If the proxy stops responding, it attempts to restart it automatically.
func (app *App) checkDNSHealth() {
	// Only check if DNS is configured by us
	if !app.networkConfig.IsTransparentDNSConfigured() {
		app.mu.Lock()
		app.dnsHealthIssue = ""
		app.consecutiveDNSFailures = 0
		app.mu.Unlock()
		return
	}

	dns_proxy_address := app.config.DNSProxy.GetListenAddress()
	dns_target_address := fmt.Sprintf("%s:53", dns_proxy_address)

	dns_client := &dns.Client{
		Net:     "udp",
		Timeout: dnsHealthCheckTimeout,
	}
	dns_query := new(dns.Msg)
	dns_query.SetQuestion(dns.Fqdn("google.com"), dns.TypeA)

	dns_response, _, query_err := dns_client.Exchange(dns_query, dns_target_address)

	app.mu.Lock()

	if query_err != nil {
		app.consecutiveDNSFailures++
		if app.consecutiveDNSFailures >= consecutiveFailuresBeforeAlert {
			app.dnsHealthIssue = fmt.Sprintf("DNS proxy not responding on %s: %v", dns_proxy_address, query_err)
			log.Printf("[network-monitor] DNS health issue: %s", app.dnsHealthIssue)
			// Auto-repair on every tick while broken — restart is cheap and the next
			// tick (15s later) will clear the issue if the proxy comes back healthy.
			app.mu.Unlock()
			app.attemptDNSProxyRestart()
			return
		}
		app.mu.Unlock()
		return
	}

	if dns_response == nil || dns_response.Rcode != dns.RcodeSuccess {
		app.consecutiveDNSFailures++
		if app.consecutiveDNSFailures >= consecutiveFailuresBeforeAlert {
			rcode_description := "nil response"
			if dns_response != nil {
				rcode_description = fmt.Sprintf("rcode=%d", dns_response.Rcode)
			}
			app.dnsHealthIssue = fmt.Sprintf("DNS proxy returned error on %s: %s", dns_proxy_address, rcode_description)
			log.Printf("[network-monitor] DNS health issue: %s", app.dnsHealthIssue)
		}
		app.mu.Unlock()
		return
	}

	// DNS is healthy - clear any previous issue
	if app.consecutiveDNSFailures > 0 || app.dnsHealthIssue != "" {
		log.Printf("[network-monitor] DNS health restored")
	}
	app.consecutiveDNSFailures = 0
	app.dnsHealthIssue = ""
	app.mu.Unlock()
}

// attemptDNSProxyRestart tries to restart the DNS proxy on port 53 when it stops responding.
// FixDNS restarts the DNS proxy and flushes the DNS cache. Exposed to frontend.
func (app *App) FixDNS() error {
	log.Printf("[dns] Manual DNS fix requested")
	app.attemptDNSProxyRestart()
	app.mu.Lock()
	app.consecutiveDNSFailures = 0
	app.dnsHealthIssue = ""
	app.mu.Unlock()
	return nil
}

func (app *App) attemptDNSProxyRestart() {
	log.Printf("[network-monitor] Attempting DNS proxy restart on port 53...")

	if restart_err := app.tunnelManager.RestartDNSProxyOnPort(53); restart_err != nil {
		// RestartDNSProxyOnPort fails fast if the proxy is nil (i.e. initial bind
		// failed at boot or the proxy was never started). Fall back to a full
		// re-init via RestartDNSProxy, which Stops (no-op if nil) then re-creates
		// the proxy from scratch via StartDNSProxy.
		log.Printf("[network-monitor] DNS proxy restart-on-port failed (%v); falling back to full re-init", restart_err)
		app.config.DNSProxy.ListenPort = 53
		app.tunnelManager.RestartDNSProxy(&app.config.DNSProxy)
	}

	system.FlushDNSCache()
	log.Printf("[network-monitor] DNS proxy restart attempt completed")
}
