package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const configFileName = "config.json"

// getExeDir returns the directory where the executable is located.
func getExeDir() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(execPath), nil
}

// isDevMode returns true if go.mod exists next to the executable,
// indicating the app is running via "wails dev" in the project directory.
func isDevMode() bool {
	execPath, err := os.Executable()
	if err != nil {
		return false
	}
	_, statErr := os.Stat(filepath.Join(filepath.Dir(execPath), "go.mod"))
	return statErr == nil
}

// getDataDir returns the base directory for all user data (config.json, configs/).
// In dev mode: returns the exe directory (so wails dev works as before).
// In production: returns %LOCALAPPDATA%\VPNMultiTunnel.
func getDataDir() (string, error) {
	if isDevMode() {
		return getExeDir()
	}

	localAppDataPath := os.Getenv("LOCALAPPDATA")
	if localAppDataPath == "" {
		// Fallback: os.UserConfigDir() returns %LOCALAPPDATA% on Windows
		var fallbackErr error
		localAppDataPath, fallbackErr = os.UserConfigDir()
		if fallbackErr != nil {
			return "", fmt.Errorf("cannot determine LOCALAPPDATA: %w", fallbackErr)
		}
	}

	dataDirectoryPath := filepath.Join(localAppDataPath, "VPNMultiTunnel")
	if mkdirErr := os.MkdirAll(dataDirectoryPath, 0755); mkdirErr != nil {
		return "", fmt.Errorf("cannot create data directory %s: %w", dataDirectoryPath, mkdirErr)
	}

	return dataDirectoryPath, nil
}

// GetDataDir returns the base directory for all user data (public wrapper).
func GetDataDir() (string, error) {
	return getDataDir()
}

// getConfigPath returns the path to config.json (in the data directory)
func getConfigPath() (string, error) {
	dataDirectoryPath, err := getDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDirectoryPath, configFileName), nil
}

// Load reads the configuration from disk
func Load() (*AppConfig, error) {
	// In production, migrate config from exe directory to LOCALAPPDATA if needed
	if !isDevMode() {
		migrateFromExeDir()
	}

	configPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create default config
			cfg := Default()
			if err := Save(cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, err
	}

	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Migration: ensure version is set
	if cfg.Version == 0 {
		cfg.Version = 1
	}

	// Ensure defaults for missing fields
	if cfg.Settings.PortRangeStart == 0 {
		cfg.Settings.PortRangeStart = 10800
	}
	if cfg.DNSProxy.ListenPort == 0 {
		cfg.DNSProxy.ListenPort = 10053
	}
	if cfg.Settings.DNSListenAddress == "" {
		cfg.Settings.DNSListenAddress = "127.0.0.53"
	}
	if cfg.Settings.DNSFallbackServer == "" {
		cfg.Settings.DNSFallbackServer = "8.8.8.8"
	}

	// Migration: ensure all profiles have a VPN type (default to WireGuard)
	profileTypeMigrationNeeded := false
	for idx_profile := range cfg.Profiles {
		if cfg.Profiles[idx_profile].Type == "" {
			cfg.Profiles[idx_profile].Type = VPNTypeWireGuard
			profileTypeMigrationNeeded = true
		}
	}
	if profileTypeMigrationNeeded {
		Save(&cfg)
	}

	// Migration: convert Settings.AutoConnect (list of IDs) to per-profile AutoConnect flags
	if len(cfg.Settings.AutoConnect) > 0 {
		autoConnectSet := make(map[string]bool)
		for _, id := range cfg.Settings.AutoConnect {
			autoConnectSet[id] = true
		}
		trueVal := true
		falseVal := false
		for idx_profile := range cfg.Profiles {
			if _, shouldAuto := autoConnectSet[cfg.Profiles[idx_profile].ID]; shouldAuto {
				cfg.Profiles[idx_profile].AutoConnect = &trueVal
			} else {
				cfg.Profiles[idx_profile].AutoConnect = &falseVal
			}
		}
		// Clear the old list
		cfg.Settings.AutoConnect = []string{}
		// Save migrated config
		Save(&cfg)
	}

	return &cfg, nil
}

