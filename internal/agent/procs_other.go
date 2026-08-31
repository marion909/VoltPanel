//go:build !linux

package agent

import "errors"

// Ohne /proc gibt es keine Prozessliste. Der Agent läuft im Betrieb nur unter
// Linux; diese Fassung existiert, damit sich das Projekt auf einem Mac
// übersetzen und testen lässt.
func readProcesses() ([]ProcessInfo, error) {
	return nil, errors.New("die prozessliste gibt es nur unter linux")
}

func processOwner(int) (string, error) {
	return "", errors.New("prozesse lassen sich nur unter linux ansprechen")
}
