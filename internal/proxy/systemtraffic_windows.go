package proxy

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
	"unsafe"
)

var (
	procGetExtendedTcpTable       = modIphlpapi.NewProc("GetExtendedTcpTable")
	procSetPerTcpConnectionEStats = modIphlpapi.NewProc("SetPerTcpConnectionEStats")
	procGetPerTcpConnectionEStats = modIphlpapi.NewProc("GetPerTcpConnectionEStats")
)

const (
	tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL — returns connections in ALL states
)

// TCP connection states
const (
	tcpStateClosed      = 1
	tcpStateListen      = 2
	tcpStateSynSent     = 3
	tcpStateSynReceived = 4
	tcpStateEstablished = 5
	tcpStateFinWait1    = 6
	tcpStateFinWait2    = 7
	tcpStateCloseWait   = 8
	tcpStateClosing     = 9
	tcpStateLastAck     = 10
	tcpStateTimeWait    = 11
	tcpStateDeleteTCB   = 12
)

// EStats API constants
const (
	tcpConnectionEstatsData = 2 // TCP_ESTATS_TYPE = TcpConnectionEstatsData
)

// mibTCPRowOwnerPID represents a single TCP connection with owning process ID
type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

// mibTCPRow is the 5-field row used by the EStats APIs (no PID field)
type mibTCPRow struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
}

// tcpEstatsDataRWv0 enables/disables stats collection on a connection
type tcpEstatsDataRWv0 struct {
	EnableCollection uint8
	Pad1             uint8
	Pad2             uint8
	Pad3             uint8
}

// tcpEstatsDataRODv0 contains read-only dynamic TCP byte counters
type tcpEstatsDataRODv0 struct {
	DataBytesOut      uint64
	DataSegsOut       uint64
	DataBytesIn       uint64
	DataSegsIn        uint64
	SegsOut           uint64
	SegsIn            uint64
	SoftErrors        uint32
	SoftErrorReason   uint32
	SndUna            uint32
	SndNxt            uint32
	SndMax            uint32
	ThruBytesAcked    uint64
	RcvNxt            uint32
	ThruBytesReceived uint64
}

// trackedSystemConnection holds metadata for a system TCP connection we're tracking
type trackedSystemConnection struct {
	connectionID      string
	remoteIP          string
	remotePort        uint16
	localPort         uint16
	processName       string
	processPID        uint32
	firstSeenAt       time.Time
	tcpRow            mibTCPRow // needed for EStats API calls
	lastSeenState     uint32
	lastBytesSent     int64
	lastBytesReceived int64
	statsEnabled      bool
	alreadyClosed     bool // true if we already reported this connection as closed
}

// SystemTrafficMonitor polls the Windows TCP table to detect non-tunnel TCP connections
type SystemTrafficMonitor struct {
	trackedConnections map[string]*trackedSystemConnection // connectionID -> tracked info
	trafficMonitor     *TrafficMonitor
	processResolver    *ProcessResolver
	loopbackPrefixes   []string // IP prefixes to exclude (tunnel traffic)
	enabled            bool
	stopChannel        chan struct{}
	mu                 sync.Mutex
}

var (
	systemTrafficInstance *SystemTrafficMonitor
	systemTrafficOnce     sync.Once
)

// GetSystemTrafficMonitor returns the singleton SystemTrafficMonitor
func GetSystemTrafficMonitor() *SystemTrafficMonitor {
	systemTrafficOnce.Do(func() {
		systemTrafficInstance = &SystemTrafficMonitor{
			trackedConnections: make(map[string]*trackedSystemConnection),
			trafficMonitor:     GetTrafficMonitor(),
			processResolver:    GetProcessResolver(),
			loopbackPrefixes:   []string{"127.", "0.0.0.0", "::1"},
			stopChannel:        make(chan struct{}),
		}
	})
	return systemTrafficInstance
}

// Start begins polling the TCP table for system connections
func (system_monitor *SystemTrafficMonitor) Start() {
	system_monitor.mu.Lock()
	if system_monitor.enabled {
		system_monitor.mu.Unlock()
		return
	}
	system_monitor.enabled = true
	system_monitor.stopChannel = make(chan struct{})
	system_monitor.mu.Unlock()

	log.Printf("SystemTrafficMonitor: started (polling every 1s)")
	go system_monitor.pollLoop()
}

