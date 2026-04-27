package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpnmultitunnel/internal/debug"
)

// DebugProvider provides access to app state for debugging
type DebugProvider interface {
	// VPN Status
	GetVPNStatusList() []debug.VPNStatusInfo
	GetProfileNames() map[string]string // profileID -> name

	// Host mappings
	GetHostMappings() []debug.HostMappingInfo

	// DNS
	GetDNSConfig() debug.DNSConfigInfo
	DiagnoseDNS(hostname string) debug.DNSDiagnostic
	GetMatchingRule(hostname string) *debug.DNSRuleInfo
	QueryDNS(hostname string, queryType string, dnsServer string, profileID string) debug.DNSQueryResult

	// TCP Proxy
	GetTCPProxyInfo() debug.TCPProxyInfo

	// Testing
	TestHost(hostname string, port int, profileID string, useSystemDNS bool) debug.HostTestResult

	// DNS Control
	IsDNSConfigured() bool
	ConfigureDNS() debug.DNSConfigResult
	RestoreDNS() error

	// VPN Control
	Connect(id string) error
	Disconnect(id string) error
	GetConnectErrors() map[string]string

	// Master switch
	IsMasterEnabled() bool
	SetMasterEnabled(enabled bool) error

	// DNS health diagnostics
	GetDNSHealthIssue() string

	// OpenVPN
	GetOpenVPNStatusMap() map[string]any
	InstallOpenVPN() error

	// System
	GetSystemInfo() debug.SystemInfo

	// Full diagnostic
	GenerateDiagnosticReport() debug.DiagnosticReport
}

// Server is an HTTP server for the debug API
type Server struct {
	port     int
	provider DebugProvider
	server   *http.Server
	mu       sync.Mutex
}

// NewServer creates a new debug API server
func NewServer(port int, provider DebugProvider) *Server {
	return &Server{
		port:     port,
		provider: provider,
	}
}

// Start starts the debug API server
func (api_server *Server) Start() error {
	api_server.mu.Lock()
	defer api_server.mu.Unlock()

	if api_server.server != nil {
		return fmt.Errorf("server already running")
	}

	mux := http.NewServeMux()

	// Register endpoints
	mux.HandleFunc("/api/status", api_server.handleStatus)
	mux.HandleFunc("/api/host-mappings", api_server.handleHostMappings)
	mux.HandleFunc("/api/test-host", api_server.handleTestHost)
	mux.HandleFunc("/api/diagnose-dns", api_server.handleDiagnoseDNS)
	mux.HandleFunc("/api/logs", api_server.handleLogs)
	mux.HandleFunc("/api/logs/frontend", api_server.handleFrontendLogs)
	mux.HandleFunc("/api/errors", api_server.handleErrors)
	mux.HandleFunc("/api/metrics", api_server.handleMetrics)
	mux.HandleFunc("/api/diagnostic", api_server.handleDiagnostic)
	mux.HandleFunc("/api/dns-query", api_server.handleDNSQuery)
	mux.HandleFunc("/api/dns-configure", api_server.handleDNSConfigure)
	mux.HandleFunc("/api/dns-restore", api_server.handleDNSRestore)
	mux.HandleFunc("/api/vpn-connect", api_server.handleVPNConnect)
	mux.HandleFunc("/api/vpn-disconnect", api_server.handleVPNDisconnect)
	mux.HandleFunc("/api/connect-errors", api_server.handleConnectErrors)
	mux.HandleFunc("/api/health", api_server.handleHealth)
	mux.HandleFunc("/api/openvpn-status", api_server.handleOpenVPNStatus)
	mux.HandleFunc("/api/openvpn-upgrade", api_server.handleOpenVPNUpgrade)
	mux.HandleFunc("/api/master-status", api_server.handleMasterStatus)
	mux.HandleFunc("/api/master-set", api_server.handleMasterSet)

	// CORS middleware
	handler := corsMiddleware(mux)

	addr := fmt.Sprintf("127.0.0.1:%d", api_server.port)
	api_server.server = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	go func() {
		if err := api_server.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("Debug API server error: %v", err)
		}
	}()

	log.Printf("Debug API server started on http://%s", addr)
	debug.Info("api", "Debug API server started", map[string]any{"port": api_server.port})

	return nil
}

