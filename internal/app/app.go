package app

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"vpnmultitunnel/internal/api"
	"vpnmultitunnel/internal/config"
	"vpnmultitunnel/internal/debug"
	"vpnmultitunnel/internal/service"
	"vpnmultitunnel/internal/system"
	"vpnmultitunnel/internal/tray"
	"vpnmultitunnel/internal/tunnel"
)

// App struct
type App struct {
	ctx            context.Context
	config         *config.AppConfig
	tunnelManager  *tunnel.Manager
	profileService *service.ProfileService
	systemTray     *tray.SystemTray
	networkConfig  *system.NetworkConfig
	debugServer    *api.Server
	mu             sync.RWMutex

	// Tracks profiles currently in the process of connecting
	connectingProfiles map[string]bool

	// Tracks the last connection error per profile (cleared on successful connect)
	lastConnectErrors map[string]string

	// Cache for DNS status checks (to avoid running netstat every 2 seconds)
	dnsStatusMu          sync.RWMutex
	dnsStatusPort53Free  bool
	dnsStatusClientDown  bool
	dnsStatusCacheTime   time.Time
	dnsStatusCacheTTL    time.Duration

	// Network monitor state
	networkMonitorStop       chan struct{}
	dnsHealthIssue           string
	lastActiveInterface      string
	consecutiveDNSFailures   int

	// Update checker state
	updateCheckerStop  chan struct{}
	latestUpdateInfo   *UpdateInfo
	updateInfoMu       sync.RWMutex

	// Cached VPN client version strings (refreshed on startup and after install)
	cachedOpenVPNVersion string
}

// ProfileStatus represents the status of a profile for the UI
type ProfileStatus struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	ConfigFile    string `json:"configFile"`
	Connected     bool   `json:"connected"`
	Connecting    bool   `json:"connecting"`
	Healthy       bool   `json:"healthy"`
	TunnelIP      string `json:"tunnelIP"`
	BytesSent     uint64 `json:"bytesSent"`
	BytesRecv     uint64 `json:"bytesRecv"`
	LastHandshake string `json:"lastHandshake"`
	Endpoint      string `json:"endpoint"`
	LastError      string `json:"lastError,omitempty"`
	DNSIssue       string `json:"dnsIssue,omitempty"`
	ClientVersion  string `json:"clientVersion,omitempty"`
}

// WireGuardConfigDisplay represents WireGuard config metadata for UI display
type WireGuardConfigDisplay struct {
	Interface struct {
		Address    string `json:"address"`
		DNS        string `json:"dns"`
		PublicKey  string `json:"publicKey"`
		ListenPort int    `json:"listenPort,omitempty"`
	} `json:"interface"`
	Peer struct {
		Endpoint   string `json:"endpoint"`
		AllowedIPs string `json:"allowedIPs"`
		PublicKey  string `json:"publicKey"`
	} `json:"peer"`
}

// AdapterSummary represents a network adapter for the frontend adapter picker
type AdapterSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	IPv4Addrs   []string `json:"ipv4Addrs"`
	DNSServers  []string `json:"dnsServers"`
	IsUp        bool     `json:"isUp"`
	IsVPN       bool     `json:"isVPN"`
}

// refreshOpenVPNVersionCache detects the installed OpenVPN version and caches it
func (app *App) refreshOpenVPNVersionCache() {
	ovpnStatus := app.GetOpenVPNStatus()
	if ovpnStatus.Installed && ovpnStatus.Version != "" {
		app.cachedOpenVPNVersion = "OpenVPN " + ovpnStatus.Version
	} else {
		app.cachedOpenVPNVersion = ""
	}
}

// New creates a new App application struct
func New() *App {
	return &App{
		connectingProfiles: make(map[string]bool),
		lastConnectErrors:  make(map[string]string),
		dnsStatusCacheTTL:  10 * time.Second, // Cache DNS status for 10 seconds
	}
}

