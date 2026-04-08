package proxy

import (
	"encoding/binary"
	"net"
	"strings"
	"time"
)

// SNIParseResult holds the result of inspecting the first bytes of a connection
type SNIParseResult struct {
	ServerName    string   // TLS SNI hostname (empty if not TLS or SNI not found)
	IsTLS         bool     // Whether the connection appears to be TLS
	IsWebSocket   bool     // Whether a plaintext HTTP Upgrade: websocket was detected
	WebSocketPath string   // The request path of the WebSocket upgrade
	ProtocolHint  ProtocolHint
	PrefetchBytes []byte   // The bytes we read that must be replayed to the connection
}

// ParseConnectionProtocol reads the first bytes of a connection to detect TLS SNI
// or HTTP WebSocket upgrade. The prefetched bytes must be replayed using PrefixedConn.
func ParseConnectionProtocol(connection net.Conn) (*SNIParseResult, error) {
	prefetch_buffer := make([]byte, 1024)
	connection.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	bytes_read_count, read_error := connection.Read(prefetch_buffer)
	connection.SetReadDeadline(time.Time{}) // clear deadline

	if bytes_read_count == 0 {
		return &SNIParseResult{
			ProtocolHint:  ProtocolHintPlain,
			PrefetchBytes: nil,
		}, read_error
	}

	prefetch_data := prefetch_buffer[:bytes_read_count]
	parse_result := &SNIParseResult{
		PrefetchBytes: prefetch_data,
		ProtocolHint:  ProtocolHintPlain,
	}

	// Check for TLS record header: ContentType=0x16 (Handshake)
	if bytes_read_count >= 5 && prefetch_data[0] == 0x16 {
		parse_result.IsTLS = true
		parse_result.ProtocolHint = ProtocolHintTLS
		parse_result.ServerName = extractSNIFromClientHello(prefetch_data)
	} else {
		// Check for plaintext HTTP WebSocket upgrade
		is_websocket, ws_path := detectHTTPWebSocketUpgrade(prefetch_data)
		if is_websocket {
			parse_result.IsWebSocket = true
			parse_result.WebSocketPath = ws_path
			parse_result.ProtocolHint = ProtocolHintWebSocket
		}
	}

	return parse_result, nil
}

// extractSNIFromClientHello parses a TLS ClientHello message to extract the SNI hostname.
// Returns empty string if SNI is not found or the data is malformed.
func extractSNIFromClientHello(data []byte) string {
	data_length := len(data)

	// TLS record header: 5 bytes (type[1] + version[2] + length[2])
	if data_length < 5 {
		return ""
	}

	// Handshake header starts at offset 5
	// HandshakeType[1] + Length[3] + ClientVersion[2] + Random[32]
	handshake_offset := 5
	if data_length < handshake_offset+1 {
		return ""
	}

	// Verify it's a ClientHello (type 0x01)
	if data[handshake_offset] != 0x01 {
		return ""
	}

	// Skip: HandshakeType[1] + Length[3] + ClientVersion[2] + Random[32] = 38 bytes
	current_offset := handshake_offset + 38
	if current_offset >= data_length {
		return ""
	}

	// Session ID: length[1] + data[variable]
	session_id_length := int(data[current_offset])
	current_offset += 1 + session_id_length
	if current_offset+2 > data_length {
		return ""
	}

	// Cipher Suites: length[2] + data[variable]
	cipher_suites_length := int(binary.BigEndian.Uint16(data[current_offset:]))
	current_offset += 2 + cipher_suites_length
	if current_offset+1 > data_length {
		return ""
	}

	// Compression Methods: length[1] + data[variable]
	compression_methods_length := int(data[current_offset])
	current_offset += 1 + compression_methods_length
	if current_offset+2 > data_length {
		return ""
	}

	// Extensions: length[2] + data[variable]
	extensions_total_length := int(binary.BigEndian.Uint16(data[current_offset:]))
	current_offset += 2
	extensions_end := current_offset + extensions_total_length
	if extensions_end > data_length {
		extensions_end = data_length
	}

	// Walk through extensions looking for SNI (type 0x0000)
	for current_offset+4 <= extensions_end {
		extension_type := binary.BigEndian.Uint16(data[current_offset:])
		extension_data_length := int(binary.BigEndian.Uint16(data[current_offset+2:]))
		current_offset += 4

		if extension_type == 0x0000 { // server_name extension
			// SNI extension data: ServerNameListLength[2] + ServerNameType[1] + HostNameLength[2] + HostName
			if current_offset+5 > extensions_end {
				return ""
			}
			// Skip ServerNameListLength[2]
			sni_offset := current_offset + 2
			// ServerNameType should be 0 (host_name)
			if data[sni_offset] != 0x00 {
				return ""
			}
			sni_offset++
			hostname_length := int(binary.BigEndian.Uint16(data[sni_offset:]))
			sni_offset += 2
			if sni_offset+hostname_length > extensions_end {
				return ""
			}
			return string(data[sni_offset : sni_offset+hostname_length])
		}

		current_offset += extension_data_length
	}

	return ""
}

// detectHTTPWebSocketUpgrade checks if the given bytes represent an HTTP request
// with a WebSocket upgrade header. Only works on plaintext (non-TLS) connections.
func detectHTTPWebSocketUpgrade(data []byte) (bool, string) {
	data_as_string := string(data)

	// Must start with an HTTP method
	if !strings.HasPrefix(data_as_string, "GET ") {
		return false, ""
	}

	// Extract the request path (between "GET " and " HTTP/")
	request_path := ""
	http_version_index := strings.Index(data_as_string, " HTTP/")
	if http_version_index > 4 {
		request_path = data_as_string[4:http_version_index]
	}

	// Look for the Upgrade: websocket header (case-insensitive)
	lowercase_data := strings.ToLower(data_as_string)
	has_upgrade_header := strings.Contains(lowercase_data, "upgrade: websocket")

	if has_upgrade_header {
		return true, request_path
	}

	return false, ""
}