// Stop halts the polling loop
func (system_monitor *SystemTrafficMonitor) Stop() {
	system_monitor.mu.Lock()
	defer system_monitor.mu.Unlock()

	if !system_monitor.enabled {
		return
	}
	system_monitor.enabled = false
	close(system_monitor.stopChannel)
	log.Printf("SystemTrafficMonitor: stopped")
}

// IsEnabled returns whether the monitor is currently running
func (system_monitor *SystemTrafficMonitor) IsEnabled() bool {
	system_monitor.mu.Lock()
	defer system_monitor.mu.Unlock()
	return system_monitor.enabled
}

// pollLoop periodically scans the TCP table and detects connection changes
func (system_monitor *SystemTrafficMonitor) pollLoop() {
	poll_ticker := time.NewTicker(1 * time.Second)
	defer poll_ticker.Stop()

	for {
		select {
		case <-system_monitor.stopChannel:
			return
		case <-poll_ticker.C:
			system_monitor.scanTCPTable()
		}
	}
}

// isClosingState returns true if the TCP state indicates the connection is closing/closed
func isClosingState(tcp_state uint32) bool {
	switch tcp_state {
	case tcpStateTimeWait, tcpStateCloseWait, tcpStateClosing,
		tcpStateFinWait1, tcpStateFinWait2, tcpStateLastAck,
		tcpStateClosed, tcpStateDeleteTCB:
		return true
	default:
		return false
	}
}

// scanTCPTable reads the current TCP table and detects new/closed connections
func (system_monitor *SystemTrafficMonitor) scanTCPTable() {
	raw_connections := system_monitor.getAllTCPConnections()
	if raw_connections == nil {
		return
	}

	system_monitor.mu.Lock()
	defer system_monitor.mu.Unlock()

	// Build a set of current connection IDs for quick lookup
	current_connection_map := make(map[string]*rawTCPConnection, len(raw_connections))
	for idx := range raw_connections {
		current_connection_map[raw_connections[idx].connectionID] = &raw_connections[idx]
	}

	// Phase 1: Detect NEW connections
	for conn_id, raw_conn := range current_connection_map {
		if _, already_tracked := system_monitor.trackedConnections[conn_id]; already_tracked {
			continue
		}

		tracked := &trackedSystemConnection{
			connectionID:  raw_conn.connectionID,
			remoteIP:      raw_conn.remoteIP,
			remotePort:    raw_conn.remotePort,
			localPort:     raw_conn.localPort,
			processName:   raw_conn.processName,
			processPID:    raw_conn.processPID,
			firstSeenAt:   time.Now(),
			tcpRow:        raw_conn.tcpRow,
			lastSeenState: raw_conn.state,
		}
		system_monitor.trackedConnections[conn_id] = tracked

		// Enable stats collection
		system_monitor.enableStatsCollection(tracked)

		// Record connection opened
		system_monitor.recordConnectionOpened(tracked)

		// If first seen already in a closing state → short-lived connection
		if isClosingState(raw_conn.state) {
			// Try to read final bytes before closing
			system_monitor.updateBytesFromEStats(tracked)
			system_monitor.recordConnectionClosed(tracked)
		}
	}

	// Phase 2: Update EXISTING tracked connections still in table
	for conn_id, tracked := range system_monitor.trackedConnections {
		if tracked.alreadyClosed {
			continue
		}

		raw_conn, still_in_table := current_connection_map[conn_id]
		if !still_in_table {
			// Connection disappeared from table — close with last known bytes
			system_monitor.recordConnectionClosed(tracked)
			continue
		}

		// Update bytes from EStats
		system_monitor.updateBytesFromEStats(tracked)

		// Detect state transition: ESTABLISHED → closing state
		previous_state := tracked.lastSeenState
		tracked.lastSeenState = raw_conn.state
		tracked.tcpRow.State = raw_conn.state

		if !isClosingState(previous_state) && isClosingState(raw_conn.state) {
			// Connection just closed — read final bytes and report
			system_monitor.updateBytesFromEStats(tracked)
			system_monitor.recordConnectionClosed(tracked)
		}
	}

	// Phase 3: Clean up connections that have been closed AND removed from table
	for conn_id, tracked := range system_monitor.trackedConnections {
		if tracked.alreadyClosed {
			if _, still_in_table := current_connection_map[conn_id]; !still_in_table {
				delete(system_monitor.trackedConnections, conn_id)
			}
		}
	}
}

