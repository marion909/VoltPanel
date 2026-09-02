package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/dockerspec"
	"github.com/marion909/voltpanel/internal/templates"
)

const (
	dockerTimeout = 5 * time.Minute
	// pullTimeout: ein großes Image über eine langsame Leitung dauert.
	pullTimeout = 20 * time.Minute
)

// ContainerVolume ist ein Verzeichnis der Site, das im Container erscheint.
type ContainerVolume struct {
	// Source ist relativ zur Wurzel der Site. Absolut wäre er ein Weg, jedes
	// Verzeichnis des Servers in den Container zu hängen.
	Source string `json:"source"`
	Target string `json:"target"`
	// ReadOnly ist die Voreinstellung dieses Panels, nicht die von Docker.
	ReadOnly bool `json:"read_only"`
}

// ContainerParams beschreibt einen Container vollständig.
//
// Auffällig ist, was fehlt: kein Feld für Capabilities, keines für den
// Netzmodus, keines für ein Gerät, keines für den Benutzer im Container, keines
// für ein Kommando. Was hier nicht steht, lässt sich nicht anfordern — und das
// ist der Unterschied zwischen "geprüfte Schalter" und "keine Schalter".
type ContainerParams struct {
	Name       string            `json:"name"`
	SystemUser string            `json:"system_user"`
	RootPath   string            `json:"root_path"`
	Image      string            `json:"image"`
	Env        map[string]string `json:"env"`
	// HostPort liegt auf 127.0.0.1 und wird vom Panel vergeben. Port ist der
	// Port im Container.
	HostPort int               `json:"host_port"`
	Port     int               `json:"port"`
	Volumes  []ContainerVolume `json:"volumes"`
	MemoryMB int64             `json:"memory_mb"`
	CPUs     string            `json:"cpus"`
}

type ContainerNameParams struct {
	Name string `json:"name"`
	// Lines nur für Logs.
	Lines int `json:"lines"`
}

type ContainerResult struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Ports   string `json:"ports"`
	Created string `json:"created"`
}

// dockerStatus sagt, ob Docker überhaupt läuft und wie sicher es steht.
type DockerStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	// UserNSRemap ist die eigentliche Trennung zwischen Container und Wirt.
	// Ohne sie ist root im Container dieselbe UID wie root auf dem Server.
	UserNSRemap bool     `json:"userns_remap"`
	Rootless    bool     `json:"rootless"`
	Warnings    []string `json:"warnings,omitempty"`
}

// opDockerStatus berichtet, was der Server kann — und was ihm fehlt.
//
// Ehrliche Auskunft statt Automatik: die Trennung, auf die es ankommt
// (userns-remap), ist eine Einstellung des Docker-Daemons und lässt sich nicht
// je Container nachholen. Ein Panel, das dafür an /etc/docker/daemon.json
// schreibt und den Daemon neu startet, nähme jedem laufenden Container die
// Grundlage.
func (s *Server) opDockerStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	res := DockerStatus{}
	if !fileExists(allowedBinaries["docker"]) {
		res.Warnings = append(res.Warnings, "Docker ist auf diesem Server nicht installiert.")
		return res, nil
	}

	out, err := run(ctx, shortTimeout, "docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		res.Warnings = append(res.Warnings,
			"Docker ist installiert, der Daemon antwortet aber nicht: "+truncate(out, 200))
		return res, nil
	}
	res.Available = true
	res.Version = strings.TrimSpace(truncate(out, 40))

	// `docker info` nennt "userns" unter den SecurityOptions, wenn der Daemon
	// mit Benutzernamensraum-Abbildung läuft.
	if info, err := run(ctx, shortTimeout, "docker", "info", "--format",
		"{{range .SecurityOptions}}{{.}} {{end}}"); err == nil {
		res.UserNSRemap = strings.Contains(info, "userns")
		res.Rootless = strings.Contains(info, "rootless")
	}
	if !res.UserNSRemap && !res.Rootless {
		res.Warnings = append(res.Warnings,
			"Der Docker-Daemon läuft ohne Benutzernamensraum-Abbildung. Root in einem "+
				"Container ist damit dieselbe Kennung wie Root auf dem Server. VoltPanel "+
				"startet Container deshalb ausschließlich unter der Kennung der Site und "+
				"ohne jede Capability — mit userns-remap in /etc/docker/daemon.json wäre "+
				"die Trennung aber eine Ebene tiefer und damit belastbarer.")
	}
	return res, nil
}

