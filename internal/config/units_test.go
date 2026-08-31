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
// Zweimal aus echten Ausfällen gewachsen. Erst war /etc gar nicht
// freigegeben, dann stand es zwar in ReadWritePaths — und blieb trotzdem
// schreibgeschützt, weil ProtectSystem=full denselben Pfad beansprucht. Die
// Unit sah beide Male richtig aus.
func TestAgentMayWriteWhereItWorks(t *testing.T) {
	unit := readUnit(t, "volt-agent.service")
	cfg := Default()

	needed := map[string]string{
		cfg.NginxDir:     "Vhosts und htpasswd",
		cfg.PHPFPMDir:    "FPM-Pools",
		cfg.SitesDir:     "Site-Verzeichnisse",
		cfg.CertDir:      "Zertifikate",
		cfg.LogDir:       "Site-Logs",
		"/etc":           "Systembenutzer (useradd sperrt /etc/passwd) und Cronjobs in /etc/cron.d",
		"/etc/cron.d":    "Cronjobs",
		"/usr/local/bin": "Binärtausch beim Update",
	}

	for path, why := range needed {
		if !unitCanWrite(unit, path) {
			t.Errorf("volt-agent kann %s nicht beschreiben, braucht es aber für: %s", path, why)
		}
	}
}

// unitCanWrite bildet nach, was systemd aus ProtectSystem und ReadWritePaths
// tatsächlich macht.
//
// Der Kern ist die letzte Regel: eine Freigabe wirkt für Unterpfade, aber
// nicht für denselben Pfad. Steht er in beiden Listen, gewinnt die
// restriktivere Angabe. Genau daran ist die vorige Fassung gescheitert, und
// eine Prüfung, die nur "steht es in ReadWritePaths?" fragt, hätte sie
// durchgewinkt.
func unitCanWrite(unit, path string) bool {
	var readOnly []string
	switch protectSystem(unit) {
	case "strict":
		readOnly = []string{"/"}
	case "full":
		readOnly = []string{"/usr", "/boot", "/efi", "/etc"}
	case "true", "yes", "on", "1":
		readOnly = []string{"/usr", "/boot", "/efi"}
	}

	blocked := ""
	for _, ro := range readOnly {
		if ro == path || under(path, ro) {
			blocked = ro
		}
	}
	if blocked == "" {
		return true
	}

	for _, rw := range unitValues(unit, "ReadWritePaths=") {
		if rw == blocked {
			// Derselbe Pfad in beiden Listen: die Freigabe verpufft.
			continue
		}
		if rw == path || under(path, rw) {
			return true
		}
	}
	return false
}

func protectSystem(unit string) string {
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "ProtectSystem="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func unitValues(unit, key string) []string {
	var out []string
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, key); ok {
			out = append(out, strings.Fields(v)...)
		}
	}
	return out
}

func under(path, root string) bool {
	if root == "/" {
		return path != "/"
	}
	return strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
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
