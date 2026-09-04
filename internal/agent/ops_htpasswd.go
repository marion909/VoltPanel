package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// htpasswdDir liegt außerhalb des Site-Verzeichnisses: läge die Datei unter
// /var/www, könnte der PHP-Prozess der Site sie lesen — und damit die Hashes
// aller Benutzer, die die Site schützen sollen.
func (s *Server) htpasswdPath(domain string) string {
	return filepath.Join(s.nginxDir, "volt-auth", domain+".htpasswd")
}

// reHtpasswdLine lässt genau das durch, was nginx als Zeile erwartet:
// Benutzername, Doppelpunkt, Hash. Kein Leerzeichen, kein Zeilenumbruch —
// eine zweite Zeile wäre ein zusätzlicher, ungeprüfter Zugang.
var reHtpasswdLine = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}:\$(2[aby]|apr1|6|5)\$[A-Za-z0-9./$]{8,255}$`)

func (s *Server) opNginxWriteAuth(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[HtpasswdParams](raw, OpNginxWriteAuth)
	if err != nil {
		return nil, err
	}
	domain, err := checkDomain(p.Domain)
	if err != nil {
		return nil, err
	}
	p.Domain = domain
	if len(p.Entries) == 0 {
		return nil, opErr(OpNginxWriteAuth, "keine benutzer übergeben")
	}

	for _, entry := range p.Entries {
		if !reHtpasswdLine.MatchString(entry) {
			// Der Hash wird bewusst nicht mit ausgegeben.
			name, _, _ := strings.Cut(entry, ":")
			return nil, opErr(OpNginxWriteAuth,
				"eintrag für %q hat kein gültiges htpasswd-format", truncate(name, 64))
		}
	}

	path := s.htpasswdPath(p.Domain)
	if _, err := jail(path, []string{s.nginxDir}); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, opErr(OpNginxWriteAuth, "verzeichnis anlegen: %v", err)
	}

	content := strings.Join(p.Entries, "\n") + "\n"
	// 0640 root:www-data — nginx muss lesen, sonst niemand.
	if err := writeFileAtomic(path, []byte(content), 0o640); err != nil {
		return nil, opErr(OpNginxWriteAuth, "%v", err)
	}
	if err := applyOwner(path, "root", "www-data", false); err != nil {
		// Fehlt die Gruppe www-data, bleibt die Datei root:root. Nginx läuft
		// als root-Master und kann sie trotzdem lesen.
		s.log.Warn("htpasswd-gruppe nicht gesetzt", "pfad", path, "err", err)
	}

	return TextResult{Text: path}, nil
}

func (s *Server) opNginxRemoveAuth(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[HtpasswdParams](raw, OpNginxRemoveAuth)
	if err != nil {
		return nil, err
	}
	domain, err := checkDomain(p.Domain)
	if err != nil {
		return nil, err
	}
	p.Domain = domain

	path := s.htpasswdPath(p.Domain)
	if _, err := jail(path, []string{s.nginxDir}); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, opErr(OpNginxRemoveAuth, "%v", err)
	}
	return TextResult{Text: "passwortschutz entfernt"}, nil
}