// opDockerRun legt einen Container an und startet ihn.
//
// Ein gleichnamiger Container wird vorher entfernt: das macht die Operation
// wiederholbar. "Starte diesen Container" soll dasselbe Ergebnis haben,
// gleichgültig was vorher lief.
func (s *Server) opDockerRun(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ContainerParams](raw, OpDockerRun)
	if err != nil {
		return nil, err
	}
	uid, gid, err := siteUserIDs(OpDockerRun, p.SystemUser)
	if err != nil {
		return nil, err
	}
	args, err := s.dockerRunArgs(p, uid, gid)
	if err != nil {
		return nil, err
	}

	name := dockerspec.ContainerName(p.Name)
	// Fehler beim Entfernen sind kein Abbruch: meistens gibt es ihn noch nicht.
	if out, err := run(ctx, dockerTimeout, "docker", "rm", "-f", name); err != nil {
		s.log.Debug("kein alter container", "name", name, "out", truncate(out, 200))
	}

	out, err := run(ctx, dockerTimeout, "docker", args...)
	if err != nil {
		return nil, opErr(OpDockerRun, "container starten: %s", truncate(out, 500))
	}
	return ContainerResult{Name: name, ID: strings.TrimSpace(truncate(out, 64))}, nil
}

// dockerRunArgs baut die Kommandozeile.
//
// Ausgelagert, weil hier die ganze Sicherheit steckt und ein Test sie Wort für
// Wort ansehen soll. uid und gid kommen als Argumente statt aus dem Nachschlag:
// sonst liefe der Test nur auf einem Server, auf dem die Systembenutzer der
// Sites wirklich angelegt sind — und übersprungen würde ausgerechnet der Test,
// der die Schranken prüft.
func (s *Server) dockerRunArgs(p ContainerParams, uid, gid int) ([]string, error) {
	if !templates.ValidAppName(p.Name) {
		return nil, opInputErr(OpDockerRun, "%q ist kein gültiger name", p.Name)
	}
	if err := dockerspec.ValidImage(p.Image); err != nil {
		return nil, opInputErr(OpDockerRun, "%v", err)
	}
	root, err := jail(p.RootPath, s.roots)
	if err != nil {
		return nil, err
	}
	if p.HostPort < appPortMin || p.HostPort > appPortMax {
		return nil, opInputErr(OpDockerRun, "der host-port muss zwischen %d und %d liegen",
			appPortMin, appPortMax)
	}
	if p.Port < 1 || p.Port > 65535 {
		return nil, opInputErr(OpDockerRun, "der container-port muss zwischen 1 und 65535 liegen")
	}
	if p.CPUs != "" && !dockerspec.ValidCPUs(p.CPUs) {
		return nil, opInputErr(OpDockerRun, "%q ist keine cpu-angabe", p.CPUs)
	}
	if p.MemoryMB < 0 || p.MemoryMB > 262144 {
		return nil, opInputErr(OpDockerRun, "die speichergrenze ist unbrauchbar")
	}
	if uid < 1000 || gid < 1000 {
		// Dieselbe Schranke wie beim FTP-Zugang und bei der App: ein Container
		// unter einem Systemkonto wäre ein Prozess mit dessen Rechten, den ein
		// Kunde selbst gestartet hat.
		return nil, opInputErr(OpDockerRun, "ein container läuft nicht unter einem systemkonto")
	}

	args := []string{
		"run", "--detach",
		"--name", dockerspec.ContainerName(p.Name),
		"--restart", "unless-stopped",

		// Die Kennung der Site, nicht root. Ohne Benutzernamensraum-Abbildung
		// im Daemon ist root im Container dieselbe UID wie root auf dem Server;
		// ein Bind-Mount reicht dann, um dessen Dateien zu übernehmen.
		//
		// Das schließt Images aus, die als root starten müssen. Das ist der
		// Preis, und er ist der richtige herum bezahlt.
		"--user", fmt.Sprintf("%d:%d", uid, gid),

		// Keine einzige Capability. CAP_SYS_ADMIN allein reicht für einen
		// Ausbruch, und keines der Images, die hier laufen sollen, braucht
		// eine.
		"--cap-drop", "ALL",
		// Kein setuid-Programm im Container kann mehr Rechte erlangen, als der
		// Prozess schon hat.
		"--security-opt", "no-new-privileges",

		// Eine Fork-Bombe im Container soll den Server nicht mitnehmen.
		"--pids-limit", "512",

		// Eigener Netz-Namensraum (Docker-Vorgabe, hier ausgeschrieben):
		// --net=host wäre der Zugriff auf alles, was auf 127.0.0.1 horcht —
		// die Datenbank des Servers zum Beispiel.
		"--network", "bridge",

		// Nur auf 127.0.0.1. Der Weg von außen führt über den Vhost, wo TLS,
		// Zugriffsregeln und Protokollierung schon stehen.
		"--publish", fmt.Sprintf("127.0.0.1:%d:%d", p.HostPort, p.Port),

		"--label", "volt.site=" + p.Name,
	}
	if p.MemoryMB > 0 {
		args = append(args, "--memory", strconv.FormatInt(p.MemoryMB, 10)+"m")
	}
	if p.CPUs != "" {
		args = append(args, "--cpus", p.CPUs)
	}
	if len(p.Env) > 0 {
		args = append(args, "--env-file", s.containerEnvPath(p.Name))
	}

	for _, v := range p.Volumes {
		if err := dockerspec.CheckRelPath(v.Source); err != nil {
			return nil, opInputErr(OpDockerRun, "%v", err)
		}
		if err := dockerspec.CheckContainerPath(v.Target); err != nil {
			return nil, opInputErr(OpDockerRun, "%v", err)
		}
		// Die Quelle entsteht hier aus der geprüften Wurzel. Sie geht nicht
		// noch einmal durch jail(): der Pfad ist zusammengesetzt, nicht
		// übergeben, und checkRelPath hat "…" schon ausgeschlossen.
		mount := filepath.Join(root, v.Source) + ":" + v.Target
		if v.ReadOnly {
			mount += ":ro"
		}
		args = append(args, "--volume", mount)
	}

	// Das Image zuletzt und ohne Kommando dahinter. Ein Kommando wäre die
	// Stelle, an der ein Container doch wieder beliebigen Code ausführt, den
	// nicht der Image-Autor bestimmt hat.
	return append(args, p.Image), nil
}

