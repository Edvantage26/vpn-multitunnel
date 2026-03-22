package ipc

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/Microsoft/go-winio"
)

// RequestHandler is a function that handles IPC requests
type RequestHandler func(req *Request) *Response

// Server represents the IPC server that runs in the Windows service
type Server struct {
	listener net.Listener
	handler  RequestHandler
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
}

// NewServer creates a new IPC server
func NewServer(handler RequestHandler) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		handler: handler,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start starts the IPC server
func (s *Server) Start() error {
	// Create a security descriptor that allows local users to connect
	// SDDL: Allow Generic All to Authenticated Users and Local System
	cfg := &winio.PipeConfig{
		SecurityDescriptor: "D:(A;;GA;;;AU)(A;;GA;;;SY)",
		MessageMode:        false, // Use byte stream mode
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	}

	listener, err := winio.ListenPipe(PipeName, cfg)
	if err != nil {
		return fmt.Errorf("failed to create named pipe: %w", err)
	}

	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	log.Printf("IPC server started on %s", PipeName)

	// Accept connections in a goroutine
	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop stops the IPC server
func (s *Server) Stop() {
	s.cancel()

	s.mu.Lock()
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
	log.Printf("IPC server stopped")
}

// acceptLoop accepts incoming connections
func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				log.Printf("Failed to accept connection: %v", err)
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

// handleConnection handles a single client connection
func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		// Read message length (4 bytes, big-endian)
		lenBuf := make([]byte, 4)
		_, err := io.ReadFull(reader, lenBuf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Failed to read message length: %v", err)
			}
			return
		}

		msgLen := binary.BigEndian.Uint32(lenBuf)
		if msgLen > 1024*1024 { // Max 1MB message
			log.Printf("Message too large: %d bytes", msgLen)
			return
		}

		// Read message body
		msgBuf := make([]byte, msgLen)
		_, err = io.ReadFull(reader, msgBuf)
		if err != nil {
			log.Printf("Failed to read message body: %v", err)
			return
		}

		// Decode request
		req, err := DecodeRequest(msgBuf)
		if err != nil {
			log.Printf("Failed to decode request: %v", err)
			s.sendResponse(conn, ErrorResponse(err))
			continue
		}

		log.Printf("Received request: %s", req.Operation)

		// Handle request
		resp := s.handler(req)

		// Send response
		if err := s.sendResponse(conn, resp); err != nil {
			log.Printf("Failed to send response: %v", err)
			return
		}
	}
}

// sendResponse sends a response to the client
func (s *Server) sendResponse(conn net.Conn, resp *Response) error {
	data, err := resp.Encode()
	if err != nil {
		return fmt.Errorf("failed to encode response: %w", err)
	}

	// Write message length
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := conn.Write(lenBuf); err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}

	// Write message body
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to write message body: %w", err)
	}

	return nil
}
