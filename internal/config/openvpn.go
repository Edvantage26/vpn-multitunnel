package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenVPNConfig represents a minimally-parsed OpenVPN .ovpn configuration.
// We only extract metadata needed for display and proxy integration;
// openvpn.exe handles the full config parsing.
type OpenVPNConfig struct {
	RemoteHost    string   // Server hostname or IP (from "remote" directive)
	RemotePort    string   // Server port (from "remote" directive, default "1194")
	Protocol      string   // "udp" or "tcp" (from "proto" directive)
	DeviceType    string   // "tun" or "tap" (from "dev" directive)
	DNSServers    []string // DNS servers (from "dhcp-option DNS" push directives)
	AuthUserPass  bool     // Whether username/password auth is required
	RawConfig     string   // Original config content
}

// ParseOpenVPNConfig parses an OpenVPN .ovpn file, extracting only the metadata
// needed for the tunnel manager and proxy integration.
func ParseOpenVPNConfig(filePath string) (*OpenVPNConfig, error) {
	configFile, openErr := os.Open(filePath)
	if openErr != nil {
		return nil, fmt.Errorf("failed to open OpenVPN config: %w", openErr)
	}
	defer configFile.Close()

	ovpnConfig := &OpenVPNConfig{
		RemotePort: "1194",
		Protocol:   "udp",
		DeviceType: "tun",
	}

	var rawLines []string
	lineScanner := bufio.NewScanner(configFile)

	for lineScanner.Scan() {
		rawLine := lineScanner.Text()
		rawLines = append(rawLines, rawLine)

		trimmedLine := strings.TrimSpace(rawLine)

		// Skip empty lines and comments
		if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") || strings.HasPrefix(trimmedLine, ";") {
			continue
		}

		directiveParts := strings.Fields(trimmedLine)
		if len(directiveParts) == 0 {
			continue
		}

		directiveName := strings.ToLower(directiveParts[0])

		switch directiveName {
		case "remote":
			if len(directiveParts) >= 2 {
				ovpnConfig.RemoteHost = directiveParts[1]
			}
			if len(directiveParts) >= 3 {
				ovpnConfig.RemotePort = directiveParts[2]
			}
			if len(directiveParts) >= 4 {
				ovpnConfig.Protocol = strings.ToLower(directiveParts[3])
			}

		case "proto":
			if len(directiveParts) >= 2 {
				ovpnConfig.Protocol = strings.ToLower(directiveParts[1])
			}

		case "dev":
			if len(directiveParts) >= 2 {
				ovpnConfig.DeviceType = strings.ToLower(directiveParts[1])
			}

		case "auth-user-pass":
			ovpnConfig.AuthUserPass = true

		case "dhcp-option":
			// "dhcp-option DNS 10.0.0.1" (can appear in config or be pushed by server)
			if len(directiveParts) >= 3 && strings.ToUpper(directiveParts[1]) == "DNS" {
				ovpnConfig.DNSServers = append(ovpnConfig.DNSServers, directiveParts[2])
			}
		}
	}

	ovpnConfig.RawConfig = strings.Join(rawLines, "\n")

	if scanErr := lineScanner.Err(); scanErr != nil {
		return nil, fmt.Errorf("error reading OpenVPN config: %w", scanErr)
	}

	if ovpnConfig.RemoteHost == "" {
		return nil, fmt.Errorf("missing 'remote' directive in OpenVPN config")
	}

	return ovpnConfig, nil
}

// ProfileFromOpenVPNConfig creates a Profile from an OpenVPN config
func ProfileFromOpenVPNConfig(ovpnConfig *OpenVPNConfig, configFilePath string) *Profile {
	filename := filepath.Base(configFilePath)
	profileName := strings.TrimSuffix(filename, filepath.Ext(filename))
	profileID := sanitizeID(profileName)

	return &Profile{
		ID:         profileID,
		Name:       profileName,
		Type:       VPNTypeOpenVPN,
		ConfigFile: filepath.Base(configFilePath),
		Enabled:    true,
		HealthCheck: HealthCheck{
			Enabled:         true,
			IntervalSeconds: 30,
		},
		DNS: ProfileDNS{
			Domains: []string{},
		},
	}
}