// appPortMin/Max spiegeln den Bereich, aus dem das Panel Ports vergibt. Hier
// noch einmal, damit der Agent auch dann nicht auf einen fremden Port
// veröffentlicht, wenn im Panel jemand die Grenzen verschiebt.
const (
	appPortMin = 21000
	appPortMax = 21999
)

// containerEnvPath bildet den Pfad der Umgebungsdatei aus dem geprüften Namen.
func (s *Server) containerEnvPath(name string) string {
	return filepath.Join(s.appDir, "container-"+name+".env")
}

// opDockerEnv schreibt die Umgebungsdatei eines Containers.
//
// Eigene Operation, damit die Datei dasteht, bevor `docker run` sie liest — und
// eigene Datei mit 0640, aus demselben Grund wie bei den Apps: `docker inspect`
// gibt die Umgebung eines Containers an jeden heraus, der die Docker-Gruppe
// hat, und in ihr stehen regelmäßig Datenbankpasswörter.
func (s *Server) opDockerEnv(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ContainerParams](raw, OpDockerEnv)
	if err != nil {
		return nil, err
	}
	if !templates.ValidAppName(p.Name) {
		return nil, opInputErr(OpDockerEnv, "%q ist kein gültiger name", p.Name)
	}
	uid, gid, err := siteUserIDs(OpDockerEnv, p.SystemUser)
	if err != nil {
		return nil, err
	}

	data, err := templates.RenderAppEnv(templates.AppData{
		// Nur die Umgebung wird gerendert; die übrigen Felder tragen die
		// Prüfung des Templates und sind hier bedeutungslos.
		Name: p.Name, User: p.SystemUser, WorkingDir: "/tmp",
		EnvPath: "/tmp/x", Command: []string{"/bin/true"},
		Env: envList(p.Env),
	})
	if err != nil {
		return nil, opInputErr(OpDockerEnv, "%v", err)
	}

	path := s.containerEnvPath(p.Name)
	if err := os.MkdirAll(s.appDir, 0o750); err != nil {
		return nil, opErr(OpDockerEnv, "verzeichnis: %v", err)
	}
	if _, err := writeIfChanged(path, []byte(data), 0o640); err != nil {
		return nil, opErr(OpDockerEnv, "umgebung schreiben: %v", err)
	}
	if err := os.Lchown(path, 0, gid); err != nil {
		return nil, opErr(OpDockerEnv, "umgebung übereignen: %v", err)
	}
	_ = uid
	return TextResult{Text: path}, nil
}

