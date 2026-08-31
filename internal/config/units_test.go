package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecretKeyPathIsWritableForTheUnit verbindet zwei Dinge, die sonst
// unabhängig voneinander driften: den Standardpfad des Schlüssels und die
// Sandbox der systemd-Unit.
//
// Der Anlass ist ein echter Ausfall. volt-web lief unter ProtectSystem=strict
// mit ReadOnlyPaths=/etc/volt und konnte seinen Schlüssel nicht anlegen. Der
// Dienst startete im Sekundentakt neu, und weil die Datei nur fehlte statt
// falsch zu sein, sah die Ursache nach einem Rechteproblem aus.
func TestSecretKeyPathIsWritableForTheUnit(t *testing.T) {
	unit := readUnit(t, "volt-web.service")

	if !strings.Contains(unit, "ProtectSystem=") {
		t.Skip("die Unit sandboxt das Dateisystem nicht mehr — Prüfung gegenstandslos")
	}

	keyDir := filepath.Dir(Default().SecretKeyPath)
	if !writable(unit, keyDir) {
		t.Errorf("volt-web darf %s nicht beschreiben, legt dort aber seinen Schlüssel an.\n"+
			"ReadWritePaths in volt-web.service ergänzen.", keyDir)
	}
}

// TestConfigFileStaysReadOnly hält die Eigenschaft fest, deretwegen
// /etc/volt überhaupt schreibgeschützt ist: die config.yaml bestimmt die
// Wurzeln des Agents. Wäre sie beschreibbar, führte ein übernommener
// Web-Prozess über einen Agent-Neustart zu root.
func TestConfigFileStaysReadOnly(t *testing.T) {
	unit := readUnit(t, "volt-web.service")
	cfg := Default()

	if writable(unit, cfg.ConfigDir) {
		t.Errorf("%s ist für volt-web beschreibbar — damit ist die config.yaml änderbar", cfg.ConfigDir)
	}
	if !strings.HasPrefix(filepath.Dir(cfg.SecretKeyPath), cfg.ConfigDir+string(filepath.Separator)) {
		t.Errorf("der Schlüssel liegt außerhalb von %s und wäre nicht im Backup", cfg.ConfigDir)
	}
}

// TestAgentMayWriteWhereItWorks prüft die Unit des Agents gegen die Pfade,
// die er tatsächlich anfasst.
//
// Auch das kommt aus einem echten Ausfall: ProtectSystem=full macht /etc
// schreibgeschützt, und useradd meldete daraufhin "cannot lock /etc/passwd".
// Nichts davon war am Panel zu sehen — die Site-Anlage brach erst beim
// Anlegen des Systembenutzers ab, nachdem der Vhost schon gedacht war.
func TestAgentMayWriteWhereItWorks(t *testing.T) {
	unit := readUnit(t, "volt-agent.service")
	cfg := Default()

	needed := map[string]string{
		cfg.NginxDir:  "Vhosts und htpasswd",
		cfg.PHPFPMDir: "FPM-Pools",
		cfg.SitesDir:  "Site-Verzeichnisse",
		cfg.CertDir:   "Zertifikate",
		cfg.LogDir:    "Site-Logs",
		"/etc":        "Systembenutzer (useradd sperrt /etc/passwd) und Cronjobs in /etc/cron.d",
	}

	for path, why := range needed {
		if !writableUnder(unit, path) {
			t.Errorf("volt-agent darf %s nicht beschreiben, braucht es aber für: %s", path, why)
		}
	}
}

// writableUnder erlaubt auch ein übergeordnetes Verzeichnis: /var/lib/volt
// deckt /var/lib/volt/certs mit ab. Für den Web-Prozess gilt das bewusst
// nicht — dort entzieht ReadOnlyPaths einen Teilbaum wieder.
func writableUnder(unit, path string) bool {
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ReadWritePaths=") {
			continue
		}
		for _, p := range strings.Fields(strings.TrimPrefix(line, "ReadWritePaths=")) {
			if p == path || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/") {
				return true
			}
		}
	}
	return false
}

func readUnit(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "packaging", "systemd", name))
	if err != nil {
		t.Fatalf("unit %s nicht lesbar: %v", name, err)
	}
	return string(data)
}

// writable prüft, ob der Pfad selbst in ReadWritePaths steht. Ein
// übergeordnetes Verzeichnis zählt nicht: ReadOnlyPaths kann es wieder
// entziehen, und genau diese Reihenfolge war die Falle.
func writable(unit, path string) bool {
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ReadWritePaths=") {
			continue
		}
		for _, p := range strings.Fields(strings.TrimPrefix(line, "ReadWritePaths=")) {
			if p == path {
				return true
			}
		}
	}
	return false
}
