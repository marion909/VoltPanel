package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// reExtension begrenzt, was hinter "php8.3-" stehen darf.
//
// Zusammen mit dem festen Präfix ist das die eigentliche Schranke: der Agent
// installiert Pakete über apt, und ohne diese Begrenzung wäre die Operation
// ein Weg, ein beliebiges Paket auf den Server zu holen. Mit ihr lässt sich
// ausschließlich ein PHP-Modul derselben Version nachrüsten.
var reExtension = regexp.MustCompile(`^[a-z][a-z0-9]{1,19}$`)

// nonRemovable sind Module, ohne die ein FPM-Pool nicht mehr startet.
// Abschalten liesse sich das Panel selbst nicht mehr reparieren, weil die
// Site danach tot ist und die Ursache nicht in der Oberfläche steht.
var nonRemovable = map[string]bool{
	"opcache":  false, // darf abgeschaltet werden, kostet nur Tempo
	"json":     true,
	"mbstring": true,
}

func checkExtension(name string) error {
	if !reExtension.MatchString(name) {
		return opInputErr(OpPHPExtInstall,
			"%q ist kein gültiger modulname — erlaubt sind Kleinbuchstaben und Ziffern, "+
				"etwa imagick oder redis", name)
	}
	return nil
}

// opPHPExtensions listet, was für eine Version bereitsteht und was davon aktiv
// ist.
//
// Beides kommt aus dem Dateisystem: mods-available zählt, was installiert ist,
// die Symlinks in fpm/conf.d, was der Pool tatsächlich lädt. Ein Aufruf von
// `php -m` wäre ein weiteres Binary in der Whitelist, ohne mehr zu wissen.
func (s *Server) opPHPExtensions(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[PoolParams](raw, OpPHPExtensions)
	if err != nil {
		return nil, err
	}
	if err := checkPHPVersion(p.PHPVersion); err != nil {
		return nil, err
	}

	base := filepath.Join(s.phpDir, p.PHPVersion)
	if _, err := jail(base, []string{s.phpDir}); err != nil {
		return nil, err
	}

	available := iniNames(filepath.Join(base, "mods-available"))
	enabled := map[string]bool{}
	for _, name := range iniNames(filepath.Join(base, "fpm", "conf.d")) {
		enabled[name] = true
	}

	out := make([]PHPExtension, 0, len(available))
	for _, name := range available {
		out = append(out, PHPExtension{
			Name:      name,
			Enabled:   enabled[name],
			Essential: nonRemovable[name],
		})
	}
	return out, nil
}

// opPHPExtInstall holt ein Modul über apt nach.
func (s *Server) opPHPExtInstall(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[PHPExtParams](raw, OpPHPExtInstall)
	if err != nil {
		return nil, err
	}
	if err := checkPHPVersion(p.PHPVersion); err != nil {
		return nil, err
	}
	if err := checkExtension(p.Name); err != nil {
		return nil, err
	}

	pkg := "php" + p.PHPVersion + "-" + p.Name
	if out, err := s.aptInstall(ctx, pkg); err != nil {
		return nil, opErr(OpPHPExtInstall, "%s konnte nicht installiert werden: %s",
			pkg, aptMessage(out))
	}

	if out, err := run(ctx, longTimeout, "systemctl", "restart", "php"+p.PHPVersion+"-fpm"); err != nil {
		return nil, opErr(OpPHPExtInstall, "%s installiert, fpm-neustart fehlgeschlagen: %s",
			pkg, truncate(out, 300))
	}
	return TextResult{Text: pkg + " installiert"}, nil
}

// opPHPExtToggle schaltet ein installiertes Modul an oder ab.
//
// phpenmod und phpdismod setzen nur Symlinks — das Paket bleibt liegen. Ein
// Abschalten ist damit umkehrbar, ein `apt-get remove` wäre es nicht.
func (s *Server) opPHPExtToggle(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[PHPExtParams](raw, OpPHPExtToggle)
	if err != nil {
		return nil, err
	}
	if err := checkPHPVersion(p.PHPVersion); err != nil {
		return nil, err
	}
	if err := checkExtension(p.Name); err != nil {
		return nil, err
	}
	if !p.Enable && nonRemovable[p.Name] {
		return nil, opErr(OpPHPExtToggle,
			"%s lässt sich nicht abschalten — ohne das Modul startet kein Pool mehr", p.Name)
	}

	tool := "phpdismod"
	if p.Enable {
		tool = "phpenmod"
	}
	if out, err := run(ctx, shortTimeout, tool, "-v", p.PHPVersion, "-s", "fpm", p.Name); err != nil {
		return nil, opErr(OpPHPExtToggle, "%s: %s", tool, truncate(out, 300))
	}
	if out, err := run(ctx, longTimeout, "systemctl", "restart", "php"+p.PHPVersion+"-fpm"); err != nil {
		return nil, opErr(OpPHPExtToggle, "umgestellt, fpm-neustart fehlgeschlagen: %s", truncate(out, 300))
	}

	state := "abgeschaltet"
	if p.Enable {
		state = "aktiv"
	}
	return TextResult{Text: p.Name + " ist " + state}, nil
}

// iniNames liefert die Modulnamen aus einem Verzeichnis mit ini-Dateien.
// Debian benennt sie "20-redis.ini"; die Ziffern sind die Ladereihenfolge und
// gehören nicht zum Namen.
func iniNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".ini") {
			continue
		}
		name = strings.TrimSuffix(name, ".ini")
		if idx := strings.IndexByte(name, '-'); idx >= 0 && isDigits(name[:idx]) {
			name = name[idx+1:]
		}
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
