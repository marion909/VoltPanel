package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHandleConnUeberlebtPipeliningOhneDatenverlust deckt den Fund ab, dass
// handleConn pro Anfrage einen neuen json.Decoder um denselben Reader legte.
// Ein Decoder puffert intern mehr, als ein einzelner Decode()-Aufruf
// verbraucht — landen zwei Anfragen in einem einzigen Read() (hier absichtlich
// erzwungen, indem beide Zeilen in einem Schreibvorgang gesendet werden), lag
// der Anfang der zweiten Anfrage bereits im internen Puffer des ersten
// Decoders. Der wurde nach der ersten Antwort weggeworfen und durch einen
// neuen um einen frischen io.LimitReader ersetzt — der bereits gepufferte
// Rest war weg, und die zweite Antwort kam nie.
func TestHandleConnUeberlebtPipeliningOhneDatenverlust(t *testing.T) {
	// Kurzer Pfad unter /tmp, nicht t.TempDir(): AF_UNIX begrenzt sun_path auf
	// ~104 Zeichen, und t.TempDir() verschachtelt hier zu tief für den langen
	// Testnamen.
	dir, err := os.MkdirTemp("/tmp", "volt-pipeline")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	srv, err := NewServer(ServerOptions{
		SocketPath: filepath.Join(dir, "a.sock"),
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	conn, err := net.Dial("unix", srv.socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	if err := writeJSON(writer, Hello{Protocol: ProtocolVersion, Client: "test"}); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("handshake-antwort: %v", err)
	}
	var ack HelloAck
	if err := json.Unmarshal(line, &ack); err != nil || !ack.OK {
		t.Fatalf("handshake abgelehnt: err=%v ack=%+v", err, ack)
	}

	req1, err := json.Marshal(Request{ID: "1", Op: OpPing})
	if err != nil {
		t.Fatal(err)
	}
	req2, err := json.Marshal(Request{ID: "2", Op: OpPing})
	if err != nil {
		t.Fatal(err)
	}
	// Beide Zeilen in einem einzigen Schreibvorgang: so landen sie mit hoher
	// Wahrscheinlichkeit gemeinsam in einem Read() auf der Serverseite, statt
	// dass der Server die zweite erst nach einem separaten Read() sieht.
	both := append(append(append([]byte{}, req1...), '\n'), append(req2, '\n')...)
	if _, err := conn.Write(both); err != nil {
		t.Fatal(err)
	}

	for i, wantID := range []string{"1", "2"} {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("antwort %d: %v (die zweite Anfrage ging offenbar verloren)", i+1, err)
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("antwort %d unlesbar: %v", i+1, err)
		}
		if resp.ID != wantID {
			t.Fatalf("antwort %d hat id %q, erwartet %q", i+1, resp.ID, wantID)
		}
		if !resp.OK {
			t.Fatalf("antwort %d: %s", i+1, resp.Error)
		}
	}
}
