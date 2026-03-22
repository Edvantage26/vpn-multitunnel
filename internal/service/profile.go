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
func (s *ProfileService) GetAll() []config.Profile {
	return s.config.Profiles
}

// GetByID returns a profile by ID
func (s *ProfileService) GetByID(id string) (*config.Profile, error) {
	for i := range s.config.Profiles {
		if s.config.Profiles[i].ID == id {
			return &s.config.Profiles[i], nil
		}
	}
	return nil, fmt.Errorf("profile not found: %s", id)
}

// Create adds a new profile
func (s *ProfileService) Create(profile config.Profile) error {
	// Check for duplicate ID
	for _, p := range s.config.Profiles {
		if p.ID == profile.ID {
			return fmt.Errorf("profile with ID %s already exists", profile.ID)
		}
	}

	// Validate port uniqueness
	if err := s.validatePorts(profile, ""); err != nil {
		return err
	}

	s.config.Profiles = append(s.config.Profiles, profile)
	return config.Save(s.config)
}

// Update updates an existing profile
func (s *ProfileService) Update(profile config.Profile) error {
	for i, p := range s.config.Profiles {
		if p.ID == profile.ID {
			// Validate port uniqueness (excluding this profile)
			if err := s.validatePorts(profile, profile.ID); err != nil {
				return err
			}

			s.config.Profiles[i] = profile
			return config.Save(s.config)
		}
	}
	return fmt.Errorf("profile not found: %s", profile.ID)
}

// Delete removes a profile
func (s *ProfileService) Delete(id string) error {
	var newProfiles []config.Profile
	found := false

	for _, p := range s.config.Profiles {
		if p.ID != id {
			newProfiles = append(newProfiles, p)
		} else {
			found = true
			// Optionally delete the config file
			if p.ConfigFile != "" {
				configPath, err := config.GetConfigFilePath(p.ConfigFile)
				if err == nil {
					os.Remove(configPath)
				}
			}
		}
	}

	if !found {
		return fmt.Errorf("profile not found: %s", id)
	}

	s.config.Profiles = newProfiles
	return config.Save(s.config)
}

// Import imports a WireGuard configuration file
func (s *ProfileService) Import(filePath string) (*config.Profile, error) {
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
	tunnelIP := s.assignTunnelIP(profile.ID)
	if tunnelIP != "" {
		if s.config.TCPProxy.TunnelIPs == nil {
			s.config.TCPProxy.TunnelIPs = make(map[string]string)
		}
		s.config.TCPProxy.TunnelIPs[profile.ID] = tunnelIP
	}

	// Add to config
	if err := s.Create(*profile); err != nil {
		// Cleanup copied file on error
		os.Remove(destPath)
		return nil, err
	}

	return profile, nil
}

// assignTunnelIP assigns a unique tunnel IP for the transparent proxy
func (s *ProfileService) assignTunnelIP(profileID string) string {
	// Ensure TunnelIPs map exists
	if s.config.TCPProxy.TunnelIPs == nil {
		s.config.TCPProxy.TunnelIPs = make(map[string]string)
	}

	// Check if profile already has a tunnel IP
	if ip, exists := s.config.TCPProxy.TunnelIPs[profileID]; exists {
		return ip
	}

	// Find the next available IP in the 127.0.x.1 range
	used := make(map[int]bool)
	for _, ip := range s.config.TCPProxy.TunnelIPs {
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
	for i := 1; i < 255; i++ {
		if !used[i] {
			return fmt.Sprintf("127.0.%d.1", i)
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
func (s *ProfileService) GetTunnelIP(profileID string) string {
	if s.config.TCPProxy.TunnelIPs == nil {
		return ""
	}
	return s.config.TCPProxy.TunnelIPs[profileID]
}

// validatePorts checks that ports don't conflict with other profiles
func (s *ProfileService) validatePorts(profile config.Profile, excludeID string) error {
	// No per-profile port validation needed after SOCKS5 removal
	_ = profile
	_ = excludeID
	return nil
}
