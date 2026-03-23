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

// checkDNSHealth verifies that the DNS proxy is reachable and responding correctly.
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
	defer app.mu.Unlock()

	if query_err != nil {
		app.consecutiveDNSFailures++
		if app.consecutiveDNSFailures >= consecutiveFailuresBeforeAlert {
			app.dnsHealthIssue = fmt.Sprintf("DNS proxy not responding on %s: %v", dns_proxy_address, query_err)
			log.Printf("[network-monitor] DNS health issue: %s", app.dnsHealthIssue)
		}
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
		return
	}

	// DNS is healthy - clear any previous issue
	if app.consecutiveDNSFailures > 0 || app.dnsHealthIssue != "" {
		log.Printf("[network-monitor] DNS health restored")
	}
	app.consecutiveDNSFailures = 0
	app.dnsHealthIssue = ""
}
