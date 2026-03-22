package ipc

import (
	"encoding/json"
	"fmt"
)

// Service name and pipe path
const (
	ServiceName = "VPNMultiTunnelService"
	PipeName    = `\\.\pipe\VPNMultiTunnelService`
)

// Operation types supported by the service
type Operation string

const (
	// Loopback IP operations
	OpAddLoopbackIP     Operation = "add_loopback_ip"
	OpRemoveLoopbackIP  Operation = "remove_loopback_ip"
	OpEnsureLoopbackIPs Operation = "ensure_loopback_ips"

	// DNS operations
	OpSetDNS             Operation = "set_dns"
	OpSetDNSv6           Operation = "set_dns_v6"
	OpResetDNS           Operation = "reset_dns"
	OpConfigureSystemDNS Operation = "configure_system_dns"
	OpRestoreSystemDNS   Operation = "restore_system_dns"

	// DNS Client Service operations
	OpStopDNSClient  Operation = "stop_dns_client"
	OpStartDNSClient Operation = "start_dns_client"

	// Health check
	OpPing Operation = "ping"
)

// Request represents an IPC request from the GUI to the service
type Request struct {
	Operation Operation              `json:"operation"`
	Params    map[string]interface{} `json:"params,omitempty"`
}

// Response represents an IPC response from the service to the GUI
type Response struct {
	Success bool                   `json:"success"`
	Error   string                 `json:"error,omitempty"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// NewRequest creates a new request
func NewRequest(op Operation) *Request {
	return &Request{
		Operation: op,
		Params:    make(map[string]interface{}),
	}
}

// SetParam sets a parameter
func (r *Request) SetParam(key string, value interface{}) *Request {
	r.Params[key] = value
	return r
}

// GetString gets a string parameter
func (r *Request) GetString(key string) (string, error) {
	v, ok := r.Params[key]
	if !ok {
		return "", fmt.Errorf("parameter %s not found", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s is not a string", key)
	}
	return s, nil
}

// GetStringSlice gets a string slice parameter
func (r *Request) GetStringSlice(key string) ([]string, error) {
	v, ok := r.Params[key]
	if !ok {
		return nil, fmt.Errorf("parameter %s not found", key)
	}

	// Handle different possible types
	switch val := v.(type) {
	case []string:
		return val, nil
	case []interface{}:
		result := make([]string, len(val))
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("parameter %s contains non-string element", key)
			}
			result[i] = s
		}
		return result, nil
	default:
		return nil, fmt.Errorf("parameter %s is not a string slice", key)
	}
}

// SuccessResponse creates a success response
func SuccessResponse() *Response {
	return &Response{
		Success: true,
		Data:    make(map[string]interface{}),
	}
}

// ErrorResponse creates an error response
func ErrorResponse(err error) *Response {
	return &Response{
		Success: false,
		Error:   err.Error(),
	}
}

// SetData sets response data
func (r *Response) SetData(key string, value interface{}) *Response {
	if r.Data == nil {
		r.Data = make(map[string]interface{})
	}
	r.Data[key] = value
	return r
}

// Encode encodes the request to JSON bytes
func (r *Request) Encode() ([]byte, error) {
	return json.Marshal(r)
}

// Encode encodes the response to JSON bytes
func (r *Response) Encode() ([]byte, error) {
	return json.Marshal(r)
}

// DecodeRequest decodes a request from JSON bytes
func DecodeRequest(data []byte) (*Request, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("failed to decode request: %w", err)
	}
	return &req, nil
}

// DecodeResponse decodes a response from JSON bytes
func DecodeResponse(data []byte) (*Response, error) {
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &resp, nil
}
