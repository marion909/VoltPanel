package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	// quotaProjectBase hält die Projektnummern dieses Panels von denen fern,
	// die jemand anderes auf dem Server schon vergeben hat. 0 ist die Nummer,
	// die jede Datei ohne Projekt trägt, und darf nie gesetzt werden.
	quotaProjectBase = 100000
	quotaProjectMax  = 4_000_000_000
)

// reQuotaArg begrenzt, was in einem Pfad stehen darf, den der Agent an
// xfs_quota weitergibt.
//
// Der Grund ist die Aufrufform: xfs_quota nimmt seine eigenen Befehle als *eine*
// Zeichenkette hinter -c entgegen und zerlegt sie selbst an Leerzeichen. Das ist
// keine Shell, aber es ist eine zweite Zerlegung — und in die darf kein Pfad
// geraten, der sie verschiebt. Für ext4 wäre die Prüfung entbehrlich (dort geht
// jedes Argument einzeln), sie gilt trotzdem für beide: eine Regel, die nur auf
// einem von zwei Wegen greift, wird beim nächsten Umbau vergessen.
var reQuotaArg = regexp.MustCompile(`^/[A-Za-z0-9/._-]*$`)

// QuotaSupport ist die Auskunft darüber, ob dieser Server echte
// Dateisystem-Quotas führen kann.
type QuotaSupport struct {
	Path       string `json:"path"`
	Device     string `json:"device"`
	FSType     string `json:"fstype"`
	MountPoint string `json:"mount_point"`
	// Mounted: das Dateisystem ist mit Project Quota eingehängt.
	Mounted bool `json:"mounted"`
	// Tools: die Werkzeuge dafür sind installiert.
	Tools   bool     `json:"tools"`
	Missing []string `json:"missing,omitempty"`
	Ready   bool     `json:"ready"`
	// Hinweis sagt, was zu tun ist. Leer, wenn nichts zu tun ist.
	Hinweis string `json:"hinweis,omitempty"`
}

type QuotaStatusParams struct {
	Path string `json:"path"`
}

// opQuotaStatus sagt, ob unter einem Pfad echte Quotas möglich sind.
//
// Eine ehrliche Auskunft und keine Automatik: Project Quota hängt an einer
// Mount-Option, und die lässt sich nicht setzen, ohne das Dateisystem neu
// einzuhängen. Ein Panel, das dafür an /etc/fstab schreibt und neu einhängt,
// riskiert einen Server, der nicht mehr hochkommt — für ein Feature, das den
// Betrieb nicht aufhält, wenn es fehlt.
func (s *Server) opQuotaStatus(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[QuotaStatusParams](raw, OpQuotaStatus)
	if err != nil {
		return nil, err
	}
	path, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}
	return quotaSupport(path, readMounts(), installiert), nil
}

// installiert sagt, ob ein Programm der Whitelist wirklich auf dem Server
// liegt. Als Parameter, nicht fest verdrahtet: sonst hinge der Test daran, dass
// auf dem Rechner, der ihn ausführt, zufällig setquota installiert ist.
func installiert(bin string) bool {
	return fileExists(allowedBinaries[bin])
}

// quotaSupport stellt die Auskunft für einen Pfad zusammen.
func quotaSupport(path string, mounts []mountEntry, have func(string) bool) QuotaSupport {
	res := QuotaSupport{Path: path}

	m, ok := mountFor(path, mounts)
	if !ok {
		res.Hinweis = "Zu " + path + " ist kein Einhängepunkt zu finden."
		return res
	}
	res.Device, res.FSType, res.MountPoint = m.Device, m.FSType, m.Point
	res.Mounted = m.projectQuota()

	tools, ok := quotaTools[m.FSType]
	if !ok {
		res.Hinweis = fmt.Sprintf("%s liegt auf einem %s-Dateisystem. Project Quota gibt es "+
			"dort nicht; die Grenzen des Panels wirken weiter auf Anwendungsebene.",
			res.MountPoint, res.FSType)
		return res
	}

	for _, t := range tools {
		if !have(t) {
			res.Missing = append(res.Missing, t)
		}
	}
	res.Tools = len(res.Missing) == 0
	res.Ready = res.Mounted && res.Tools

	switch {
	case res.Ready:
	case !res.Mounted:
		res.Hinweis = mountHinweis(res)
	default:
		res.Hinweis = fmt.Sprintf("Es fehlen die Werkzeuge: %s. Nachinstallieren mit %s.",
			strings.Join(res.Missing, ", "), quotaPakete[m.FSType])
	}
	return res
}

// quotaTools sind die Programme, ohne die auf diesem Dateisystem nichts geht.
var quotaTools = map[string][]string{
	"ext4": {"chattr", "setquota"},
	"xfs":  {"xfs_quota"},
}

