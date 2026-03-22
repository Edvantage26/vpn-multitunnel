package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// WireGuardConfig represents a parsed WireGuard configuration file
type WireGuardConfig struct {
	Interface InterfaceConfig
	Peers     []PeerConfig
	RawConfig string // Original config content
}

// InterfaceConfig represents the [Interface] section
type InterfaceConfig struct {
	PrivateKey string
	Address    []string // Can have multiple addresses (IPv4 and IPv6)
	DNS        []string
	MTU        int
	ListenPort int
}

// PeerConfig represents a [Peer] section
type PeerConfig struct {
	PublicKey           string
	PresharedKey        string
	AllowedIPs          []string
	Endpoint            string
	PersistentKeepalive int
}

// ParseWireGuardConfig parses a WireGuard .conf file
func ParseWireGuardConfig(filePath string) (*WireGuardConfig, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	config := &WireGuardConfig{
		Peers: []PeerConfig{},
	}

	var rawLines []string
	var currentSection string
	var currentPeer *PeerConfig

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		rawLines = append(rawLines, scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for section headers
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.ToLower(strings.Trim(line, "[]"))
			currentSection = section

			if section == "peer" {
				if currentPeer != nil {
					config.Peers = append(config.Peers, *currentPeer)
				}
				currentPeer = &PeerConfig{}
			}
			continue
		}

		// Parse key-value pairs
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(strings.ToLower(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch currentSection {
		case "interface":
			parseInterfaceKey(&config.Interface, key, value)
		case "peer":
			if currentPeer != nil {
				parsePeerKey(currentPeer, key, value)
			}
		}
	}

	// Add last peer
	if currentPeer != nil {
		config.Peers = append(config.Peers, *currentPeer)
	}

	config.RawConfig = strings.Join(rawLines, "\n")

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Validate required fields
	if config.Interface.PrivateKey == "" {
		return nil, fmt.Errorf("missing PrivateKey in [Interface]")
	}
	if len(config.Peers) == 0 {
		return nil, fmt.Errorf("no [Peer] sections found")
	}
	for idx_peer, peer := range config.Peers {
		if peer.PublicKey == "" {
			return nil, fmt.Errorf("missing PublicKey in [Peer] %d", idx_peer+1)
		}
	}

	return config, nil
}

func parseInterfaceKey(iface *InterfaceConfig, key, value string) {
	switch key {
	case "privatekey":
		iface.PrivateKey = value
	case "address":
		addresses := splitAndTrim(value, ",")
		iface.Address = append(iface.Address, addresses...)
	case "dns":
		dns := splitAndTrim(value, ",")
		iface.DNS = append(iface.DNS, dns...)
	case "mtu":
		fmt.Sscanf(value, "%d", &iface.MTU)
	case "listenport":
		fmt.Sscanf(value, "%d", &iface.ListenPort)
	}
}

func parsePeerKey(peer *PeerConfig, key, value string) {
	switch key {
	case "publickey":
		peer.PublicKey = value
	case "presharedkey":
		peer.PresharedKey = value
	case "allowedips":
		ips := splitAndTrim(value, ",")
		peer.AllowedIPs = append(peer.AllowedIPs, ips...)
	case "endpoint":
		peer.Endpoint = value
	case "persistentkeepalive":
		fmt.Sscanf(value, "%d", &peer.PersistentKeepalive)
	}
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// ProfileFromWireGuardConfig creates a Profile from a WireGuardConfig
func ProfileFromWireGuardConfig(wgConfig *WireGuardConfig, configFilePath string) *Profile {
	// Generate ID from filename
	filename := filepath.Base(configFilePath)
	name := strings.TrimSuffix(filename, filepath.Ext(filename))

	// Create a URL-safe ID
	id := sanitizeID(name)

	// Get first peer endpoint for display
	var endpoint string
	if len(wgConfig.Peers) > 0 && wgConfig.Peers[0].Endpoint != "" {
		endpoint = wgConfig.Peers[0].Endpoint
	}

	// Get target IP for health check (first Address without CIDR)
	var targetIP string
	if len(wgConfig.Interface.Address) > 0 {
		targetIP = strings.Split(wgConfig.Interface.Address[0], "/")[0]
	}

	// Get DNS server if configured
	var dnsServer string
	if len(wgConfig.Interface.DNS) > 0 {
		dnsServer = wgConfig.Interface.DNS[0]
	}

	_ = endpoint // Could be used for display

	return &Profile{
		ID:         id,
		Name:       name,
		ConfigFile: filepath.Base(configFilePath),
		Enabled:    true,
		HealthCheck: HealthCheck{
			Enabled:         true,
			TargetIP:        targetIP,
			IntervalSeconds: 30,
		},
		DNS: ProfileDNS{
			Server:  dnsServer,
			Domains: []string{},
		},
	}
}

// sanitizeID creates a safe ID from a name
func sanitizeID(name string) string {
	// Convert to lowercase
	id := strings.ToLower(name)

	// Replace spaces and special chars with hyphens
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	id = reg.ReplaceAllString(id, "-")

	// Trim hyphens from ends
	id = strings.Trim(id, "-")

	// Ensure uniqueness with a short UUID suffix
	shortUUID := uuid.New().String()[:8]

	return fmt.Sprintf("%s-%s", id, shortUUID)
}

// CopyConfigFile copies a WireGuard config file to the configs directory
func CopyConfigFile(srcPath string) (string, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return "", err
	}

	filename := filepath.Base(srcPath)
	destPath := filepath.Join(configDir, filename)

	// Read source file
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("failed to read source file: %w", err)
	}

	// Write to destination
	if err := os.WriteFile(destPath, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write config file: %w", err)
	}

	return filename, nil
}

// GetConfigFilePath returns the full path to a config file
func GetConfigFilePath(filename string) (string, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, filename), nil
}
