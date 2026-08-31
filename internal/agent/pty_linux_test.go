//go:build linux

package agent

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestPseudoterminalOeffnet prüft die drei ioctls, aus denen das Terminal
// entsteht. Sie brauchen keine Rechte — anders als die Shell dahinter, die
// setuid macht und deshalb nur auf einem echten Server läuft.
//
// Ein falsch geratener ioctl-Wert fiele sonst erst dort auf, und zwar als
// Terminal, das sich öffnen lässt und nichts tut.
func TestPseudoterminalOeffnet(t *testing.T) {
	master, slavePath, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY: %v", err)
	}
	defer master.Close()

	if !strings.HasPrefix(slavePath, "/dev/pts/") {
		t.Errorf("slave liegt unter %q, erwartet /dev/pts/…", slavePath)
	}
	if _, err := os.Stat(slavePath); err != nil {
		t.Fatalf("den slave gibt es nicht: %v", err)
	}

	if err := setWinsize(master, 132, 43); err != nil {
		t.Fatalf("setWinsize: %v", err)
	}
	size, err := unix.IoctlGetWinsize(int(master.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatalf("größe zurücklesen: %v", err)
	}
	if size.Col != 132 || size.Row != 43 {
		t.Errorf("größe ist %dx%d, gesetzt war 132x43", size.Col, size.Row)
	}

	// Was durch den Master geht, kommt am Slave an. Ohne diese Zusage wäre die
	// Verbindung zwischen Browser und Shell nur scheinbar da.
	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("slave öffnen: %v", err)
	}
	defer slave.Close()

	if _, err := master.WriteString("hallo\n"); err != nil {
		t.Fatalf("schreiben: %v", err)
	}
	buf := make([]byte, 32)
	n, err := slave.Read(buf)
	if err != nil {
		t.Fatalf("lesen: %v", err)
	}
	if got := strings.TrimSpace(string(buf[:n])); got != "hallo" {
		t.Errorf("am slave kam %q an", got)
	}
}
