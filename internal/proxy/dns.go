package proxy

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/debug"
)

// DNSProxy is a DNS proxy that routes queries based on domain rules
type DNSProxy struct {
	config          *config.DNSProxy
	server          *dns.Server
	serverV6        *dns.Server          // IPv6 server
	connV4          net.PacketConn       // Pre-bound IPv4 UDP connection
	connV6          net.PacketConn       // Pre-bound IPv6 UDP connection
	dialerGetter    func(profileID string) TunnelDialer
	dnsServerGetter func(profileID string) string // Resolves DNS server from WireGuard .conf
	// Transparent proxy support
	tunnelIPs            map[string]string    // profileID -> "127.0.x.1"
	hostMapping          *HostMappingCache    // Shared cache for transparent proxy
	tcpProxyEnabled      bool
	// Callbacks for dynamic IP management
	onNewIP         func(ip string, profileID string) error // Called when a new unique IP is assigned
	mu              sync.RWMutex
}

// NewDNSProxy creates a new DNS proxy
func NewDNSProxy(cfg *config.DNSProxy, dialerGetter func(profileID string) TunnelDialer, dnsServerGetter func(profileID string) string) (*DNSProxy, error) {
	return &DNSProxy{
		config:          cfg,
		dialerGetter:    dialerGetter,
		dnsServerGetter: dnsServerGetter,
		tunnelIPs:       make(map[string]string),
	}, nil
}

// SetTransparentProxyConfig sets the transparent proxy configuration
func (dns_proxy *DNSProxy) SetTransparentProxyConfig(tunnelIPs map[string]string, hostMapping *HostMappingCache, enabled bool, onNewIP func(ip string, profileID string) error) {
	dns_proxy.mu.Lock()
	defer dns_proxy.mu.Unlock()
	dns_proxy.tunnelIPs = tunnelIPs
	dns_proxy.hostMapping = hostMapping
	dns_proxy.tcpProxyEnabled = enabled
	dns_proxy.onNewIP = onNewIP
}

// listenPacketWithReuseAddr creates a UDP PacketConn with SO_REUSEADDR set.
// This allows binding to a specific loopback IP (e.g. 127.0.0.53:53) even when
// another process (SharedAccess/ICS) holds a wildcard binding on 0.0.0.0:53.
func listenPacketWithReuseAddr(network, address string) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, rawConn syscall.RawConn) error {
			var opErr error
			err := rawConn.Control(func(fd uintptr) {
				opErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
			})
			if err != nil {
				return err
			}
			return opErr
		},
	}
	return lc.ListenPacket(context.Background(), network, address)
}

// Start starts the DNS proxy on both IPv4 and IPv6
func (dns_proxy *DNSProxy) Start() error {
	handler := dns.HandlerFunc(dns_proxy.handleDNS)

	// Start IPv4 server on configured loopback address (default: 127.0.0.53)
	listenAddr := dns_proxy.config.GetListenAddress()
	addrV4 := fmt.Sprintf("%s:%d", listenAddr, dns_proxy.config.ListenPort)

	// Pre-bind with SO_REUSEADDR to coexist with SharedAccess/ICS on 0.0.0.0:53.
	// The specific-address binding (127.0.0.53) takes priority over the wildcard (0.0.0.0)
	// for traffic directed to our address.
	connV4, err := listenPacketWithReuseAddr("udp4", addrV4)
	if err != nil {
		return fmt.Errorf("cannot bind DNS proxy to %s: %w (is the loopback IP configured?)", addrV4, err)
	}
	dns_proxy.connV4 = connV4

	dns_proxy.server = &dns.Server{
		PacketConn: connV4,
		Handler:    handler,
	}

	// Channel to capture startup errors
	errChan := make(chan error, 1)
	go func() {
		if err := dns_proxy.server.ActivateAndServe(); err != nil {
			log.Printf("DNS proxy IPv4 error: %v", err)
			select {
			case errChan <- err:
			default:
			}
		}
	}()

	// Give it a moment to start and check for immediate errors
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-errChan:
		dns_proxy.connV4.Close()
		dns_proxy.connV4 = nil
		return fmt.Errorf("DNS proxy IPv4 failed to start on %s: %w", addrV4, err)
	default:
		// No error, continue
	}

	// Start IPv6 server on [::1]
	addrV6 := fmt.Sprintf("[::1]:%d", dns_proxy.config.ListenPort)
	connV6, err := listenPacketWithReuseAddr("udp6", addrV6)
	if err != nil {
		log.Printf("DNS proxy IPv6 bind warning: %v (IPv6 will be unavailable)", err)
	} else {
		dns_proxy.connV6 = connV6
		dns_proxy.serverV6 = &dns.Server{
			PacketConn: connV6,
			Handler:    handler,
		}

		go func() {
			if err := dns_proxy.serverV6.ActivateAndServe(); err != nil {
				log.Printf("DNS proxy IPv6 error: %v", err)
			}
		}()
	}

	log.Printf("DNS proxy started on %s and %s", addrV4, addrV6)
	return nil
}

