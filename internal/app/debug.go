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
			Connected:   app.tunnelManager.IsConnected(p.ID),
			TunnelIP:    app.profileService.GetTunnelIP(p.ID),
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

	rules := make([]debug.DNSRuleInfo, 0, len(app.config.DNSProxy.Rules))
	for _, r := range app.config.DNSProxy.Rules {
		stripSuffix := true
		if r.StripSuffix != nil {
			stripSuffix = *r.StripSuffix
		}
		rules = append(rules, debug.DNSRuleInfo{
			Suffix:      r.Suffix,
			ProfileID:   r.ProfileID,
			ProfileName: profileNames[r.ProfileID],
			DNSServer:   r.DNSServer,
			StripSuffix: stripSuffix,
			Hosts:       r.Hosts,
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

	diagnostic.WouldResolve = true
	diagnostic.Reason = fmt.Sprintf("Will resolve via tunnel DNS server %s (profile: %s)", matchedRule.DNSServer, matchedRule.ProfileName)
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

	// If no DNS server specified, try to find from config
	if dnsServer == "" {
		for _, p := range app.config.Profiles {
			if p.ID == profileID && p.DNS.Server != "" {
				dnsServer = p.DNS.Server
				break
			}
		}
		if dnsServer == "" {
			result.Error = "no DNS server specified and none configured for profile"
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

	for _, r := range app.config.DNSProxy.Rules {
		suffix := strings.ToLower(r.Suffix)
		if strings.HasSuffix(hostname, suffix) || hostname == strings.TrimPrefix(suffix, ".") {
			stripSuffix := true
			if r.StripSuffix != nil {
				stripSuffix = *r.StripSuffix
			}
			return &debug.DNSRuleInfo{
				Suffix:      r.Suffix,
				ProfileID:   r.ProfileID,
				ProfileName: profileNames[r.ProfileID],
				DNSServer:   r.DNSServer,
				StripSuffix: stripSuffix,
				Hosts:       r.Hosts,
			}
		}
	}
	return nil
}

// GetTCPProxyInfo returns TCP proxy configuration and status
func (app *App) GetTCPProxyInfo() debug.TCPProxyInfo {
	return debug.TCPProxyInfo{
		Enabled:       app.config.TCPProxy.Enabled,
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

	// If using system DNS, resolve through the OS (which should use our DNS proxy)
	if useSystemDNS {
		ips, err := net.LookupHost(hostname)
		if err != nil {
			result.DNSError = fmt.Sprintf("System DNS resolution failed: %v", err)
			return result
		}
		if len(ips) > 0 {
			result.RealIP = ips[0]
			result.DNSResolved = true
			result.DNSServer = "system"

			// Try TCP connection through system (normal network stack)
			addr := net.JoinHostPort(result.RealIP, fmt.Sprintf("%d", port))
			start := time.Now()
			conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
			elapsed := time.Since(start)

			if err != nil {
				result.TCPError = err.Error()
			} else {
				conn.Close()
				result.TCPConnected = true
				result.TCPLatencyMs = elapsed.Milliseconds()
			}
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
	var dnsServer string
	var staticHosts map[string]string
	var stripSuffix bool = true
	var ruleSuffix string
	for _, r := range app.config.DNSProxy.Rules {
		if r.ProfileID == profileID {
			dnsServer = r.DNSServer
			staticHosts = r.Hosts
			if r.StripSuffix != nil {
				stripSuffix = *r.StripSuffix
			}
			ruleSuffix = r.Suffix
			result.DNSServer = dnsServer
			result.DNSRule = r.Suffix
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
		ip, err := app.tunnelManager.ResolveViaTunnel(profileID, hostname, dnsServer)
		if err != nil {
			result.DNSError = err.Error()
		} else {
			result.RealIP = ip
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
