package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/templates"
)

// Apps: eine Anwendung ist eine systemd-Unit plus Reverse-Proxy.
//
// Der Vhost steht schon — der Site-Typ `proxy` schreibt einen fertigen
// proxy_pass. Was fehlte, ist die andere Hälfte: der Prozess, auf den er zeigt.
//
// Zwei Dinge kommen hier nicht aus der Anfrage. Der Unit-Name entsteht aus dem
// App-Namen mit festem Präfix — sonst wäre "meine App neu starten" ein Weg an
// der Dienst-Whitelist vorbei zu jedem Dienst des Servers. Und der Pfad der
// Umgebungsdatei entsteht aus dem App-Namen, nicht aus einem übergebenen Pfad.

const unitDir = "/etc/systemd/system"

// AppParams beschreibt eine App vollständig. Der Agent schreibt genau das —
// eine Unit, die dem nicht entspricht, gibt es nach diesem Aufruf nicht mehr.
type AppParams struct {
	Name string `json:"name"`
	// SystemUser ist der Benutzer der Site. Seine UID wird nicht übergeben,
	// sondern nachgeschlagen.
	SystemUser string `json:"system_user"`
	WorkingDir string `json:"working_dir"`
	// Command ist das Programm mit seinen Argumenten, jedes für sich.
	Command []string          `json:"command"`
	Env     map[string]string `json:"env"`
}

type AppResult struct {
	Name     string `json:"name"`
	Unit     string `json:"unit"`
	UnitPath string `json:"unit_path"`
	EnvPath  string `json:"env_path"`
	Active   bool   `json:"active"`
	Enabled  bool   `json:"enabled"`
	// Changed sagt, ob sich an den Dateien etwas geändert hat. Ohne Änderung
	// wird nicht neu gestartet: ein Neustart bei jedem Speichern der
	// Einstellungen wäre eine Unterbrechung ohne Anlass.
	Changed bool `json:"changed"`
}

type AppNameParams struct {
	Name string `json:"name"`
}

// opAppWrite schreibt Unit und Umgebung und startet die App.
func (s *Server) opAppWrite(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[AppParams](raw, OpAppWrite)
	if err != nil {
		return nil, err
	}
	if !templates.ValidAppName(p.Name) {
		return nil, opInputErr(OpAppWrite, "%q ist kein gültiger app-name — 3 bis 32 zeichen, "+
			"kleinbuchstaben, ziffern und bindestrich", p.Name)
	}
	// Erst der Pfad, dann der Benutzer: die Wurzelprüfung sieht nur die
	// Anfrage an, der Benutzernachschlag geht über NSS und damit womöglich ins
	// Netz. Was ohne Nachfrage entschieden werden kann, wird ohne Nachfrage
	// entschieden.
	//
	// Das Arbeitsverzeichnis wird ReadWritePaths der Unit. Ein Pfad außerhalb
	// der Wurzeln machte genau das Verzeichnis schreibbar, das die Härtung
	// schützen soll.
	workdir, err := jail(p.WorkingDir, s.roots)
	if err != nil {
		return nil, err
	}
	// Derselbe Nachschlag wie beim FTP-Zugang: der Name muss ein Site-Konto
	// sein, und die aufgelöste UID muss über dem Bereich der Systemkonten
	// liegen. Eine App mit der UID 0 wäre ein Rootprozess, den ein Kunde selbst
	// gestartet hat — mit einem Kommando, das er selbst bestimmt.
	if _, _, err := siteUserIDs(OpAppWrite, p.SystemUser); err != nil {
		return nil, err
	}

	data := templates.AppData{
		Name:        p.Name,
		GeneratedAt: time.Now().Format(time.RFC3339),
		User:        p.SystemUser,
		Group:       p.SystemUser,
		WorkingDir:  workdir,
		EnvPath:     s.appEnvPath(p.Name),
		Command:     p.Command,
		Env:         envList(p.Env),
	}

	unit, err := templates.RenderApp(data)
	if err != nil {
		return nil, opInputErr(OpAppWrite, "%v", err)
	}
	env, err := templates.RenderAppEnv(data)
	if err != nil {
		return nil, opInputErr(OpAppWrite, "%v", err)
	}

	res := AppResult{
		Name:     p.Name,
		Unit:     templates.UnitName(p.Name),
		UnitPath: s.appUnitPath(p.Name),
		EnvPath:  data.EnvPath,
	}

	// Die Umgebungsdatei zuerst: sie gehört root und der Gruppe der Site, mit
	// 0640. In einer App-Umgebung stehen regelmäßig Datenbankpasswörter, und
	// eine Unit-Datei ist für jeden Benutzer des Servers lesbar.
	if err := os.MkdirAll(s.appDir, 0o750); err != nil {
		return nil, opErr(OpAppWrite, "app-verzeichnis: %v", err)
	}
	envChanged, err := writeIfChanged(data.EnvPath, []byte(env), 0o640)
	if err != nil {
		return nil, opErr(OpAppWrite, "umgebung schreiben: %v", err)
	}
	// root:<gruppe der site>. Der Prozess läuft unter dieser Gruppe und muss
	// die Datei lesen; schreiben darf sie nur root, sonst setzte sich die App
	// ihre eigene Umgebung.
	if err := applyOwner(data.EnvPath, "root", p.SystemUser, false); err != nil {
		return nil, opErr(OpAppWrite, "umgebung übereignen: %v", err)
	}

	unitChanged, err := writeIfChanged(res.UnitPath, []byte(unit), 0o644)
	if err != nil {
		return nil, opErr(OpAppWrite, "unit schreiben: %v", err)
	}
	res.Changed = envChanged || unitChanged

	if unitChanged {
		if out, err := run(ctx, longTimeout, "systemctl", "daemon-reload"); err != nil {
			return nil, opErr(OpAppWrite, "daemon-reload: %s", truncate(out, 300))
		}
	}
	if out, err := run(ctx, longTimeout, "systemctl", "enable", res.Unit); err != nil {
		return nil, opErr(OpAppWrite, "app eintragen: %s", truncate(out, 300))
	}
	res.Enabled = true

	// Nur bei einer Änderung neu starten. Ein Neustart bei jedem Speichern
	// wäre eine Unterbrechung ohne Anlass — und bei einer App, die gerade
	// Anfragen bedient, eine gut sichtbare.
	action := "start"
	if res.Changed {
		action = "restart"
	}
	if err := s.startService(ctx, OpAppWrite, res.Unit, action); err != nil {
		return nil, err
	}
	res.Active = true
	return res, nil
}

