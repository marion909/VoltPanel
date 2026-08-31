package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMkdirKeepsSetgidAndDropsSetuid deckt die Rechte ab, an denen die erste
// ausgelieferte Site gescheitert ist.
//
// setgid muss durchkommen: nur so behalten Dateien, die PHP im
// Dokumentenstamm anlegt, die Gruppe des Webservers — sonst hängt die
// Lesbarkeit an der umask des FPM-Pools. setuid dagegen bewirkt auf einem
// Verzeichnis unter Linux nichts und hat hier nichts verloren.
func TestMkdirKeepsSetgidAndDropsSetuid(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{roots: []string{dir}}

	cases := []struct {
		name    string
		mode    uint32
		setgid  bool
		perm    os.FileMode
		comment string
	}{
		{"mit setgid", 0o2750, true, 0o750, "Dokumentenstamm einer Site"},
		{"ohne", 0o750, false, 0o750, "tmp und logs"},
		{"setuid wird verworfen", 0o4750, false, 0o750, "auf Verzeichnissen wirkungslos"},
		{"sticky wird verworfen", 0o1750, false, 0o750, "nicht vorgesehen"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			raw, _ := json.Marshal(FileMkdirParams{Path: path, Mode: tc.mode})
			if _, err := srv.opFileMkdir(context.Background(), raw); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode()&os.ModeSetgid != 0; got != tc.setgid {
				t.Errorf("setgid=%v, erwartet %v (%s)", got, tc.setgid, tc.comment)
			}
			if info.Mode()&os.ModeSetuid != 0 {
				t.Error("setuid wurde übernommen")
			}
			if perm := info.Mode().Perm(); perm != tc.perm {
				t.Errorf("Rechte %o, erwartet %o", perm, tc.perm)
			}
		})
	}
}

// TestMkdirDefaultsToPrivate: ohne Angabe darf nichts weltlesbar entstehen.
func TestMkdirDefaultsToPrivate(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{roots: []string{dir}}

	path := filepath.Join(dir, "ohne-modus")
	raw, _ := json.Marshal(FileMkdirParams{Path: path})
	if _, err := srv.opFileMkdir(context.Background(), raw); err != nil {
		t.Fatal(err)
	}

	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o750 {
		t.Errorf("Vorgabe %o, erwartet 750", perm)
	}
}
