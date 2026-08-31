package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marion909/voltpanel/internal/version"
	"github.com/shirou/gopsutil/v4/host"
)

func (s *Server) opSystemInfo(ctx context.Context, _ json.RawMessage) (any, error) {
	info := SystemInfo{AgentVersion: version.Version, PHPVersions: detectPHPVersions(s.phpDir)}
	if h, err := host.InfoWithContext(ctx); err == nil {
		info.Hostname, info.OS, info.Platform = h.Hostname, h.OS, h.Platform
		info.Kernel, info.Arch, info.Uptime = h.KernelVersion, h.KernelArch, h.Uptime
	}
	return info, nil
}

// --- Nginx -----------------------------------------------------------------

// opNginxWriteVhost schreibt die Site-Config und prüft sie, bevor sie gilt.
//
// Der Ablauf ist bewusst zweistufig: schreiben, `nginx -t`, und bei einem Fehler
// die Datei wieder auf den vorherigen Stand bringen. Damit kann eine kaputte
// Config den Webserver nicht beim nächsten Reload umwerfen — das Risiko, das
// die Roadmap unter "Nginx-Reload mit kaputter Config" führt.
func (s *Server) opNginxWriteVhost(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[VhostParams](raw, OpNginxWriteVhost)
	if err != nil {
		return nil, err
	}
	if err := checkDomain(p.Domain); err != nil {
		return nil, err
	}

	path := filepath.Join(s.nginxDir, "sites-available", p.Domain+".conf")
	if _, err := jail(path, []string{s.nginxDir}); err != nil {
		return nil, err
	}

	previous, hadPrevious := readIfExists(path)
	if err := writeFileAtomic(path, []byte(p.Content), 0o644); err != nil {
		return nil, opErr(OpNginxWriteVhost, "vhost schreiben: %v", err)
	}

	link := filepath.Join(s.nginxDir, "sites-enabled", p.Domain+".conf")
	if err := ensureSymlink(path, link); err != nil {
		restoreVhost(path, previous, hadPrevious)
		return nil, opErr(OpNginxWriteVhost, "vhost aktivieren: %v", err)
	}

	if out, err := run(ctx, shortTimeout, "nginx", "-t"); err != nil {
		// Zurück auf den letzten funktionierenden Stand, sonst reißt der
		// nächste Reload — egal von wem ausgelöst — den Webserver mit.
		restoreVhost(path, previous, hadPrevious)
		if !hadPrevious {
			_ = os.Remove(link)
		}
		return nil, opErr(OpNginxWriteVhost, "config abgelehnt, änderung zurückgenommen: %s", truncate(out, 500))
	}

	if out, err := run(ctx, shortTimeout, "systemctl", "reload", "nginx"); err != nil {
		return nil, opErr(OpNginxWriteVhost, "reload fehlgeschlagen: %s", truncate(out, 300))
	}
	return TextResult{Text: "vhost " + p.Domain + " aktiv"}, nil
}

// opNginxWriteShared legt die vhost-übergreifende Config ab.
//
// Sie enthält den Standardserver, der /.well-known/acme-challenge/ für jeden
// unbekannten Hostnamen ausliefert. Ohne sie lässt sich ein Zertifikat nur
// für Domains holen, die schon einen Vhost haben — für das Panel selbst also
// nie, und das ist genau der erste Fall nach einer Installation.
//
// Der Ablageort ist fest verdrahtet: es gibt genau eine solche Datei, und ein
// Pfad aus der Anfrage wäre eine Angriffsfläche ohne jeden Nutzen.
func (s *Server) opNginxWriteShared(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[SharedParams](raw, OpNginxWriteShared)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(p.Content, "server {") {
		return nil, opErr(OpNginxWriteShared, "inhalt enthält keinen server-block")
	}

	path := filepath.Join(s.nginxDir, "conf.d", "volt-shared.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, opErr(OpNginxWriteShared, "conf.d anlegen: %v", err)
	}

	previous, hadPrevious := readIfExists(path)
	if err := writeFileAtomic(path, []byte(p.Content), 0o644); err != nil {
		return nil, opErr(OpNginxWriteShared, "config schreiben: %v", err)
	}

	// Die Standardseite der Distribution beansprucht denselben
	// default_server. Bliebe sie aktiv, lehnte nginx beide ab ("duplicate
	// default server"). Entfernt wird nur der Link — die Datei bleibt unter
	// sites-available liegen und ist mit einem Symlink wiederherstellbar.
	distDefault := filepath.Join(s.nginxDir, "sites-enabled", "default")
	removedDefault := false
	if _, err := os.Lstat(distDefault); err == nil {
		if err := os.Remove(distDefault); err != nil {
			restoreVhost(path, previous, hadPrevious)
			return nil, opErr(OpNginxWriteShared, "standardseite abschalten: %v", err)
		}
		removedDefault = true
	}

	if out, err := run(ctx, shortTimeout, "nginx", "-t"); err != nil {
		restoreVhost(path, previous, hadPrevious)
		if !hadPrevious {
			_ = os.Remove(path)
		}
		if removedDefault {
			_ = ensureSymlink(filepath.Join(s.nginxDir, "sites-available", "default"), distDefault)
		}
		return nil, opErr(OpNginxWriteShared, "config abgelehnt, änderung zurückgenommen: %s", truncate(out, 500))
	}

	if out, err := run(ctx, shortTimeout, "systemctl", "reload", "nginx"); err != nil {
		return nil, opErr(OpNginxWriteShared, "reload fehlgeschlagen: %s", truncate(out, 300))
	}

	msg := "gemeinsame config aktiv"
	if removedDefault {
		msg += ", standardseite der distribution abgeschaltet"
	}
	return TextResult{Text: msg}, nil
}