// Stop stops the DNS proxy (both IPv4 and IPv6)
func (dns_proxy *DNSProxy) Stop() {
	if dns_proxy.server != nil {
		dns_proxy.server.Shutdown()
		dns_proxy.server = nil
	}
	if dns_proxy.connV4 != nil {
		dns_proxy.connV4.Close()
		dns_proxy.connV4 = nil
	}
	if dns_proxy.serverV6 != nil {
		dns_proxy.serverV6.Shutdown()
		dns_proxy.serverV6 = nil
	}
	if dns_proxy.connV6 != nil {
		dns_proxy.connV6.Close()
		dns_proxy.connV6 = nil
	}
}

// Restart restarts the DNS proxy on a new port
func (dns_proxy *DNSProxy) Restart(newPort int) error {
	dns_proxy.Stop()
	dns_proxy.config.ListenPort = newPort
	return dns_proxy.Start()
}

// GetPort returns the current listening port
func (dns_proxy *DNSProxy) GetPort() int {
	return dns_proxy.config.ListenPort
}

// GetListenAddress returns the listen address (e.g., "127.0.0.53")
func (dns_proxy *DNSProxy) GetListenAddress() string {
	return dns_proxy.config.GetListenAddress()
}

func (dns_proxy *DNSProxy) handleDNS(response_writer dns.ResponseWriter, request *dns.Msg) {
	if len(request.Question) == 0 {
		return
	}

	query_start_time := time.Now()
	question := request.Question[0]
	domain := strings.ToLower(strings.TrimSuffix(question.Name, "."))
	query_type_string := dns.TypeToString[question.Qtype]

	// Resolve which process made this DNS query from the UDP source port
	source_process_name := ""
	if remote_addr := response_writer.RemoteAddr(); remote_addr != nil {
		if udp_addr, ok := remote_addr.(*net.UDPAddr); ok {
			source_process_name = GetProcessResolver().ResolveUDPSourceProcess(uint16(udp_addr.Port))
		}
	}

	// Find matching rule
	rule := dns_proxy.findRule(domain)

	var response *dns.Msg
	var err error

	if rule != nil {
		// Check if transparent proxy is enabled for this profile
		dns_proxy.mu.RLock()
		_, hasTunnelIP := dns_proxy.tunnelIPs[rule.ProfileID]
		tcpProxyEnabled := dns_proxy.tcpProxyEnabled
		hostMapping := dns_proxy.hostMapping
		onNewIP := dns_proxy.onNewIP
		dns_proxy.mu.RUnlock()

		// Strip suffix if configured (default: true)
		queryDomain := domain
		if rule.ShouldStripSuffix() {
			queryDomain = stripSuffix(domain, rule.Suffix)
			log.Printf("DNS: stripping suffix %s -> %s", domain, queryDomain)
		}

		// Check for static host mapping first
		if rule.Hosts != nil {
			if staticIP, exists := rule.Hosts[queryDomain]; exists {
				log.Printf("DNS: static host mapping %s -> %s (profile: %s)", domain, staticIP, rule.ProfileID)

				static_tunnel_ip := ""
				// If transparent proxy is enabled, route through it
				if tcpProxyEnabled && hasTunnelIP && hostMapping != nil {
					static_tunnel_ip = hostMapping.GetOrAssignIP(domain, rule.ProfileID)
					mapping := &HostMapping{
						Hostname:  domain,
						RealIP:    staticIP,
						TunnelIP:  static_tunnel_ip,
						ProfileID: rule.ProfileID,
					}
					hostMapping.Set(mapping)
					log.Printf("DNS transparent proxy (static): %s -> %s (real: %s, profile: %s)", domain, static_tunnel_ip, staticIP, rule.ProfileID)

					if onNewIP != nil {
						if err := onNewIP(static_tunnel_ip, rule.ProfileID); err != nil {
							log.Printf("DNS: failed to configure new IP %s: %v", static_tunnel_ip, err)
						}
					}
					response = createAResponse(request, static_tunnel_ip, question.Qtype)
				} else {
					// Return static IP directly
					response = createAResponse(request, staticIP, question.Qtype)
				}
				response_writer.WriteMsg(response)
				GetTrafficMonitor().RecordDNSQuery(DNSLogEntry{
					Timestamp:      query_start_time,
					Domain:         domain,
					QueryType:      query_type_string,
					ResolvedIP:     staticIP,
					TunnelIP:       static_tunnel_ip,
					ProfileID:      rule.ProfileID,
					ResponseTimeMs: time.Since(query_start_time).Milliseconds(),
					Success:        true,
					ViaTunnel:      false,
					SourceProcess:  source_process_name,
				})
				return
			}
		}

		// Create modified query with stripped domain
		modifiedRequest := request.Copy()
		modifiedRequest.Question[0].Name = dns.Fqdn(queryDomain)

		// Query through tunnel to get the real IP (DNS server resolved from .conf)
		profileDNSServer := dns_proxy.dnsServerGetter(rule.ProfileID)
		if profileDNSServer == "" {
			log.Printf("DNS: no DNS server configured for profile %s, falling back", rule.ProfileID)
			response, err = dns_proxy.queryFallback(request)
		} else {
			response, err = dns_proxy.queryThroughTunnel(modifiedRequest, rule.ProfileID, profileDNSServer)
		}

		// If transparent proxy is enabled and we have a tunnel IP, replace the response
		if err == nil && tcpProxyEnabled && hasTunnelIP && hostMapping != nil {
			// Extract real IP from response
			realIP := extractIPFromResponse(response)
			if realIP != "" {
				// Get or assign a unique IP for this hostname
				tunnelIP := hostMapping.GetOrAssignIP(domain, rule.ProfileID)

				// Store the mapping (use original domain with suffix for lookups)
				mapping := &HostMapping{
					Hostname:  domain,
					RealIP:    realIP,
					TunnelIP:  tunnelIP,
					ProfileID: rule.ProfileID,
				}
				hostMapping.Set(mapping)
				log.Printf("DNS transparent proxy: %s -> %s (real: %s, profile: %s)", domain, tunnelIP, realIP, rule.ProfileID)

				// Notify callback to configure loopback IP and TCP listeners
				if onNewIP != nil {
					if err := onNewIP(tunnelIP, rule.ProfileID); err != nil {
						log.Printf("DNS: failed to configure new IP %s: %v", tunnelIP, err)
					}
				}

				// Create new response with tunnel IP (use original question name)
				response = createAResponse(request, tunnelIP, question.Qtype)
			}
		} else if err == nil {
			// Fix response to use original domain name (with suffix)
			response.Question[0].Name = question.Name
			for _, ans := range response.Answer {
				ans.Header().Name = question.Name
			}
		}
	} else {
		// Use system DNS or fallback
		response, err = dns_proxy.queryFallback(request)
	}

	if err != nil {
		log.Printf("DNS query error for %s: %v", domain, err)
		// Return SERVFAIL
		servfail_response := new(dns.Msg)
		servfail_response.SetRcode(request, dns.RcodeServerFailure)
		response_writer.WriteMsg(servfail_response)

		error_profile_id := ""
		if rule != nil {
			error_profile_id = rule.ProfileID
		}
		GetTrafficMonitor().RecordDNSQuery(DNSLogEntry{
			Timestamp:      query_start_time,
			Domain:         domain,
			QueryType:      query_type_string,
			ProfileID:      error_profile_id,
			ResponseTimeMs: time.Since(query_start_time).Milliseconds(),
			Success:        false,
			ErrorMessage:   err.Error(),
			ViaTunnel:      rule != nil,
			SourceProcess:  source_process_name,
		})
		return
	}

	// Determine the profile and resolved IP for the successful response
	success_profile_id := ""
	resolved_via_tunnel := false
	if rule != nil {
		success_profile_id = rule.ProfileID
		resolved_via_tunnel = true
	}
	success_resolved_ip := extractIPFromResponse(response)

	response_writer.WriteMsg(response)

	GetTrafficMonitor().RecordDNSQuery(DNSLogEntry{
		Timestamp:      query_start_time,
		Domain:         domain,
		QueryType:      query_type_string,
		ResolvedIP:     success_resolved_ip,
		ProfileID:      success_profile_id,
		ResponseTimeMs: time.Since(query_start_time).Milliseconds(),
		Success:        true,
		ViaTunnel:      resolved_via_tunnel,
		SourceProcess:  source_process_name,
	})
}

