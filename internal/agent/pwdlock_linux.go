//go:build linux

package agent

import (
	"fmt"
	"os"
	"syscall"
)

// pwdLockHolder nennt den Prozess, der /etc/.pwd.lock hält.
//
// Diese Datei ist die eigentliche Sperre der Benutzerverwaltung: sie existiert
// dauerhaft und wird über fcntl gesperrt, nicht über ihre Existenz. Ein
// hängengebliebenes useradd hält sie weiter, und jedes weitere meldet dann
// "cannot lock /etc/passwd" — ohne zu sagen, wer im Weg steht.
//
// F_GETLK beantwortet genau das: es verändert nichts und liefert die PID des
// Halters zurück.
func pwdLockHolder() string {
	f, err := os.Open("/etc/.pwd.lock")
	if err != nil {
		return ""
	}
	defer f.Close()

	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lk); err != nil {
		return ""
	}
	if lk.Type == syscall.F_UNLCK {
		return "" // frei
	}

	who := fmt.Sprintf("Prozess %d", lk.Pid)
	if name, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", lk.Pid)); err == nil {
		who = fmt.Sprintf("%s (PID %d)", trimNewline(string(name)), lk.Pid)
	}
	return who
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// PwdLockHolder ist die Fassung für Aufrufer außerhalb des Pakets — `volt
// doctor` prüft dieselbe Sperre.
func PwdLockHolder() string { return pwdLockHolder() }
