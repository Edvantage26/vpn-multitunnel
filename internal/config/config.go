package config

// AppConfig is the root configuration structure
type AppConfig struct {
	Version  int       `json:"version"`
	Settings Settings  `json:"settings"`
	Profiles []Profile `json:"profiles"`
	DNSProxy DNSProxy  `json:"dnsProxy"`
	TCPProxy TCPProxy  `json:"tcpProxy"`
}

// Settings contains global application settings
type Settings struct {
	LogLevel       string   `json:"logLevel"`
	AutoConnect    []string `json:"autoConnect"`
	PortRangeStart int      `json:"portRangeStart"` // Deprecated: was used for SOCKS5 auto-assignment
	StartMinimized bool     `json:"startMinimized"`
	// Transparent proxy automation settings
	AutoConfigureLoopback bool   `json:"autoConfigureLoopback"` // Auto-add loopback IPs on startup
	AutoConfigureDNS      bool   `json:"autoConfigureDNS"`      // Auto-configure system DNS when VPNs connect
	UsePort53             bool   `json:"usePort53"`             // Use port 53 instead of 10053 for DNS proxy
	DNSListenAddress      string `json:"dnsListenAddress"`      // Loopback IP for DNS proxy (default: 127.0.0.53)
	DNSFallbackServer     string `json:"dnsFallbackServer"`     // Fallback DNS server (default: 8.8.8.8)
	// Service settings
	UseService bool `json:"useService"` // Use Windows service for privileged ops (no UAC)
	// Debug settings
	DebugAPIEnabled bool `json:"debugApiEnabled"` // Enable debug HTTP API
	DebugAPIPort    int  `json:"debugApiPort"`    // Port for debug API (default: 8765)
	LogBufferSize   int  `json:"logBufferSize"`   // Size of log ring buffer (default: 10000)
	ErrorBufferSize int  `json:"errorBufferSize"` // Size of error ring buffer (default: 1000)
	MetricsEnabled  bool `json:"metricsEnabled"`  // Enable metrics collection
}

// Profile represents a WireGuard VPN profile
type Profile struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	ConfigFile  string        `json:"configFile"`
	Enabled     bool          `json:"enabled"`
	AutoConnect *bool         `json:"autoConnect,omitempty"` // nil = true (default)
	HealthCheck HealthCheck   `json:"healthCheck"`
	DNS           ProfileDNS    `json:"dns,omitempty"`
	TCPProxyPorts []int         `json:"tcpProxyPorts,omitempty"` // TCP proxy ports for this profile (empty = no ports proxied)
}

// ShouldAutoConnect returns whether this profile should auto-connect on startup (default: true)
func (profile *Profile) ShouldAutoConnect() bool {
	if profile.AutoConnect == nil {
		return true
	}
	return *profile.AutoConnect
}

// GetTCPProxyPorts returns this profile's TCP proxy ports.
func (profile *Profile) GetTCPProxyPorts() []int {
	return profile.TCPProxyPorts
}

// HealthCheck configuration for a profile
type HealthCheck struct {
	Enabled         bool   `json:"enabled"`
	TargetIP        string `json:"targetIP"`
	IntervalSeconds int    `json:"intervalSeconds"`
}

// ProfileDNS configures DNS for a specific profile
type ProfileDNS struct {
	Server      string            `json:"server"`
	Domains     []string          `json:"domains"`     // Domains that should use this profile's DNS
	StripSuffix bool              `json:"stripSuffix"` // Strip suffix before querying DNS (e.g., db.svi -> db)
	Hosts       map[string]string `json:"hosts"`       // Static host mappings (e.g., "db" -> "10.10.0.3")
}

// DNSProxy configures the global DNS proxy
type DNSProxy struct {
	Enabled       bool      `json:"enabled"`
	ListenAddress string    `json:"listenAddress"` // IP address to listen on (default: 127.0.0.53)
	ListenPort    int       `json:"listenPort"`
	Rules         []DNSRule `json:"rules"`
	Fallback      string    `json:"fallback"` // "system" or specific DNS server
}

// GetListenAddress returns the listen address, defaulting to 127.0.0.53 to avoid conflicts with Windows DNS Client
func (dns_proxy *DNSProxy) GetListenAddress() string {
	if dns_proxy.ListenAddress == "" {
		return "127.0.0.53"
	}
	return dns_proxy.ListenAddress
}

// DNSRule maps domain suffixes to profiles
type DNSRule struct {
	Suffix      string            `json:"suffix"`
	ProfileID   string            `json:"profileId"`
	DNSServer   string            `json:"dnsServer"`   // DNS server IP for this rule (e.g., "172.23.0.53")
	StripSuffix *bool             `json:"stripSuffix"` // Strip suffix before querying (default: true)
	Hosts       map[string]string `json:"hosts"`       // Static host mappings (e.g., "db" -> "10.10.0.3")
}

// ShouldStripSuffix returns whether to strip the suffix (default true)
func (dns_rule *DNSRule) ShouldStripSuffix() bool {
	if dns_rule.StripSuffix == nil {
		return true // Default: strip suffix
	}
	return *dns_rule.StripSuffix
}

// TCPProxy configures the transparent TCP proxy
type TCPProxy struct {
	Enabled   bool              `json:"enabled"`
	TunnelIPs map[string]string `json:"tunnelIPs"` // profileID -> "127.0.x.1"
}

// Default returns a default configuration
func Default() *AppConfig {
	return &AppConfig{
		Version: 1,
		Settings: Settings{
			LogLevel:              "info",
			AutoConnect:           []string{},
			PortRangeStart:        10800,
			StartMinimized:        true,
			AutoConfigureLoopback: true,         // Enable by default
			AutoConfigureDNS:      true,         // Enable by default
			UsePort53:             true,         // Use port 53 for transparent proxy
			DNSListenAddress:      "127.0.0.53", // Default DNS proxy loopback IP
			DNSFallbackServer:     "8.8.8.8",    // Default fallback DNS
			UseService:            true,         // Use service for privileged ops
			// Debug settings defaults
			DebugAPIEnabled: true,  // Enable debug API by default
			DebugAPIPort:    8765,  // Default debug API port
			LogBufferSize:   10000, // Default log buffer size
			ErrorBufferSize: 1000,  // Default error buffer size
			MetricsEnabled:  true,  // Enable metrics by default
		},
		Profiles: []Profile{},
		DNSProxy: DNSProxy{
			Enabled:    false,
			ListenPort: 10053,
			Rules:      []DNSRule{},
			Fallback:   "system",
		},
		TCPProxy: TCPProxy{
			Enabled:   true,
			TunnelIPs: make(map[string]string),
		},
	}
}