// rawTCPConnection holds parsed data from one row of the TCP table
type rawTCPConnection struct {
	connectionID string
	remoteIP     string
	remotePort   uint16
	localPort    uint16
	processName  string
	processPID   uint32
	state        uint32
	tcpRow       mibTCPRow
}

// getAllTCPConnections queries the Windows TCP table for ALL connection states
func (system_monitor *SystemTrafficMonitor) getAllTCPConnections() []rawTCPConnection {
	// First call to get required buffer size
	var table_size uint32
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&table_size)), 0,
		afINET, tcpTableOwnerPIDAll, 0)

	if table_size == 0 {
		return nil
	}

	table_buffer := make([]byte, table_size)
	return_code, _, _ := procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&table_buffer[0])),
		uintptr(unsafe.Pointer(&table_size)),
		0, // no sorting
		afINET,
		tcpTableOwnerPIDAll,
		0,
	)
	if return_code != 0 {
		return nil
	}

	// Parse the table: first 4 bytes = number of entries
	num_entries := *(*uint32)(unsafe.Pointer(&table_buffer[0]))
	entry_offset := 4
	entry_size := int(unsafe.Sizeof(mibTCPRowOwnerPID{}))

	var parsed_connections []rawTCPConnection

	for idx_entry := uint32(0); idx_entry < num_entries; idx_entry++ {
		offset := entry_offset + int(idx_entry)*entry_size
		if offset+entry_size > len(table_buffer) {
			break
		}
		row := (*mibTCPRowOwnerPID)(unsafe.Pointer(&table_buffer[offset]))

		// Skip LISTEN state (these are servers, not outgoing connections)
		if row.State == tcpStateListen {
			continue
		}

		// Skip SYN_RECEIVED (incoming connections to local servers)
		if row.State == tcpStateSynReceived {
			continue
		}

		remote_ip := uint32ToIPv4(row.RemoteAddr)
		local_ip := uint32ToIPv4(row.LocalAddr)

		// Skip loopback connections (tunnel traffic handled by existing monitor)
		if system_monitor.isLoopbackIP(remote_ip) || system_monitor.isLoopbackIP(local_ip) {
			continue
		}

		// Skip connections to 0.0.0.0 or unspecified
		if remote_ip == "0.0.0.0" {
			continue
		}

		remote_port := networkByteOrderPort(row.RemotePort)
		local_port := networkByteOrderPort(row.LocalPort)

		// Build a stable connection ID from the 4-tuple + PID
		conn_id := fmt.Sprintf("sys-%s:%d-%s:%d-%d",
			local_ip, local_port, remote_ip, remote_port, row.OwningPID)

		// Resolve process name
		process_name := system_monitor.processResolver.getProcessName(row.OwningPID)

		tcp_row := mibTCPRow{
			State:      row.State,
			LocalAddr:  row.LocalAddr,
			LocalPort:  row.LocalPort,
			RemoteAddr: row.RemoteAddr,
			RemotePort: row.RemotePort,
		}

		parsed_connections = append(parsed_connections, rawTCPConnection{
			connectionID: conn_id,
			remoteIP:     remote_ip,
			remotePort:   remote_port,
			localPort:    local_port,
			processName:  process_name,
			processPID:   row.OwningPID,
			state:        row.State,
			tcpRow:       tcp_row,
		})
	}

	return parsed_connections
}

// enableStatsCollection calls SetPerTcpConnectionEStats to enable byte tracking
func (system_monitor *SystemTrafficMonitor) enableStatsCollection(tracked *trackedSystemConnection) {
	rw_data := tcpEstatsDataRWv0{EnableCollection: 1}
	// We need the row in ESTABLISHED state for SetPerTcpConnectionEStats
	stats_row := tracked.tcpRow
	stats_row.State = tcpStateEstablished // API requires ESTABLISHED state in the row

	return_code, _, _ := procSetPerTcpConnectionEStats.Call(
		uintptr(unsafe.Pointer(&stats_row)),
		tcpConnectionEstatsData,
		uintptr(unsafe.Pointer(&rw_data)),
		0,
		uintptr(unsafe.Sizeof(rw_data)),
		0,
	)
	tracked.statsEnabled = (return_code == 0)
}