var quotaPakete = map[string]string{
	"ext4": "apt install quota e2fsprogs",
	"xfs":  "apt install xfsprogs",
}

// mountHinweis sagt, was am Einhängen zu ändern ist — für jedes Dateisystem
// etwas anderes, und in beiden Fällen nichts, was im laufenden Betrieb nebenbei
// geschieht.
func mountHinweis(res QuotaSupport) string {
	switch res.FSType {
	case "xfs":
		h := fmt.Sprintf("%s ist ohne Project Quota eingehängt. In /etc/fstab bei %s die "+
			"Option prjquota ergänzen und neu einhängen.", res.MountPoint, res.MountPoint)
		if res.MountPoint == "/" {
			h += " Für die Wurzel nimmt XFS die Option nur beim Booten: rootflags=prjquota " +
				"in die Kernel-Kommandozeile und neu starten."
		}
		return h
	case "ext4":
		return fmt.Sprintf("%s ist ohne Project Quota eingehängt. Zwei Schritte: erst "+
			"`tune2fs -O project,quota %s` — das geht nur an einem nicht eingehängten "+
			"Dateisystem —, dann in /etc/fstab bei %s die Option prjquota ergänzen und "+
			"neu einhängen.", res.MountPoint, res.Device, res.MountPoint)
	}
	return res.MountPoint + " ist ohne Project Quota eingehängt."
}

// QuotaProjectParams setzt die Grenze eines Mandanten im Dateisystem.
type QuotaProjectParams struct {
	// Tenant bestimmt die Projektnummer. Die Quota des Panels gilt je Mandant,
	// nicht je Site: wer fünf Sites hat, hat eine Grenze über alle fünf.
	Tenant int64 `json:"tenant"`
	// Dirs sind die Site-Wurzeln des Mandanten. Jede geht durch jail() — ein
	// Pfad außerhalb der Wurzeln bekäme sonst die Projektnummer eines fremden
	// Mandanten, und sein Verbrauch zählte dort mit.
	Dirs []string `json:"dirs"`
	// LimitMB ist die harte Grenze. 0 heißt unbegrenzt, dieselbe Bedeutung wie
	// überall sonst im Panel.
	LimitMB int64 `json:"limit_mb"`
}

type QuotaProjectResult struct {
	Tenant     int64    `json:"tenant"`
	ProjectID  uint32   `json:"project_id"`
	Dirs       []string `json:"dirs"`
	MountPoint string   `json:"mount_point,omitempty"`
	LimitMB    int64    `json:"limit_mb"`
	Applied    bool     `json:"applied"`
	// Skipped sagt, warum nichts geschah. Ein Server ohne Project Quota ist
	// kein Fehler — die Grenzen wirken dann weiter auf Anwendungsebene.
	Skipped string `json:"skipped,omitempty"`
}