func (s *Server) opNginxRemoveVhost(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[VhostParams](raw, OpNginxRemoveVhost)
	if err != nil {
		return nil, err
	}
	if err := checkDomain(p.Domain); err != nil {
		return nil, err
	}

	for _, dir := range []string{"sites-enabled", "sites-available"} {
		path := filepath.Join(s.nginxDir, dir, p.Domain+".conf")
		if _, err := jail(path, []string{s.nginxDir}); err != nil {
			return nil, err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, opErr(OpNginxRemoveVhost, "%s entfernen: %v", path, err)
		}
	}

	if out, err := run(ctx, shortTimeout, "nginx", "-t"); err != nil {
		return nil, opErr(OpNginxRemoveVhost, "config nach entfernen ungültig: %s", truncate(out, 500))
	}
	if _, err := run(ctx, shortTimeout, "systemctl", "reload", "nginx"); err != nil {
		return nil, err
	}
	return TextResult{Text: "vhost " + p.Domain + " entfernt"}, nil
}

func (s *Server) opNginxTest(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := run(ctx, shortTimeout, "nginx", "-t")
	if err != nil {
		return nil, opErr(OpNginxTest, "%s", truncate(out, 500))
	}
	return TextResult{Text: out}, nil
}

// opNginxReload lädt nur nach bestandenem Test neu — nie ungeprüft.
func (s *Server) opNginxReload(ctx context.Context, _ json.RawMessage) (any, error) {
	if out, err := run(ctx, shortTimeout, "nginx", "-t"); err != nil {
		return nil, opErr(OpNginxReload, "reload verweigert, config fehlerhaft: %s", truncate(out, 500))
	}
	if out, err := run(ctx, shortTimeout, "systemctl", "reload", "nginx"); err != nil {
		return nil, opErr(OpNginxReload, "%s", truncate(out, 300))
	}
	return TextResult{Text: "nginx neu geladen"}, nil
}

// --- PHP-FPM ---------------------------------------------------------------

func (s *Server) opPHPWritePool(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[PoolParams](raw, OpPHPWritePool)
	if err != nil {
		return nil, err
	}
	if err := checkPHPVersion(p.PHPVersion); err != nil {
		return nil, err
	}
	if err := checkPoolName(p.PoolName); err != nil {
		return nil, err
	}

	path := filepath.Join(s.phpDir, p.PHPVersion, "fpm", "pool.d", p.PoolName+".conf")
	if _, err := jail(path, []string{s.phpDir}); err != nil {
		return nil, err
	}
	previous, hadPrevious := readIfExists(path)
	if err := writeFileAtomic(path, []byte(p.Content), 0o644); err != nil {
		return nil, opErr(OpPHPWritePool, "pool schreiben: %v", err)
	}

	svc := "php" + p.PHPVersion + "-fpm"
	if out, err := run(ctx, longTimeout, "systemctl", "reload", svc); err != nil {
		restoreVhost(path, previous, hadPrevious)
		// Ohne den zweiten Reload liefe FPM mit der zurückgenommenen Datei
		// weiter im alten Zustand — das ist genau, was wir wollen.
		_, _ = run(ctx, longTimeout, "systemctl", "reload", svc)
		return nil, opErr(OpPHPWritePool, "pool abgelehnt, änderung zurückgenommen: %s", truncate(out, 400))
	}
	return TextResult{Text: "pool " + p.PoolName + " aktiv"}, nil
}

