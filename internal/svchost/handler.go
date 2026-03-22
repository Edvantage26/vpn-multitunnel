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
func (h *ServiceHandler) handleRequest(req *ipc.Request) *ipc.Response {
	log.Printf("Handling request: %s", req.Operation)

	switch req.Operation {
	case ipc.OpPing:
		return ipc.SuccessResponse().SetData("status", "ok")

	case ipc.OpAddLoopbackIP:
		ip, err := req.GetString("ip")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := h.ops.AddLoopbackIP(ip); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpRemoveLoopbackIP:
		ip, err := req.GetString("ip")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := h.ops.RemoveLoopbackIP(ip); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpEnsureLoopbackIPs:
		ips, err := req.GetStringSlice("ips")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := h.ops.EnsureLoopbackIPs(ips); err != nil {
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
		if err := h.ops.SetDNS(iface, servers); err != nil {
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
		if err := h.ops.SetDNSv6(iface, servers); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpResetDNS:
		iface, err := req.GetString("interface")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := h.ops.ResetDNS(iface); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpConfigureSystemDNS:
		addr, err := req.GetString("address")
		if err != nil {
			return ipc.ErrorResponse(err)
		}
		if err := h.ops.ConfigureSystemDNS(addr); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpRestoreSystemDNS:
		if err := h.ops.RestoreSystemDNS(); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpStopDNSClient:
		if err := h.ops.StopDNSClient(); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	case ipc.OpStartDNSClient:
		if err := h.ops.StartDNSClient(); err != nil {
			return ipc.ErrorResponse(err)
		}
		return ipc.SuccessResponse()

	default:
		return ipc.ErrorResponse(nil).SetData("error", "unknown operation")
	}
}

// Execute implements the Windows service handler interface
func (h *ServiceHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	// Start the IPC server
	if err := h.server.Start(); err != nil {
		log.Printf("Failed to start IPC server: %v", err)
		return false, 1
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
	log.Printf("Service started and accepting requests")

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				log.Printf("Service received stop/shutdown signal")
				break loop
			default:
				log.Printf("Unexpected control request: %v", c.Cmd)
			}
		case <-h.stopCh:
			break loop
		}
	}

	changes <- svc.Status{State: svc.StopPending}

	// Restore DNS before stopping (in case app crashed)
	if h.ops.IsDNSConfigured() {
		log.Printf("Restoring DNS configuration before shutdown...")
		if err := h.ops.RestoreSystemDNS(); err != nil {
			log.Printf("Failed to restore DNS: %v", err)
		}
	}

	// Stop the IPC server
	h.server.Stop()

	return false, 0
}

// Stop signals the service to stop (for manual stopping)
func (h *ServiceHandler) Stop() {
	close(h.stopCh)
}