func (dns_proxy *DNSProxy) findRule(domain string) *config.DNSRule {
	dns_proxy.mu.RLock()
	defer dns_proxy.mu.RUnlock()

	for idx_rule := range dns_proxy.config.Rules {
		rule := &dns_proxy.config.Rules[idx_rule]
		suffix := strings.ToLower(rule.Suffix)
		if strings.HasSuffix(domain, suffix) || domain == strings.TrimPrefix(suffix, ".") {
			return rule
		}
	}

	return nil
}

// stripSuffix removes the suffix from a domain name
func stripSuffix(domain, suffix string) string {
	suffix = strings.ToLower(suffix)
	if !strings.HasPrefix(suffix, ".") {
		suffix = "." + suffix
	}
	if strings.HasSuffix(strings.ToLower(domain), suffix) {
		return domain[:len(domain)-len(suffix)]
	}
	return domain
}

func (dns_proxy *DNSProxy) queryThroughTunnel(request *dns.Msg, profileID, dnsServer string) (*dns.Msg, error) {
	// Log the actual query being sent
	if len(request.Question) > 0 {
		debug.Debug("dns", fmt.Sprintf("queryThroughTunnel: querying %s via %s (profile: %s)", request.Question[0].Name, dnsServer, profileID), nil)
	}

	dialer := dns_proxy.dialerGetter(profileID)
	if dialer == nil {
		return nil, fmt.Errorf("tunnel not connected for profile: %s", profileID)
	}

	// Create DNS client with tunnel dialer
	client := &dns.Client{
		Net:     "udp",
		Timeout: 5 * time.Second,
		Dialer: &net.Dialer{
			Timeout: 5 * time.Second,
		},
	}

	// Connect through tunnel
	conn, err := dialer.Dial("udp", dnsServer+":53")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to DNS server: %w", err)
	}
	defer conn.Close()

	// Create DNS connection
	dnsConn := &dns.Conn{Conn: conn}

	// Send query
	if err := dnsConn.WriteMsg(request); err != nil {
		return nil, fmt.Errorf("failed to send DNS query: %w", err)
	}

	// Set read deadline
	conn.SetReadDeadline(time.Now().Add(client.Timeout))

	// Read response
	response, err := dnsConn.ReadMsg()
	if err != nil {
		return nil, fmt.Errorf("failed to read DNS response: %w", err)
	}

	// Log response details
	debug.Debug("dns", fmt.Sprintf("queryThroughTunnel: got response with %d answers, rcode=%d", len(response.Answer), response.Rcode), nil)

	return response, nil
}

