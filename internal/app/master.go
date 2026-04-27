package app

import (
	"log"

	"vpnmultitunnel/internal/config"
)

// IsMasterEnabled returns whether the master switch is currently on.
// When off, all VPN activity has been disabled by the user.
func (app *App) IsMasterEnabled() bool {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.masterEnabled
}

// GetDNSHealthIssue exposes the current DNS health flag (used by debug API).
// Empty string means DNS is healthy.
func (app *App) GetDNSHealthIssue() string {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.dnsHealthIssue
}

// SetMasterEnabled toggles the master switch. Side effects:
//   - true  -> false: disconnects every active profile and restores system DNS.
//   - false -> true:  reconnects all profiles flagged as auto-connect; the
//     existing doConnect flow re-establishes transparent DNS automatically.
//
// Idempotent: a no-op if the requested state matches the current state.
func (app *App) SetMasterEnabled(enabled bool) error {
	app.mu.Lock()
	if app.masterEnabled == enabled {
		app.mu.Unlock()
		return nil
	}
	app.masterEnabled = enabled
	app.mu.Unlock()

	if !enabled {
		log.Printf("Master switch OFF: disconnecting all profiles and restoring DNS")
		return app.DisconnectAll()
	}

	log.Printf("Master switch ON: starting auto-connect profiles")
	var profiles_to_connect []*config.Profile
	for idx_profile := range app.config.Profiles {
		profile_entry := &app.config.Profiles[idx_profile]
		if !profile_entry.Enabled || !profile_entry.ShouldAutoConnect() {
			continue
		}
		profiles_to_connect = append(profiles_to_connect, profile_entry)
	}

	go func() {
		for _, profile_entry := range profiles_to_connect {
			// Abort if the user toggled OFF mid-flight — otherwise we'd keep
			// connecting after a DisconnectAll, leaving stale tunnels behind.
			if !app.IsMasterEnabled() {
				log.Printf("Master enable aborted: switch turned OFF mid auto-connect")
				return
			}
			if connect_err := app.connectInternal(profile_entry.ID, false); connect_err != nil {
				log.Printf("Master enable: failed to connect %s: %v", profile_entry.Name, connect_err)
			}
		}
		app.updateTrayStatus()
	}()

	return nil
}
