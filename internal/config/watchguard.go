package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WatchGuardConfig represents the connection parameters for a WatchGuard Mobile VPN with SSL tunnel.
// Unlike WireGuard and OpenVPN, WatchGuard SSL VPN doesn't use a standard config format —
// it connects to a Firebox and downloads its configuration at connect time.
// We store connection parameters in a .wgjson file.
type WatchGuardConfig struct {
	ServerAddress string `json:"serverAddress"` // Firebox hostname or IP
	ServerPort    string `json:"serverPort"`    // Port (default: "443")
	Username      string `json:"username"`      // Login username
	// Password is not stored here — it should be prompted or stored securely
}

// ParseWatchGuardConfig reads a WatchGuard config JSON file (.wgjson)
func ParseWatchGuardConfig(filePath string) (*WatchGuardConfig, error) {
	fileData, readErr := os.ReadFile(filePath)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read WatchGuard config: %w", readErr)
	}

	wgConfig := &WatchGuardConfig{
		ServerPort: "443",
	}
	if jsonErr := json.Unmarshal(fileData, wgConfig); jsonErr != nil {
		return nil, fmt.Errorf("failed to parse WatchGuard config JSON: %w", jsonErr)
	}

	if wgConfig.ServerAddress == "" {
		return nil, fmt.Errorf("missing serverAddress in WatchGuard config")
	}
	if wgConfig.ServerPort == "" {
		wgConfig.ServerPort = "443"
	}

	return wgConfig, nil
}

// SaveWatchGuardConfig writes a WatchGuard config to a .wgjson file in the configs directory
func SaveWatchGuardConfig(wgConfig *WatchGuardConfig, profileName string) (string, error) {
	configDir, dirErr := GetConfigDir()
	if dirErr != nil {
		return "", fmt.Errorf("failed to get config directory: %w", dirErr)
	}

	safeName := sanitizeConfigName(profileName)
	filename := safeName + ".wgjson"
	destPath := filepath.Join(configDir, filename)

	jsonData, marshalErr := json.MarshalIndent(wgConfig, "", "  ")
	if marshalErr != nil {
		return "", fmt.Errorf("failed to marshal WatchGuard config: %w", marshalErr)
	}

	if writeErr := os.WriteFile(destPath, jsonData, 0600); writeErr != nil {
		return "", fmt.Errorf("failed to write WatchGuard config: %w", writeErr)
	}

	return filename, nil
}

// sanitizeConfigName creates a safe filename from a profile name
func sanitizeConfigName(profileName string) string {
	safeName := ""
	for _, charValue := range profileName {
		if (charValue >= 'a' && charValue <= 'z') || (charValue >= 'A' && charValue <= 'Z') ||
			(charValue >= '0' && charValue <= '9') || charValue == '-' || charValue == '_' {
			safeName += string(charValue)
		} else if charValue == ' ' {
			safeName += "-"
		}
	}
	if safeName == "" {
		safeName = "watchguard"
	}
	return safeName
}

// ProfileFromWatchGuardConfig creates a Profile from WatchGuard connection parameters
func ProfileFromWatchGuardConfig(serverAddress string, profileName string) *Profile {
	profileID := sanitizeID(profileName)

	return &Profile{
		ID:      profileID,
		Name:    profileName,
		Type:    VPNTypeWatchGuard,
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
