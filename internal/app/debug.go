package app

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/debug"
	"vpnmultitunnel/internal/system"
)

// TestConnection tests connectivity through a tunnel
func (app *App) TestConnection(profileID, targetHost string, targetPort int) (bool, string) {
	active_tunnel := app.tunnelManager.GetTunnel(profileID)
	if active_tunnel == nil {
		return false, "Tunnel not connected"
	}

	addr := fmt.Sprintf("%s:%d", targetHost, targetPort)

	// Measure connection time
	start := time.Now()
	conn, err := active_tunnel.Dial("tcp", addr)
	elapsed := time.Since(start)

	if err != nil {
		return false, fmt.Sprintf("Connection failed: %v", err)
	}
	conn.Close()

	return true, fmt.Sprintf("Connected to %s in %dms", addr, elapsed.Milliseconds())
}

// GetTunnelDebugInfo returns debug information for a tunnel
func (app *App) GetTunnelDebugInfo(profileID string) string {
	active_tunnel := app.tunnelManager.GetTunnel(profileID)
	if active_tunnel == nil {
		return "Tunnel not found"
	}
	return active_tunnel.GetDebugInfo()
}

// GetWireGuardConfig returns parsed WireGuard config metadata for UI display
func (app *App) GetWireGuardConfig(profileID string) (*WireGuardConfigDisplay, error) {
	profile, err := app.profileService.GetByID(profileID)
	if err != nil {
		return nil, err
	}

	// Get the config file path
	configPath, err := config.GetConfigFilePath(profile.ConfigFile)
	if err != nil {
		return nil, err
	}

	// Parse the WireGuard config
	wgConfig, err := config.ParseWireGuardConfig(configPath)
	if err != nil {
		return nil, err
	}

	// Build the display struct
	display := &WireGuardConfigDisplay{}

	// Interface section
	if len(wgConfig.Interface.Address) > 0 {
		display.Interface.Address = strings.Join(wgConfig.Interface.Address, ", ")
	}
	if len(wgConfig.Interface.DNS) > 0 {
		display.Interface.DNS = strings.Join(wgConfig.Interface.DNS, ", ")
	}
	display.Interface.ListenPort = wgConfig.Interface.ListenPort

	// Peer section (first peer only for display)
	if len(wgConfig.Peers) > 0 {
		peer := wgConfig.Peers[0]
		display.Peer.Endpoint = peer.Endpoint
		display.Peer.AllowedIPs = strings.Join(peer.AllowedIPs, ", ")
		display.Peer.PublicKey = peer.PublicKey
	}

	return display, nil
}

// GetConfigFileContent returns the raw content of a WireGuard config file
func (app *App) GetConfigFileContent(profileID string) (string, error) {
	profile, err := app.profileService.GetByID(profileID)
	if err != nil {
		return "", err
	}

	configPath, err := config.GetConfigFilePath(profile.ConfigFile)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read config file: %w", err)
	}

	return string(data), nil
}

// SaveConfigFileContent saves the raw content to a WireGuard config file
func (app *App) SaveConfigFileContent(profileID string, content string) error {
	profile, err := app.profileService.GetByID(profileID)
	if err != nil {
		return err
	}

	configPath, err := config.GetConfigFilePath(profile.ConfigFile)
	if err != nil {
		return err
	}

	// Validate the config by parsing it
	// Create a temp file with the content
	tmpFile, err := os.CreateTemp("", "wg-*.conf")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Try to parse it to validate
	if _, err := config.ParseWireGuardConfig(tmpFile.Name()); err != nil {
		return fmt.Errorf("invalid WireGuard config: %w", err)
	}

	// Write the content to the actual file
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}

	return nil
}

// =============================================================================
// DebugProvider Interface Implementation
// =============================================================================

// GetVPNStatusList returns detailed status for all VPN tunnels
func (app *App) GetVPNStatusList() []debug.VPNStatusInfo {
	app.mu.RLock()
	defer app.mu.RUnlock()

	profiles := app.profileService.GetAll()
	result := make([]debug.VPNStatusInfo, 0, len(profiles))

	for _, p := range profiles {
		status := debug.VPNStatusInfo{
			ProfileID:   p.ID,
			ProfileName: p.Name,
			VPNType:     string(p.GetVPNType()),
			Connected:   app.tunnelManager.IsConnected(p.ID),
			Connecting:  app.connectingProfiles[p.ID],
			TunnelIP:    app.profileService.GetTunnelIP(p.ID),
			LastError:   app.lastConnectErrors[p.ID],
		}

		if status.Connected {
			if stats := app.tunnelManager.GetStats(p.ID); stats != nil {
				status.BytesSent = stats.BytesSent
				status.BytesRecv = stats.BytesRecv
				status.Endpoint = stats.Endpoint
				status.Healthy = stats.Connected
			}
			// Get latency metrics
			if tm := debug.GetMetricsCollector().GetTunnelMetrics(p.ID); tm != nil {
				status.AvgLatencyMs = tm.AvgLatencyMs
			}
		}

		result = append(result, status)
	}

	return result
}

// GetConnectErrors returns all current connection errors keyed by profile ID
func (app *App) GetConnectErrors() map[string]string {
	app.mu.RLock()
	defer app.mu.RUnlock()
	errorsCopy := make(map[string]string, len(app.lastConnectErrors))
	for profileID, errorMessage := range app.lastConnectErrors {
		errorsCopy[profileID] = errorMessage
	}
	return errorsCopy
}

// GetOpenVPNStatusMap returns OpenVPN installation info as a generic map for the debug API
func (app *App) GetOpenVPNStatusMap() map[string]any {
	ovpn_status := app.GetOpenVPNStatus()
	return map[string]any{
		"installed":    ovpn_status.Installed,
		"version":      ovpn_status.Version,
		"path":         ovpn_status.Path,
		"needsUpgrade": ovpn_status.NeedsUpgrade,
	}
}

