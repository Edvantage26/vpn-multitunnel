package debug

import (
	"time"
)

// HostTestResult contains the result of a complete host test
type HostTestResult struct {
	Hostname    string `json:"hostname"`
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`

	// DNS Resolution
	DNSResolved   bool   `json:"dnsResolved"`
	RealIP        string `json:"realIP"`             // IP real del host
	LoopbackIP    string `json:"loopbackIP"`         // IP loopback asignada (127.0.x.1)
	DNSServer     string `json:"dnsServer"`          // DNS server que resolvió
	DNSRule       string `json:"dnsRule"`            // Regla que matcheó (ej: ".svi")
	DNSError      string `json:"dnsError,omitempty"`
	UsedSystemDNS bool   `json:"usedSystemDNS"`      // True if resolved via system DNS (like apps do)

	// Detailed diagnostics (populated on DNS failure)
	DNSDiagnostics *DNSDiagnosticDetail `json:"dnsDiagnostics,omitempty"`

	// TCP Connectivity
	TCPConnected bool   `json:"tcpConnected"`
	TCPPort      int    `json:"tcpPort"`
	TCPLatencyMs int64  `json:"tcpLatencyMs"`
	TCPError     string `json:"tcpError,omitempty"`
}

// DNSDiagnosticStep represents one step in the DNS resolution chain with pass/fail status
type DNSDiagnosticStep struct {
	Name    string `json:"name"`              // Step name (e.g., "DNS Proxy Listening")
	Status  string `json:"status"`            // "ok", "fail", "warn", "skip"
	Detail  string `json:"detail"`            // What was found
	Fix     string `json:"fix,omitempty"`     // How to fix if failed
}

// DNSDiagnosticDetail contains exhaustive diagnostic info when DNS or TCP fails
type DNSDiagnosticDetail struct {
	// Chain steps - ordered diagnostic checks
	Steps []DNSDiagnosticStep `json:"steps"`

	// System state snapshot
	ActiveInterface     string   `json:"activeInterface"`
	CurrentSystemDNS    []string `json:"currentSystemDNS"`
	ExpectedDNSAddress  string   `json:"expectedDnsAddress"`
	DNSProxyEnabled     bool     `json:"dnsProxyEnabled"`
	DNSProxyListenPort  int      `json:"dnsProxyListenPort"`
	DNSClientRunning    bool     `json:"dnsClientRunning"`
	ServiceConnected    bool     `json:"serviceConnected"`
	SystemDNSConfigured bool     `json:"systemDnsConfigured"`

	// Rule matching
	HasMatchingRule     bool   `json:"hasMatchingRule"`
	MatchedRuleSuffix   string `json:"matchedRuleSuffix,omitempty"`
	MatchedRuleProfile  string `json:"matchedRuleProfile,omitempty"`
	MatchedRuleDNS      string `json:"matchedRuleDns,omitempty"`
	TunnelConnected     bool   `json:"tunnelConnected"`

	// TCP Proxy state
	TCPProxyEnabled       bool              `json:"tcpProxyEnabled"`
	TCPProxyTunnelIPs     map[string]string `json:"tcpProxyTunnelIPs"`     // all configured profileID -> loopback IP
	ProfileHasTunnelIP    bool              `json:"profileHasTunnelIP"`    // does THIS profile have a tunnelIP?
	ProfileTunnelIP       string            `json:"profileTunnelIP,omitempty"` // the assigned loopback IP for this profile
	TCPProxyListenerCount int               `json:"tcpProxyListenerCount"`
	ResolvedToLoopback    bool              `json:"resolvedToLoopback"`    // did DNS return a loopback IP?
	ResolvedAddress       string            `json:"resolvedAddress,omitempty"` // what the system DNS actually returned

	// Direct tunnel test (bypassing system DNS)
	DirectTunnelDNSResult string `json:"directTunnelDnsResult,omitempty"` // IP or error
	DirectTunnelDNSOk     bool   `json:"directTunnelDnsOk"`

	// DNS proxy direct test
	ProxyDirectResult string `json:"proxyDirectResult,omitempty"` // IP or error
	ProxyDirectOk     bool   `json:"proxyDirectOk"`

	// Summary
	RootCause string `json:"rootCause"` // Most likely cause in plain language
}

// HostMappingInfo contains information about a host mapping
type HostMappingInfo struct {
	Hostname    string    `json:"hostname"`
	RealIP      string    `json:"realIP"`
	LoopbackIP  string    `json:"loopbackIP"`
	ProfileID   string    `json:"profileId"`
	ProfileName string    `json:"profileName"`
	ResolvedAt  time.Time `json:"resolvedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// DNSRuleInfo contains information about a DNS rule
type DNSRuleInfo struct {
	Suffix      string            `json:"suffix"`
	ProfileID   string            `json:"profileId"`
	ProfileName string            `json:"profileName"`
	DNSServer   string            `json:"dnsServer"`
	StripSuffix bool              `json:"stripSuffix"`
	Hosts       map[string]string `json:"hosts,omitempty"`
}

// DNSDiagnostic contains diagnostic information for DNS resolution
type DNSDiagnostic struct {
	Hostname     string        `json:"hostname"`
	MatchedRule  *DNSRuleInfo  `json:"matchedRule"`  // Regla que matcheó (o null)
	AllRules     []DNSRuleInfo `json:"allRules"`     // Todas las reglas configuradas
	WouldResolve bool          `json:"wouldResolve"` // Si resolvería con la config actual
	Reason       string        `json:"reason"`       // Explicación legible
	SuggestedFix string        `json:"suggestedFix,omitempty"`
}

// VPNStatusInfo contains detailed status information for a VPN tunnel
type VPNStatusInfo struct {
	ProfileID     string    `json:"profileId"`
	ProfileName   string    `json:"profileName"`
	VPNType       string    `json:"vpnType"`
	Connected     bool      `json:"connected"`
	Connecting    bool      `json:"connecting"`
	Healthy       bool      `json:"healthy"`
	Endpoint      string    `json:"endpoint"`
	TunnelIP      string    `json:"tunnelIP"`
	BytesSent     uint64    `json:"bytesSent"`
	BytesRecv     uint64    `json:"bytesRecv"`
	LastHandshake time.Time `json:"lastHandshake"`
	AvgLatencyMs  float64   `json:"avgLatencyMs"`
	LastError     string    `json:"lastError,omitempty"`
}

// DiagnosticReport contains a full diagnostic report
type DiagnosticReport struct {
	GeneratedAt   time.Time                 `json:"generatedAt"`
	AppVersion    string                    `json:"appVersion"`
	SystemInfo    SystemInfo                `json:"systemInfo"`
	VPNStatus     []VPNStatusInfo           `json:"vpnStatus"`
	DNSConfig     DNSConfigInfo             `json:"dnsConfig"`
	TCPProxyInfo  TCPProxyInfo              `json:"tcpProxyInfo"`
	HostMappings  []HostMappingInfo         `json:"hostMappings"`
	RecentErrors  []ErrorEntry              `json:"recentErrors"`
	RecentLogs    []LogEntry                `json:"recentLogs"`
	Metrics       map[string]any            `json:"metrics"`
}

// SystemInfo contains system information
type SystemInfo struct {
	IsAdmin           bool   `json:"isAdmin"`
	ServiceConnected  bool   `json:"serviceConnected"`
	DNSConfigured     bool   `json:"dnsConfigured"`
	CurrentDNS        string `json:"currentDNS"`
	Platform          string `json:"platform"`
}

// DNSConfigInfo contains DNS proxy configuration information
type DNSConfigInfo struct {
	Enabled    bool          `json:"enabled"`
	ListenPort int           `json:"listenPort"`
	Rules      []DNSRuleInfo `json:"rules"`
	Fallback   string        `json:"fallback"`
}

// TCPProxyInfo contains TCP proxy configuration information
type TCPProxyInfo struct {
	Enabled       bool              `json:"enabled"`
	ListenerCount int               `json:"listenerCount"`
	TunnelIPs     map[string]string `json:"tunnelIPs"`
}

// APIResponse is a standard API response wrapper
type APIResponse struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// DNSRecord represents a single DNS record
type DNSRecord struct {
	Type  string `json:"type"`  // A, AAAA, CNAME, etc.
	Name  string `json:"name"`
	Value string `json:"value"`
	TTL   uint32 `json:"ttl"`
}

// DNSQueryResult contains the result of a DNS query through a VPN tunnel
type DNSQueryResult struct {
	// Query info
	Hostname   string `json:"hostname"`
	QueryType  string `json:"queryType"` // A, AAAA, ANY, etc.
	DNSServer  string `json:"dnsServer"`
	ProfileID  string `json:"profileId"`
	ProfileName string `json:"profileName"`

	// Result
	Success   bool        `json:"success"`
	Rcode     int         `json:"rcode"`     // DNS response code (0=success, 3=NXDOMAIN)
	RcodeName string      `json:"rcodeName"` // Human-readable rcode
	Records   []DNSRecord `json:"records"`
	Error     string      `json:"error,omitempty"`

	// Timing
	LatencyMs int64 `json:"latencyMs"`
}

// DNSConfigResult contains the result of DNS configuration/restore
type DNSConfigResult struct {
	Success       bool   `json:"success"`
	DNSAddress    string `json:"dnsAddress"`
	Port53Free    bool   `json:"port53Free"`
	DNSClientDown bool   `json:"dnsClientDown"`
	Error         string `json:"error,omitempty"`
}
