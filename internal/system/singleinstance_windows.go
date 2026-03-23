package system

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	singleInstanceMutexName = "Global\\VPNMultiTunnel_SingleInstance"
	shutdownPipeName        = `\\.\pipe\VPNMultiTunnel_Shutdown`
	shutdownMessage         = "SHUTDOWN"
	shutdownTimeout         = 5 * time.Second
)

// SingleInstance manages single-instance enforcement and graceful handoff
type SingleInstance struct {
	mutexHandle   windows.Handle
	shutdownChan  chan struct{}
	onShutdownReq func() // called when another instance requests us to quit
}

// NewSingleInstance creates a new single-instance manager.
// If another instance is already running, it sends a shutdown request and waits
// for it to exit before returning.
func NewSingleInstance() (*SingleInstance, error) {
	instance := &SingleInstance{
		shutdownChan: make(chan struct{}),
	}

	mutexNamePtr, err := windows.UTF16PtrFromString(singleInstanceMutexName)
	if err != nil {
		return nil, fmt.Errorf("failed to create mutex name: %w", err)
	}

	// Try to create/acquire the mutex.
	// Note: CreateMutex returns a valid handle even with ERROR_ALREADY_EXISTS.
	// We must check the error separately to detect an existing instance.
	mutexHandle, err := windows.CreateMutex(nil, false, mutexNamePtr)
	if err == windows.ERROR_ALREADY_EXISTS {
		// Close the handle we just got (it's a duplicate reference to the existing mutex)
		if mutexHandle != 0 {
			windows.CloseHandle(mutexHandle)
		}

		// Another instance is running — ask it to shut down gracefully
		log.Printf("Another instance detected, requesting graceful shutdown...")
		if sendErr := requestShutdownViaPipe(); sendErr != nil {
			log.Printf("Could not send shutdown request: %v (will force-kill)", sendErr)
			killExistingInstances()
		} else {
			// Wait for the other instance to release the mutex
			waitForMutexRelease(mutexNamePtr)
			// Clean up any leftover tray icons just in case
			cleanupZombieTrayIcons()
		}

		// Try again to acquire the mutex
		mutexHandle, err = windows.CreateMutex(nil, false, mutexNamePtr)
		if err != nil {
			return nil, fmt.Errorf("failed to acquire mutex after shutdown: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to create mutex: %w", err)
	}

	instance.mutexHandle = mutexHandle
	return instance, nil
}

// SetShutdownCallback sets the function to call when another instance requests shutdown.
// This should trigger a graceful cleanup (tray icon removal, tunnel teardown, etc).
func (instance *SingleInstance) SetShutdownCallback(callback func()) {
	instance.onShutdownReq = callback
}

// ListenForShutdown starts listening on the named pipe for shutdown requests
// from new instances. Runs in a background goroutine.
func (instance *SingleInstance) ListenForShutdown() {
	go instance.listenOnPipe()
}

// ListenForSignals catches OS signals (SIGINT, SIGTERM) and calls the shutdown
// callback so the tray icon is cleaned up even on Ctrl+C or taskkill (without /F).
func (instance *SingleInstance) ListenForSignals() {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		receivedSignal := <-signalChan
		log.Printf("Received signal %v, shutting down gracefully...", receivedSignal)
		if instance.onShutdownReq != nil {
			instance.onShutdownReq()
		}
	}()
}

