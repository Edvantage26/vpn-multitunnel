package proxy

import (
	"io"
	"net"
	"sync/atomic"
)

// CountingWriter wraps a writer and atomically counts bytes written
type CountingWriter struct {
	destination  io.Writer
	bytesWritten *atomic.Int64
}

// NewCountingWriter creates a new CountingWriter wrapping the given destination
func NewCountingWriter(destination io.Writer, counter *atomic.Int64) *CountingWriter {
	return &CountingWriter{
		destination:  destination,
		bytesWritten: counter,
	}
}

// Write writes data to the destination and increments the byte counter
func (counting_writer *CountingWriter) Write(buffer_data []byte) (int, error) {
	bytes_written_count, write_error := counting_writer.destination.Write(buffer_data)
	counting_writer.bytesWritten.Add(int64(bytes_written_count))
	return bytes_written_count, write_error
}

// PrefixedConn wraps a net.Conn and prepends prefetched bytes before the real connection data.
// This is used after SNI/HTTP sniffing to replay the bytes we already read.
type PrefixedConn struct {
	net.Conn
	prefetch_reader io.Reader
}

// NewPrefixedConn creates a connection that first reads from the prefetch buffer,
// then seamlessly transitions to reading from the underlying connection
func NewPrefixedConn(original_conn net.Conn, prefetch_data []byte) *PrefixedConn {
	return &PrefixedConn{
		Conn:            original_conn,
		prefetch_reader: io.MultiReader(io.LimitReader(newBytesReader(prefetch_data), int64(len(prefetch_data))), original_conn),
	}
}

// Read reads from the prefetch buffer first, then from the underlying connection
func (prefixed_conn *PrefixedConn) Read(buffer []byte) (int, error) {
	return prefixed_conn.prefetch_reader.Read(buffer)
}

// bytesReader is a simple bytes.Reader replacement to avoid importing bytes package just for this
type bytesReader struct {
	data   []byte
	offset int
}

func newBytesReader(data []byte) *bytesReader {
	return &bytesReader{data: data}
}

func (reader *bytesReader) Read(buffer []byte) (int, error) {
	if reader.offset >= len(reader.data) {
		return 0, io.EOF
	}
	bytes_copied := copy(buffer, reader.data[reader.offset:])
	reader.offset += bytes_copied
	return bytes_copied, nil
}
