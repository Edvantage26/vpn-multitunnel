package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"vpnmultitunnel/internal/config"
)

// ProfileService handles profile CRUD operations
type ProfileService struct {
	config *config.AppConfig
}

// NewProfileService creates a new profile service
func NewProfileService(cfg *config.AppConfig) *ProfileService {
	return &ProfileService{
		config: cfg,
	}
}

// GetAll returns all profiles
func (profile_service *ProfileService) GetAll() []config.Profile {
	return profile_service.config.Profiles
}

// GetByID returns a profile by ID
func (profile_service *ProfileService) GetByID(id string) (*config.Profile, error) {
	for idx_profile := range profile_service.config.Profiles {
		if profile_service.config.Profiles[idx_profile].ID == id {
			return &profile_service.config.Profiles[idx_profile], nil
		}
	}
	return nil, fmt.Errorf("profile not found: %s", id)
}

// Create adds a new profile
func (profile_service *ProfileService) Create(profile config.Profile) error {
	// Check for duplicate ID
	for _, existing_profile := range profile_service.config.Profiles {
		if existing_profile.ID == profile.ID {
			return fmt.Errorf("profile with ID %s already exists", profile.ID)
		}
	}

	// Validate port uniqueness
	if err := profile_service.validatePorts(profile, ""); err != nil {
		return err
	}

	profile_service.config.Profiles = append(profile_service.config.Profiles, profile)
	return config.Save(profile_service.config)
}

// Update updates an existing profile
func (profile_service *ProfileService) Update(profile config.Profile) error {
	for idx_profile, profile_entry := range profile_service.config.Profiles {
		if profile_entry.ID == profile.ID {
			// Validate port uniqueness (excluding this profile)
			if err := profile_service.validatePorts(profile, profile.ID); err != nil {
				return err
			}

			profile_service.config.Profiles[idx_profile] = profile
			return config.Save(profile_service.config)
		}
	}
	return fmt.Errorf("profile not found: %s", profile.ID)
}

// Delete removes a profile. If deleteConfigFile is true, the associated
// WireGuard config file on disk is also removed.
func (profile_service *ProfileService) Delete(id string, deleteConfigFile bool) error {
	var newProfiles []config.Profile
	found := false

	for _, existing_profile := range profile_service.config.Profiles {
		if existing_profile.ID != id {
			newProfiles = append(newProfiles, existing_profile)
		} else {
			found = true
			if deleteConfigFile && existing_profile.ConfigFile != "" {
				configPath, resolve_error := config.GetConfigFilePath(existing_profile.ConfigFile)
				if resolve_error == nil {
					os.Remove(configPath)
				}
			}
		}
	}

	if !found {
		return fmt.Errorf("profile not found: %s", id)
	}

	profile_service.config.Profiles = newProfiles
	return config.Save(profile_service.config)
}

// GetConfigFilePath returns the full path to the config file for a profile, or empty string if not found.
func (profile_service *ProfileService) GetConfigFilePath(id string) string {
	for _, existing_profile := range profile_service.config.Profiles {
		if existing_profile.ID == id && existing_profile.ConfigFile != "" {
			resolved_path, resolve_error := config.GetConfigFilePath(existing_profile.ConfigFile)
			if resolve_error == nil {
				return resolved_path
			}
		}
	}
	return ""
}

// Import imports a WireGuard configuration file
func (profile_service *ProfileService) Import(filePath string) (*config.Profile, error) {
	// Parse the config
	wgConfig, err := config.ParseWireGuardConfig(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Copy config file to config directory
	configDir, err := config.GetConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", err)
	}

	filename := filepath.Base(filePath)
	destPath := filepath.Join(configDir, filename)

	// Read source file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Write to destination
	if err := os.WriteFile(destPath, data, 0600); err != nil {
		return nil, fmt.Errorf("failed to copy config file: %w", err)
	}

	// Create profile
	profile := config.ProfileFromWireGuardConfig(wgConfig, filePath)

	// Assign tunnel IP for transparent proxy
	tunnelIP := profile_service.assignTunnelIP(profile.ID)
	if tunnelIP != "" {
		if profile_service.config.TCPProxy.TunnelIPs == nil {
			profile_service.config.TCPProxy.TunnelIPs = make(map[string]string)
		}
		profile_service.config.TCPProxy.TunnelIPs[profile.ID] = tunnelIP
	}

	// Add to config
	if err := profile_service.Create(*profile); err != nil {
		// Cleanup copied file on error
		os.Remove(destPath)
		return nil, err
	}

	return profile, nil
}