// GetProfileNames returns a map of profile IDs to names
func (app *App) GetProfileNames() map[string]string {
	app.mu.RLock()
	defer app.mu.RUnlock()

	profiles := app.profileService.GetAll()
	result := make(map[string]string, len(profiles))
	for _, p := range profiles {
		result[p.ID] = p.Name
	}
	return result
}

// GetHostMappings returns all active host mappings
func (app *App) GetHostMappings() []debug.HostMappingInfo {
	mappings := app.tunnelManager.GetHostMappings()
	profileNames := app.GetProfileNames()

	result := make([]debug.HostMappingInfo, 0, len(mappings))
	for _, m := range mappings {
		info := debug.HostMappingInfo{
			Hostname:    m.Hostname,
			RealIP:      m.RealIP,
			LoopbackIP:  m.TunnelIP,
			ProfileID:   m.ProfileID,
			ProfileName: profileNames[m.ProfileID],
			ResolvedAt:  m.ResolvedAt,
			ExpiresAt:   m.ResolvedAt.Add(30 * time.Minute), // Default TTL
		}
		result = append(result, info)
	}
	return result
}

// GetDNSConfig returns the DNS proxy configuration
func (app *App) GetDNSConfig() debug.DNSConfigInfo {
	profileNames := app.GetProfileNames()

	// Build rules from profiles at runtime
	runtimeRules := config.BuildDNSRulesFromProfiles(app.config.Profiles)
	rules := make([]debug.DNSRuleInfo, 0, len(runtimeRules))
	for _, rule := range runtimeRules {
		stripSuffix := true
		if rule.StripSuffix != nil {
			stripSuffix = *rule.StripSuffix
		}
		rules = append(rules, debug.DNSRuleInfo{
			Suffix:      rule.Suffix,
			ProfileID:   rule.ProfileID,
			ProfileName: profileNames[rule.ProfileID],
			DNSServer:   app.tunnelManager.GetDNSServerForProfile(rule.ProfileID),
			StripSuffix: stripSuffix,
			Hosts:       rule.Hosts,
		})
	}

	return debug.DNSConfigInfo{
		Enabled:    app.config.DNSProxy.Enabled,
		ListenPort: app.config.DNSProxy.ListenPort,
		Rules:      rules,
		Fallback:   app.config.DNSProxy.Fallback,
	}
}

// DiagnoseDNS diagnoses why a hostname might not resolve correctly
func (app *App) DiagnoseDNS(hostname string) debug.DNSDiagnostic {
	hostname = strings.ToLower(hostname)
	dnsConfig := app.GetDNSConfig()

	diagnostic := debug.DNSDiagnostic{
		Hostname: hostname,
		AllRules: dnsConfig.Rules,
	}

	// Check if DNS proxy is enabled
	if !dnsConfig.Enabled {
		diagnostic.WouldResolve = false
		diagnostic.Reason = "DNS proxy is disabled"
		diagnostic.SuggestedFix = "Enable DNS proxy in settings"
		return diagnostic
	}

	// Find matching rule
	matchedRule := app.GetMatchingRule(hostname)
	diagnostic.MatchedRule = matchedRule

	if matchedRule == nil {
		diagnostic.WouldResolve = true
		diagnostic.Reason = fmt.Sprintf("No rule matches '%s', will use fallback DNS (%s)", hostname, dnsConfig.Fallback)
		return diagnostic
	}

	// Check if the profile is connected
	if !app.tunnelManager.IsConnected(matchedRule.ProfileID) {
		diagnostic.WouldResolve = false
		diagnostic.Reason = fmt.Sprintf("Rule '%s' matches, but profile '%s' is not connected", matchedRule.Suffix, matchedRule.ProfileName)
		diagnostic.SuggestedFix = fmt.Sprintf("Connect the '%s' VPN profile", matchedRule.ProfileName)
		return diagnostic
	}

	// Check static hosts
	if matchedRule.Hosts != nil {
		queryDomain := hostname
		if matchedRule.StripSuffix {
			queryDomain = strings.TrimSuffix(hostname, matchedRule.Suffix)
			queryDomain = strings.TrimSuffix(queryDomain, ".")
		}
		if ip, exists := matchedRule.Hosts[queryDomain]; exists {
			diagnostic.WouldResolve = true
			diagnostic.Reason = fmt.Sprintf("Static host mapping: %s -> %s", queryDomain, ip)
			return diagnostic
		}
	}

	resolvedDNSServer := app.tunnelManager.GetDNSServerForProfile(matchedRule.ProfileID)
	if resolvedDNSServer == "" {
		resolvedDNSServer = "(not configured)"
	}
	diagnostic.WouldResolve = true
	diagnostic.Reason = fmt.Sprintf("Will resolve via tunnel DNS server %s (profile: %s)", resolvedDNSServer, matchedRule.ProfileName)
	return diagnostic
}

