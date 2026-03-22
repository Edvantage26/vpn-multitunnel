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
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		return fmt.Errorf("server already running")
	}

	mux := http.NewServeMux()

	// Register endpoints
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/host-mappings", s.handleHostMappings)
	mux.HandleFunc("/api/test-host", s.handleTestHost)
	mux.HandleFunc("/api/diagnose-dns", s.handleDiagnoseDNS)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/logs/frontend", s.handleFrontendLogs)
	mux.HandleFunc("/api/errors", s.handleErrors)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/diagnostic", s.handleDiagnostic)
	mux.HandleFunc("/api/dns-query", s.handleDNSQuery)
	mux.HandleFunc("/api/dns-configure", s.handleDNSConfigure)
	mux.HandleFunc("/api/dns-restore", s.handleDNSRestore)
	mux.HandleFunc("/api/vpn-connect", s.handleVPNConnect)
	mux.HandleFunc("/api/vpn-disconnect", s.handleVPNDisconnect)
	mux.HandleFunc("/api/health", s.handleHealth)

	// CORS middleware
	handler := corsMiddleware(mux)

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	s.server = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("Debug API server error: %v", err)
		}
	}()

	log.Printf("Debug API server started on http://%s", addr)
	debug.Info("api", "Debug API server started", map[string]any{"port": s.port})

	return nil
}

// Stop stops the debug API server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := s.server.Shutdown(ctx)
	s.server = nil

	debug.Info("api", "Debug API server stopped", nil)
	return err
}

// corsMiddleware adds CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeSuccess writes a successful JSON response
func writeSuccess(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, debug.APIResponse{
		Success: true,
		Data:    data,
	})
}

// writeError writes an error JSON response
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, debug.APIResponse{
		Success: false,
		Error:   message,
	})
}

// handleHealth handles GET /api/health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	writeSuccess(w, map[string]string{"status": "ok"})
}

// handleStatus handles GET /api/status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	status := map[string]any{
		"vpns":      s.provider.GetVPNStatusList(),
		"dns":       s.provider.GetDNSConfig(),
		"tcpProxy":  s.provider.GetTCPProxyInfo(),
		"system":    s.provider.GetSystemInfo(),
	}

	writeSuccess(w, status)
}

// handleHostMappings handles GET /api/host-mappings
func (s *Server) handleHostMappings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	mappings := s.provider.GetHostMappings()
	writeSuccess(w, mappings)
}

// handleTestHost handles POST /api/test-host
func (s *Server) handleTestHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Hostname     string `json:"hostname"`
		Port         int    `json:"port"`
		ProfileID    string `json:"profileId"`
		UseSystemDNS bool   `json:"useSystemDNS"` // If true, resolve via system DNS (same path as apps)
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	if req.Port == 0 {
		req.Port = 443 // Default port
	}

	result := s.provider.TestHost(req.Hostname, req.Port, req.ProfileID, req.UseSystemDNS)
	writeSuccess(w, result)
}

// handleDiagnoseDNS handles POST /api/diagnose-dns
func (s *Server) handleDiagnoseDNS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Hostname string `json:"hostname"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	diagnostic := s.provider.DiagnoseDNS(req.Hostname)
	writeSuccess(w, diagnostic)
}

// handleLogs handles GET /api/logs
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse query parameters
	query := r.URL.Query()
	level := debug.LogLevel(query.Get("level"))
	component := query.Get("component")
	profileID := query.Get("profileId")
	limitStr := query.Get("limit")

	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	var logs []debug.LogEntry
	if level != "" || component != "" || profileID != "" {
		logs = debug.GetLogger().GetLogsFiltered(level, component, profileID, limit)
	} else {
		logs = debug.GetLogger().GetLogs(limit)
	}

	writeSuccess(w, logs)
}

// handleFrontendLogs handles GET /api/logs/frontend - returns only frontend logs
func (s *Server) handleFrontendLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Filter for frontend logs (component starts with "frontend:")
	allLogs := debug.GetLogger().GetLogs(limit * 2) // Get more to filter
	frontendLogs := make([]debug.LogEntry, 0)
	for _, log := range allLogs {
		if strings.HasPrefix(log.Component, "frontend:") {
			frontendLogs = append(frontendLogs, log)
			if len(frontendLogs) >= limit {
				break
			}
		}
	}

	writeSuccess(w, frontendLogs)
}

// handleErrors handles GET /api/errors
func (s *Server) handleErrors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	errors := debug.GetErrorCollector().GetRecent(limit)
	writeSuccess(w, errors)
}

// handleMetrics handles GET /api/metrics
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	metrics := debug.GetMetricsCollector().GetAllMetrics()
	writeSuccess(w, metrics)
}

// handleDiagnostic handles POST /api/diagnostic
func (s *Server) handleDiagnostic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	report := s.provider.GenerateDiagnosticReport()
	writeSuccess(w, report)
}

// handleDNSQuery handles POST /api/dns-query
func (s *Server) handleDNSQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Hostname  string `json:"hostname"`
		QueryType string `json:"queryType"` // A, AAAA, ANY, etc.
		DNSServer string `json:"dnsServer"`
		ProfileID string `json:"profileId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	if req.ProfileID == "" {
		writeError(w, http.StatusBadRequest, "profileId is required")
		return
	}

	if req.QueryType == "" {
		req.QueryType = "A"
	}

	result := s.provider.QueryDNS(req.Hostname, req.QueryType, req.DNSServer, req.ProfileID)
	writeSuccess(w, result)
}

// handleDNSConfigure handles POST /api/dns-configure
func (s *Server) handleDNSConfigure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	result := s.provider.ConfigureDNS()
	writeSuccess(w, result)
}

// handleDNSRestore handles POST /api/dns-restore
func (s *Server) handleDNSRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	err := s.provider.RestoreDNS()
	if err != nil {
		writeSuccess(w, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeSuccess(w, map[string]any{"success": true})
}

// handleVPNConnect handles POST /api/vpn-connect
func (s *Server) handleVPNConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		ProfileID string `json:"profileId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ProfileID == "" {
		writeError(w, http.StatusBadRequest, "profileId is required")
		return
	}
	err := s.provider.Connect(req.ProfileID)
	if err != nil {
		writeSuccess(w, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeSuccess(w, map[string]any{"success": true, "profileId": req.ProfileID})
}

// handleVPNDisconnect handles POST /api/vpn-disconnect
func (s *Server) handleVPNDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		ProfileID string `json:"profileId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ProfileID == "" {
		writeError(w, http.StatusBadRequest, "profileId is required")
		return
	}
	err := s.provider.Disconnect(req.ProfileID)
	if err != nil {
		writeSuccess(w, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeSuccess(w, map[string]any{"success": true, "profileId": req.ProfileID})
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
