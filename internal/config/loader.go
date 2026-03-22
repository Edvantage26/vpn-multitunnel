package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const configFileName = "config.json"

// getExeDir returns the directory where the executable is located
// In development mode (go.mod exists in cwd), returns cwd instead
func getExeDir() (string, error) {
	// Check if we're in development mode (go.mod exists in cwd)
	cwd, err := os.Getwd()
	if err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			return cwd, nil
		}
	}

	// Production: use executable directory
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(execPath), nil
}

// getConfigPath returns the path to the config file (always next to exe)
func getConfigPath() (string, error) {
	exeDir, err := getExeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(exeDir, configFileName), nil
}

// Load reads the configuration from disk
func Load() (*AppConfig, error) {
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

// GetConfigDir returns the directory for storing WireGuard configs (always <exe_dir>/configs)
func GetConfigDir() (string, error) {
	exeDir, err := getExeDir()
	if err != nil {
		return "", err
	}

	configDir := filepath.Join(exeDir, "configs")

	// Create directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}

	return configDir, nil
}