// listenOnPipe listens on the named pipe for shutdown messages
func (instance *SingleInstance) listenOnPipe() {
	for {
		select {
		case <-instance.shutdownChan:
			return
		default:
		}

		pipeHandle, err := createNamedPipeServer()
		if err != nil {
			log.Printf("Failed to create shutdown pipe: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		// Wait for a client to connect (blocking)
		err = windows.ConnectNamedPipe(pipeHandle, nil)
		if err != nil {
			windows.CloseHandle(pipeHandle)
			continue
		}

		// Read the message
		buffer := make([]byte, 256)
		var bytesRead uint32
		err = windows.ReadFile(pipeHandle, buffer, &bytesRead, nil)
		windows.CloseHandle(pipeHandle)

		if err != nil {
			continue
		}

		message := string(buffer[:bytesRead])
		if message == shutdownMessage {
			log.Printf("Received shutdown request from new instance")
			if instance.onShutdownReq != nil {
				instance.onShutdownReq()
			}
			return
		}
	}
}

// Release releases the single-instance mutex
func (instance *SingleInstance) Release() {
	select {
	case <-instance.shutdownChan:
		// already closed
	default:
		close(instance.shutdownChan)
	}
	if instance.mutexHandle != 0 {
		windows.ReleaseMutex(instance.mutexHandle)
		windows.CloseHandle(instance.mutexHandle)
		instance.mutexHandle = 0
	}
}

// requestShutdownViaPipe sends a shutdown message to the existing instance
func requestShutdownViaPipe() error {
	pipePathPtr, err := windows.UTF16PtrFromString(shutdownPipeName)
	if err != nil {
		return err
	}

	fileHandle, err := windows.CreateFile(
		pipePathPtr,
		windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return fmt.Errorf("failed to connect to shutdown pipe: %w", err)
	}
	defer windows.CloseHandle(fileHandle)

	messageBytes := []byte(shutdownMessage)
	var bytesWritten uint32
	err = windows.WriteFile(fileHandle, messageBytes, &bytesWritten, nil)
	if err != nil {
		return fmt.Errorf("failed to write shutdown message: %w", err)
	}

	return nil
}

// waitForMutexRelease waits for the named mutex to be released by the other instance
func waitForMutexRelease(mutexNamePtr *uint16) {
	mutexHandle, err := windows.OpenMutex(windows.SYNCHRONIZE, false, mutexNamePtr)
	if err != nil {
		// Mutex doesn't exist anymore — the other instance is gone
		return
	}
	defer windows.CloseHandle(mutexHandle)

	// Wait up to shutdownTimeout for the mutex to be released
	waitResult, _ := windows.WaitForSingleObject(mutexHandle, uint32(shutdownTimeout.Milliseconds()))
	if waitResult == uint32(windows.WAIT_TIMEOUT) {
		log.Printf("Timed out waiting for previous instance to exit, force-killing...")
		killExistingInstances()
	}
}

// killExistingInstances forcefully kills any running VPNMultiTunnel.exe processes (other than ourselves)
func killExistingInstances() {
	currentPID := uint32(os.Getpid())

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snapshot)

	var processEntry windows.ProcessEntry32
	processEntry.Size = uint32(unsafe.Sizeof(processEntry))

	err = windows.Process32First(snapshot, &processEntry)
	for err == nil {
		processName := windows.UTF16ToString(processEntry.ExeFile[:])
		if processName == "VPNMultiTunnel.exe" && processEntry.ProcessID != currentPID {
			processHandle, openErr := windows.OpenProcess(windows.PROCESS_TERMINATE, false, processEntry.ProcessID)
			if openErr == nil {
				windows.TerminateProcess(processHandle, 1)
				windows.CloseHandle(processHandle)
				log.Printf("Force-killed previous instance (PID %d)", processEntry.ProcessID)
			}
		}
		err = windows.Process32Next(snapshot, &processEntry)
	}

	// Give it a moment to fully terminate
	time.Sleep(500 * time.Millisecond)

	// Sweep the notification area to remove zombie tray icons
	cleanupZombieTrayIcons()
}

// cleanupZombieTrayIcons forces Windows to re-evaluate all tray icons by simulating
// a mouse sweep across the notification area. This removes icons whose owning
// process has died without calling Shell_NotifyIcon(NIM_DELETE).
func cleanupZombieTrayIcons() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	procFindWindowW := user32.NewProc("FindWindowW")
	procGetClientRect := user32.NewProc("GetClientRect")
	procSendMessageW := user32.NewProc("SendMessageW")

	// Find the notification area window.
	// The tray icons live in "Shell_TrayWnd" > "TrayNotifyWnd" > "SysPager" > "ToolbarWindow32"
	trayNotifyClasses := [][]string{
		{"Shell_TrayWnd", "TrayNotifyWnd", "SysPager", "ToolbarWindow32"},
		// Windows 11 sometimes uses NotifyIconOverflowWindow
		{"NotifyIconOverflowWindow", "ToolbarWindow32"},
	}

	const wmMousemove = 0x0200

	for _, classChain := range trayNotifyClasses {
		var parentHandle uintptr
		for _, className := range classChain {
			classNamePtr, _ := syscall.UTF16PtrFromString(className)
			if parentHandle == 0 {
				parentHandle, _, _ = procFindWindowW.Call(uintptr(unsafe.Pointer(classNamePtr)), 0)
			} else {
				findWindowExProc := user32.NewProc("FindWindowExW")
				parentHandle, _, _ = findWindowExProc.Call(parentHandle, 0, uintptr(unsafe.Pointer(classNamePtr)), 0)
			}
			if parentHandle == 0 {
				break
			}
		}

		if parentHandle == 0 {
			continue
		}

		// Get the bounds of the toolbar
		type rect struct {
			Left, Top, Right, Bottom int32
		}
		var toolbarRect rect
		procGetClientRect.Call(parentHandle, uintptr(unsafe.Pointer(&toolbarRect)))

		// Sweep the mouse across the toolbar area to force Windows to check each icon.
		// Icons whose process no longer exists will be removed.
		iconSize := int32(24) // approximate icon spacing in the tray
		for yPos := toolbarRect.Top; yPos < toolbarRect.Bottom; yPos += iconSize {
			for xPos := toolbarRect.Left; xPos < toolbarRect.Right; xPos += iconSize {
				lParam := uintptr(yPos)<<16 | uintptr(xPos)&0xFFFF
				procSendMessageW.Call(parentHandle, wmMousemove, 0, lParam)
			}
		}
	}
}

// createNamedPipeServer creates a named pipe server for receiving shutdown requests
func createNamedPipeServer() (windows.Handle, error) {
	pipeNamePtr, err := windows.UTF16PtrFromString(shutdownPipeName)
	if err != nil {
		return 0, err
	}

	pipeHandle, err := windows.CreateNamedPipe(
		pipeNamePtr,
		windows.PIPE_ACCESS_INBOUND,
		windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
		1,    // max instances
		256,  // out buffer size
		256,  // in buffer size
		5000, // default timeout ms
		nil,  // default security
	)
	if err != nil {
		return 0, fmt.Errorf("CreateNamedPipe failed: %w", err)
	}

	return pipeHandle, nil
}
