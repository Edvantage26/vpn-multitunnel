package app

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/tunnel"
)

// OpenVPN MSI download URL (latest stable community release)
const openVPNInstallerURL = "https://swupdate.openvpn.org/community/releases/OpenVPN-2.7.0-I017-amd64.msi"
const openVPNMinRecommendedVersion = "2.6"

// OpenVPNStatus contains information about the OpenVPN installation
type OpenVPNStatus struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"`   // e.g., "2.5.3" or "" if not installed
	Path      string `json:"path"`      // Full path to openvpn.exe
	NeedsUpgrade bool `json:"needsUpgrade"` // True if version < 2.6 (no Wintun)
}

// IsOpenVPNInstalled checks if openvpn.exe is available on the system
func (app *App) IsOpenVPNInstalled() bool {
	_, findErr := tunnel.FindOpenVPNBinary()
	return findErr == nil
}

// GetOpenVPNStatus returns detailed info about the OpenVPN installation
func (app *App) GetOpenVPNStatus() OpenVPNStatus {
	binaryPath, findErr := tunnel.FindOpenVPNBinary()
	if findErr != nil {
		return OpenVPNStatus{Installed: false}
	}

	version := getOpenVPNVersion(binaryPath)
	needsUpgrade := version != "" && strings.Compare(version, openVPNMinRecommendedVersion) < 0

	return OpenVPNStatus{
		Installed:    true,
		Version:      version,
		Path:         binaryPath,
		NeedsUpgrade: needsUpgrade,
	}
}

// getOpenVPNVersion runs openvpn.exe --version and extracts the version number
func getOpenVPNVersion(binaryPath string) string {
	versionCmd := exec.Command(binaryPath, "--version")
	versionOutput, versionErr := versionCmd.CombinedOutput()
	if versionErr != nil {
		// openvpn --version exits with code 1 but still prints version
		if len(versionOutput) == 0 {
			return ""
		}
	}
	// First line: "OpenVPN 2.5.3 x86_64-w64-mingw32 ..."
	firstLine := strings.SplitN(string(versionOutput), "\n", 2)[0]
	parts := strings.Fields(firstLine)
	if len(parts) >= 2 && parts[0] == "OpenVPN" {
		return parts[1]
	}
	return ""
}

// IsWatchGuardInstalled checks if the WatchGuard SSL VPN client is available on the system
func (app *App) IsWatchGuardInstalled() bool {
	_, findErr := tunnel.FindWatchGuardBinary()
	return findErr == nil
}

// emitInstallProgress sends a progress update to the frontend
func (app *App) emitInstallProgress(message string) {
	log.Printf("[Dependencies] %s", message)
	if app.ctx != nil {
		wailsRuntime.EventsEmit(app.ctx, "install-progress", message)
	}
}

// InstallOpenVPN downloads and installs OpenVPN from the official source.
// If an older version is installed, it is uninstalled first.
// Returns nil on success. Requires admin privileges (UAC prompt).
func (app *App) InstallOpenVPN() error {
	// If already installed, try to uninstall old version first
	currentStatus := app.GetOpenVPNStatus()
	if currentStatus.Installed && currentStatus.NeedsUpgrade {
		app.emitInstallProgress(fmt.Sprintf("Uninstalling old OpenVPN v%s...", currentStatus.Version))
		uninstallErr := app.uninstallOldOpenVPN()
		if uninstallErr != nil {
			app.emitInstallProgress(fmt.Sprintf("Warning: Could not uninstall old version: %v", uninstallErr))
		} else {
			app.emitInstallProgress("Old version uninstalled")
		}
	}

	app.emitInstallProgress("Downloading OpenVPN 2.7 from openvpn.net...")

	// Use ProgramData temp dir so the SYSTEM service can access the MSI
	tempDir := filepath.Join(os.Getenv("ProgramData"), "VPNMultiTunnel")
	os.MkdirAll(tempDir, 0755)
	msiPath := filepath.Join(tempDir, "openvpn-installer.msi")

	downloadErr := downloadFile(openVPNInstallerURL, msiPath)
	if downloadErr != nil {
		return fmt.Errorf("failed to download OpenVPN installer: %w", downloadErr)
	}
	defer os.Remove(msiPath)

	app.emitInstallProgress("Installing via service (no UAC required)...")

	// Use the Windows service to install the MSI silently (runs as SYSTEM)
	if app.networkConfig != nil && app.networkConfig.IsServiceConnected() {
		installErr := app.networkConfig.InstallMSI(msiPath, "")
		if installErr != nil {
			return fmt.Errorf("MSI install via service failed: %w", installErr)
		}
	} else {
		// Fallback: elevate via PowerShell if service not available
		app.emitInstallProgress("Service not available, using UAC elevation...")
		installCmd := exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-Command",
			fmt.Sprintf(`Start-Process msiexec -ArgumentList '/i','%s','/quiet','/norestart' -Verb RunAs -Wait`, msiPath),
		)
		installOutput, installErr := installCmd.CombinedOutput()
		if installErr != nil {
			return fmt.Errorf("OpenVPN installation failed: %w\nOutput: %s", installErr, string(installOutput))
		}
	}

	app.emitInstallProgress("Installation completed. Verifying...")

	// Verify installation
	newStatus := app.GetOpenVPNStatus()
	if !newStatus.Installed {
		return fmt.Errorf("OpenVPN installation completed but openvpn.exe not found. You may need to install it manually from https://openvpn.net/community-downloads/")
	}

	app.emitInstallProgress(fmt.Sprintf("OpenVPN v%s installed successfully", newStatus.Version))
	app.refreshOpenVPNVersionCache()
	return nil
}