func (dns_proxy *DNSProxy) queryFallback(request *dns.Msg) (*dns.Msg, error) {
	client := &dns.Client{
		Net:     "udp",
		Timeout: 5 * time.Second,
	}

	// Get system DNS servers or use common fallbacks
	dnsServers := getSystemDNS()
	if len(dnsServers) == 0 {
		dnsServers = []string{"8.8.8.8", "1.1.1.1"}
	}

	var lastErr error
	for _, server := range dnsServers {
		response, _, err := client.Exchange(request, server+":53")
		if err == nil {
			return response, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("all DNS servers failed: %w", lastErr)
}

// getSystemDNS gets the system's configured DNS servers
func getSystemDNS() []string {
	// On Windows, we could parse the network configuration
	// For simplicity, return common public DNS servers
	return []string{"8.8.8.8", "8.8.4.4", "1.1.1.1", "1.0.0.1"}
}

// UpdateRules updates the DNS routing rules
func (dns_proxy *DNSProxy) UpdateRules(rules []config.DNSRule) {
	dns_proxy.mu.Lock()
	defer dns_proxy.mu.Unlock()
	dns_proxy.config.Rules = rules
}

// extractIPFromResponse extracts the first A record IP from a DNS response
func extractIPFromResponse(response *dns.Msg) string {
	if response == nil {
		return ""
	}

	for _, answer := range response.Answer {
		if a_record, ok := answer.(*dns.A); ok {
			return a_record.A.String()
		}
	}

	return ""
}

// createAResponse creates a DNS response with a single A record
func createAResponse(request *dns.Msg, ip string, qtype uint16) *dns.Msg {
	response := new(dns.Msg)
	response.SetReply(request)
	response.Authoritative = true

	if qtype == dns.TypeA {
		parsedIP := net.ParseIP(ip)
		if parsedIP != nil && parsedIP.To4() != nil {
			rr := &dns.A{
				Hdr: dns.RR_Header{
					Name:   request.Question[0].Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    60,
				},
				A: parsedIP.To4(),
			}
			response.Answer = append(response.Answer, rr)
		}
	}

	return response
}

// ResolveViaTunnel resolves a hostname via a specific tunnel's DNS server
func (dns_proxy *DNSProxy) ResolveViaTunnel(profileID, hostname, dnsServer string) (string, error) {
	// Find the matching rule to check if we should strip suffix
	queryHostname := hostname
	debug.Debug("dns", fmt.Sprintf("ResolveViaTunnel: looking for rule for %s (rules count: %d)", hostname, len(dns_proxy.config.Rules)), nil)
	rule := dns_proxy.findRule(strings.ToLower(hostname))
	if rule != nil {
		debug.Debug("dns", fmt.Sprintf("ResolveViaTunnel: found rule %s, stripSuffix=%v", rule.Suffix, rule.ShouldStripSuffix()), nil)
		if rule.ShouldStripSuffix() {
			queryHostname = stripSuffix(hostname, rule.Suffix)
			debug.Info("dns", fmt.Sprintf("ResolveViaTunnel: stripping suffix %s -> %s", hostname, queryHostname), nil)
		}
	} else {
		debug.Warn("dns", fmt.Sprintf("ResolveViaTunnel: no rule found for %s", hostname), nil)
	}

	// Use provided dnsServer, or resolve from .conf cache
	resolvedDNSServer := dnsServer
	if resolvedDNSServer == "" && dns_proxy.dnsServerGetter != nil {
		resolvedDNSServer = dns_proxy.dnsServerGetter(profileID)
	}
	if resolvedDNSServer == "" {
		return "", fmt.Errorf("no DNS server configured for profile %s", profileID)
	}

	// Create DNS query
	dns_query := new(dns.Msg)
	dns_query.SetQuestion(dns.Fqdn(queryHostname), dns.TypeA)
	dns_query.RecursionDesired = true

	// Query through tunnel
	response, err := dns_proxy.queryThroughTunnel(dns_query, profileID, resolvedDNSServer)
	if err != nil {
		return "", err
	}

	// Extract IP from response
	resolvedIP := extractIPFromResponse(response)
	if resolvedIP == "" {
		return "", fmt.Errorf("no A record found for %s", hostname)
	}

	return resolvedIP, nil
}