// opAppRemove hält die App an und räumt ihre Dateien weg.
//
// Das Verzeichnis der Site bleibt unangetastet: es gehört der Site, nicht der
// App. Was dort liegt, hat jemand hochgeladen oder gebaut.
func (s *Server) opAppRemove(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[AppNameParams](raw, OpAppRemove)
	if err != nil {
		return nil, err
	}
	if !templates.ValidAppName(p.Name) {
		return nil, opInputErr(OpAppRemove, "%q ist kein gültiger app-name", p.Name)
	}
	unit := templates.UnitName(p.Name)

	// Fehler beim Anhalten sind kein Abbruch: eine App, die schon aus ist,
	// meldet hier ebenfalls einen. Ziel ist der Zustand, nicht der Weg.
	if out, err := run(ctx, longTimeout, "systemctl", "disable", "--now", unit); err != nil {
		s.log.Debug("app war schon aus", "unit", unit, "out", truncate(out, 200))
	}

	for _, path := range []string{s.appUnitPath(p.Name), s.appEnvPath(p.Name)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, opErr(OpAppRemove, "%s entfernen: %v", path, err)
		}
	}
	if out, err := run(ctx, longTimeout, "systemctl", "daemon-reload"); err != nil {
		return nil, opErr(OpAppRemove, "daemon-reload: %s", truncate(out, 300))
	}
	return TextResult{Text: "app " + p.Name + " entfernt"}, nil
}

// opAppStatus sagt, was der Dienst wirklich tut — nicht, was das Panel glaubt.
func (s *Server) opAppStatus(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[AppNameParams](raw, OpAppStatus)
	if err != nil {
		return nil, err
	}
	if !templates.ValidAppName(p.Name) {
		return nil, opInputErr(OpAppStatus, "%q ist kein gültiger app-name", p.Name)
	}

	res := AppResult{
		Name:     p.Name,
		Unit:     templates.UnitName(p.Name),
		UnitPath: s.appUnitPath(p.Name),
		EnvPath:  s.appEnvPath(p.Name),
	}
	out, err := run(ctx, shortTimeout, "systemctl", "is-active", res.Unit)
	res.Active = err == nil && strings.TrimSpace(out) == "active"
	out, err = run(ctx, shortTimeout, "systemctl", "is-enabled", res.Unit)
	res.Enabled = err == nil && strings.TrimSpace(out) == "enabled"
	return res, nil
}

// appUnitPath und appEnvPath bilden die Pfade aus dem geprüften Namen.
//
// Aus dem Namen, nicht aus der Anfrage: käme der Pfad von außen, wäre "eine App
// schreiben" ein Weg, jede Datei des Servers durch eine systemd-Unit zu
// ersetzen — und die nächste davon läuft als root.
func (s *Server) appUnitPath(name string) string {
	return filepath.Join(unitDir, templates.UnitName(name)+".service")
}

func (s *Server) appEnvPath(name string) string {
	return filepath.Join(s.appDir, name+".env")
}

// writeIfChanged schreibt nur, wenn sich der Inhalt unterscheidet, und sagt, ob
// geschrieben wurde.
//
// Der Rückgabewert entscheidet über den Neustart. Ohne ihn startete jedes
// Speichern der Einstellungen die App neu — bei einer, die gerade Anfragen
// bedient, eine gut sichtbare Unterbrechung ohne Anlass.
func writeIfChanged(path string, data []byte, mode os.FileMode) (bool, error) {
	if alt, err := os.ReadFile(path); err == nil && bytes.Equal(alt, data) {
		return false, nil
	}
	return true, writeFileAtomic(path, data, mode)
}

// envList bringt die Umgebung in eine feste Reihenfolge. Eine Map hat keine.
func envList(m map[string]string) []templates.EnvVar {
	out := make([]templates.EnvVar, 0, len(m))
	for k, v := range m {
		out = append(out, templates.EnvVar{Key: k, Value: v})
	}
	return out
}
