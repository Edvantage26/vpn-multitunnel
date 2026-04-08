package proxy

import (
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modIphlpapi              = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedUdpTable  = modIphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	udpTableOwnerPID = 1
	afINET           = 2
)

// MIB_UDPROW_OWNER_PID represents a single UDP endpoint with owning process ID
type mibUDPRowOwnerPID struct {
	LocalAddr uint32
	LocalPort uint32
	OwningPID uint32
}

// ProcessResolver maps UDP source ports to process names using Windows API
type ProcessResolver struct {
	processNameCache     map[uint32]cachedProcessName // PID -> name
	mu                   sync.RWMutex
}

type cachedProcessName struct {
	name      string
	cachedAt  time.Time
}

const processNameCacheTTL = 30 * time.Second

var (
	processResolverInstance *ProcessResolver
	processResolverOnce    sync.Once
)

// GetProcessResolver returns the singleton ProcessResolver
func GetProcessResolver() *ProcessResolver {
	processResolverOnce.Do(func() {
		processResolverInstance = &ProcessResolver{
			processNameCache: make(map[uint32]cachedProcessName),
		}
	})
	return processResolverInstance
}

// ResolveUDPSourceProcess finds the process name that owns a given local UDP port.
// Returns empty string if the process cannot be determined.
func (resolver *ProcessResolver) ResolveUDPSourceProcess(source_port uint16) string {
	owner_pid := resolver.findPIDForUDPPort(source_port)
	if owner_pid == 0 {
		return ""
	}
	return resolver.getProcessName(owner_pid)
}

// findPIDForUDPPort scans the system UDP table to find which PID owns a local port
func (resolver *ProcessResolver) findPIDForUDPPort(target_port uint16) uint32 {
	// First call to get required buffer size
	var table_size uint32
	procGetExtendedUdpTable.Call(0, uintptr(unsafe.Pointer(&table_size)), 0,
		afINET, udpTableOwnerPID, 0)

	if table_size == 0 {
		return 0
	}

	table_buffer := make([]byte, table_size)
	return_code, _, _ := procGetExtendedUdpTable.Call(
		uintptr(unsafe.Pointer(&table_buffer[0])),
		uintptr(unsafe.Pointer(&table_size)),
		0, // no sorting
		afINET,
		udpTableOwnerPID,
		0,
	)
	if return_code != 0 {
		return 0
	}

	// Parse the table: first 4 bytes = number of entries, then entries follow
	num_entries := *(*uint32)(unsafe.Pointer(&table_buffer[0]))
	entry_offset := 4 // skip dwNumEntries
	entry_size := int(unsafe.Sizeof(mibUDPRowOwnerPID{}))

	for idx_entry := uint32(0); idx_entry < num_entries; idx_entry++ {
		offset := entry_offset + int(idx_entry)*entry_size
		if offset+entry_size > len(table_buffer) {
			break
		}
		row := (*mibUDPRowOwnerPID)(unsafe.Pointer(&table_buffer[offset]))

		// Port is stored in network byte order (big-endian) in the lower 16 bits
		row_port := uint16((row.LocalPort >> 8) | ((row.LocalPort & 0xFF) << 8))
		if row_port == target_port {
			return row.OwningPID
		}
	}

	return 0
}

// getProcessName returns the executable name for a PID, using cache when possible
func (resolver *ProcessResolver) getProcessName(process_id uint32) string {
	// Check cache first
	resolver.mu.RLock()
	cached, exists := resolver.processNameCache[process_id]
	resolver.mu.RUnlock()

	if exists && time.Since(cached.cachedAt) < processNameCacheTTL {
		return cached.name
	}

	// Open process to query its name
	process_handle, open_err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, process_id)
	if open_err != nil {
		return ""
	}
	defer windows.CloseHandle(process_handle)

	// Get the executable path
	var exe_path_buffer [windows.MAX_PATH]uint16
	exe_path_length := uint32(len(exe_path_buffer))
	query_err := windows.QueryFullProcessImageName(process_handle, 0,
		&exe_path_buffer[0], &exe_path_length)
	if query_err != nil {
		return ""
	}

	full_path := windows.UTF16ToString(exe_path_buffer[:exe_path_length])
	exe_name := filepath.Base(full_path)

	// Cache the result
	resolver.mu.Lock()
	resolver.processNameCache[process_id] = cachedProcessName{
		name:     exe_name,
		cachedAt: time.Now(),
	}
	resolver.mu.Unlock()

	return exe_name
}