// opQuotaProject hängt die Verzeichnisse eines Mandanten an eine Projektnummer
// und setzt darauf die Grenze.
//
// Idempotent: dieselbe Nummer noch einmal zu setzen ändert nichts, und die
// Grenze wird geschrieben, nicht verrechnet.
func (s *Server) opQuotaProject(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[QuotaProjectParams](raw, OpQuotaProject)
	if err != nil {
		return nil, err
	}
	id, err := quotaProjectID(p.Tenant)
	if err != nil {
		return nil, err
	}
	if p.LimitMB < 0 {
		return nil, opInputErr(OpQuotaProject, "die grenze kann nicht negativ sein")
	}
	if len(p.Dirs) > 500 {
		return nil, opInputErr(OpQuotaProject, "höchstens 500 verzeichnisse auf einmal")
	}

	res := QuotaProjectResult{Tenant: p.Tenant, ProjectID: id, LimitMB: p.LimitMB}

	for _, d := range p.Dirs {
		dir, err := jail(d, s.roots)
		if err != nil {
			return nil, err
		}
		// Ein Verzeichnis, das es nicht mehr gibt, ist kein Fehler: die Site
		// kann zwischen der Liste des Panels und diesem Aufruf gelöscht worden
		// sein. Der Rest des Mandanten soll deswegen nicht ungeschützt bleiben.
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if !reQuotaArg.MatchString(dir) {
			return nil, opInputErr(OpQuotaProject,
				"%q enthält zeichen, die in einem quota-aufruf nicht sicher stehen können", dir)
		}
		res.Dirs = append(res.Dirs, dir)
	}
	if len(res.Dirs) == 0 {
		res.Skipped = "kein vorhandenes verzeichnis"
		return res, nil
	}

	mounts := readMounts()
	mp, err := singleMount(res.Dirs, mounts)
	if err != nil {
		res.Skipped = err.Error()
		return res, nil
	}
	res.MountPoint = mp.Point

	sup := quotaSupport(mp.Point, mounts, installiert)
	if !sup.Ready {
		res.Skipped = sup.Hinweis
		return res, nil
	}

	switch mp.FSType {
	case "ext4":
		err = s.ext4Project(ctx, mp, id, res.Dirs, p.LimitMB)
	case "xfs":
		err = s.xfsProject(ctx, mp, id, res.Dirs, p.LimitMB)
	default:
		res.Skipped = "unbekanntes dateisystem " + mp.FSType
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	res.Applied = true
	return res, nil
}

// quotaProjectID bildet die Mandantennummer auf eine Projektnummer ab.
//
// Der Versatz hält Abstand zu 0 — das ist die Nummer jeder Datei ohne Projekt,
// und eine Grenze darauf träfe das halbe Dateisystem.
func quotaProjectID(tenant int64) (uint32, error) {
	if tenant < 1 || tenant > quotaProjectMax-quotaProjectBase {
		return 0, fmt.Errorf("%w: mandant %d hat keine projektnummer", errBadInput, tenant)
	}
	return uint32(quotaProjectBase + tenant), nil
}

// singleMount verlangt, dass alle Verzeichnisse auf demselben Dateisystem
// liegen.
//
// Sonst müsste dieselbe Grenze auf zwei Dateisystemen stehen, und der Mandant
// hätte das Doppelte — der Kernel kennt keine Grenze über zwei Dateisysteme
// hinweg. Lieber gar keine Quota als eine, die das Doppelte erlaubt und so
// aussieht, als täte sie es nicht.
func singleMount(dirs []string, mounts []mountEntry) (mountEntry, error) {
	var first mountEntry
	for i, d := range dirs {
		m, ok := mountFor(d, mounts)
		if !ok {
			return mountEntry{}, fmt.Errorf("zu %s ist kein einhängepunkt zu finden", d)
		}
		if i == 0 {
			first = m
			continue
		}
		if m.Point != first.Point {
			return mountEntry{}, fmt.Errorf(
				"die verzeichnisse liegen auf zwei dateisystemen (%s und %s); "+
					"eine gemeinsame grenze kann der kernel darüber nicht führen",
				first.Point, m.Point)
		}
	}
	return first, nil
}

// ext4Project setzt Projektnummer und Grenze auf ext4.
func (s *Server) ext4Project(ctx context.Context, mp mountEntry, id uint32,
	dirs []string, limitMB int64) error {

	num := strconv.FormatUint(uint64(id), 10)
	for _, dir := range dirs {
		// -R für den Bestand, +P für alles Künftige: das Vererbungsflag sorgt
		// dafür, dass eine morgen hochgeladene Datei dieselbe Nummer bekommt.
		// Ohne +P zählte sie wieder auf Projekt 0, und die Quota wäre eine
		// Momentaufnahme statt einer Grenze.
		if out, err := run(ctx, longTimeout, "chattr", "-R", "-p", num, "+P", dir); err != nil {
			return opErr(OpQuotaProject, "projektnummer auf %s: %s", dir, truncate(out, 300))
		}
	}
	// setquota rechnet in 1-KiB-Blöcken, und 0 heißt auch dort unbegrenzt.
	// Reihenfolge: weiche und harte Blockgrenze, weiche und harte Inode-Grenze.
	hard := strconv.FormatInt(limitMB*1024, 10)
	if out, err := run(ctx, longTimeout, "setquota", "-P", num,
		"0", hard, "0", "0", mp.Point); err != nil {
		return opErr(OpQuotaProject, "grenze auf %s: %s", mp.Point, truncate(out, 300))
	}
	return nil
}

// xfsProject setzt Projektnummer und Grenze auf XFS.
func (s *Server) xfsProject(ctx context.Context, mp mountEntry, id uint32,
	dirs []string, limitMB int64) error {

	for _, dir := range dirs {
		// -s durchläuft den Baum und setzt das Vererbungsflag; die Nummer steht
		// hier am Aufruf statt in /etc/projects, damit der Agent keine zweite
		// Datei pflegen muss, die zu seiner Vorstellung passen müsste.
		cmd := fmt.Sprintf("project -s -p %s %d", dir, id)
		if out, err := run(ctx, longTimeout, "xfs_quota", "-x", "-c", cmd, mp.Point); err != nil {
			return opErr(OpQuotaProject, "projektnummer auf %s: %s", dir, truncate(out, 300))
		}
	}
	// bhard=0 ist auch bei xfs_quota "unbegrenzt".
	cmd := fmt.Sprintf("limit -p bhard=%dm %d", limitMB, id)
	if out, err := run(ctx, longTimeout, "xfs_quota", "-x", "-c", cmd, mp.Point); err != nil {
		return opErr(OpQuotaProject, "grenze auf %s: %s", mp.Point, truncate(out, 300))
	}
	return nil
}
