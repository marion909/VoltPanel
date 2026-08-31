//go:build !linux

package agent

import (
	"errors"
	"os"
	"os/exec"
)

// Pseudoterminals sind hier nicht nachgebaut: der Agent läuft im Betrieb nur
// unter Linux. Diese Fassung hält die Übersetzung auf einem Entwicklungsrechner
// am Leben.
func openPTY() (*os.File, string, error) {
	return nil, "", errors.New("ein terminal gibt es nur unter linux")
}

func setWinsize(*os.File, int, int) error {
	return errors.New("ein terminal gibt es nur unter linux")
}

func startShell(string, string, string, *os.File) (*exec.Cmd, error) {
	return nil, errors.New("ein terminal gibt es nur unter linux")
}
