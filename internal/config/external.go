package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ExternalVPNConfig stores connection parameters for an external VPN profile.
// External profiles don't manage any VPN process — they detect an adapter
// created by an external VPN client and route traffic through it.
type ExternalVPNConfig struct {
	AdapterName       string `json:"adapterName"`       // Substring to match adapter name (e.g., "WatchGuard", "Cisco")
	AdapterAutoDetect bool   `json:"adapterAutoDetect"` // If true, detect any new adapter that appears
	DNSServer         string `json:"dnsServer"`         // DNS server to use (optional — auto-detected from adapter if empty)
	PollIntervalSec   int    `json:"pollIntervalSec"`   // Polling interval in seconds (default: 2)
}

// ParseExternalVPNConfig reads an external VPN config JSON file (.extjson)
func ParseExternalVPNConfig(filePath string) (*ExternalVPNConfig, error) {
	fileData, readErr := os.ReadFile(filePath)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read external VPN config: %w", readErr)
	}

	extConfig := &ExternalVPNConfig{
		PollIntervalSec: 2,
	}
	if jsonErr := json.Unmarshal(fileData, extConfig); jsonErr != nil {
		return nil, fmt.Errorf("failed to parse external VPN config JSON: %w", jsonErr)
	}

	if extConfig.AdapterName == "" && !extConfig.AdapterAutoDetect {
		return nil, fmt.Errorf("external VPN config must specify adapterName or set adapterAutoDetect to true")
	}
	if extConfig.PollIntervalSec <= 0 {
		extConfig.PollIntervalSec = 2
	}

	return extConfig, nil
}

// SaveExternalVPNConfig writes an external VPN config to a .extjson file in the configs directory
func SaveExternalVPNConfig(extConfig *ExternalVPNConfig, profileName string) (string, error) {
	configDir, dirErr := GetConfigDir()
	if dirErr != nil {
		return "", fmt.Errorf("failed to get config directory: %w", dirErr)
	}

	safeName := sanitizeConfigName(profileName)
	filename := safeName + ".extjson"
	destPath := filepath.Join(configDir, filename)

	jsonData, marshalErr := json.MarshalIndent(extConfig, "", "  ")
	if marshalErr != nil {
		return "", fmt.Errorf("failed to marshal external VPN config: %w", marshalErr)
	}

	if writeErr := os.WriteFile(destPath, jsonData, 0600); writeErr != nil {
		return "", fmt.Errorf("failed to write external VPN config: %w", writeErr)
	}

	return filename, nil
}

// ProfileFromExternalVPNConfig creates a Profile for an external VPN
func ProfileFromExternalVPNConfig(profileName string, adapterName string) *Profile {
	profileID := sanitizeID(profileName)

	return &Profile{
		ID:      profileID,
		Name:    profileName,
		Type:    VPNTypeExternal,
		Enabled: true,
		HealthCheck: HealthCheck{
			Enabled:         true,
			IntervalSeconds: 30,
		},
		DNS: ProfileDNS{
			Domains: []string{},
		},
	}
}
