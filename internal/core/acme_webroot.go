package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// webrootProvider beantwortet die HTTP-01-Prüfung über eine Datei im
// ACME-Webroot.
//
// lego bringt einen eigenen Provider mit, der aber einen zusätzlichen Port
// aufmachen würde. Über den Webroot liefert der bereits laufende Nginx aus —
// kein zweiter Listener, keine Firewall-Regel, und es funktioniert auch, wenn
// das Panel hinter einem Proxy sitzt.
type webrootProvider struct {
	dir string
}

// reToken beschränkt den Dateinamen auf das, was ACME als Token vergibt.
// Ohne diese Prüfung wäre der Tokenname ein Pfad, den der ACME-Server bestimmt.
var reToken = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func (w webrootProvider) Present(_, token, keyAuth string) error {
	if !reToken.MatchString(token) {
		return fmt.Errorf("acme-token %q hat ein unerwartetes format", token)
	}

	dir := filepath.Join(w.dir, ".well-known", "acme-challenge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("challenge-verzeichnis: %w", err)
	}
	// 0644, damit der Nginx-Benutzer die Datei lesen kann.
	if err := os.WriteFile(filepath.Join(dir, token), []byte(keyAuth), 0o644); err != nil {
		return fmt.Errorf("challenge-datei: %w", err)
	}
	return nil
}

func (w webrootProvider) CleanUp(_, token, _ string) error {
	if !reToken.MatchString(token) {
		return nil
	}
	err := os.Remove(filepath.Join(w.dir, ".well-known", "acme-challenge", token))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