// Stop stops the debug API server
func (api_server *Server) Stop() error {
	api_server.mu.Lock()
	defer api_server.mu.Unlock()

	if api_server.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := api_server.server.Shutdown(ctx)
	api_server.server = nil

	debug.Info("api", "Debug API server stopped", nil)
	return err
}

// corsMiddleware adds CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response_writer http.ResponseWriter, http_request *http.Request) {
		response_writer.Header().Set("Access-Control-Allow-Origin", "*")
		response_writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		response_writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if http_request.Method == "OPTIONS" {
			response_writer.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(response_writer, http_request)
	})
}

// writeJSON writes a JSON response
func writeJSON(response_writer http.ResponseWriter, status int, data any) {
	response_writer.Header().Set("Content-Type", "application/json")
	response_writer.WriteHeader(status)
	json.NewEncoder(response_writer).Encode(data)
}

// writeSuccess writes a successful JSON response
func writeSuccess(response_writer http.ResponseWriter, data any) {
	writeJSON(response_writer, http.StatusOK, debug.APIResponse{
		Success: true,
		Data:    data,
	})
}

// writeError writes an error JSON response
func writeError(response_writer http.ResponseWriter, status int, message string) {
	writeJSON(response_writer, status, debug.APIResponse{
		Success: false,
		Error:   message,
	})
}

// handleHealth handles GET /api/health
func (api_server *Server) handleHealth(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	writeSuccess(response_writer, map[string]string{"status": "ok"})
}

// handleOpenVPNStatus handles GET /api/openvpn-status
func (api_server *Server) handleOpenVPNStatus(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	writeSuccess(response_writer, api_server.provider.GetOpenVPNStatusMap())
}

// handleOpenVPNUpgrade handles POST /api/openvpn-upgrade
func (api_server *Server) handleOpenVPNUpgrade(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	install_err := api_server.provider.InstallOpenVPN()
	if install_err != nil {
		writeSuccess(response_writer, map[string]any{"success": false, "error": install_err.Error()})
		return
	}
	writeSuccess(response_writer, map[string]any{"success": true})
}

// handleStatus handles GET /api/status
func (api_server *Server) handleStatus(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	status := map[string]any{
		"vpns":     api_server.provider.GetVPNStatusList(),
		"dns":      api_server.provider.GetDNSConfig(),
		"tcpProxy": api_server.provider.GetTCPProxyInfo(),
		"system":   api_server.provider.GetSystemInfo(),
	}

	writeSuccess(response_writer, status)
}

// handleHostMappings handles GET /api/host-mappings
func (api_server *Server) handleHostMappings(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	mappings := api_server.provider.GetHostMappings()
	writeSuccess(response_writer, mappings)
}