// uninstallOldOpenVPN finds and uninstalls existing OpenVPN via its registered MSI product code
func (app *App) uninstallOldOpenVPN() error {
	// Find OpenVPN product code from registry
	findCmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`(Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*' | Where-Object { $_.DisplayName -like 'OpenVPN 2.*' }).PSChildName`)
	findOutput, findErr := findCmd.CombinedOutput()
	if findErr != nil {
		return fmt.Errorf("failed to find OpenVPN in registry: %w", findErr)
	}

	productCode := strings.TrimSpace(string(findOutput))
	if productCode == "" {
		return fmt.Errorf("OpenVPN product code not found in registry")
	}

	log.Printf("[Dependencies] Found OpenVPN product code: %s, uninstalling...", productCode)

	// Use service if available (no UAC, no visible windows)
	if app.networkConfig != nil && app.networkConfig.IsServiceConnected() {
		return app.networkConfig.UninstallMSI(productCode)
	}

	// Fallback to UAC elevation
	uninstallCmd := exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-Command",
		fmt.Sprintf(`Start-Process msiexec -ArgumentList '/x','%s','/quiet','/norestart' -Verb RunAs -Wait`, productCode))
	uninstallOutput, uninstallErr := uninstallCmd.CombinedOutput()
	if uninstallErr != nil {
		return fmt.Errorf("uninstall failed: %w\nOutput: %s", uninstallErr, string(uninstallOutput))
	}

	log.Printf("[Dependencies] Old OpenVPN uninstalled successfully")
	return nil
}

// EnsureTAPAdapter creates an additional TAP adapter if all existing ones are in use.
// Called automatically when OpenVPN fails with "All tap-windows6 adapters in use".
// Returns nil on success. Requires admin privileges.
func (app *App) EnsureTAPAdapter() error {
	binaryPath, findErr := tunnel.FindOpenVPNBinary()
	if findErr != nil {
		return fmt.Errorf("OpenVPN not installed")
	}

	// tapctl.exe is in the same directory as openvpn.exe
	tapctlPath := filepath.Join(filepath.Dir(binaryPath), "tapctl.exe")
	if _, statErr := os.Stat(tapctlPath); statErr != nil {
		return fmt.Errorf("tapctl.exe not found at %s", tapctlPath)
	}

	log.Printf("[Dependencies] Creating additional TAP adapter using %s", tapctlPath)

	createCmd := exec.Command("powershell", "-Command",
		fmt.Sprintf(`Start-Process "%s" -ArgumentList 'create','--hwid','tap0901' -Verb RunAs -Wait`, tapctlPath),
	)

	createOutput, createErr := createCmd.CombinedOutput()
	if createErr != nil {
		return fmt.Errorf("failed to create TAP adapter: %w\nOutput: %s", createErr, string(createOutput))
	}

	log.Printf("[Dependencies] TAP adapter created successfully")
	return nil
}

// GetWatchGuardDownloadURL returns the SSLVPN download URL for the WatchGuard server
// configured in the given profile. Returns empty string if not a WatchGuard profile.
func (app *App) GetWatchGuardDownloadURL(profileID string) string {
	profile, profileErr := app.profileService.GetByID(profileID)
	if profileErr != nil || profile.ConfigFile == "" {
		return ""
	}
	configPath, pathErr := config.GetConfigFilePath(profile.ConfigFile)
	if pathErr != nil {
		return ""
	}
	wgConfig, parseErr := config.ParseWatchGuardConfig(configPath)
	if parseErr != nil || wgConfig.ServerAddress == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/sslvpn.html", wgConfig.ServerAddress)
}

// downloadFile downloads a URL to a local file path
func downloadFile(downloadURL string, destPath string) error {
	httpClient := &http.Client{
		Timeout: 5 * time.Minute,
	}

	response, requestErr := httpClient.Get(downloadURL)
	if requestErr != nil {
		return fmt.Errorf("HTTP request failed: %w", requestErr)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}

	outputFile, createErr := os.Create(destPath)
	if createErr != nil {
		return fmt.Errorf("failed to create file %s: %w", destPath, createErr)
	}
	defer outputFile.Close()

	bytesCopied, copyErr := io.Copy(outputFile, response.Body)
	if copyErr != nil {
		return fmt.Errorf("download interrupted: %w", copyErr)
	}

	log.Printf("[Dependencies] Downloaded %d bytes to %s", bytesCopied, destPath)
	return nil
}
