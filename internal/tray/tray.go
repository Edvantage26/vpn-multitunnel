package tray

import (
	_ "embed"
	"fmt"
	"log"
	"runtime"
	"sync"

	"github.com/energye/systray"
)

//go:embed icon.ico
var embeddedIcon []byte

//go:embed icon_connected.ico
var embeddedIconConnected []byte

// VPNStatus represents the status of a VPN connection for the tray
type VPNStatus struct {
	Name      string
	Connected bool
}

// AppInterface defines the methods needed from the app
type AppInterface interface {
	ConnectAll() error
	DisconnectAll() error
}

// SystemTray manages the system tray icon and menu
type SystemTray struct {
	app            AppInterface
	showWindowFunc func()
	quitFunc       func()
	quitChan       chan struct{}
	initialized    bool
	vpnStatuses    []VPNStatus
	hasConnected   bool
	mu             sync.Mutex
}

var (
	instance *SystemTray
	once     sync.Once
)

// GetInstance returns the singleton system tray instance
func GetInstance() *SystemTray {
	once.Do(func() {
		instance = &SystemTray{
			quitChan:    make(chan struct{}),
			vpnStatuses: []VPNStatus{},
		}
	})
	return instance
}

// Start starts the system tray in a dedicated OS-thread-locked goroutine.
// All systray Win32 operations (window creation + message loop) must stay
// on the same OS thread, so we lock the goroutine before calling Run.
func (systemTray *SystemTray) Start(app AppInterface) {
	systemTray.mu.Lock()
	if systemTray.initialized {
		systemTray.mu.Unlock()
		return
	}
	systemTray.app = app
	systemTray.initialized = true
	systemTray.mu.Unlock()

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		systray.Run(systemTray.onReady, systemTray.onExit)
	}()
}

// SetShowWindowFunc sets the callback to show the main window
func (systemTray *SystemTray) SetShowWindowFunc(fn func()) {
	systemTray.mu.Lock()
	defer systemTray.mu.Unlock()
	systemTray.showWindowFunc = fn
}

// SetQuitFunc sets the callback to quit the application
func (systemTray *SystemTray) SetQuitFunc(fn func()) {
	systemTray.mu.Lock()
	defer systemTray.mu.Unlock()
	systemTray.quitFunc = fn
}

// UpdateVPNStatus updates the VPN status list and refreshes the tooltip and icon
func (systemTray *SystemTray) UpdateVPNStatus(statuses []VPNStatus) {
	systemTray.mu.Lock()
	systemTray.vpnStatuses = statuses
	systemTray.mu.Unlock()

	systemTray.updateTooltip()
	systemTray.updateIcon()
}

// updateIcon switches between normal and connected ICO based on connection status
func (systemTray *SystemTray) updateIcon() {
	systemTray.mu.Lock()
	statuses := systemTray.vpnStatuses
	systemTray.mu.Unlock()

	connected := false
	for _, vpnStatus := range statuses {
		if vpnStatus.Connected {
			connected = true
			break
		}
	}

	systemTray.mu.Lock()
	changed := connected != systemTray.hasConnected
	systemTray.hasConnected = connected
	systemTray.mu.Unlock()

	if !changed {
		return
	}

	if connected {
		systray.SetIcon(embeddedIconConnected)
	} else {
		systray.SetIcon(embeddedIcon)
	}
}

// updateTooltip updates the system tray tooltip with current VPN status
func (systemTray *SystemTray) updateTooltip() {
	systemTray.mu.Lock()
	statuses := systemTray.vpnStatuses
	systemTray.mu.Unlock()

	connectedCount := 0
	var connectedNames []string

	for _, vpnStatus := range statuses {
		if vpnStatus.Connected {
			connectedCount++
			connectedNames = append(connectedNames, vpnStatus.Name)
		}
	}

	var tooltip string
	if connectedCount == 0 {
		tooltip = "VPN MultiTunnel - No VPNs connected"
	} else if connectedCount == 1 {
		tooltip = fmt.Sprintf("VPN MultiTunnel - Connected: %s", connectedNames[0])
	} else {
		tooltip = fmt.Sprintf("VPN MultiTunnel - %d VPNs connected", connectedCount)
		if connectedCount <= 3 {
			tooltip = fmt.Sprintf("VPN MultiTunnel - Connected: %s", joinNames(connectedNames))
		}
	}

	systray.SetTooltip(tooltip)
}