// opDockerAction hält an, startet oder entfernt einen Container.
func (s *Server) opDockerAction(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Name   string `json:"name"`
		Action string `json:"action"`
	}](raw, OpDockerAction)
	if err != nil {
		return nil, err
	}
	if !templates.ValidAppName(p.Name) {
		return nil, opInputErr(OpDockerAction, "%q ist kein gültiger name", p.Name)
	}
	// Der Name geht mit Präfix hinein — der Agent setzt es, nicht der Aufrufer.
	// Ohne das wäre "meinen Container anhalten" ein Weg, jeden Container des
	// Servers anzuhalten, auch den einer fremden Site.
	name := dockerspec.ContainerName(p.Name)

	var args []string
	switch p.Action {
	case "start":
		args = []string{"start", name}
	case "stop":
		args = []string{"stop", name}
	case "restart":
		args = []string{"restart", name}
	case "remove":
		args = []string{"rm", "-f", name}
	default:
		return nil, opInputErr(OpDockerAction, "%q ist keine bekannte aktion", p.Action)
	}

	out, err := run(ctx, dockerTimeout, "docker", args...)
	if err != nil {
		return nil, opErr(OpDockerAction, "%s: %s", p.Action, truncate(out, 400))
	}
	return TextResult{Text: strings.TrimSpace(truncate(out, 200))}, nil
}

// opDockerList zeigt ausschließlich die Container dieses Panels.
//
// Über das Label, nicht über den Namen: ein Label setzt nur, wer den Container
// angelegt hat, und ein Name lässt sich nachträglich vergeben.
func (s *Server) opDockerList(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := run(ctx, shortTimeout, "docker", "ps", "--all",
		"--filter", "label=volt.site",
		"--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.State}}\t{{.Status}}\t{{.Ports}}\t{{.CreatedAt}}")
	if err != nil {
		return nil, opErr(OpDockerList, "%s", truncate(out, 300))
	}

	res := []ContainerResult{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) < 7 {
			continue
		}
		// Auch hier noch einmal: was nicht unser Präfix trägt, gehört uns nicht,
		// gleichgültig welches Label daran hängt.
		if !dockerspec.ContainerNameOwned(f[1]) {
			continue
		}
		res = append(res, ContainerResult{
			ID: f[0], Name: f[1], Image: f[2], State: f[3],
			Status: f[4], Ports: f[5], Created: f[6],
		})
	}
	return res, nil
}

// opDockerLogs liefert die letzten Zeilen eines Containers.
func (s *Server) opDockerLogs(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[ContainerNameParams](raw, OpDockerLogs)
	if err != nil {
		return nil, err
	}
	if !templates.ValidAppName(p.Name) {
		return nil, opInputErr(OpDockerLogs, "%q ist kein gültiger name", p.Name)
	}
	lines := p.Lines
	if lines < 1 || lines > 2000 {
		lines = 200
	}

	out, err := run(ctx, shortTimeout, "docker", "logs", "--tail",
		strconv.Itoa(lines), dockerspec.ContainerName(p.Name))
	if err != nil {
		return nil, opErr(OpDockerLogs, "%s", truncate(out, 300))
	}
	return TextResult{Text: truncate(out, 256<<10)}, nil
}

// opDockerPull holt ein Image.
//
// Getrennt vom Start, weil es Minuten dauert und weil die Meldung dann sagt,
// woran es lag: ein Tippfehler im Image-Namen ist etwas anderes als ein
// Container, der nicht startet.
func (s *Server) opDockerPull(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Image string `json:"image"`
	}](raw, OpDockerPull)
	if err != nil {
		return nil, err
	}
	if err := dockerspec.ValidImage(p.Image); err != nil {
		return nil, opInputErr(OpDockerPull, "%v", err)
	}

	// "--" vor dem Namen: ohne das läse docker einen Namen, der mit einem
	// Bindestrich beginnt, als Schalter. ValidImage schließt das schon aus;
	// beides zusammen heißt, dass hier auch dann nichts passiert, wenn dort
	// einmal etwas durchrutscht.
	out, err := run(ctx, pullTimeout, "docker", "pull", "--", p.Image)
	if err != nil {
		return nil, opErr(OpDockerPull, "image holen: %s", truncate(out, 500))
	}
	return TextResult{Text: truncate(out, 4000)}, nil
}
