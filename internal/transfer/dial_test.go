package transfer

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// TestMetadatenDienstIstUnerreichbar ist die Kernzusage dieses Pakets.
//
// Der Kunde gibt die Adresse eines Backup-Ziels ein, und der Panel-Server baut
// die Verbindung auf. 169.254.169.254 ist bei praktisch jedem Cloud-Anbieter
// der Metadaten-Dienst; eine Anfrage dorthin liefert die Zugangsschlüssel der
// Maschine. Ein Backup-Ziel mit dieser Adresse wäre kein Backup-Ziel, sondern
// ein Ausleseversuch — und ohne diese Prüfung würde er beantwortet.
func TestMetadatenDienstIstUnerreichbar(t *testing.T) {
	abgelehnt := []struct{ addr, enthält string }{
		{"169.254.169.254", "link-local"},     // AWS, GCP, Azure, Hetzner, DO
		{"fe80::1", "link-local"},             // dasselbe über IPv6
		{"127.0.0.1", "diesen server"},        // das Panel selbst
		{"::1", "diesen server"},              // dasselbe über IPv6
		{"::ffff:127.0.0.1", "diesen server"}, // Loopback als IPv6 verkleidet
		{"0.0.0.0", "kein ziel"},
		{"224.0.0.1", "multicast"},
	}
	for _, tc := range abgelehnt {
		addr, err := netip.ParseAddr(tc.addr)
		if err != nil {
			t.Fatalf("%s: %v", tc.addr, err)
		}
		err = CheckAddr(addr)
		if err == nil {
			t.Errorf("%s wurde als ziel zugelassen", tc.addr)
			continue
		}
		if !strings.Contains(err.Error(), tc.enthält) {
			t.Errorf("%s abgelehnt, aber aus dem falschen Grund: %v", tc.addr, err)
		}
	}

	// Öffentliche und private Adressen bleiben erlaubt: ein MinIO im selben
	// Rechenzentrumsnetz ist ein üblicher Aufbewahrungsort für Backups.
	erlaubt := []string{"93.184.216.34", "10.0.0.5", "192.168.1.10", "2606:2800:220:1::1"}
	for _, a := range erlaubt {
		addr, _ := netip.ParseAddr(a)
		if err := CheckAddr(addr); err != nil {
			t.Errorf("%s wurde abgelehnt: %v", a, err)
		}
	}
}

// TestPruefungHaengtAmVerbindungsaufbau: die Prüfung sitzt in Control, also
// hinter der Namensauflösung und vor connect(). Das ist der Unterschied
// zwischen "der Name zeigte auf etwas Erlaubtes" und "die Verbindung ging
// dorthin" — wer den DNS-Eintrag stellt, kann dazwischen die Antwort wechseln.
//
// Geprüft wird an einem echten Lauscher: käme die Verbindung durch, würde er
// sie annehmen.
func TestPruefungHaengtAmVerbindungsaufbau(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	angenommen := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			angenommen <- struct{}{}
			c.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := DialContext(ctx, "tcp", ln.Addr().String())
	if err == nil {
		conn.Close()
		t.Fatal("die Verbindung zu 127.0.0.1 kam zustande")
	}
	if !strings.Contains(err.Error(), "diesen server") {
		t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
	}

	select {
	case <-angenommen:
		t.Error("der Lauscher hat die Verbindung angenommen — es wurde verbunden, " +
			"bevor geprüft wurde")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestNurTCP: ein anderes Protokoll — udp etwa — käme an der Adressprüfung
// vorbei, wenn sie nur für tcp gälte.
func TestNurTCP(t *testing.T) {
	if err := controlAddr("udp", "93.184.216.34:53", nil); err == nil {
		t.Error("udp wurde zugelassen")
	}
	if err := controlAddr("unix", "/var/run/etwas.sock", nil); err == nil {
		t.Error("ein unix-socket wurde zugelassen")
	}
}