// QueryDNS performs a DNS query through a VPN tunnel
func (app *App) QueryDNS(hostname string, queryType string, dnsServer string, profileID string) debug.DNSQueryResult {
	start := time.Now()
	profileNames := app.GetProfileNames()

	result := debug.DNSQueryResult{
		Hostname:    hostname,
		QueryType:   queryType,
		DNSServer:   dnsServer,
		ProfileID:   profileID,
		ProfileName: profileNames[profileID],
	}

	// Get tunnel
	active_tunnel := app.tunnelManager.GetTunnel(profileID)
	if active_tunnel == nil {
		result.Error = fmt.Sprintf("tunnel not connected for profile: %s", profileID)
		return result
	}

	// If no DNS server specified, resolve from WireGuard .conf
	if dnsServer == "" {
		dnsServer = app.tunnelManager.GetDNSServerForProfile(profileID)
		if dnsServer == "" {
			result.Error = "no DNS server specified and none configured in WireGuard .conf for profile"
			return result
		}
		result.DNSServer = dnsServer
	}

	// Parse query type
	var qtype uint16
	switch strings.ToUpper(queryType) {
	case "A":
		qtype = dns.TypeA
	case "AAAA":
		qtype = dns.TypeAAAA
	case "CNAME":
		qtype = dns.TypeCNAME
	case "MX":
		qtype = dns.TypeMX
	case "TXT":
		qtype = dns.TypeTXT
	case "NS":
		qtype = dns.TypeNS
	case "SOA":
		qtype = dns.TypeSOA
	case "PTR":
		qtype = dns.TypePTR
	case "ANY":
		qtype = dns.TypeANY
	default:
		qtype = dns.TypeA
		result.QueryType = "A"
	}

	// Create DNS query
	dns_msg := new(dns.Msg)
	dns_msg.SetQuestion(dns.Fqdn(hostname), qtype)
	dns_msg.RecursionDesired = true

	// Connect through tunnel
	conn, err := active_tunnel.Dial("udp", dnsServer+":53")
	if err != nil {
		result.Error = fmt.Sprintf("failed to connect to DNS server: %v", err)
		return result
	}
	defer conn.Close()

	// Create DNS connection and send query
	dnsConn := &dns.Conn{Conn: conn}
	if err := dnsConn.WriteMsg(dns_msg); err != nil {
		result.Error = fmt.Sprintf("failed to send DNS query: %v", err)
		return result
	}

	// Set read deadline
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// Read response
	response, err := dnsConn.ReadMsg()
	if err != nil {
		result.Error = fmt.Sprintf("failed to read DNS response: %v", err)
		return result
	}

	result.LatencyMs = time.Since(start).Milliseconds()
	result.Rcode = response.Rcode
	result.RcodeName = dns.RcodeToString[response.Rcode]
	result.Success = response.Rcode == dns.RcodeSuccess

	// Parse records
	result.Records = make([]debug.DNSRecord, 0)
	for _, answer := range response.Answer {
		record := debug.DNSRecord{
			Name: answer.Header().Name,
			TTL:  answer.Header().Ttl,
		}

		switch rr := answer.(type) {
		case *dns.A:
			record.Type = "A"
			record.Value = rr.A.String()
		case *dns.AAAA:
			record.Type = "AAAA"
			record.Value = rr.AAAA.String()
		case *dns.CNAME:
			record.Type = "CNAME"
			record.Value = rr.Target
		case *dns.MX:
			record.Type = "MX"
			record.Value = fmt.Sprintf("%d %s", rr.Preference, rr.Mx)
		case *dns.TXT:
			record.Type = "TXT"
			record.Value = strings.Join(rr.Txt, " ")
		case *dns.NS:
			record.Type = "NS"
			record.Value = rr.Ns
		case *dns.SOA:
			record.Type = "SOA"
			record.Value = fmt.Sprintf("%s %s", rr.Ns, rr.Mbox)
		case *dns.PTR:
			record.Type = "PTR"
			record.Value = rr.Ptr
		default:
			record.Type = dns.TypeToString[answer.Header().Rrtype]
			record.Value = answer.String()
		}

		result.Records = append(result.Records, record)
	}

	return result
}

// GetMatchingRule finds the DNS rule that matches a hostname
func (app *App) GetMatchingRule(hostname string) *debug.DNSRuleInfo {
	hostname = strings.ToLower(hostname)
	profileNames := app.GetProfileNames()

	runtimeRules := config.BuildDNSRulesFromProfiles(app.config.Profiles)
	for _, rule := range runtimeRules {
		suffix := strings.ToLower(rule.Suffix)
		if strings.HasSuffix(hostname, suffix) || hostname == strings.TrimPrefix(suffix, ".") {
			stripSuffix := true
			if rule.StripSuffix != nil {
				stripSuffix = *rule.StripSuffix
			}
			return &debug.DNSRuleInfo{
				Suffix:      rule.Suffix,
				ProfileID:   rule.ProfileID,
				ProfileName: profileNames[rule.ProfileID],
				DNSServer:   app.tunnelManager.GetDNSServerForProfile(rule.ProfileID),
				StripSuffix: stripSuffix,
				Hosts:       rule.Hosts,
			}
		}
	}
	return nil
}

// GetTCPProxyInfo returns TCP proxy configuration and status
func (app *App) GetTCPProxyInfo() debug.TCPProxyInfo {
	return debug.TCPProxyInfo{
		Enabled:       app.config.TCPProxy.IsEnabled(),
		ListenerCount: app.tunnelManager.GetTCPProxyListenerCount(),
		TunnelIPs:     app.config.TCPProxy.TunnelIPs,
	}
}