// joinNames joins VPN names with commas
func joinNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	result := names[0]
	for nameIdx := 1; nameIdx < len(names); nameIdx++ {
		result += ", " + names[nameIdx]
	}
	return result
}

// Stop stops the system tray
func (systemTray *SystemTray) Stop() {
	systemTray.mu.Lock()
	defer systemTray.mu.Unlock()

	if systemTray.initialized {
		systray.Quit()
		systemTray.initialized = false
	}
}

// showWindowAsync calls the showWindowFunc in a separate goroutine to avoid
// blocking the systray Win32 message loop. Wails runtime calls (WindowShow,
// SetAlwaysOnTop) may send synchronous Win32 messages; if we called them
// directly inside the systray wndProc callback, the message loop would
// deadlock and the tray icon would appear frozen / unresponsive.
func (systemTray *SystemTray) showWindowAsync() {
	systemTray.mu.Lock()
	showFn := systemTray.showWindowFunc
	systemTray.mu.Unlock()

	if showFn != nil {
		go showFn()
	}
}

func (systemTray *SystemTray) onReady() {
	systray.SetIcon(embeddedIcon)
	systray.SetTitle("VPN MultiTunnel")
	systray.SetTooltip("VPN MultiTunnel - No VPNs connected")

	// Left-click: show the main window (async to avoid blocking the message loop)
	systray.SetOnClick(func(menu systray.IMenu) {
		systemTray.showWindowAsync()
	})

	// Double-click: also show the main window
	systray.SetOnDClick(func(menu systray.IMenu) {
		systemTray.showWindowAsync()
	})

	// Right-click: explicitly show the context menu.
	// Without this, the library default also shows the menu, but being explicit
	// ensures the menu always appears even if internal library state is odd.
	systray.SetOnRClick(func(menu systray.IMenu) {
		if menu != nil {
			if showErr := menu.ShowMenu(); showErr != nil {
				log.Printf("systray: failed to show context menu: %v", showErr)
			}
		}
	})

	// Show Window
	menuShowWindow := systray.AddMenuItem("Show Window", "Open the main window")
	menuShowWindow.Click(func() {
		systemTray.showWindowAsync()
	})

	systray.AddSeparator()

	// Connect All
	menuConnectAll := systray.AddMenuItem("Connect All", "Connect all VPN profiles")
	menuConnectAll.Click(func() {
		if systemTray.app != nil {
			go systemTray.app.ConnectAll()
		}
	})

	// Disconnect All
	menuDisconnectAll := systray.AddMenuItem("Disconnect All", "Disconnect all VPN profiles")
	menuDisconnectAll.Click(func() {
		if systemTray.app != nil {
			go systemTray.app.DisconnectAll()
		}
	})

	systray.AddSeparator()

	// Quit
	menuQuit := systray.AddMenuItem("Quit", "Quit the application")
	menuQuit.Click(func() {
		systemTray.mu.Lock()
		quitFn := systemTray.quitFunc
		systemTray.mu.Unlock()

		select {
		case <-systemTray.quitChan:
			// already closed
		default:
			close(systemTray.quitChan)
		}

		if quitFn != nil {
			go quitFn()
		}

		systray.Quit()
	})
}

func (systemTray *SystemTray) onExit() {
	// Cleanup
}

// QuitChan returns a channel that's closed when quit is requested
func (systemTray *SystemTray) QuitChan() <-chan struct{} {
	return systemTray.quitChan
}
