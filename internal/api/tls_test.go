package api

import (
	"testing"

	"github.com/marion909/voltpanel/internal/config"
)

// TestSniCertsEvict deckt den Fund ab, dass der SNI-Zertifikats-Cache
// (sniCerts.by) bei jedem erstmaligen TLS-Handshake für eine Anmeldedomain
// einen neuen Eintrag bekam, aber nichts ihn je wieder entfernte — auch
// nicht, wenn die zugehörige Anmeldedomain später aus dem Tenant-Datensatz
// gelöscht oder geändert wurde. Der Eintrag (ein *certReloader) blieb dann
// für die Prozesslaufzeit im Speicher.
func TestSniCertsEvict(t *testing.T) {
	s := &sniCerts{
		cfg:   &config.Config{CertDir: t.TempDir()},
		known: func(string) bool { return true },
		by:    map[string]*certReloader{},
	}

	// certFor legt einen Eintrag an, auch wenn keine Zertifikatsdatei existiert
	// (load() scheitert dann nur beim eigentlichen Lesen) — genau das
	// Verhalten, das den Cache bei jedem erstmaligen Handshake füllt.
	s.certFor("Login.example.at")
	if len(s.by) != 1 {
		t.Fatalf("certFor hat keinen Eintrag angelegt: %v", s.by)
	}

	s.evict("login.example.at")
	if len(s.by) != 0 {
		t.Errorf("evict hat den Eintrag nicht entfernt: %v", s.by)
	}
}

// TestSniCertsEvictUnbekannterNameIstNoop: ein Name, der nie im Cache stand
// (nie ein Handshake dafür, oder ein Tippfehler), darf evict nicht zum
// Absturz bringen.
func TestSniCertsEvictUnbekannterNameIstNoop(t *testing.T) {
	s := &sniCerts{by: map[string]*certReloader{}}
	s.evict("nie-gesehen.example.at")
	s.evict("")
}