// TestHost performs a complete test of a host (DNS + TCP connectivity)
// If useSystemDNS is true, it resolves via the system DNS (same path as real apps like DBeaver)
func (app *App) TestHost(hostname string, port int, profileID string, useSystemDNS bool) debug.HostTestResult {
	result := debug.HostTestResult{
		Hostname:      hostname,
		TCPPort:       port,
		UsedSystemDNS: useSystemDNS,
	}

	// If using system DNS, resolve through the OS (same path as a browser)
	// This goes through: system DNS → our DNS proxy → tunnel DNS
	// Then TCP connects through: system stack → loopback IP → TCP proxy → tunnel
	if useSystemDNS {
		// Find the matching DNS rule for display purposes
		matchingRule := app.GetMatchingRule(hostname)
		if matchingRule != nil {
			result.DNSRule = matchingRule.Suffix
			result.DNSServer = matchingRule.DNSServer
			result.ProfileID = matchingRule.ProfileID
			profile, profileErr := app.profileService.GetByID(matchingRule.ProfileID)
			if profileErr == nil {
				result.ProfileName = profile.Name
			}
		}

		resolvedIPs, lookupErr := net.LookupHost(hostname)
		if lookupErr != nil {
			result.DNSError = fmt.Sprintf("System DNS resolution failed: %v", lookupErr)
			result.DNSDiagnostics = app.gatherDNSDiagnostics(hostname, matchingRule, "")
			return result
		}
		if len(resolvedIPs) == 0 {
			result.DNSError = "System DNS returned no addresses"
			result.DNSDiagnostics = app.gatherDNSDiagnostics(hostname, matchingRule, "")
			return result
		}

		resolvedAddress := resolvedIPs[0]
		result.DNSResolved = true

		// Check if the resolved IP is a loopback (meaning our DNS proxy returned it
		// for transparent proxying). If so, look up the real IP from host mappings.
		if strings.HasPrefix(resolvedAddress, "127.0.") && resolvedAddress != "127.0.0.1" {
			result.LoopbackIP = resolvedAddress
			// Find the real IP from host mappings
			hostMappings := app.tunnelManager.GetHostMappings()
			for _, mapping := range hostMappings {
				if mapping.Hostname == hostname {
					result.RealIP = mapping.RealIP
					break
				}
			}
			if result.RealIP == "" {
				result.RealIP = resolvedAddress
			}
		} else {
			result.RealIP = resolvedAddress
		}

		// TCP connection through system stack (goes through transparent proxy if loopback)
		tcpTargetAddress := net.JoinHostPort(resolvedAddress, fmt.Sprintf("%d", port))
		tcpStartTime := time.Now()
		tcpConnection, tcpErr := net.DialTimeout("tcp", tcpTargetAddress, 10*time.Second)
		tcpElapsed := time.Since(tcpStartTime)

		if tcpErr != nil {
			result.TCPError = tcpErr.Error()
		} else {
			tcpConnection.Close()
			result.TCPConnected = true
			result.TCPLatencyMs = tcpElapsed.Milliseconds()
		}

		// Record latency metric
		if result.ProfileID != "" {
			debug.RecordLatencySample(result.ProfileID, tcpTargetAddress, tcpElapsed, result.TCPConnected)
		}

		// Gather diagnostics on any failure: TCP error, or DNS returned real IP (transparent proxy not working)
		if result.TCPError != "" || (result.DNSResolved && result.LoopbackIP == "") {
			result.DNSDiagnostics = app.gatherDNSDiagnostics(hostname, matchingRule, resolvedAddress)
		}

		return result
	}

	// Original behavior: resolve via tunnel directly
	// Find the matching rule if profileID not specified
	if profileID == "" {
		rule := app.GetMatchingRule(hostname)
		if rule != nil {
			profileID = rule.ProfileID
			result.DNSRule = rule.Suffix
		}
	}

	if profileID == "" {
		result.DNSError = "No DNS rule matches this hostname and no profileId specified"
		return result
	}

	// Get profile info
	profile, err := app.profileService.GetByID(profileID)
	if err != nil {
		result.DNSError = fmt.Sprintf("Profile not found: %s", profileID)
		return result
	}
	result.ProfileID = profileID
	result.ProfileName = profile.Name

	// Check if tunnel is connected
	if !app.tunnelManager.IsConnected(profileID) {
		result.DNSError = "Tunnel not connected"
		return result
	}

	// Get the tunnel
	active_tunnel := app.tunnelManager.GetTunnel(profileID)
	if active_tunnel == nil {
		result.DNSError = "Tunnel not available"
		return result
	}

	// Find the DNS rule for this profile
	dnsServer := app.tunnelManager.GetDNSServerForProfile(profileID)
	var staticHosts map[string]string
	var stripSuffix bool = true
	var ruleSuffix string
	runtimeRules := config.BuildDNSRulesFromProfiles(app.config.Profiles)
	for _, rule := range runtimeRules {
		if rule.ProfileID == profileID {
			staticHosts = rule.Hosts
			if rule.StripSuffix != nil {
				stripSuffix = *rule.StripSuffix
			}
			ruleSuffix = rule.Suffix
			result.DNSServer = dnsServer
			result.DNSRule = rule.Suffix
			break
		}
	}

	// Check host mappings cache first
	mappings := app.tunnelManager.GetHostMappings()
	for _, m := range mappings {
		if m.Hostname == hostname {
			result.RealIP = m.RealIP
			result.LoopbackIP = m.TunnelIP
			result.DNSResolved = true
			break
		}
	}

	// Check static hosts mapping if not found in cache
	if !result.DNSResolved && staticHosts != nil {
		queryDomain := strings.ToLower(hostname)
		if stripSuffix && ruleSuffix != "" {
			suffix := strings.ToLower(ruleSuffix)
			if !strings.HasPrefix(suffix, ".") {
				suffix = "." + suffix
			}
			if strings.HasSuffix(queryDomain, suffix) {
				queryDomain = queryDomain[:len(queryDomain)-len(suffix)]
			}
		}
		if staticIP, exists := staticHosts[queryDomain]; exists {
			result.RealIP = staticIP
			result.DNSResolved = true
			result.DNSServer = "static"
		}
	}

	// If not in cache/static and we have a DNS server, try to resolve
	if !result.DNSResolved && dnsServer != "" {
		// Try to resolve via tunnel DNS
		resolvedIP, resolveErr := app.tunnelManager.ResolveViaTunnel(profileID, hostname)
		if resolveErr != nil {
			result.DNSError = resolveErr.Error()
		} else {
			result.RealIP = resolvedIP
			result.DNSResolved = true
		}
	}

	// Test TCP connectivity
	if result.RealIP != "" || hostname != "" {
		targetHost := result.RealIP
		if targetHost == "" {
			targetHost = hostname
		}

		addr := fmt.Sprintf("%s:%d", targetHost, port)
		start := time.Now()
		conn, err := active_tunnel.Dial("tcp", addr)
		elapsed := time.Since(start)

		if err != nil {
			result.TCPError = err.Error()
		} else {
			conn.Close()
			result.TCPConnected = true
			result.TCPLatencyMs = elapsed.Milliseconds()
		}

		// Record latency metric
		debug.RecordLatencySample(profileID, addr, elapsed, result.TCPConnected)
	}

	return result
}

