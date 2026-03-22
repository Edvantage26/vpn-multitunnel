package svchost

import (
	"log"
	"sync"

	"golang.org/x/sys/windows/svc"

	"vpnmultitunnel/internal/ipc"
)

// ServiceHandler implements the Windows service handler
type ServiceHandler struct {
	server     *ipc.Server
	ops        *Operations
	stopCh     chan struct{}
	stoppedWg  sync.WaitGroup
}

// NewServiceHandler creates a new service handler
func NewServiceHandler() *ServiceHandler {
	ops := NewOperations()
	handler := &ServiceHandler{
		ops:    ops,
		stopCh: make(chan struct{}),
	}
	handler.server = ipc.NewServer(handler.handleRequest)
	return handler
}

// handleRequest handles incoming IPC requests
func (service_handler *ServiceHandler) handleRequest(req *ipc.Request) *ipc.Response {
	log.Printf("Handling request: %s", req.Operation)

	switch req.Operation {
	case ipc.OpPing:
		return ipc.SuccessResponse().SetData("status", "ok")

	case ipc.OpAddLoopbackIP:
		ip, err := req.GetString("ip")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := service_handler.ops.AddLoopbackIP(ip); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpRemoveLoopbackIP:
		ip, err := req.GetString("ip")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := service_handler.ops.RemoveLoopbackIP(ip); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpEnsureLoopbackIPs:
		ips, err := req.GetStringSlice("ips")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := service_handler.ops.EnsureLoopbackIPs(ips); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpSetDNS:
		iface, err := req.GetString("interface")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		servers, err := req.GetStringSlice("servers")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := service_handler.ops.SetDNS(iface, servers); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpSetDNSv6:
		iface, err := req.GetString("interface")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		servers, err := req.GetStringSlice("servers")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := service_handler.ops.SetDNSv6(iface, servers); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpResetDNS:
		iface, err := req.GetString("interface")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := service_handler.ops.ResetDNS(iface); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpConfigureSystemDNS:
		addr, err := req.GetString("address")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := service_handler.ops.ConfigureSystemDNS(addr); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpRestoreSystemDNS:
		if err := service_handler.ops.RestoreSystemDNS(); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpStopDNSClient:
		if err := service_handler.ops.StopDNSClient(); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpStartDNSClient:
		if err := service_handler.ops.StartDNSClient(); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	default:
		return ipc.ErrorResponse(nil).SetData("error", "unknown operation")
	}
}

// Execute implements the Windows service handler interface
func (service_handler *ServiceHandler) Execute(args []string, change_requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	// Start the IPC server
	if err := service_handler.server.Start(); err != nil {
		log.Printf("Failed to start IPC server: %v", err)
		return false, 1
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
	log.Printf("Service started and accepting requests")

loop:
	for {
		select {
		case change_request := <-change_requests:
			switch change_request.Cmd {
			case svc.Interrogate:
				changes <- change_request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				log.Printf("Service received stop/shutdown signal")
				break loop
			default:
				log.Printf("Unexpected control request: %v", change_request.Cmd)
			}
		case <-service_handler.stopCh:
			break loop
		}
	}

	changes <- svc.Status{State: svc.StopPending}

	// Restore DNS before stopping (in case app crashed)
	if service_handler.ops.IsDNSConfigured() {
		log.Printf("Restoring DNS configuration before shutdown...")
		if err := service_handler.ops.RestoreSystemDNS(); err != nil {
			log.Printf("Failed to restore DNS: %v", err)
		}
	}

	// Stop the IPC server
	service_handler.server.Stop()

	return false, 0
}

// Stop signals the service to stop (for manual stopping)
func (service_handler *ServiceHandler) Stop() {
	close(service_handler.stopCh)
}
