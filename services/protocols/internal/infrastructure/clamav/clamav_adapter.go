package clamav

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/valueobject"
)

type ClamAVAdapter struct {
	address string
	timeout time.Duration
}

func NewClamAVAdapter(address string) *ClamAVAdapter {
	if address == "" {
		address = "localhost:3310"
	}
	return &ClamAVAdapter{
		address: address,
		timeout: 10 * time.Second,
	}
}

func (a *ClamAVAdapter) Scan(ctx context.Context, input port.ScanInput) (*valueobject.ScanResult, error) {
	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.address)
	if err != nil {
		return nil, fmt.Errorf("connect to clamd at %s: %w", a.address, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(a.timeout))
	}

	// Send zINSTREAM command
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return nil, fmt.Errorf("write zINSTREAM: %w", err)
	}

	// Stream payload in chunks of up to 4096 bytes
	chunkSize := 4096
	data := input.Payload
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]

		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, uint32(len(chunk)))

		if _, err := conn.Write(lenBuf); err != nil {
			return nil, fmt.Errorf("write chunk length: %w", err)
		}
		if _, err := conn.Write(chunk); err != nil {
			return nil, fmt.Errorf("write chunk data: %w", err)
		}
	}

	// Write zero-length terminator chunk
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return nil, fmt.Errorf("write eof chunk: %w", err)
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.CloseWrite()
	}

	// Read clamd response
	reader := bufio.NewReader(conn)
	respStr, err := reader.ReadString('\x00')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read clamd response: %w", err)
	}
	respStr = strings.TrimRight(respStr, "\x00\r\n")

	if strings.HasSuffix(respStr, "OK") {
		res := valueobject.NewCleanScanResult()
		res.HeadersToAdd["X-Virus-Scanned"] = "ClamAV clean"
		return res, nil
	}

	if strings.HasSuffix(respStr, "FOUND") {
		// Response format: "stream: EICAR-Test-Signature FOUND"
		parts := strings.Split(respStr, ":")
		virusName := "Unknown"
		if len(parts) >= 2 {
			virusName = strings.TrimSuffix(strings.TrimSpace(parts[1]), " FOUND")
		}
		return valueobject.NewVirusScanResult(virusName), nil
	}

	return nil, fmt.Errorf("unexpected clamd response: %q", respStr)
}