// updateBytesFromEStats reads current byte counters from Windows TCP stack
func (system_monitor *SystemTrafficMonitor) updateBytesFromEStats(tracked *trackedSystemConnection) {
	if !tracked.statsEnabled {
		return
	}

	bytes_sent, bytes_received, read_ok := system_monitor.readConnectionBytes(tracked.tcpRow)
	if !read_ok {
		return
	}

	tracked.lastBytesSent = bytes_sent
	tracked.lastBytesReceived = bytes_received

	// Emit update to traffic monitor (if connection is still active)
	if !tracked.alreadyClosed {
		system_monitor.trafficMonitor.UpdateConnectionBytes(
			tracked.connectionID, bytes_sent, bytes_received)
	}
}

// readConnectionBytes calls GetPerTcpConnectionEStats to get byte counters
func (system_monitor *SystemTrafficMonitor) readConnectionBytes(tcp_row mibTCPRow) (bytes_sent int64, bytes_received int64, success bool) {
	var rw_data tcpEstatsDataRWv0
	var rod_data tcpEstatsDataRODv0

	// API requires ESTABLISHED state in the row for lookup
	stats_row := tcp_row
	stats_row.State = tcpStateEstablished

	return_code, _, _ := procGetPerTcpConnectionEStats.Call(
		uintptr(unsafe.Pointer(&stats_row)),
		tcpConnectionEstatsData,
		uintptr(unsafe.Pointer(&rw_data)),
		0,
		uintptr(unsafe.Sizeof(rw_data)),
		0, 0, 0, // ROS params (not needed)
		uintptr(unsafe.Pointer(&rod_data)),
		0,
		uintptr(unsafe.Sizeof(rod_data)),
	)

	if return_code != 0 {
		return 0, 0, false
	}
	if rw_data.EnableCollection == 0 {
		return 0, 0, false
	}

	return int64(rod_data.DataBytesOut), int64(rod_data.DataBytesIn), true
}

// recordConnectionOpened notifies the TrafficMonitor of a new system connection
func (system_monitor *SystemTrafficMonitor) recordConnectionOpened(tracked *trackedSystemConnection) {
	traffic_entry := TrafficEntry{
		ConnectionID: tracked.connectionID,
		Hostname:     tracked.remoteIP,
		SNIHostname:  tracked.processName, // Store process name in SNI field for display
		RealIP:       tracked.remoteIP,
		ProfileID:    "", // Empty = system/internet traffic
		Port:         int(tracked.remotePort),
		ProtocolHint: guessProtocolFromPort(tracked.remotePort),
	}

	system_monitor.trafficMonitor.RecordConnectionOpen(traffic_entry)
}

// recordConnectionClosed notifies the TrafficMonitor that a system connection closed
func (system_monitor *SystemTrafficMonitor) recordConnectionClosed(tracked *trackedSystemConnection) {
	if tracked.alreadyClosed {
		return
	}
	tracked.alreadyClosed = true
	system_monitor.trafficMonitor.CloseConnection(
		tracked.connectionID, tracked.lastBytesSent, tracked.lastBytesReceived)
}

// isLoopbackIP checks if an IP is a loopback address
func (system_monitor *SystemTrafficMonitor) isLoopbackIP(ip_address string) bool {
	for _, prefix := range system_monitor.loopbackPrefixes {
		if len(ip_address) >= len(prefix) && ip_address[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// uint32ToIPv4 converts a uint32 IP address (in network byte order) to dotted string
func uint32ToIPv4(raw_addr uint32) string {
	ip_bytes := net.IPv4(
		byte(raw_addr),
		byte(raw_addr>>8),
		byte(raw_addr>>16),
		byte(raw_addr>>24),
	)
	return ip_bytes.String()
}

// networkByteOrderPort converts a port from network byte order uint32 to host uint16
func networkByteOrderPort(raw_port uint32) uint16 {
	return uint16((raw_port >> 8) | ((raw_port & 0xFF) << 8))
}

// guessProtocolFromPort returns a protocol hint based on well-known port numbers
func guessProtocolFromPort(port uint16) ProtocolHint {
	switch port {
	case 443, 8443:
		return ProtocolHintTLS
	case 80, 8080:
		return ProtocolHintPlain
	default:
		return ProtocolHintPlain
	}
}