// gatherDNSDiagnostics runs an exhaustive diagnostic of the entire chain when DNS or TCP fails.
// It checks every link: system DNS config → DNS proxy → tunnel → remote DNS → TCP proxy → transparent proxy.
// resolvedAddress is the IP that system DNS returned (empty if DNS itself failed).
func (app *App) gatherDNSDiagnostics(hostname string, matchingRule *debug.DNSRuleInfo, resolvedAddress string) *debug.DNSDiagnosticDetail {
	diagnostics := &debug.DNSDiagnosticDetail{
		Steps:           []debug.DNSDiagnosticStep{},
		ResolvedAddress: resolvedAddress,
	}

	// Detect if system DNS returned a loopback (transparent proxy) or real IP
	if resolvedAddress != "" {
		isLoopback := strings.HasPrefix(resolvedAddress, "127.0.") && resolvedAddress != "127.0.0.1"
		diagnostics.ResolvedToLoopback = isLoopback
	}

	// --- Step 1: DNS Proxy enabled? ---
	dnsProxyEnabled := app.config.DNSProxy.Enabled
	diagnostics.DNSProxyEnabled = dnsProxyEnabled
	diagnostics.DNSProxyListenPort = app.config.DNSProxy.ListenPort
	if dnsProxyEnabled {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Proxy Enabled",
			Status: "ok",
			Detail: fmt.Sprintf("DNS proxy is enabled (port %d)", app.config.DNSProxy.ListenPort),
		})
	} else {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Proxy Enabled",
			Status: "fail",
			Detail: "DNS proxy is disabled. No DNS routing is active.",
			Fix:    "Enable the DNS proxy in Settings or connect a VPN profile with DNS rules configured.",
		})
		diagnostics.RootCause = "DNS proxy is disabled — no domain-based routing is active"
		return diagnostics
	}

	// --- Step 2: DNS rule matching ---
	diagnostics.HasMatchingRule = matchingRule != nil
	if matchingRule != nil {
		diagnostics.MatchedRuleSuffix = matchingRule.Suffix
		diagnostics.MatchedRuleProfile = matchingRule.ProfileName
		diagnostics.MatchedRuleDNS = matchingRule.DNSServer
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Rule Match",
			Status: "ok",
			Detail: fmt.Sprintf("Hostname matches rule '%s' → profile '%s' (DNS: %s)", matchingRule.Suffix, matchingRule.ProfileName, matchingRule.DNSServer),
		})
	} else {
		var configuredSuffixes []string
		diagnosticRules := config.BuildDNSRulesFromProfiles(app.config.Profiles)
		for _, rule := range diagnosticRules {
			configuredSuffixes = append(configuredSuffixes, rule.Suffix)
		}
		suffixList := "none configured"
		if len(configuredSuffixes) > 0 {
			suffixList = strings.Join(configuredSuffixes, ", ")
		}
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Rule Match",
			Status: "fail",
			Detail: fmt.Sprintf("No DNS rule matches '%s'. Configured suffixes: %s", hostname, suffixList),
			Fix:    fmt.Sprintf("Add a DNS rule with a suffix that matches '%s', or check the hostname is correct.", hostname),
		})
		diagnostics.RootCause = fmt.Sprintf("No DNS rule matches hostname '%s' — the DNS proxy doesn't know how to route this domain", hostname)
		return diagnostics
	}

	// --- Step 3: Tunnel connected? ---
	tunnelConnected := app.tunnelManager.IsConnected(matchingRule.ProfileID)
	diagnostics.TunnelConnected = tunnelConnected
	if tunnelConnected {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "Tunnel Connected",
			Status: "ok",
			Detail: fmt.Sprintf("Tunnel '%s' is connected", matchingRule.ProfileName),
		})
	} else {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "Tunnel Connected",
			Status: "fail",
			Detail: fmt.Sprintf("Tunnel '%s' is NOT connected", matchingRule.ProfileName),
			Fix:    fmt.Sprintf("Connect the VPN profile '%s' first.", matchingRule.ProfileName),
		})
		diagnostics.RootCause = fmt.Sprintf("Tunnel '%s' is disconnected — DNS queries cannot reach the remote DNS server", matchingRule.ProfileName)
		return diagnostics
	}

	// --- Step 4: TCP Proxy state ---
	tcpProxyConfig := app.config.TCPProxy
	diagnostics.TCPProxyEnabled = tcpProxyConfig.IsEnabled()
	diagnostics.TCPProxyTunnelIPs = tcpProxyConfig.TunnelIPs
	diagnostics.TCPProxyListenerCount = app.tunnelManager.GetTCPProxyListenerCount()

	profileTunnelIP, profileHasTunnelIP := tcpProxyConfig.TunnelIPs[matchingRule.ProfileID]
	diagnostics.ProfileHasTunnelIP = profileHasTunnelIP
	diagnostics.ProfileTunnelIP = profileTunnelIP

	if !tcpProxyConfig.IsEnabled() {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "TCP Proxy Enabled",
			Status: "fail",
			Detail: "TCP proxy is DISABLED. The DNS proxy will return real IPs instead of loopback IPs, and TCP connections cannot be routed through the tunnel.",
			Fix:    "Enable the TCP proxy in the app configuration. Go to the profile's TCP Proxy section and ensure it's enabled.",
		})
		if resolvedAddress != "" && !diagnostics.ResolvedToLoopback {
			diagnostics.RootCause = fmt.Sprintf("TCP proxy is disabled — DNS resolved to real IP %s but the system has no route to reach it (it's only accessible through the VPN tunnel)", resolvedAddress)
		}
	} else if !profileHasTunnelIP {
		// TCP proxy enabled but this profile has no tunnel IP assigned
		var allAssignments []string
		for assignedProfileID, assignedIP := range tcpProxyConfig.TunnelIPs {
			allAssignments = append(allAssignments, fmt.Sprintf("%s=%s", assignedProfileID, assignedIP))
		}
		assignmentsDisplay := "none"
		if len(allAssignments) > 0 {
			assignmentsDisplay = strings.Join(allAssignments, ", ")
		}
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "TCP Proxy — Profile Tunnel IP",
			Status: "fail",
			Detail: fmt.Sprintf("Profile '%s' (ID: %s) has NO loopback IP assigned in tcpProxy.tunnelIPs. Current assignments: [%s]", matchingRule.ProfileName, matchingRule.ProfileID, assignmentsDisplay),
			Fix:    fmt.Sprintf("Assign a loopback IP (e.g., 127.0.1.1) to profile '%s' in the TCP proxy configuration. This is required for the transparent proxy to intercept connections.", matchingRule.ProfileName),
		})
		if diagnostics.RootCause == "" {
			diagnostics.RootCause = fmt.Sprintf("Profile '%s' has no loopback IP in tcpProxy.tunnelIPs — the DNS proxy returns the real IP (%s) instead of a loopback, so TCP goes through the normal network stack which can't reach the VPN-only host", matchingRule.ProfileName, resolvedAddress)
		}
	} else {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "TCP Proxy — Profile Tunnel IP",
			Status: "ok",
			Detail: fmt.Sprintf("Profile '%s' has loopback IP %s assigned. TCP proxy has %d active listeners.", matchingRule.ProfileName, profileTunnelIP, diagnostics.TCPProxyListenerCount),
		})
	}

	// --- Step 5: Loopback IP check (did DNS return loopback or real IP?) ---
	if resolvedAddress != "" {
		if diagnostics.ResolvedToLoopback {
			diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
				Name:   "DNS → Loopback IP",
				Status: "ok",
				Detail: fmt.Sprintf("System DNS returned loopback IP %s (transparent proxy is active)", resolvedAddress),
			})
		} else {
			diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
				Name:   "DNS → Loopback IP",
				Status: "fail",
				Detail: fmt.Sprintf("System DNS returned real IP %s instead of a loopback IP. The transparent proxy is NOT intercepting this hostname.", resolvedAddress),
				Fix:    "The DNS proxy should return a loopback IP (127.0.x.1) when transparent proxy is configured. Check that the TCP proxy is enabled and has a tunnel IP assigned for this profile.",
			})
			if diagnostics.RootCause == "" {
				diagnostics.RootCause = fmt.Sprintf("DNS returned real IP %s instead of loopback — TCP connection goes through normal network stack which cannot reach VPN-only hosts", resolvedAddress)
			}
		}
	}

	// --- Step 6: Service connected? ---
	serviceConnected := false
	if app.networkConfig != nil {
		serviceConnected = app.networkConfig.IsServiceConnected()
	}
	diagnostics.ServiceConnected = serviceConnected
	if serviceConnected {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "Service Connected",
			Status: "ok",
			Detail: "VPN MultiTunnel Service is connected (privileged operations available)",
		})
	} else {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "Service Connected",
			Status: "warn",
			Detail: "VPN MultiTunnel Service is NOT connected. DNS configuration may require UAC elevation.",
			Fix:    "Install and start the service: VPNMultiTunnel-service.exe install && VPNMultiTunnel-service.exe start",
		})
	}

	// --- Step 7: DNS Client (Dnscache) status ---
	dnsClientRunning := system.IsDNSClientRunning()
	diagnostics.DNSClientRunning = dnsClientRunning
	if !dnsClientRunning {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Client Service (Dnscache)",
			Status: "ok",
			Detail: "Dnscache is stopped (not interfering with port 53)",
		})
	} else {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Client Service (Dnscache)",
			Status: "warn",
			Detail: "Dnscache is RUNNING. It may be caching stale DNS responses or intercepting queries before they reach our proxy.",
			Fix:    "The app should stop Dnscache automatically. If this persists, try disconnecting and reconnecting the VPN profile.",
		})
	}

	// --- Step 8: System DNS configuration ---
	expectedDNSAddress := "127.0.0.53"
	if app.networkConfig != nil {
		expectedDNSAddress = app.networkConfig.GetDNSProxyAddress()
	}
	diagnostics.ExpectedDNSAddress = expectedDNSAddress

	activeInterface := ""
	var currentDNSServers []string
	if app.networkConfig != nil {
		interfaceName, interfaceErr := app.networkConfig.GetActiveNetworkInterface()
		if interfaceErr == nil {
			activeInterface = interfaceName
			dnsServers, dnsErr := app.networkConfig.GetCurrentDNS(interfaceName)
			if dnsErr == nil {
				currentDNSServers = dnsServers
			}
		}
	}
	diagnostics.ActiveInterface = activeInterface
	diagnostics.CurrentSystemDNS = currentDNSServers

	systemDNSPointsToProxy := false
	if len(currentDNSServers) > 0 && currentDNSServers[0] == expectedDNSAddress {
		systemDNSPointsToProxy = true
	}
	diagnostics.SystemDNSConfigured = systemDNSPointsToProxy

	if systemDNSPointsToProxy {
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "System DNS → Proxy",
			Status: "ok",
			Detail: fmt.Sprintf("Interface '%s' DNS is set to %s (our proxy)", activeInterface, currentDNSServers[0]),
		})
	} else {
		currentDNSDisplay := "unknown"
		if len(currentDNSServers) > 0 {
			currentDNSDisplay = strings.Join(currentDNSServers, ", ")
		}
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "System DNS → Proxy",
			Status: "fail",
			Detail: fmt.Sprintf("Interface '%s' DNS is [%s] but should be [%s]. System DNS is NOT pointing to our proxy!", activeInterface, currentDNSDisplay, expectedDNSAddress),
			Fix:    "Click 'Configure DNS' in the app or disconnect/reconnect the VPN. The system DNS should be automatically set to " + expectedDNSAddress + " when a tunnel connects.",
		})
		if diagnostics.RootCause == "" {
			diagnostics.RootCause = fmt.Sprintf("System DNS is [%s] instead of [%s] — DNS queries are going to an external server, not our proxy", currentDNSDisplay, expectedDNSAddress)
		}
	}

	// --- Step 9: DNS proxy direct test (send query directly to proxy, bypassing system DNS) ---
	proxyTestAddress := fmt.Sprintf("%s:53", expectedDNSAddress)
	dnsClient := &dns.Client{
		Net:     "udp",
		Timeout: 3 * time.Second,
	}
	dnsQuery := new(dns.Msg)
	dnsQuery.SetQuestion(dns.Fqdn(hostname), dns.TypeA)
	dnsResponse, _, proxyQueryErr := dnsClient.Exchange(dnsQuery, proxyTestAddress)

	if proxyQueryErr != nil {
		diagnostics.ProxyDirectOk = false
		diagnostics.ProxyDirectResult = proxyQueryErr.Error()
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Proxy Direct Query",
			Status: "fail",
			Detail: fmt.Sprintf("Direct query to proxy (%s) failed: %s", proxyTestAddress, proxyQueryErr.Error()),
			Fix:    "The DNS proxy may not be listening. Check if port 53 is free and the proxy started correctly. Try disconnecting and reconnecting.",
		})
		if diagnostics.RootCause == "" {
			diagnostics.RootCause = fmt.Sprintf("DNS proxy at %s is not responding — it may not be running or port 53 is blocked", proxyTestAddress)
		}
	} else if dnsResponse != nil && dnsResponse.Rcode != dns.RcodeSuccess {
		diagnostics.ProxyDirectOk = false
		rcodeName := dns.RcodeToString[dnsResponse.Rcode]
		diagnostics.ProxyDirectResult = fmt.Sprintf("Response code: %s (%d)", rcodeName, dnsResponse.Rcode)
		proxyStepDNSServer := app.tunnelManager.GetDNSServerForProfile(matchingRule.ProfileID)
		fixMessage := "Check the hostname is correct and exists in the remote DNS server."
		if dnsResponse.Rcode == dns.RcodeNameError {
			fixMessage = fmt.Sprintf("The remote DNS server (%s) does not know this hostname. Verify the hostname is correct and that it exists in the remote network.", proxyStepDNSServer)
		}
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Proxy Direct Query",
			Status: "fail",
			Detail: fmt.Sprintf("Proxy responded with %s for '%s'", rcodeName, hostname),
			Fix:    fixMessage,
		})
		if diagnostics.RootCause == "" {
			diagnostics.RootCause = fmt.Sprintf("DNS proxy is running but remote DNS (%s) returned %s — hostname '%s' may not exist in the remote network", proxyStepDNSServer, rcodeName, hostname)
		}
	} else if dnsResponse != nil {
		var resolvedIP string
		for _, answerRecord := range dnsResponse.Answer {
			if aRecord, isARecord := answerRecord.(*dns.A); isARecord {
				resolvedIP = aRecord.A.String()
				break
			}
		}
		diagnostics.ProxyDirectOk = true
		diagnostics.ProxyDirectResult = resolvedIP
		diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
			Name:   "DNS Proxy Direct Query",
			Status: "ok",
			Detail: fmt.Sprintf("Direct query to proxy resolved '%s' → %s", hostname, resolvedIP),
		})

		if diagnostics.RootCause == "" && resolvedAddress == "" {
			diagnostics.RootCause = "DNS proxy resolves correctly but system DNS is not reaching it — check system DNS settings and Dnscache"
		}
	}

	// --- Step 10: Direct tunnel DNS test (bypass everything, query remote DNS through tunnel) ---
	directTestDNSServer := app.tunnelManager.GetDNSServerForProfile(matchingRule.ProfileID)
	if tunnelConnected && directTestDNSServer != "" {
		directIP, directErr := app.tunnelManager.ResolveViaTunnel(matchingRule.ProfileID, hostname)
		if directErr != nil {
			diagnostics.DirectTunnelDNSOk = false
			diagnostics.DirectTunnelDNSResult = directErr.Error()
			diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
				Name:   "Direct Tunnel DNS Query",
				Status: "fail",
				Detail: fmt.Sprintf("Direct query through tunnel to %s failed: %s", directTestDNSServer, directErr.Error()),
				Fix:    fmt.Sprintf("The remote DNS server %s is unreachable through the tunnel. Check the tunnel health and the DNS server configuration.", directTestDNSServer),
			})
			if diagnostics.RootCause == "" {
				diagnostics.RootCause = fmt.Sprintf("Remote DNS server %s is unreachable through tunnel '%s' — the tunnel may be unhealthy or the DNS server is down", directTestDNSServer, matchingRule.ProfileName)
			}
		} else {
			diagnostics.DirectTunnelDNSOk = true
			diagnostics.DirectTunnelDNSResult = directIP
			diagnostics.Steps = append(diagnostics.Steps, debug.DNSDiagnosticStep{
				Name:   "Direct Tunnel DNS Query",
				Status: "ok",
				Detail: fmt.Sprintf("Direct tunnel query to %s resolved '%s' → %s", directTestDNSServer, hostname, directIP),
			})
		}
	}

	// --- Final root cause if not set ---
	if diagnostics.RootCause == "" {
		diagnostics.RootCause = "The exact cause could not be determined automatically. Check all diagnostic steps above for clues."
	}

	return diagnostics
}