func (s *Server) opPHPRemovePool(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[PoolParams](raw, OpPHPRemovePool)
	if err != nil {
		return nil, err
	}
	if err := checkPHPVersion(p.PHPVersion); err != nil {
		return nil, err
	}
	if err := checkPoolName(p.PoolName); err != nil {
		return nil, err
	}

	path := filepath.Join(s.phpDir, p.PHPVersion, "fpm", "pool.d", p.PoolName+".conf")
	if _, err := jail(path, []string{s.phpDir}); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, opErr(OpPHPRemovePool, "pool entfernen: %v", err)
	}
	_, _ = run(ctx, longTimeout, "systemctl", "reload", "php"+p.PHPVersion+"-fpm")
	return TextResult{Text: "pool " + p.PoolName + " entfernt"}, nil
}

func (s *Server) opPHPReload(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[PoolParams](raw, OpPHPReload)
	if err != nil {
		return nil, err
	}
	if err := checkPHPVersion(p.PHPVersion); err != nil {
		return nil, err
	}
	if out, err := run(ctx, longTimeout, "systemctl", "reload", "php"+p.PHPVersion+"-fpm"); err != nil {
		return nil, opErr(OpPHPReload, "%s", truncate(out, 300))
	}
	return TextResult{Text: "php-fpm " + p.PHPVersion + " neu geladen"}, nil
}

func (s *Server) opPHPVersions(context.Context, json.RawMessage) (any, error) {
	return detectPHPVersions(s.phpDir), nil
}

