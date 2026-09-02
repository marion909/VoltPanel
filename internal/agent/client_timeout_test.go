package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallFeatureUsesPackageTimeout(t *testing.T) {
	socket := startSlowFeatureAgent(t, 50*time.Millisecond)
	client := &Client{socketPath: socket, timeout: 10 * time.Millisecond}

	got, err := client.InstallFeature(context.Background(), "opendkim")
	if err != nil {
		t.Fatalf("InstallFeature lief in den Default-Timeout: %v", err)
	}
	if got != "opendkim installiert" {
		t.Fatalf("InstallFeature = %q", got)
	}
}

func startSlowFeatureAgent(t *testing.T, delay time.Duration) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "volt-client-timeout")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ln.Close()

		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)

		var hello Hello
		if err := json.NewDecoder(reader).Decode(&hello); err != nil {
			return
		}
		if err := writeJSON(writer, HelloAck{Protocol: ProtocolVersion, Agent: "test", OK: true}); err != nil {
			return
		}

		var req Request
		if err := json.NewDecoder(reader).Decode(&req); err != nil {
			return
		}
		if req.Op != OpFeatureInstall {
			_ = writeJSON(writer, Response{ID: req.ID, Error: "unerwartete operation"})
			return
		}

		time.Sleep(delay)
		body, _ := json.Marshal(TextResult{Text: "opendkim installiert"})
		_ = writeJSON(writer, Response{ID: req.ID, OK: true, Result: body})
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return socket
}
