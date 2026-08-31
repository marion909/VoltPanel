package agent

import (
	"io"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// TestSocketCarriesPeerGroup deckt einen Fehler ab, der sich lange versteckt
// hat: os.Chown nimmt als drittes Argument die Gruppen-ID, gesetzt wurde aber
// die Benutzer-ID des Peers.
//
// Auf den meisten Systemen fällt das nicht auf, weil useradd --user-group
// Benutzer und Gruppe oft dieselbe Nummer gibt. Wo sie auseinanderlaufen,
// gehört der Socket einer fremden Gruppe, und der Panel-Prozess bekommt
// "permission denied" — obwohl alles richtig aussieht.
func TestSocketCarriesPeerGroup(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip("aktueller Benutzer nicht ermittelbar")
	}
	wantGID, err := strconv.Atoi(me.Gid)
	if err != nil {
		t.Skipf("gid %q nicht numerisch", me.Gid)
	}

	// Unix-Socket-Pfade sind auf rund 104 Zeichen begrenzt.
	dir, err := os.MkdirTemp("/tmp", "volt-sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	srv, err := NewServer(ServerOptions{
		SocketPath: filepath.Join(dir, "a.sock"),
		PeerUser:   me.Username,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		NginxDir:   dir, PHPDir: dir, CertDir: dir, SitesDir: dir, LogDir: dir,
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { srv.listener.Close() })

	info, err := os.Stat(srv.socketPath)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("Dateieigentümer auf diesem System nicht lesbar")
	}

	if int(st.Gid) != wantGID {
		t.Errorf("socket gehört gruppe %d, erwartet %d — der Panel-Benutzer käme nicht durch",
			st.Gid, wantGID)
	}
	// 0660: nur Eigentümer und Gruppe. Ein weltweit beschreibbarer Socket
	// hiesse, dass jeder lokale Benutzer den root-Agent ansprechen darf.
	if perm := info.Mode().Perm(); perm != 0o660 {
		t.Errorf("socket-rechte %o, erwartet 660", perm)
	}
}

// TestPeerLookupResolvesBothIDs hält fest, dass beide Nummern nachgeschlagen
// werden — nicht nur die des Benutzers.
func TestPeerLookupResolvesBothIDs(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip("aktueller Benutzer nicht ermittelbar")
	}

	srv, err := NewServer(ServerOptions{
		SocketPath: "/tmp/volt-unused.sock",
		PeerUser:   me.Username,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	if srv.peerUID < 0 {
		t.Error("peerUID wurde nicht aufgelöst")
	}
	if srv.peerGID < 0 {
		t.Error("peerGID wurde nicht aufgelöst")
	}
}