// GetSystemInfo returns system information
func (app *App) GetSystemInfo() debug.SystemInfo {
	info := debug.SystemInfo{
		IsAdmin:  app.IsRunningAsAdmin(),
		Platform: "windows",
	}

	if app.networkConfig != nil {
		info.ServiceConnected = app.networkConfig.IsServiceConnected()
		info.DNSConfigured = app.networkConfig.IsTransparentDNSConfigured()

		interfaceName, err := app.networkConfig.GetActiveNetworkInterface()
		if err == nil {
			dnsServers, err := app.networkConfig.GetCurrentDNS(interfaceName)
			if err == nil && len(dnsServers) > 0 {
				info.CurrentDNS = dnsServers[0]
			}
		}
	}

	return info
}

// GenerateDiagnosticReport generates a complete diagnostic report
func (app *App) GenerateDiagnosticReport() debug.DiagnosticReport {
	return debug.DiagnosticReport{
		GeneratedAt:  time.Now(),
		AppVersion:   "1.2.0", // TODO: Get from build info
		SystemInfo:   app.GetSystemInfo(),
		VPNStatus:    app.GetVPNStatusList(),
		DNSConfig:    app.GetDNSConfig(),
		TCPProxyInfo: app.GetTCPProxyInfo(),
		HostMappings: app.GetHostMappings(),
		RecentErrors: debug.GetErrorCollector().GetRecent(50),
		RecentLogs:   debug.GetLogger().GetLogs(100),
		Metrics:      debug.GetMetricsCollector().GetAllMetrics(),
	}
}