// detectPHPVersions liest die installierten Versionen aus /etc/php.
func detectPHPVersions(phpDir string) []string {
	entries, err := os.ReadDir(phpDir)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() || !rePHPVersion.MatchString(e.Name()) {
			continue
		}
		// Nur Versionen mit FPM-Pool-Verzeichnis zählen als nutzbar.
		if _, err := os.Stat(filepath.Join(phpDir, e.Name(), "fpm", "pool.d")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// --- Zertifikate -----------------------------------------------------------

// opCertInstall legt Zertifikat und Key ab; der Key bekommt 0600.
func (s *Server) opCertInstall(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[CertInstallParams](raw, OpCertInstall)
	if err != nil {
		return nil, err
	}
	if err := checkDomain(p.Domain); err != nil {
		return nil, err
	}
	if !strings.Contains(p.CertPEM, "BEGIN CERTIFICATE") {
		return nil, opErr(OpCertInstall, "cert_pem enthält kein zertifikat")
	}
	if !strings.Contains(p.KeyPEM, "PRIVATE KEY") {
		return nil, opErr(OpCertInstall, "key_pem enthält keinen privaten schlüssel")
	}

	dir := filepath.Join(s.certDir, p.Domain)
	if _, err := jail(dir, []string{s.certDir}); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, opErr(OpCertInstall, "zertifikatsverzeichnis: %v", err)
	}

	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := writeFileAtomic(certPath, []byte(p.CertPEM), 0o644); err != nil {
		return nil, opErr(OpCertInstall, "zertifikat schreiben: %v", err)
	}
	if err := writeFileAtomic(keyPath, []byte(p.KeyPEM), 0o600); err != nil {
		return nil, opErr(OpCertInstall, "schlüssel schreiben: %v", err)
	}

	// Das Panel terminiert TLS selbst und läuft dabei nicht als root. Ohne
	// diesen Schritt läge sein eigenes Zertifikat zwar da, wäre für volt-web
	// aber unlesbar — und das Panel bliebe beim selbstsignierten stehen.
	if p.Domain != "" && p.Domain == s.panelDomain && s.peerUID >= 0 {
		for path, mode := range map[string]os.FileMode{dir: 0o750, keyPath: 0o600, certPath: 0o644} {
			if err := os.Chown(path, s.peerUID, -1); err != nil {
				return nil, opErr(OpCertInstall, "eigentümer von %s: %v", path, err)
			}
			if err := os.Chmod(path, mode); err != nil {
				return nil, opErr(OpCertInstall, "rechte von %s: %v", path, err)
			}
		}
	}

	if out, err := run(ctx, shortTimeout, "nginx", "-t"); err != nil {
		return nil, opErr(OpCertInstall, "zertifikat abgelegt, aber nginx-config fehlerhaft: %s", truncate(out, 400))
	}
	_, _ = run(ctx, shortTimeout, "systemctl", "reload", "nginx")

	return map[string]string{"cert_path": certPath, "key_path": keyPath}, nil
}

// --- Systembenutzer --------------------------------------------------------

func (s *Server) opUserCreate(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[UserCreateParams](raw, OpUserCreate)
	if err != nil {
		return nil, err
	}
	if err := checkUsername(p.Username); err != nil {
		return nil, err
	}
	home, err := jail(p.HomeDir, s.roots)
	if err != nil {
		return nil, err
	}

	// Kein Login-Shell-Zugang für Site-User. Wer SSH will, bekommt es explizit.
	shell := "/usr/sbin/nologin"
	if p.Shell == "/bin/bash" {
		shell = p.Shell
	}

	// longTimeout statt shortTimeout: wird useradd mitten im Lauf abgebrochen,
	// bleibt seine Sperrdatei in /etc liegen und blockiert danach jeden
	// weiteren Versuch. Ein langsamer Durchlauf ist billiger als das.
	if out, err := run(ctx, longTimeout, "useradd",
		"--home-dir", home, "--create-home", "--shell", shell,
		"--user-group", "--comment", "voltpanel site user", p.Username); err != nil {
		// Exit 9 = Benutzer existiert bereits. Idempotenz ist Prinzip 2 der Roadmap.
		if strings.Contains(out, "already exists") {
			return TextResult{Text: "benutzer " + p.Username + " existierte bereits"}, nil
		}
		// "cannot lock /etc/passwd; try again later" nennt den Grund nicht,
		// und Warten hilft in beiden möglichen Fällen nicht.
		if strings.Contains(out, "cannot lock") {
			return nil, opErr(OpUserCreate, "%s — %s", strings.TrimSpace(out), diagnoseUserLock())
		}
		return nil, opErr(OpUserCreate, "%s", truncate(out, 300))
	}
	if err := os.Chmod(home, 0o750); err != nil {
		return nil, opErr(OpUserCreate, "home-rechte: %v", err)
	}
	return TextResult{Text: "benutzer " + p.Username + " angelegt"}, nil
}

func (s *Server) opUserDelete(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[UserDeleteParams](raw, OpUserDelete)
	if err != nil {
		return nil, err
	}
	if err := checkUsername(p.Username); err != nil {
		return nil, err
	}

	args := []string{p.Username}
	if p.RemoveHome {
		args = append([]string{"--remove"}, args...)
	}
	if out, err := run(ctx, longTimeout, "userdel", args...); err != nil {
		if strings.Contains(out, "does not exist") {
			return TextResult{Text: "benutzer " + p.Username + " existierte nicht"}, nil
		}
		return nil, opErr(OpUserDelete, "%s", truncate(out, 300))
	}
	return TextResult{Text: "benutzer " + p.Username + " entfernt"}, nil
}

func (s *Server) opUserExists(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[UserCreateParams](raw, OpUserExists)
	if err != nil {
		return nil, err
	}
	if err := checkUsername(p.Username); err != nil {
		return nil, err
	}
	_, err = run(ctx, shortTimeout, "id", "-u", p.Username)
	return map[string]bool{"exists": err == nil}, nil
}

// restoreVhost setzt eine Datei auf ihren vorherigen Inhalt zurück.
func restoreVhost(path string, previous []byte, had bool) {
	if had {
		_ = writeFileAtomic(path, previous, 0o644)
		return
	}
	_ = os.Remove(path)
}

func readIfExists(path string) ([]byte, bool) {
	b, err := os.ReadFile(path)
	return b, err == nil
}

func ensureSymlink(target, link string) error {
	if existing, err := os.Readlink(link); err == nil {
		if existing == target {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	} else if _, err := os.Lstat(link); err == nil {
		// Da liegt eine echte Datei statt eines Links — nicht kommentarlos ersetzen.
		return fmt.Errorf("%s existiert und ist kein symlink", link)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, link)
}