// handleTestHost handles POST /api/test-host
func (api_server *Server) handleTestHost(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Hostname     string `json:"hostname"`
		Port         int    `json:"port"`
		ProfileID    string `json:"profileId"`
		UseSystemDNS bool   `json:"useSystemDNS"` // If true, resolve via system DNS (same path as apps)
	}

	if err := json.NewDecoder(http_request.Body).Decode(&req); err != nil {
		writeError(response_writer, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Hostname == "" {
		writeError(response_writer, http.StatusBadRequest, "hostname is required")
		return
	}

	if req.Port == 0 {
		req.Port = 443 // Default port
	}

	result := api_server.provider.TestHost(req.Hostname, req.Port, req.ProfileID, req.UseSystemDNS)
	writeSuccess(response_writer, result)
}

// handleDiagnoseDNS handles POST /api/diagnose-dns
func (api_server *Server) handleDiagnoseDNS(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Hostname string `json:"hostname"`
	}

	if err := json.NewDecoder(http_request.Body).Decode(&req); err != nil {
		writeError(response_writer, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Hostname == "" {
		writeError(response_writer, http.StatusBadRequest, "hostname is required")
		return
	}

	diagnostic := api_server.provider.DiagnoseDNS(req.Hostname)
	writeSuccess(response_writer, diagnostic)
}

// handleLogs handles GET /api/logs
func (api_server *Server) handleLogs(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse query parameters
	query := http_request.URL.Query()
	level := debug.LogLevel(query.Get("level"))
	component := query.Get("component")
	profileID := query.Get("profileId")
	limitStr := query.Get("limit")

	limit := 100
	if limitStr != "" {
		if parsed_limit, err := strconv.Atoi(limitStr); err == nil && parsed_limit > 0 {
			limit = parsed_limit
		}
	}

	var logs []debug.LogEntry
	if level != "" || component != "" || profileID != "" {
		logs = debug.GetLogger().GetLogsFiltered(level, component, profileID, limit)
	} else {
		logs = debug.GetLogger().GetLogs(limit)
	}

	writeSuccess(response_writer, logs)
}

// handleFrontendLogs handles GET /api/logs/frontend - returns only frontend logs
func (api_server *Server) handleFrontendLogs(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	limitStr := http_request.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if parsed_limit, err := strconv.Atoi(limitStr); err == nil && parsed_limit > 0 {
			limit = parsed_limit
		}
	}

	// Filter for frontend logs (component starts with "frontend:")
	allLogs := debug.GetLogger().GetLogs(limit * 2) // Get more to filter
	frontendLogs := make([]debug.LogEntry, 0)
	for _, log_entry := range allLogs {
		if strings.HasPrefix(log_entry.Component, "frontend:") {
			frontendLogs = append(frontendLogs, log_entry)
			if len(frontendLogs) >= limit {
				break
			}
		}
	}

	writeSuccess(response_writer, frontendLogs)
}

// handleErrors handles GET /api/errors
func (api_server *Server) handleErrors(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	limitStr := http_request.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if parsed_limit, err := strconv.Atoi(limitStr); err == nil && parsed_limit > 0 {
			limit = parsed_limit
		}
	}

	errors := debug.GetErrorCollector().GetRecent(limit)
	writeSuccess(response_writer, errors)
}

// handleMetrics handles GET /api/metrics
func (api_server *Server) handleMetrics(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	metrics := debug.GetMetricsCollector().GetAllMetrics()
	writeSuccess(response_writer, metrics)
}

// handleDiagnostic handles POST /api/diagnostic
func (api_server *Server) handleDiagnostic(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost && http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	report := api_server.provider.GenerateDiagnosticReport()
	writeSuccess(response_writer, report)
}

