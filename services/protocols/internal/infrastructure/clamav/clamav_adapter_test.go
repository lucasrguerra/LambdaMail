package clamav

import (
	"context"
	"net"
	"testing"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/valueobject"
)

func TestClamAVAdapter_Scan_CleanAndVirus(t *testing.T) {
	// Clean mock server
	cleanLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen clean server: %v", err)
	}
	defer cleanLn.Close()

	go func() {
		conn, err := cleanLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				break
			}
		}
		conn.Write([]byte("stream: OK\x00"))
	}()

	adapter := NewClamAVAdapter(cleanLn.Addr().String())
	res, err := adapter.Scan(context.Background(), port.ScanInput{Payload: []byte("clean content")})
	if err != nil || res.Verdict != valueobject.ScanVerdictClean {
		t.Fatalf("expected clean scan, got res=%+v, err=%v", res, err)
	}

	// Virus mock server
	virusLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen virus server: %v", err)
	}
	defer virusLn.Close()

	go func() {
		conn, err := virusLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				break
			}
		}
		conn.Write([]byte("stream: EICAR-Test-Signature FOUND\x00"))
	}()

	virusAdapter := NewClamAVAdapter(virusLn.Addr().String())
	res, err = virusAdapter.Scan(context.Background(), port.ScanInput{Payload: []byte("virus content")})
	if err != nil || res.Verdict != valueobject.ScanVerdictVirusReject || res.VirusName != "EICAR-Test-Signature" {
		t.Fatalf("expected virus scan, got res=%+v, err=%v", res, err)
	}
}