// ImportFromText creates a new profile from raw WireGuard config text and a name
func (profile_service *ProfileService) ImportFromText(config_name string, config_content string) (*config.Profile, error) {
	// Write content to a temp file for parsing
	temp_file, temp_error := os.CreateTemp("", "wg-import-*.conf")
	if temp_error != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", temp_error)
	}
	defer os.Remove(temp_file.Name())

	if _, write_error := temp_file.WriteString(config_content); write_error != nil {
		temp_file.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", write_error)
	}
	temp_file.Close()

	// Parse and validate the config
	wg_config, parse_error := config.ParseWireGuardConfig(temp_file.Name())
	if parse_error != nil {
		return nil, fmt.Errorf("invalid WireGuard config: %w", parse_error)
	}

	// Sanitize filename from the provided name
	safe_filename := strings.ToLower(strings.TrimSpace(config_name))
	safe_filename = strings.Map(func(char_value rune) rune {
		if (char_value >= 'a' && char_value <= 'z') || (char_value >= '0' && char_value <= '9') || char_value == '-' || char_value == '_' {
			return char_value
		}
		if char_value == ' ' {
			return '-'
		}
		return -1
	}, safe_filename)
	if safe_filename == "" {
		safe_filename = "tunnel"
	}
	conf_filename := safe_filename + ".conf"

	// Save config file to configs directory
	config_dir, dir_error := config.GetConfigDir()
	if dir_error != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", dir_error)
	}

	dest_path := filepath.Join(config_dir, conf_filename)

	// Avoid overwriting existing files by appending a number
	if _, stat_error := os.Stat(dest_path); stat_error == nil {
		for file_counter := 2; file_counter <= 100; file_counter++ {
			candidate_filename := safe_filename + "-" + strconv.Itoa(file_counter) + ".conf"
			candidate_path := filepath.Join(config_dir, candidate_filename)
			if _, stat_error := os.Stat(candidate_path); os.IsNotExist(stat_error) {
				conf_filename = candidate_filename
				dest_path = candidate_path
				break
			}
		}
	}

	if write_error := os.WriteFile(dest_path, []byte(config_content), 0600); write_error != nil {
		return nil, fmt.Errorf("failed to save config file: %w", write_error)
	}

	// Create profile using the saved config file path
	profile := config.ProfileFromWireGuardConfig(wg_config, dest_path)
	profile.Name = strings.TrimSpace(config_name)

	// Assign tunnel IP for transparent proxy
	tunnel_ip := profile_service.assignTunnelIP(profile.ID)
	if tunnel_ip != "" {
		if profile_service.config.TCPProxy.TunnelIPs == nil {
			profile_service.config.TCPProxy.TunnelIPs = make(map[string]string)
		}
		profile_service.config.TCPProxy.TunnelIPs[profile.ID] = tunnel_ip
	}

	// Add to config
	if create_error := profile_service.Create(*profile); create_error != nil {
		os.Remove(dest_path)
		return nil, create_error
	}

	return profile, nil
}

// assignTunnelIP assigns a unique tunnel IP for the transparent proxy
func (profile_service *ProfileService) assignTunnelIP(profileID string) string {
	// Ensure TunnelIPs map exists
	if profile_service.config.TCPProxy.TunnelIPs == nil {
		profile_service.config.TCPProxy.TunnelIPs = make(map[string]string)
	}

	// Check if profile already has a tunnel IP
	if ip, exists := profile_service.config.TCPProxy.TunnelIPs[profileID]; exists {
		return ip
	}

	// Find the next available IP in the 127.0.x.1 range
	used := make(map[int]bool)
	for _, ip := range profile_service.config.TCPProxy.TunnelIPs {
		// Extract the third octet from "127.0.X.1"
		parts := strings.Split(ip, ".")
		if len(parts) == 4 && parts[0] == "127" && parts[1] == "0" {
			octect, err := strconv.Atoi(parts[2])
			if err == nil {
				used[octect] = true
			}
		}
	}

	// Find next available (start at 1, reserve 0 for system)
	for ip_octet := 1; ip_octet < 255; ip_octet++ {
		if !used[ip_octet] {
			return fmt.Sprintf("127.0.%d.1", ip_octet)
		}
	}

	return ""
}

// Reorder rearranges profiles to match the given order of IDs.
// IDs not in the list are appended at the end.
func (profile_service *ProfileService) Reorder(orderedIDs []string) error {
	idToProfile := make(map[string]config.Profile, len(profile_service.config.Profiles))
	for _, profile := range profile_service.config.Profiles {
		idToProfile[profile.ID] = profile
	}

	reordered := make([]config.Profile, 0, len(profile_service.config.Profiles))
	seen := make(map[string]bool, len(orderedIDs))

	for _, profileID := range orderedIDs {
		if profile, exists := idToProfile[profileID]; exists && !seen[profileID] {
			reordered = append(reordered, profile)
			seen[profileID] = true
		}
	}

	// Append any profiles not in the ordered list (safety net)
	for _, profile := range profile_service.config.Profiles {
		if !seen[profile.ID] {
			reordered = append(reordered, profile)
		}
	}

	profile_service.config.Profiles = reordered
	return config.Save(profile_service.config)
}

// GetTunnelIP returns the tunnel IP for a profile
func (profile_service *ProfileService) GetTunnelIP(profileID string) string {
	if profile_service.config.TCPProxy.TunnelIPs == nil {
		return ""
	}
	return profile_service.config.TCPProxy.TunnelIPs[profileID]
}

// validatePorts checks that ports don't conflict with other profiles
func (profile_service *ProfileService) validatePorts(profile config.Profile, excludeID string) error {
	// No per-profile port validation needed after SOCKS5 removal
	_ = profile
	_ = excludeID
	return nil
}