// Startup is called when the app starts
func (app *App) Startup(ctx context.Context) {
	app.ctx = ctx

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Failed to load config: %v", err)
		cfg = config.Default()
	}
	app.config = cfg

	// Propagate DNS listen address from Settings to DNSProxy config
	if cfg.Settings.DNSListenAddress != "" {
		cfg.DNSProxy.ListenAddress = cfg.Settings.DNSListenAddress
	}

	// Initialize network configuration manager
	app.networkConfig = system.GetNetworkConfig()
	app.networkConfig.SetDNSProxyAddress(cfg.DNSProxy.GetListenAddress())
	app.networkConfig.SetDNSFallbackServer(cfg.Settings.DNSFallbackServer)

	// Try to connect to the VPN MultiTunnel service for privileged operations
	if cfg.Settings.UseService {
		if app.networkConfig.ConnectToService() {
			log.Printf("Connected to VPN MultiTunnel service - UAC prompts will be avoided")
		} else {
			log.Printf("VPN MultiTunnel service not available - will use UAC elevation when needed")
		}
	}

	// Initialize services
	app.profileService = service.NewProfileService(cfg)
	app.tunnelManager = tunnel.NewManager(cfg)

	// Cache OpenVPN version for display in profile status
	app.refreshOpenVPNVersionCache()

	// Register log listener for real-time log events to the frontend (all logs, including system)
	debug.GetLogger().AddListener(func(logEntry debug.LogEntry) {
		if app.ctx != nil {
			runtime.EventsEmit(app.ctx, "profile-log", logEntry)
		}
	})

	// Wire up traffic monitor events to the frontend
	app.tunnelManager.GetTrafficMonitor().SetEventEmitter(func(event_name string, event_data interface{}) {
		if app.ctx != nil {
			runtime.EventsEmit(app.ctx, event_name, event_data)
		}
	})

	// Set callback for dynamically configuring loopback IPs when DNS assigns new IPs
	app.tunnelManager.SetLoopbackIPCallback(func(ip string) error {
		if app.networkConfig == nil {
			return fmt.Errorf("network config not initialized")
		}
		// Check if IP already exists
		if app.networkConfig.LoopbackIPExists(ip) {
			return nil
		}
		// Try to add the IP via service or UAC elevation
		return app.networkConfig.AddLoopbackIPElevated(ip)
	})

	// Assign tunnel IPs to existing profiles that don't have one
	if err := app.AssignTunnelIPsForExistingProfiles(); err != nil {
		log.Printf("Failed to assign tunnel IPs: %v", err)
	}

	// Note: Loopback IPs are now configured on-demand when connecting a VPN
	// This avoids requiring admin at startup

	// Always use port 53 for DNS proxy when enabled
	if cfg.DNSProxy.Enabled {
		cfg.DNSProxy.ListenPort = 53
	}

	// Initialize system tray
	app.systemTray = tray.GetInstance()
	app.systemTray.Start(app)

	// Set up tray callbacks for window control
	app.systemTray.SetShowWindowFunc(func() {
		runtime.WindowShow(app.ctx)
		runtime.WindowSetAlwaysOnTop(app.ctx, true)
		runtime.WindowSetAlwaysOnTop(app.ctx, false)
	})
	app.systemTray.SetQuitFunc(func() {
		runtime.Quit(app.ctx)
	})

	// Auto-connect profiles that have autoConnect enabled (default: true)
	// Mark them as "connecting" first so the UI shows spinners immediately,
	// then run the actual connections in a goroutine so the window opens without blocking.
	var autoConnectProfiles []*config.Profile
	for idx_profile := range cfg.Profiles {
		profile := &cfg.Profiles[idx_profile]
		if !profile.Enabled || !profile.ShouldAutoConnect() {
			continue
		}
		// Check if loopback IP exists - skip if not and service unavailable
		if cfg.TCPProxy.IsEnabled() && cfg.Settings.AutoConfigureLoopback {
			tunnelIP := app.profileService.GetTunnelIP(profile.ID)
			if tunnelIP != "" && !app.networkConfig.LoopbackIPExists(tunnelIP) && !app.networkConfig.IsServiceConnected() {
				log.Printf("Auto-connect skipped for %s: loopback IP %s not configured and service not available", profile.Name, tunnelIP)
				continue
			}
		}
		app.connectingProfiles[profile.ID] = true
		autoConnectProfiles = append(autoConnectProfiles, profile)
	}

	go func() {
		// Ensure DNS proxy is ready BEFORE auto-connecting profiles.
		// WireGuard needs to resolve endpoint hostnames (e.g., dokploy.edvantage.dev),
		// and if system DNS points to our proxy (127.0.0.53) but the proxy isn't
		// listening yet, the resolution fails with "No such host is known".
		if cfg.Settings.UsePort53 && cfg.DNSProxy.Enabled && app.networkConfig.IsTransparentDNSConfigured() {
			log.Printf("System DNS already configured to proxy, ensuring DNS proxy on port 53 before auto-connect...")
			// Ensure the loopback IP exists first (needed for binding)
			dnsAddr := cfg.DNSProxy.GetListenAddress()
			if dnsAddr != "127.0.0.1" {
				app.networkConfig.AddLoopbackIPElevated(dnsAddr)
			}
			if err := app.tunnelManager.RestartDNSProxyOnPort(53); err != nil {
				log.Printf("Warning: Failed to restart DNS proxy on port 53: %v", err)
			}
			// Ensure IPv6 DNS also points to our proxy (prevents fe80::1 from taking priority)
			if active_network_interface, get_interface_err := app.networkConfig.GetActiveNetworkInterface(); get_interface_err == nil {
				app.networkConfig.SetDNSv6(active_network_interface, []string{"::1"})
			}
			system.FlushDNSCache()
		}

		// Connect profiles sequentially
		for _, profile := range autoConnectProfiles {
			if err := app.connectInternal(profile.ID, false); err != nil {
				log.Printf("Auto-connect failed for %s: %v", profile.Name, err)
			}
			// For sync VPN types (WireGuard), clear connecting state here.
			// For async types (OpenVPN/WatchGuard), connectInBackground handles it.
			vpnType := profile.GetVPNType()
			if vpnType != config.VPNTypeOpenVPN && vpnType != config.VPNTypeWatchGuard && vpnType != config.VPNTypeExternal {
				app.mu.Lock()
				delete(app.connectingProfiles, profile.ID)
				app.mu.Unlock()
			}
		}

		// Update tray status after auto-connect
		app.updateTrayStatus()

		// Start network monitor to detect interface changes and DNS health issues
		app.startNetworkMonitor()
	}()

	// Initialize and start the debug API server
	if cfg.Settings.DebugAPIEnabled {
		port := cfg.Settings.DebugAPIPort
		if port == 0 {
			port = 8765
		}
		app.debugServer = api.NewServer(port, app)
		if err := app.debugServer.Start(); err != nil {
			log.Printf("Warning: Failed to start debug API server: %v", err)
		}
	}

	// Start update checker in background
	app.startUpdateChecker()

	debug.Info("app", "Application started", map[string]any{
		"debugApiEnabled": cfg.Settings.DebugAPIEnabled,
		"debugApiPort":    cfg.Settings.DebugAPIPort,
	})
}