// handleDNSQuery handles POST /api/dns-query
func (api_server *Server) handleDNSQuery(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Hostname  string `json:"hostname"`
		QueryType string `json:"queryType"` // A, AAAA, ANY, etc.
		DNSServer string `json:"dnsServer"`
		ProfileID string `json:"profileId"`
	}

	if err := json.NewDecoder(http_request.Body).Decode(&req); err != nil {
		writeError(response_writer, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Hostname == "" {
		writeError(response_writer, http.StatusBadRequest, "hostname is required")
		return
	}

	if req.ProfileID == "" {
		writeError(response_writer, http.StatusBadRequest, "profileId is required")
		return
	}

	if req.QueryType == "" {
		req.QueryType = "A"
	}

	result := api_server.provider.QueryDNS(req.Hostname, req.QueryType, req.DNSServer, req.ProfileID)
	writeSuccess(response_writer, result)
}

// handleDNSConfigure handles POST /api/dns-configure
func (api_server *Server) handleDNSConfigure(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	result := api_server.provider.ConfigureDNS()
	writeSuccess(response_writer, result)
}

// handleDNSRestore handles POST /api/dns-restore
func (api_server *Server) handleDNSRestore(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	err := api_server.provider.RestoreDNS()
	if err != nil {
		writeSuccess(response_writer, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeSuccess(response_writer, map[string]any{"success": true})
}

// handleVPNConnect handles POST /api/vpn-connect
func (api_server *Server) handleVPNConnect(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		ProfileID string `json:"profileId"`
	}
	if err := json.NewDecoder(http_request.Body).Decode(&req); err != nil {
		writeError(response_writer, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ProfileID == "" {
		writeError(response_writer, http.StatusBadRequest, "profileId is required")
		return
	}
	err := api_server.provider.Connect(req.ProfileID)
	if err != nil {
		writeSuccess(response_writer, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeSuccess(response_writer, map[string]any{"success": true, "profileId": req.ProfileID})
}

// handleVPNDisconnect handles POST /api/vpn-disconnect
func (api_server *Server) handleVPNDisconnect(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		ProfileID string `json:"profileId"`
	}
	if err := json.NewDecoder(http_request.Body).Decode(&req); err != nil {
		writeError(response_writer, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ProfileID == "" {
		writeError(response_writer, http.StatusBadRequest, "profileId is required")
		return
	}
	err := api_server.provider.Disconnect(req.ProfileID)
	if err != nil {
		writeSuccess(response_writer, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeSuccess(response_writer, map[string]any{"success": true, "profileId": req.ProfileID})
}

// handleMasterStatus handles GET /api/master-status — returns whether the master switch is on.
func (api_server *Server) handleMasterStatus(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	writeSuccess(response_writer, map[string]any{
		"enabled":        api_server.provider.IsMasterEnabled(),
		"dnsConfigured":  api_server.provider.IsDNSConfigured(),
		"dnsHealthIssue": api_server.provider.GetDNSHealthIssue(),
	})
}

// handleMasterSet handles POST /api/master-set — toggles the master switch.
// Body: {"enabled": bool}. OFF disconnects all and restores DNS; ON reconnects auto-connect profiles.
func (api_server *Server) handleMasterSet(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodPost {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var request_body struct {
		Enabled *bool `json:"enabled"`
	}
	if decode_err := json.NewDecoder(http_request.Body).Decode(&request_body); decode_err != nil {
		writeError(response_writer, http.StatusBadRequest, "Invalid request body")
		return
	}
	if request_body.Enabled == nil {
		writeError(response_writer, http.StatusBadRequest, "'enabled' field is required (true|false)")
		return
	}
	previous_state := api_server.provider.IsMasterEnabled()
	if set_err := api_server.provider.SetMasterEnabled(*request_body.Enabled); set_err != nil {
		writeSuccess(response_writer, map[string]any{
			"success":  false,
			"error":    set_err.Error(),
			"previous": previous_state,
			"current":  api_server.provider.IsMasterEnabled(),
		})
		return
	}
	writeSuccess(response_writer, map[string]any{
		"success":  true,
		"previous": previous_state,
		"current":  api_server.provider.IsMasterEnabled(),
	})
}

// handleConnectErrors handles GET /api/connect-errors
func (api_server *Server) handleConnectErrors(response_writer http.ResponseWriter, http_request *http.Request) {
	if http_request.Method != http.MethodGet {
		writeError(response_writer, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	writeSuccess(response_writer, api_server.provider.GetConnectErrors())
}

// FindMatchingRule finds a DNS rule that matches a hostname
func FindMatchingRule(hostname string, rules []debug.DNSRuleInfo) *debug.DNSRuleInfo {
	hostname = strings.ToLower(hostname)
	for _, rule := range rules {
		suffix := strings.ToLower(rule.Suffix)
		if strings.HasSuffix(hostname, suffix) || hostname == strings.TrimPrefix(suffix, ".") {
			return &rule
		}
	}
	return nil
}
