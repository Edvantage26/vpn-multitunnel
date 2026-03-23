package app

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/service"
	"vpnmultitunnel/internal/tunnel"
)

// ExportConfiguration bundles config.json and the configs/ directory into a zip file.
// Opens a save dialog for the user to choose the destination.
func (app *App) ExportConfiguration() error {
	zipFilePath, dialogErr := runtime.SaveFileDialog(app.ctx, runtime.SaveDialogOptions{
		Title:           "Export Configuration",
		DefaultFilename: "vpn-multitunnel-backup.zip",
		Filters: []runtime.FileFilter{
			{DisplayName: "Zip Archive", Pattern: "*.zip"},
		},
	})
	if dialogErr != nil {
		return fmt.Errorf("failed to open save dialog: %w", dialogErr)
	}
	if zipFilePath == "" {
		return nil // User cancelled
	}

	configJsonPath, configPathErr := config.GetConfigPath()
	if configPathErr != nil {
		return fmt.Errorf("failed to get config path: %w", configPathErr)
	}

	configDirPath, configDirErr := config.GetConfigDir()
	if configDirErr != nil {
		return fmt.Errorf("failed to get configs directory: %w", configDirErr)
	}

	zipFileHandle, createErr := os.Create(zipFilePath)
	if createErr != nil {
		return fmt.Errorf("failed to create zip file: %w", createErr)
	}
	defer zipFileHandle.Close()

	zipWriter := zip.NewWriter(zipFileHandle)
	defer zipWriter.Close()

	// Add config.json to the zip
	configJsonContent, readErr := os.ReadFile(configJsonPath)
	if readErr != nil {
		return fmt.Errorf("failed to read config.json: %w", readErr)
	}

	configEntryWriter, entryErr := zipWriter.Create("config.json")
	if entryErr != nil {
		return fmt.Errorf("failed to create zip entry for config.json: %w", entryErr)
	}
	if _, writeErr := configEntryWriter.Write(configJsonContent); writeErr != nil {
		return fmt.Errorf("failed to write config.json to zip: %w", writeErr)
	}

	// Add all files from configs/ directory
	configDirEntries, readDirErr := os.ReadDir(configDirPath)
	if readDirErr != nil && !os.IsNotExist(readDirErr) {
		return fmt.Errorf("failed to read configs directory: %w", readDirErr)
	}

	for _, directoryEntry := range configDirEntries {
		if directoryEntry.IsDir() {
			continue
		}

		confFilePath := filepath.Join(configDirPath, directoryEntry.Name())
		confFileContent, confReadErr := os.ReadFile(confFilePath)
		if confReadErr != nil {
			return fmt.Errorf("failed to read config file %s: %w", directoryEntry.Name(), confReadErr)
		}

		confEntryWriter, confEntryErr := zipWriter.Create("configs/" + directoryEntry.Name())
		if confEntryErr != nil {
			return fmt.Errorf("failed to create zip entry for %s: %w", directoryEntry.Name(), confEntryErr)
		}
		if _, confWriteErr := confEntryWriter.Write(confFileContent); confWriteErr != nil {
			return fmt.Errorf("failed to write %s to zip: %w", directoryEntry.Name(), confWriteErr)
		}
	}

	return nil
}

