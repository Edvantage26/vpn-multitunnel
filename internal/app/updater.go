package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	githubReleasesURL      = "https://api.github.com/repos/Edvantage26/vpn-multitunnel/releases"
	updateCheckInitialDelay = 10 * time.Second
	updateCheckInterval     = 6 * time.Hour
	installerAssetSuffix    = "-amd64-installer.exe"
)

// githubRelease represents a GitHub release from the API response
type githubRelease struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	PublishedAt string         `json:"published_at"`
	Assets      []githubAsset  `json:"assets"`
}

// githubAsset represents an asset attached to a GitHub release
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// UpdateInfo holds information about available updates for the frontend
type UpdateInfo struct {
	Available      bool           `json:"available"`
	CurrentVersion string         `json:"currentVersion"`
	LatestVersion  string         `json:"latestVersion"`
	Releases       []ReleaseEntry `json:"releases"`
	InstallerURL   string         `json:"installerURL"`
}

// ReleaseEntry represents a single release in the changelog
type ReleaseEntry struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"publishedAt"`
}

// startUpdateChecker begins periodic checking for updates from GitHub releases.
func (app *App) startUpdateChecker() {
	app.updateInfoMu.Lock()
	if app.updateCheckerStop != nil {
		app.updateInfoMu.Unlock()
		return
	}
	app.updateCheckerStop = make(chan struct{})
	app.updateInfoMu.Unlock()

	go func() {
		// Wait before the first check to avoid slowing startup
		select {
		case <-app.updateCheckerStop:
			log.Printf("[updater] Stopped before initial check")
			return
		case <-time.After(updateCheckInitialDelay):
		}

		// Initial check
		app.checkForUpdatesInternal()

		update_ticker := time.NewTicker(updateCheckInterval)
		defer update_ticker.Stop()

		log.Printf("[updater] Started (interval: %s)", updateCheckInterval)

		for {
			select {
			case <-app.updateCheckerStop:
				log.Printf("[updater] Stopped")
				return
			case <-update_ticker.C:
				app.checkForUpdatesInternal()
			}
		}
	}()
}

// stopUpdateChecker signals the update checker goroutine to stop.
func (app *App) stopUpdateChecker() {
	app.updateInfoMu.Lock()
	stop_channel := app.updateCheckerStop
	app.updateCheckerStop = nil
	app.updateInfoMu.Unlock()

	if stop_channel != nil {
		close(stop_channel)
	}
}

// checkForUpdatesInternal fetches releases from GitHub and emits an event if an update is available.
func (app *App) checkForUpdatesInternal() {
	log.Printf("[updater] Checking for updates (current: v%s)...", AppVersion)

	update_info, check_err := app.fetchUpdateInfo()
	if check_err != nil {
		log.Printf("[updater] Failed to check for updates: %v", check_err)
		return
	}

	app.updateInfoMu.Lock()
	app.latestUpdateInfo = update_info
	app.updateInfoMu.Unlock()

	if update_info.Available {
		log.Printf("[updater] Update available: v%s -> v%s (%d intermediate releases)",
			update_info.CurrentVersion, update_info.LatestVersion, len(update_info.Releases))
		runtime.EventsEmit(app.ctx, "update-available", update_info)
	} else {
		log.Printf("[updater] No update available")
	}
}

// fetchUpdateInfo queries the GitHub releases API and builds an UpdateInfo.
func (app *App) fetchUpdateInfo() (*UpdateInfo, error) {
	http_client := &http.Client{Timeout: 15 * time.Second}

	api_request, request_err := http.NewRequest("GET", githubReleasesURL, nil)
	if request_err != nil {
		return nil, fmt.Errorf("creating request: %w", request_err)
	}
	api_request.Header.Set("Accept", "application/vnd.github.v3+json")
	api_request.Header.Set("User-Agent", "VPNMultiTunnel/"+AppVersion)

	api_response, response_err := http_client.Do(api_request)
	if response_err != nil {
		return nil, fmt.Errorf("fetching releases: %w", response_err)
	}
	defer api_response.Body.Close()

	if api_response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", api_response.StatusCode)
	}

	response_body, read_err := io.ReadAll(api_response.Body)
	if read_err != nil {
		return nil, fmt.Errorf("reading response: %w", read_err)
	}

	var all_releases []githubRelease
	if unmarshal_err := json.Unmarshal(response_body, &all_releases); unmarshal_err != nil {
		return nil, fmt.Errorf("parsing releases: %w", unmarshal_err)
	}

	// Filter out drafts and prereleases, keep only releases newer than current version
	var newer_releases []githubRelease
	for _, release_entry := range all_releases {
		if release_entry.Draft || release_entry.Prerelease {
			continue
		}
		release_version := strings.TrimPrefix(release_entry.TagName, "v")
		if compareVersions(release_version, AppVersion) > 0 {
			newer_releases = append(newer_releases, release_entry)
		}
	}

	update_info := &UpdateInfo{
		Available:      false,
		CurrentVersion: AppVersion,
		LatestVersion:  AppVersion,
	}

	if len(newer_releases) == 0 {
		return update_info, nil
	}

	// Sort newer releases descending by version (newest first)
	sort.Slice(newer_releases, func(idx_left, idx_right int) bool {
		version_left := strings.TrimPrefix(newer_releases[idx_left].TagName, "v")
		version_right := strings.TrimPrefix(newer_releases[idx_right].TagName, "v")
		return compareVersions(version_left, version_right) > 0
	})

	// The latest release is the first one after sorting
	latest_release := newer_releases[0]
	latest_version := strings.TrimPrefix(latest_release.TagName, "v")

	// Find installer asset in the latest release
	installer_url := ""
	for _, asset_entry := range latest_release.Assets {
		if strings.HasSuffix(asset_entry.Name, installerAssetSuffix) {
			installer_url = asset_entry.BrowserDownloadURL
			break
		}
	}

	// Build release entries for all intermediate releases
	release_entries := make([]ReleaseEntry, 0, len(newer_releases))
	for _, release_entry := range newer_releases {
		release_entries = append(release_entries, ReleaseEntry{
			Version:     strings.TrimPrefix(release_entry.TagName, "v"),
			Name:        release_entry.Name,
			Notes:       release_entry.Body,
			PublishedAt: release_entry.PublishedAt,
		})
	}

	update_info.Available = true
	update_info.LatestVersion = latest_version
	update_info.Releases = release_entries
	update_info.InstallerURL = installer_url

	return update_info, nil
}

// CheckForUpdates is exposed to the frontend via Wails bindings.
// Returns cached update info or performs a fresh check.
func (app *App) CheckForUpdates() UpdateInfo {
	app.updateInfoMu.RLock()
	cached_info := app.latestUpdateInfo
	app.updateInfoMu.RUnlock()

	if cached_info != nil {
		return *cached_info
	}

	// No cached info, perform a fresh check
	fresh_info, check_err := app.fetchUpdateInfo()
	if check_err != nil {
		log.Printf("[updater] CheckForUpdates failed: %v", check_err)
		return UpdateInfo{
			Available:      false,
			CurrentVersion: AppVersion,
			LatestVersion:  AppVersion,
		}
	}

	app.updateInfoMu.Lock()
	app.latestUpdateInfo = fresh_info
	app.updateInfoMu.Unlock()

	return *fresh_info
}

// ForceCheckForUpdates always fetches fresh update info from GitHub, bypassing the cache.
// Exposed to the frontend via Wails bindings.
func (app *App) ForceCheckForUpdates() (UpdateInfo, error) {
	log.Printf("[updater] Force-checking for updates (current: v%s)...", AppVersion)

	fresh_info, fetch_error := app.fetchUpdateInfo()
	if fetch_error != nil {
		log.Printf("[updater] ForceCheckForUpdates failed: %v", fetch_error)
		return UpdateInfo{
			Available:      false,
			CurrentVersion: AppVersion,
			LatestVersion:  AppVersion,
		}, fetch_error
	}

	app.updateInfoMu.Lock()
	app.latestUpdateInfo = fresh_info
	app.updateInfoMu.Unlock()

	if fresh_info.Available {
		log.Printf("[updater] Force check found update: v%s -> v%s", fresh_info.CurrentVersion, fresh_info.LatestVersion)
		runtime.EventsEmit(app.ctx, "update-available", fresh_info)
	} else {
		log.Printf("[updater] Force check: no update available")
	}

	return *fresh_info, nil
}

// GetAppVersion returns the current application version.
func (app *App) GetAppVersion() string {
	return AppVersion
}

// DownloadAndInstallUpdate downloads the latest installer and launches it.
func (app *App) DownloadAndInstallUpdate() error {
	app.updateInfoMu.RLock()
	cached_info := app.latestUpdateInfo
	app.updateInfoMu.RUnlock()

	if cached_info == nil || !cached_info.Available || cached_info.InstallerURL == "" {
		return fmt.Errorf("no update available or installer URL not found")
	}

	log.Printf("[updater] Downloading installer from: %s", cached_info.InstallerURL)

	// Download the installer to a temp directory
	temp_dir := os.TempDir()
	installer_filename := fmt.Sprintf("VPNMultiTunnel-%s-installer.exe", cached_info.LatestVersion)
	installer_path := filepath.Join(temp_dir, installer_filename)

	http_client := &http.Client{Timeout: 5 * time.Minute}
	download_response, download_err := http_client.Get(cached_info.InstallerURL)
	if download_err != nil {
		return fmt.Errorf("downloading installer: %w", download_err)
	}
	defer download_response.Body.Close()

	if download_response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", download_response.StatusCode)
	}

	output_file, create_err := os.Create(installer_path)
	if create_err != nil {
		return fmt.Errorf("creating installer file: %w", create_err)
	}

	_, copy_err := io.Copy(output_file, download_response.Body)
	output_file.Close()
	if copy_err != nil {
		os.Remove(installer_path)
		return fmt.Errorf("writing installer: %w", copy_err)
	}

	log.Printf("[updater] Installer downloaded to: %s", installer_path)

	// Launch the installer
	installer_cmd := exec.Command(installer_path)
	if start_err := installer_cmd.Start(); start_err != nil {
		os.Remove(installer_path)
		return fmt.Errorf("launching installer: %w", start_err)
	}

	log.Printf("[updater] Installer launched, quitting application...")

	// Quit the application so the installer can replace files
	runtime.Quit(app.ctx)

	return nil
}

// compareVersions compares two semver version strings (without "v" prefix).
// Returns 1 if version_a > version_b, -1 if version_a < version_b, 0 if equal.
func compareVersions(version_a, version_b string) int {
	parts_a := strings.Split(version_a, ".")
	parts_b := strings.Split(version_b, ".")

	max_parts := len(parts_a)
	if len(parts_b) > max_parts {
		max_parts = len(parts_b)
	}

	for idx_part := 0; idx_part < max_parts; idx_part++ {
		num_a := 0
		num_b := 0

		if idx_part < len(parts_a) {
			parsed_value, parse_err := strconv.Atoi(parts_a[idx_part])
			if parse_err == nil {
				num_a = parsed_value
			}
		}

		if idx_part < len(parts_b) {
			parsed_value, parse_err := strconv.Atoi(parts_b[idx_part])
			if parse_err == nil {
				num_b = parsed_value
			}
		}

		if num_a > num_b {
			return 1
		}
		if num_a < num_b {
			return -1
		}
	}

	return 0
}
