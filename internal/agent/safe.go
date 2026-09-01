package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Regeln für alles, was aus dem Web-Prozess kommt. Whitelist, nie Blacklist:
// nur was passt, geht durch.
var (
	reUsername    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{1,31}$`)
	reServiceName = regexp.MustCompile(`^[a-zA-Z0-9@._-]{1,64}$`)
	rePHPVersion  = regexp.MustCompile(`^[578]\.[0-9]$`)
	rePoolName    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)
	reDomain      = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// allowedServices begrenzt, welche systemd-Units der Agent überhaupt anfassen
// darf. Ohne diese Liste wäre `service.stop` mit dem Namen "ssh" ein Weg, sich
// vom Server auszusperren — oder Schlimmeres.
var allowedServices = map[string]bool{
	"nginx": true, "mariadb": true, "mysql": true, "redis-server": true,
	"pure-ftpd": true, "fail2ban": true, "docker": true, "cron": true,
	"postfix": true, "dovecot": true, "rspamd": true, "opendkim": true,
	"volt-web": true,
}

// allowedBinaries: absolute Pfade, damit kein manipulierter PATH greift.
var allowedBinaries = map[string]string{
	"systemctl": "/usr/bin/systemctl",
	// Nur lesend: holt den Grund, aus dem ein Dienst nicht startet. systemctl
	// selbst sagt darüber nur "see journalctl".
	"journalctl": "/usr/bin/journalctl",
	"nginx":      "/usr/sbin/nginx",
	"useradd":    "/usr/sbin/useradd",
	"userdel":    "/usr/sbin/userdel",
	"id":         "/usr/bin/id",
	"chown":      "/usr/bin/chown",
	"mysql":      "/usr/bin/mysql",
	"mysqldump":  "/usr/bin/mysqldump",
	"crontab":    "/usr/bin/crontab",
	// Für das Update: der Agent ruft `volt update` auf, statt den Tausch
	// selbst nachzubauen. Snapshot, Prüfsumme und automatischer Rollback
	// stecken dort und sind getestet.
	"volt": "/usr/local/bin/volt",
	// Fuer PHP-Module: apt installiert das Paket, phpenmod/phpdismod setzen
	// nur Symlinks. Abschalten bleibt damit umkehrbar.
	"apt-get": "/usr/bin/apt-get",
	// Nur lesend: sagt, ob ein Paket wirklich konfiguriert ist.
	"dpkg-query": "/usr/bin/dpkg-query",
	// Startet apt in einer eigenen Unit, ausserhalb der Sandbox des Agents.
	// Ohne das scheitert dpkg an ProtectSystem=true, siehe apt.go.
	"systemd-run": "/usr/bin/systemd-run",
	"phpenmod":    "/usr/sbin/phpenmod",
	"phpdismod":   "/usr/sbin/phpdismod",
	// Fuer FTP: pure-pw pflegt die virtuellen Zugaenge, ufw gibt die Ports
	// frei. Beide bekommen ausschliesslich feste Argumente.
	"pure-pw": "/usr/bin/pure-pw",
	"ufw":     "/usr/sbin/ufw",
	// Fuer echte Dateisystem-Quotas: chattr setzt die Projektnummer an einem
	// ext4-Baum, setquota die Grenze darauf, xfs_quota beides auf XFS. Alle
	// Argumente sind agentseitig gebildet, die Pfade gehen vorher durch jail().
	"chattr":    "/usr/bin/chattr",
	"setquota":  "/usr/sbin/setquota",
	"xfs_quota": "/usr/sbin/xfs_quota",
}

var (
	errBadInput = errors.New("ungültige eingabe")
	errNotAllow = errors.New("nicht erlaubt")
)

// isPHPService erlaubt zusätzlich php8.3-fpm & Co., ohne jede Version einzeln
// in allowedServices pflegen zu müssen.
var rePHPService = regexp.MustCompile(`^php[578]\.[0-9]-fpm$`)

func checkService(name string) error {
	if !reServiceName.MatchString(name) {
		return fmt.Errorf("%w: dienstname %q", errBadInput, name)
	}
	base := strings.TrimSuffix(name, ".service")
	if allowedServices[base] || rePHPService.MatchString(base) {
		return nil
	}
	return fmt.Errorf("%w: dienst %q steht nicht auf der whitelist", errNotAllow, base)
}

func checkUsername(u string) error {
	if !reUsername.MatchString(u) {
		return fmt.Errorf("%w: benutzername %q", errBadInput, u)
	}
	// Systemkonten sind tabu — ein Panel-User darf nie "root" heißen oder
	// einen bestehenden Dienstaccount überschreiben.
	switch u {
	case "root", "daemon", "bin", "sys", "www-data", "nobody", "volt", "volt-agent",
		"sshd", "mysql", "systemd-network", "systemd-resolve":
		return fmt.Errorf("%w: %q ist ein reservierter systembenutzer", errNotAllow, u)
	}
	return nil
}

func checkPHPVersion(v string) error {
	if !rePHPVersion.MatchString(v) {
		return fmt.Errorf("%w: php-version %q", errBadInput, v)
	}
	return nil
}

func checkPoolName(n string) error {
	if !rePoolName.MatchString(n) {
		return fmt.Errorf("%w: poolname %q", errBadInput, n)
	}
	return nil
}

func checkDomain(d string) error {
	if len(d) > 253 || !reDomain.MatchString(strings.ToLower(d)) {
		return fmt.Errorf("%w: domain %q", errBadInput, d)
	}
	return nil
}

// run führt ein Kommando aus — mit explizitem argv, ohne Shell.
//
// Es gibt hier bewusst keine Variante, die einen String an sh übergibt. Damit
// existiert im gesamten Root-Daemon kein Pfad, auf dem eine Command Injection
// möglich wäre: Argumente sind immer separate argv-Einträge, nie Text, den ein
// Interpreter noch einmal zerlegt.
func run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	return runEnv(ctx, timeout, nil, name, args...)
}

// baseEnv ist das Environment jedes Kindprozesses: minimal, fest, nichts vom
// Agent geerbt. Ein Kommando sieht damit auf jedem Server dasselbe.
func baseEnv() []string {
	return []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C",
	}
}

// runEnv ist run() mit zusätzlichen Umgebungsvariablen.
//
// „Zusätzlich“ heißt: zum Minimum aus baseEnv, nie zum Environment des Agents.
// Die Werte stehen im Quelltext des Aufrufers — nichts davon kommt je aus einer
// Anfrage, sonst wäre LD_PRELOAD ein Weg an jeder Prüfung vorbei.
func runEnv(ctx context.Context, timeout time.Duration, env []string, name string,
	args ...string) (string, error) {

	bin, ok := allowedBinaries[name]
	if !ok {
		return "", fmt.Errorf("%w: binary %q", errNotAllow, name)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(baseEnv(), env...)

	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("%s: zeitüberschreitung nach %s", name, timeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return text, fmt.Errorf("%s beendet mit code %d: %s", name, ee.ExitCode(), truncate(text, 400))
		}
		return text, fmt.Errorf("%s: %w", name, err)
	}
	return text, nil
}

// jail sperrt einen Pfad in eine erlaubte Wurzel ein.
//
// Der Symlink wird aufgelöst, bevor geprüft wird — sonst zeigt
// /var/www/example.at/link auf /etc/shadow und die Prüfung auf das Präfix
// hätte nichts gemerkt. Existiert der Pfad noch nicht (Anlegen einer Datei),
// wird das erste existierende Elternverzeichnis aufgelöst und der Rest daran
// gehängt.
func jail(path string, roots []string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: pfad %q muss absolut sein", errBadInput, path)
	}
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w: pfad enthält ein nullbyte", errBadInput)
	}

	clean := filepath.Clean(path)
	resolved, err := resolveExisting(clean)
	if err != nil {
		return "", err
	}

	for _, root := range roots {
		realRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			// Wurzel existiert auf diesem System nicht — dann kann sie auch
			// nichts erlauben.
			continue
		}
		if resolved == realRoot || strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: pfad %q liegt außerhalb der erlaubten verzeichnisse", errNotAllow, path)
}

// resolveExisting löst so viel vom Pfad auf, wie schon existiert.
func resolveExisting(path string) (string, error) {
	var missing []string
	cur := path

	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			// Die noch nicht existierenden Segmente wieder anhängen. Sie können
			// selbst keine Symlinks sein, weil es sie noch nicht gibt.
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, nil
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("%w: pfad %q nicht auflösbar", errBadInput, path)
		}
		base := filepath.Base(cur)
		if base == ".." {
			return "", fmt.Errorf("%w: pfad %q enthält nicht auflösbares ..", errBadInput, path)
		}
		missing = append(missing, base)
		cur = parent
	}
}

// runInto ist run() mit umgeleiteter Ein- und Ausgabe — für Dump und Import,
// bei denen Megabyte fließen und CombinedOutput sie alle in den Speicher holen
// würde. Auch hier gibt es keine Shell: stdout und stdin sind echte Handles,
// keine Umleitung, die jemand über die Argumente beeinflussen könnte.
func runInto(ctx context.Context, timeout time.Duration, stdout io.Writer, stdin io.Reader,
	name string, args ...string) error {
	bin, ok := allowedBinaries[name]
	if !ok {
		return fmt.Errorf("%w: binary %q", errNotAllow, name)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(baseEnv(), aptEnv...)
	cmd.Stdout = stdout
	cmd.Stdin = stdin

	// stderr wird eingesammelt, damit die Fehlermeldung etwas aussagt.
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s: zeitüberschreitung nach %s", name, timeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%s beendet mit code %d: %s", name, ee.ExitCode(),
				truncate(strings.TrimSpace(errBuf.String()), 400))
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