// =============================================================================
// Frontend Logging - Exposed to JavaScript via Wails
// =============================================================================

// LogFrontend receives log entries from the frontend (React)
func (app *App) LogFrontend(level, component, message string, fields map[string]any) {
	logger := debug.GetLogger()
	switch debug.LogLevel(level) {
	case debug.LevelDebug:
		logger.Debug("frontend:"+component, message, fields)
	case debug.LevelInfo:
		logger.Info("frontend:"+component, message, fields)
	case debug.LevelWarn:
		logger.Warn("frontend:"+component, message, fields)
	case debug.LevelError:
		logger.Error("frontend:"+component, message, fields)
	default:
		logger.Info("frontend:"+component, message, fields)
	}
}

// LogFrontendError receives error entries from the frontend
func (app *App) LogFrontendError(component, operation, errorMsg string, context map[string]any) {
	debug.RecordError("frontend:"+component, operation, fmt.Errorf("%s", errorMsg), context)
}

// GetDebugLogs returns logs for display (exposed to frontend)
func (app *App) GetDebugLogs(level, component string, limit int) []debug.LogEntry {
	if limit <= 0 {
		limit = 100
	}
	return debug.GetLogger().GetLogsFiltered(debug.LogLevel(level), component, "", limit)
}

// GetDebugErrors returns recent errors (exposed to frontend)
func (app *App) GetDebugErrors(limit int) []debug.ErrorEntry {
	if limit <= 0 {
		limit = 50
	}
	return debug.GetErrorCollector().GetRecent(limit)
}

