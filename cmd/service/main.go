package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"vpnmultitunnel/internal/ipc"
	"vpnmultitunnel/internal/svchost"
)

const (
	serviceName        = ipc.ServiceName
	serviceDisplayName = "VPN MultiTunnel Service"
	serviceDescription = "Provides privileged network operations for VPN MultiTunnel without UAC prompts"
)

var elog debug.Log

func main() {
	// Set up logging to file
	logDir := filepath.Join(os.Getenv("ProgramData"), "VPNMultiTunnel")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(
		filepath.Join(logDir, "service.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err == nil {
		log.SetOutput(logFile)
	}

	// Check if we're running as a service or from command line
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("Failed to determine if running as service: %v", err)
	}

	if isService {
		runService(false)
		return
	}

	// Command line mode
	if len(os.Args) < 2 {
		usage()
		return
	}

	switch os.Args[1] {
	case "install":
		err = installService()
	case "uninstall", "remove":
		err = uninstallService()
	case "start":
		err = startService()
	case "stop":
		err = stopService()
	case "run":
		// Run interactively (for debugging)
		runService(true)
		return
	case "status":
		err = queryStatus()
	default:
		usage()
		return
	}

	if err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func usage() {
	fmt.Printf("Usage: %s <command>\n\n", os.Args[0])
	fmt.Println("Commands:")
	fmt.Println("  install    Install the service")
	fmt.Println("  uninstall  Uninstall the service")
	fmt.Println("  start      Start the service")
	fmt.Println("  stop       Stop the service")
	fmt.Println("  status     Query service status")
	fmt.Println("  run        Run interactively (for debugging)")
}

func installService() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	// Check if service already exists
	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", serviceName)
	}

	// Create the service
	s, err = m.CreateService(
		serviceName,
		exePath,
		mgr.Config{
			DisplayName:      serviceDisplayName,
			Description:      serviceDescription,
			StartType:        mgr.StartAutomatic,
			ServiceStartName: "LocalSystem",
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer s.Close()

	// Set recovery actions: restart on failure
	err = s.SetRecoveryActions(
		[]mgr.RecoveryAction{
			{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		},
		60, // Reset failure count after 60 seconds
	)
	if err != nil {
		log.Printf("Warning: failed to set recovery actions: %v", err)
	}

	// Create event log source
	err = eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err != nil {
		log.Printf("Warning: failed to create event log source: %v", err)
	}

	fmt.Printf("Service %s installed successfully\n", serviceName)
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer s.Close()

	// Stop the service first if running
	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		_, err = s.Control(svc.Stop)
		if err != nil {
			log.Printf("Warning: failed to stop service: %v", err)
		} else {
			// Wait for service to stop
			for i := 0; i < 10; i++ {
				time.Sleep(500 * time.Millisecond)
				status, err = s.Query()
				if err != nil || status.State == svc.Stopped {
					break
				}
			}
		}
	}

	// Delete the service
	err = s.Delete()
	if err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	// Remove event log source
	eventlog.Remove(serviceName)

	fmt.Printf("Service %s uninstalled successfully\n", serviceName)
	return nil
}

func startService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer s.Close()

	err = s.Start()
	if err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	fmt.Printf("Service %s started successfully\n", serviceName)
	return nil
}

func stopService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer s.Close()

	_, err = s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("failed to stop service: %w", err)
	}

	fmt.Printf("Service %s stopped successfully\n", serviceName)
	return nil
}

func queryStatus() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Printf("Service %s: Not installed\n", serviceName)
		return nil
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("failed to query service status: %w", err)
	}

	var stateStr string
	switch status.State {
	case svc.Stopped:
		stateStr = "Stopped"
	case svc.StartPending:
		stateStr = "Starting"
	case svc.StopPending:
		stateStr = "Stopping"
	case svc.Running:
		stateStr = "Running"
	case svc.ContinuePending:
		stateStr = "Continuing"
	case svc.PausePending:
		stateStr = "Pausing"
	case svc.Paused:
		stateStr = "Paused"
	default:
		stateStr = fmt.Sprintf("Unknown (%d)", status.State)
	}

	fmt.Printf("Service %s: %s\n", serviceName, stateStr)
	return nil
}

func runService(isDebug bool) {
	var err error
	if isDebug {
		elog = debug.New(serviceName)
	} else {
		elog, err = eventlog.Open(serviceName)
		if err != nil {
			log.Printf("Failed to open event log: %v", err)
			return
		}
	}
	defer elog.Close()

	elog.Info(1, fmt.Sprintf("Starting %s service", serviceName))
	handler := svchost.NewServiceHandler()

	if isDebug {
		// Run interactively
		err = debug.Run(serviceName, handler)
	} else {
		err = svc.Run(serviceName, handler)
	}

	if err != nil {
		elog.Error(1, fmt.Sprintf("Service failed: %v", err))
		return
	}

	elog.Info(1, fmt.Sprintf("%s service stopped", serviceName))
}