// configureLoopbackIPs sets up the required loopback IP addresses
func (app *App) configureLoopbackIPs() {
	if !system.IsAdmin() {
		log.Println("Warning: Not running as admin, cannot configure loopback IPs automatically")
		log.Println("Please run as administrator or manually add loopback IPs:")
		for _, ip := range app.config.TCPProxy.TunnelIPs {
			log.Printf("  netsh interface ipv4 add address \"Loopback Pseudo-Interface 1\" %s 255.255.255.0", ip)
		}
		return
	}

	// Collect all tunnel IPs
	var ips []string
	for _, ip := range app.config.TCPProxy.TunnelIPs {
		ips = append(ips, ip)
	}

	if len(ips) > 0 {
		if err := app.networkConfig.EnsureLoopbackIPs(ips); err != nil {
			log.Printf("Warning: Failed to configure loopback IPs: %v", err)
		}
	}
}

// Shutdown is called when the app is closing
func (app *App) Shutdown(ctx context.Context) {
	debug.Info("app", "Application shutting down", nil)

	// Stop update checker
	app.stopUpdateChecker()

	// Stop network monitor
	app.stopNetworkMonitor()

	// Stop debug server
	if app.debugServer != nil {
		app.debugServer.Stop()
	}

	// Restore transparent DNS if we configured it
	if app.networkConfig != nil && app.networkConfig.IsTransparentDNSConfigured() {
		if err := app.networkConfig.RestoreTransparentDNS(); err != nil {
			log.Printf("Failed to restore DNS: %v", err)
		}
		system.FlushDNSCache()
	}

	// Stop system tray
	if app.systemTray != nil {
		app.systemTray.Stop()
	}

	// Stop all tunnels
	if app.tunnelManager != nil {
		app.tunnelManager.StopAll()
	}

	// Disconnect from service
	if app.networkConfig != nil {
		app.networkConfig.DisconnectFromService()
	}
}