// ImportConfiguration imports a zip file containing config.json and configs/ directory.
// Validates the zip, disconnects all tunnels, replaces config, and reloads.
func (app *App) ImportConfiguration() error {
	zipFilePath, dialogErr := runtime.OpenFileDialog(app.ctx, runtime.OpenDialogOptions{
		Title: "Import Configuration",
		Filters: []runtime.FileFilter{
			{DisplayName: "Zip Archive", Pattern: "*.zip"},
		},
	})
	if dialogErr != nil {
		return fmt.Errorf("failed to open file dialog: %w", dialogErr)
	}
	if zipFilePath == "" {
		return nil // User cancelled
	}

	// Validate the zip before any destructive action
	validatedContents, validationErr := validateConfigZip(zipFilePath)
	if validationErr != nil {
		return validationErr
	}

	// Disconnect all tunnels (handles DNS restore and tray update)
	app.DisconnectAll()

	// Get paths for replacement
	configJsonPath, configPathErr := config.GetConfigPath()
	if configPathErr != nil {
		return fmt.Errorf("failed to get config path: %w", configPathErr)
	}

	configDirPath, configDirErr := config.GetConfigDir()
	if configDirErr != nil {
		return fmt.Errorf("failed to get configs directory: %w", configDirErr)
	}

	// Backup current config.json
	existingConfigContent, backupReadErr := os.ReadFile(configJsonPath)
	if backupReadErr == nil {
		backupPath := configJsonPath + ".bak"
		os.WriteFile(backupPath, existingConfigContent, 0644)
	}

	// Clear configs/ directory
	configDirEntries, readDirErr := os.ReadDir(configDirPath)
	if readDirErr == nil {
		for _, existingEntry := range configDirEntries {
			if !existingEntry.IsDir() {
				os.Remove(filepath.Join(configDirPath, existingEntry.Name()))
			}
		}
	}

	// Extract configs/ files from zip
	for configFileName, configFileContent := range validatedContents.configFiles {
		targetPath := filepath.Join(configDirPath, configFileName)
		if writeErr := os.WriteFile(targetPath, configFileContent, 0644); writeErr != nil {
			return fmt.Errorf("failed to write config file %s: %w", configFileName, writeErr)
		}
	}

	// Write config.json
	if writeErr := os.WriteFile(configJsonPath, validatedContents.configJson, 0644); writeErr != nil {
		return fmt.Errorf("failed to write config.json: %w", writeErr)
	}

	// Reload in-memory config
	reloadedConfig, loadErr := config.Load()
	if loadErr != nil {
		return fmt.Errorf("failed to reload config after import: %w", loadErr)
	}

	app.mu.Lock()
	app.config = reloadedConfig
	app.profileService = service.NewProfileService(reloadedConfig)
	app.tunnelManager = tunnel.NewManager(reloadedConfig)
	app.mu.Unlock()

	// Notify frontend to refresh
	runtime.EventsEmit(app.ctx, "config-imported")

	// Update tray
	app.updateTrayStatus()

	return nil
}

// validatedZipContents holds the pre-read contents of a validated config zip
type validatedZipContents struct {
	configJson  []byte
	configFiles map[string][]byte // filename -> content (files from configs/ directory)
}

// validateConfigZip opens and validates a zip file, returning its contents if valid.
// Checks: contains config.json, all paths are safe, config.json is valid JSON.
func validateConfigZip(zipFilePath string) (*validatedZipContents, error) {
	zipReader, openErr := zip.OpenReader(zipFilePath)
	if openErr != nil {
		return nil, fmt.Errorf("failed to open zip file: %w", openErr)
	}
	defer zipReader.Close()

	contents := &validatedZipContents{
		configFiles: make(map[string][]byte),
	}

	foundConfigJson := false

	for _, zipEntry := range zipReader.File {
		entryName := filepath.ToSlash(zipEntry.Name)

		// Safety: reject paths with traversal or absolute paths
		if strings.Contains(entryName, "..") || filepath.IsAbs(entryName) {
			return nil, fmt.Errorf("invalid path in zip: %s", entryName)
		}

		// Skip directories
		if zipEntry.FileInfo().IsDir() {
			continue
		}

		if entryName == "config.json" {
			entryReader, entryOpenErr := zipEntry.Open()
			if entryOpenErr != nil {
				return nil, fmt.Errorf("failed to read config.json from zip: %w", entryOpenErr)
			}
			configJsonContent, entryReadErr := io.ReadAll(entryReader)
			entryReader.Close()
			if entryReadErr != nil {
				return nil, fmt.Errorf("failed to read config.json content: %w", entryReadErr)
			}

			// Validate JSON structure
			var parsedConfig config.AppConfig
			if unmarshalErr := json.Unmarshal(configJsonContent, &parsedConfig); unmarshalErr != nil {
				return nil, fmt.Errorf("config.json in zip is not valid: %w", unmarshalErr)
			}

			contents.configJson = configJsonContent
			foundConfigJson = true

		} else if strings.HasPrefix(entryName, "configs/") {
			configFileName := strings.TrimPrefix(entryName, "configs/")
			// Reject nested directories inside configs/
			if strings.Contains(configFileName, "/") {
				continue
			}
			if configFileName == "" {
				continue
			}

			entryReader, entryOpenErr := zipEntry.Open()
			if entryOpenErr != nil {
				return nil, fmt.Errorf("failed to read %s from zip: %w", entryName, entryOpenErr)
			}
			fileContent, entryReadErr := io.ReadAll(entryReader)
			entryReader.Close()
			if entryReadErr != nil {
				return nil, fmt.Errorf("failed to read %s content: %w", entryName, entryReadErr)
			}

			contents.configFiles[configFileName] = fileContent
		}
		// Ignore any other files silently
	}

	if !foundConfigJson {
		return nil, fmt.Errorf("zip file does not contain config.json")
	}

	return contents, nil
}
