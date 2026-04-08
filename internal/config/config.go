package config

import "strings"

// VPNType identifies the VPN protocol for a profile
type VPNType string

const (
	VPNTypeWireGuard  VPNType = "wireguard"
	VPNTypeOpenVPN    VPNType = "openvpn"
	VPNTypeWatchGuard VPNType = "watchguard"
	VPNTypeExternal   VPNType = "external"
)

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
	AdvancedMode    bool `json:"advancedMode"`    // Show global Traffic and Logs views in nav bar
}

// Profile represents a VPN profile (WireGuard, OpenVPN, or WatchGuard SSL)
type Profile struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Type          VPNType       `json:"type"`                    // "wireguard", "openvpn", "watchguard" (default: "wireguard")
	ConfigFile    string        `json:"configFile"`
	Enabled       bool          `json:"enabled"`
	AutoConnect   *bool         `json:"autoConnect,omitempty"`   // nil = true (default)
	HealthCheck   HealthCheck   `json:"healthCheck"`
	DNS           ProfileDNS    `json:"dns,omitempty"`
	TCPProxyPorts []int         `json:"tcpProxyPorts,omitempty"` // TCP proxy ports for this profile (empty = no ports proxied)
	// Credentials for VPN types that require authentication (OpenVPN auth-user-pass, WatchGuard)
	Credentials   *VPNCredentialConfig `json:"credentials,omitempty"`
}

// VPNCredentialConfig stores saved VPN credentials
type VPNCredentialConfig struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// GetVPNType returns the profile's VPN type, defaulting to WireGuard for backward compatibility
func (profile *Profile) GetVPNType() VPNType {
	if profile.Type == "" {
		return VPNTypeWireGuard
	}
	return profile.Type
}

// ShouldAutoConnect returns whether this profile should auto-connect on startup (default: true)
func (profile *Profile) ShouldAutoConnect() bool {
	if profile.AutoConnect == nil {
		return true
	}
	return *profile.AutoConnect
}

// GetTCPProxyPorts returns this profile's TCP proxy ports (absolute values).
// Negative values in config indicate "custom" ports (UI-only distinction); the backend always uses absolute values.
func (profile *Profile) GetTCPProxyPorts() []int {
	absolute_ports := make([]int, len(profile.TCPProxyPorts))
	for port_index, port_value := range profile.TCPProxyPorts {
		if port_value < 0 {
			absolute_ports[port_index] = -port_value
		} else {
			absolute_ports[port_index] = port_value
		}
	}
	return absolute_ports
}

// HealthCheck configuration for a profile
type HealthCheck struct {
	Enabled         bool   `json:"enabled"`
	TargetIP        string `json:"targetIP,omitempty"` // Read-only: resolved from WireGuard .conf Address at runtime, not persisted
	IntervalSeconds int    `json:"intervalSeconds"`
}

// ProfileDNS configures DNS for a specific profile
type ProfileDNS struct {
	Server      string            `json:"server,omitempty"` // Read-only: resolved from WireGuard .conf at runtime, not persisted
	Domains     []string          `json:"domains"`          // Domains that should use this profile's DNS
	StripSuffix bool              `json:"stripSuffix"`      // Strip suffix before querying DNS (e.g., db.svi -> db)
	Hosts       map[string]string `json:"hosts"`            // Static host mappings (e.g., "db" -> "10.10.0.3")
}

// DNSProxy configures the global DNS proxy
type DNSProxy struct {
	Enabled       bool      `json:"enabled"`
	ListenAddress string    `json:"listenAddress"` // IP address to listen on (default: 127.0.0.53)
	ListenPort    int       `json:"listenPort"`
	Rules         []DNSRule `json:"-"` // Generated at runtime from profiles, not persisted
	Fallback      string    `json:"fallback"` // "system" or specific DNS server
}

// GetListenAddress returns the listen address, defaulting to 127.0.0.53 to avoid conflicts with Windows DNS Client
func (dns_proxy *DNSProxy) GetListenAddress() string {
	if dns_proxy.ListenAddress == "" {
		return "127.0.0.53"
	}
	return dns_proxy.ListenAddress
}

// DNSRule maps domain suffixes to profiles (generated at runtime from profiles)
type DNSRule struct {
	Suffix      string            `json:"suffix"`
	ProfileID   string            `json:"profileId"`
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

// BuildDNSRulesFromProfiles generates DNS rules from profile DNS settings.
// This replaces the old persisted rules — rules are now derived at runtime.
func BuildDNSRulesFromProfiles(profiles []Profile) []DNSRule {
	var rules []DNSRule
	for _, profile := range profiles {
		if len(profile.DNS.Domains) == 0 && len(profile.DNS.Hosts) == 0 {
			continue
		}
		for _, domain := range profile.DNS.Domains {
			suffix := domain
			if !strings.HasPrefix(suffix, ".") {
				suffix = "." + suffix
			}
			stripSuffix := profile.DNS.StripSuffix
			rules = append(rules, DNSRule{
				Suffix:      suffix,
				ProfileID:   profile.ID,
				StripSuffix: &stripSuffix,
				Hosts:       profile.DNS.Hosts,
			})
		}
	}
	return rules
}

// TCPProxy configures the transparent TCP proxy
type TCPProxy struct {
	Enabled   bool              `json:"enabled"`
	TunnelIPs map[string]string `json:"tunnelIPs"` // profileID -> "127.0.x.1"
}

// IsEnabled returns true if the TCP proxy should be active.
// The TCP proxy is active when there are tunnel IPs assigned, regardless of the Enabled flag.
func (tcp_proxy *TCPProxy) IsEnabled() bool {
	return len(tcp_proxy.TunnelIPs) > 0
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
			Fallback:   "system",
		},
		TCPProxy: TCPProxy{
			Enabled:   true,
			TunnelIPs: make(map[string]string),
		},
	}
}