// GetProfileLogs returns logs filtered by profile ID (exposed to frontend)
func (app *App) GetProfileLogs(profileID, level string, limit int) []debug.LogEntry {
	if limit <= 0 {
		limit = 200
	}
	return debug.GetLogger().GetLogsFiltered(debug.LogLevel(level), "", profileID, limit)
}

// GetSystemLogs returns combined service.log + Windows Event Log entries (exposed to frontend)
func (app *App) GetSystemLogs(limit int) []system.SystemLogEntry {
	if limit <= 0 {
		limit = 500
	}
	return system.GetCombinedSystemLogs(limit)
}

// GetProfileErrors returns errors filtered by profile ID (exposed to frontend)
func (app *App) GetProfileErrors(profileID string, limit int) []debug.ErrorEntry {
	if limit <= 0 {
		limit = 50
	}
	return debug.GetErrorCollector().GetByProfile(profileID, limit)
}

// GetDebugMetrics returns metrics (exposed to frontend)
func (app *App) GetDebugMetrics() map[string]any {
	return debug.GetMetricsCollector().GetAllMetrics()
}

// TestHostConnectivity tests connectivity to a host (exposed to frontend)
// Uses system DNS by default to match real app behavior (DBeaver, etc.)
func (app *App) TestHostConnectivity(hostname string, port int) debug.HostTestResult {
	return app.TestHost(hostname, port, "", true)
}

// DiagnoseHostDNS diagnoses DNS for a hostname (exposed to frontend)
func (app *App) DiagnoseHostDNS(hostname string) debug.DNSDiagnostic {
	return app.DiagnoseDNS(hostname)
}

// GetAllHostMappings returns all host mappings (exposed to frontend)
func (app *App) GetAllHostMappings() []debug.HostMappingInfo {
	return app.GetHostMappings()
}

// PingHost performs a simple connectivity test
func (app *App) PingHost(profileID, host string, port int) (bool, int64, string) {
	if !app.tunnelManager.IsConnected(profileID) {
		return false, 0, "Tunnel not connected"
	}

	active_tunnel := app.tunnelManager.GetTunnel(profileID)
	if active_tunnel == nil {
		return false, 0, "Tunnel not available"
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	start := time.Now()
	conn, err := active_tunnel.Dial("tcp", addr)
	elapsed := time.Since(start)

	if err != nil {
		return false, elapsed.Milliseconds(), err.Error()
	}
	conn.Close()

	debug.RecordLatencySample(profileID, addr, elapsed, true)
	return true, elapsed.Milliseconds(), fmt.Sprintf("Connected in %dms", elapsed.Milliseconds())
}