// Save writes the configuration to disk
func Save(cfg *AppConfig) error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// GetConfigPath returns the full path to config.json
func GetConfigPath() (string, error) {
	return getConfigPath()
}

// GetConfigDir returns the directory for storing WireGuard configs (<data_dir>/configs)
func GetConfigDir() (string, error) {
	dataDirectoryPath, err := getDataDir()
	if err != nil {
		return "", err
	}

	configsDirectoryPath := filepath.Join(dataDirectoryPath, "configs")

	// Create directory if it doesn't exist
	if err := os.MkdirAll(configsDirectoryPath, 0755); err != nil {
		return "", err
	}

	return configsDirectoryPath, nil
}

// migrateFromExeDir copies config.json and configs/ from the exe directory
// to the new LOCALAPPDATA location. Only runs once (skips if data already exists).
func migrateFromExeDir() {
	dataDirectoryPath, dataDirErr := getDataDir()
	if dataDirErr != nil {
		log.Printf("Migration: cannot determine data directory: %v", dataDirErr)
		return
	}

	exeDirectoryPath, exeDirErr := getExeDir()
	if exeDirErr != nil {
		log.Printf("Migration: cannot determine exe directory: %v", exeDirErr)
		return
	}

	// Guard: if data dir is the same as exe dir, nothing to migrate
	if dataDirectoryPath == exeDirectoryPath {
		return
	}

	newConfigFilePath := filepath.Join(dataDirectoryPath, configFileName)
	// Only migrate if the new config does NOT exist yet
	if _, statErr := os.Stat(newConfigFilePath); statErr == nil {
		return // Already migrated
	}

	oldConfigFilePath := filepath.Join(exeDirectoryPath, configFileName)
	if _, statErr := os.Stat(oldConfigFilePath); statErr != nil {
		return // No old config to migrate
	}

	log.Printf("Migration: copying config from %s to %s", exeDirectoryPath, dataDirectoryPath)

	// Copy config.json
	if copyErr := copyFile(oldConfigFilePath, newConfigFilePath); copyErr != nil {
		log.Printf("Migration: failed to copy config.json: %v", copyErr)
		return
	}

	// Copy configs/ directory
	oldConfigsDirectoryPath := filepath.Join(exeDirectoryPath, "configs")
	newConfigsDirectoryPath := filepath.Join(dataDirectoryPath, "configs")
	if copyErr := copyDirectory(oldConfigsDirectoryPath, newConfigsDirectoryPath); copyErr != nil {
		log.Printf("Migration: failed to copy configs directory: %v", copyErr)
	}

	log.Printf("Migration: successfully migrated config to %s", dataDirectoryPath)
}

// copyFile copies a single file from source to destination.
func copyFile(sourceFilePath string, destinationFilePath string) error {
	fileContent, readErr := os.ReadFile(sourceFilePath)
	if readErr != nil {
		return fmt.Errorf("failed to read %s: %w", sourceFilePath, readErr)
	}
	return os.WriteFile(destinationFilePath, fileContent, 0644)
}

// copyDirectory copies all files from a source directory to a destination directory.
func copyDirectory(sourceDirectoryPath string, destinationDirectoryPath string) error {
	dirEntries, readErr := os.ReadDir(sourceDirectoryPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return nil // Source directory doesn't exist, nothing to copy
		}
		return fmt.Errorf("failed to read directory %s: %w", sourceDirectoryPath, readErr)
	}

	if mkdirErr := os.MkdirAll(destinationDirectoryPath, 0755); mkdirErr != nil {
		return fmt.Errorf("failed to create directory %s: %w", destinationDirectoryPath, mkdirErr)
	}

	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() {
			continue // Only copy files, not subdirectories
		}
		sourceEntryPath := filepath.Join(sourceDirectoryPath, dirEntry.Name())
		destinationEntryPath := filepath.Join(destinationDirectoryPath, dirEntry.Name())
		if copyErr := copyFile(sourceEntryPath, destinationEntryPath); copyErr != nil {
			log.Printf("Migration: failed to copy %s: %v", dirEntry.Name(), copyErr)
		}
	}

	return nil
}
