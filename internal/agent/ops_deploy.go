package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/gitspec"
	"github.com/marion909/voltpanel/internal/templates"
)

// Git-Deploy: einen Stand holen, bauen, umschalten.
//
// Der Umschalter ist ein Symlink. Das ist der ganze Grund für die
// Verzeichnisstruktur: ein Deploy, der in das laufende Verzeichnis schreibt,
// hat zwischen "halb kopiert" und "fertig" einen Zustand, in dem die Site
// kaputt ist. Ein Symlink wechselt in einem Schritt, und der Stand davor bleibt
// stehen — ein Rollback ist dann das Zurückzeigen, nicht ein zweiter Deploy.
//
//	<root>/releases/<zeit>/   der Klon samt Build
//	<root>/current            zeigt auf den gültigen davon
//	<root>/shared/            überlebt jeden Deploy (Uploads, lokale Daten)
//
// Alles darin gehört dem Systembenutzer der Site und entsteht unter seiner
// Kennung. Ein Buildschritt ist fremder Code aus einem Repository; als root
// ausgeführt wäre er ein Rootzugang, den sich der Kunde selbst mitbringt.

const (
	releasesDir = "releases"
	currentLink = "current"
	sharedDir   = "shared"

	// keepReleases ist, wie viele alte Stände stehen bleiben. Genug für ein
	// paar Schritte zurück, wenig genug, dass die Quota es aushält.
	keepReleases = 5

	// deployTimeout deckt Klon und Build ab. Ein npm-Build auf einem kleinen
	// Server braucht Minuten.
	deployTimeout = 20 * time.Minute
	cloneTimeout  = 5 * time.Minute
)

// DeployParams beschreibt einen Deploy vollständig.
type DeployParams struct {
	// Name ist der App-/Site-Name. Aus ihm entsteht der Pfad des Deploy-Keys.
	Name       string `json:"name"`
	SystemUser string `json:"system_user"`
	RootPath   string `json:"root_path"`
	RepoURL    string `json:"repo_url"`
	Ref        string `json:"ref"`
	// Steps sind Namen aus deploySteps, in der Reihenfolge der Ausführung.
	Steps []string `json:"steps"`
}

type DeployResult struct {
	Release string `json:"release"`
	Path    string `json:"path"`
	Commit  string `json:"commit"`
	// Log ist die gesammelte Ausgabe. Sie geht ins Panel, damit ein
	// fehlgeschlagener Build dort steht und nicht nur "Deploy fehlgeschlagen".
	Log     string `json:"log"`
	Removed int    `json:"removed"`
}

// opDeployRun holt den Stand, baut ihn und schaltet um.
func (s *Server) opDeployRun(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[DeployParams](raw, OpDeployRun)
	if err != nil {
		return nil, err
	}
	plan, err := s.deployPlan(p)
	if err != nil {
		return nil, err
	}

	// Jeder Stand in seinem eigenen Verzeichnis. Der Name ist die Zeit in UTC:
	// sortierbar, und beim Aufräumen unten entscheidet die Reihenfolge.
	release := time.Now().UTC().Format("20060102-150405")
	target := filepath.Join(plan.root, releasesDir, release)

	res := &DeployResult{Release: release, Path: target}
	var log strings.Builder

	if err := s.prepareDirs(plan, &log); err != nil {
		res.Log = log.String()
		return res, err
	}
	if err := s.cloneRelease(ctx, plan, target, &log); err != nil {
		// Ein halber Klon bleibt nicht liegen: er zählt auf die Quota und
		// sähe beim nächsten Mal wie ein gültiger Stand aus.
		_ = os.RemoveAll(target)
		res.Log = log.String()
		return res, err
	}

	res.Commit = s.headCommit(ctx, plan, target)

	if err := s.runSteps(ctx, plan, target, &log); err != nil {
		_ = os.RemoveAll(target)
		res.Log = log.String()
		return res, err
	}
	// Erst umschalten, wenn der Build durch ist. Ein Stand, der nicht gebaut
	// werden konnte, darf nie der gültige werden.
	if err := s.switchCurrent(ctx, plan, target); err != nil {
		_ = os.RemoveAll(target)
		res.Log = log.String()
		return res, err
	}
	res.Removed = s.pruneReleases(plan)
	res.Log = log.String()
	return res, nil
}

// deployPlan ist das Ergebnis aller Prüfungen: nur geprüfte Werte.
type deployPlan struct {
	name    string
	root    string
	uid     int
	gid     int
	repoURL string
	ref     string
	steps   [][]string
	keyPath string
}

func (s *Server) deployPlan(p DeployParams) (*deployPlan, error) {
	if !validDeployName(p.Name) {
		return nil, opInputErr(OpDeployRun, "%q ist kein gültiger name", p.Name)
	}
	root, err := jail(p.RootPath, s.roots)
	if err != nil {
		return nil, err
	}
	repoURL, err := gitspec.NormalizeURL(p.RepoURL)
	if err != nil {
		return nil, opInputErr(OpDeployRun, "%v", err)
	}
	ref := p.Ref
	if ref == "" {
		ref = "main"
	}
	if !gitspec.ValidRef(ref) {
		return nil, opInputErr(OpDeployRun, "%q ist kein gültiger branch- oder tagname", ref)
	}
	if len(p.Steps) > 10 {
		return nil, opInputErr(OpDeployRun, "höchstens 10 buildschritte")
	}

	steps := make([][]string, 0, len(p.Steps))
	for _, name := range p.Steps {
		cmd, ok := gitspec.Steps[name]
		if !ok {
			return nil, opInputErr(OpDeployRun, "%q ist kein bekannter buildschritt", name)
		}
		if !fileExists(allowedBinaries[cmd[0]]) {
			return nil, opInputErr(OpDeployRun, "für %q fehlt %s auf diesem server", name, cmd[0])
		}
		steps = append(steps, cmd)
	}

	// Zuletzt der Benutzer: alles darüber beurteilt die Anfrage und braucht
	// dafür niemanden zu fragen. Der Nachschlag geht über NSS und damit
	// womöglich ins Netz — und seine Meldung verdeckte sonst die eigentliche.
	uid, gid, err := siteUserIDs(OpDeployRun, p.SystemUser)
	if err != nil {
		return nil, err
	}

	return &deployPlan{
		name: p.Name, root: root, uid: uid, gid: gid,
		repoURL: repoURL, ref: ref, steps: steps, keyPath: s.deployKeyPath(p.Name),
	}, nil
}

// prepareDirs legt releases/ und shared/ an, beide dem Kunden gehörend.
func (s *Server) prepareDirs(plan *deployPlan, log *strings.Builder) error {
	for _, sub := range []string{releasesDir, sharedDir} {
		path := filepath.Join(plan.root, sub)
		if err := os.MkdirAll(path, 0o750); err != nil {
			return opErr(OpDeployRun, "%s anlegen: %v", sub, err)
		}
		if err := os.Lchown(path, plan.uid, plan.gid); err != nil {
			return opErr(OpDeployRun, "%s übereignen: %v", sub, err)
		}
	}
	fmt.Fprintf(log, "verzeichnisse bereit in %s\n", plan.root)
	return nil
}

// cloneRelease holt genau einen Stand.
//
// Flach und ohne Historie: gebraucht wird der Inhalt, nicht die Geschichte, und
// die Historie eines großen Repositorys zählt auf die Quota des Kunden.
func (s *Server) cloneRelease(ctx context.Context, plan *deployPlan, target string,
	log *strings.Builder) error {

	// "--" vor der Adresse: ohne das läse git eine Adresse, die mit einem
	// Bindestrich beginnt, als Option. NormalizeGitURL schließt das schon aus;
	// beides zusammen heißt, dass hier auch dann nichts passiert, wenn dort
	// einmal etwas durchrutscht.
	args := []string{
		"clone", "--depth", "1", "--single-branch",
		"--branch", plan.ref, "--config", "advice.detachedHead=false",
		"--", plan.repoURL, target,
	}
	out, err := runAsUser(ctx, cloneTimeout, plan.uid, plan.gid, plan.root,
		s.gitEnv(plan), "git", args...)
	fmt.Fprintf(log, "$ git clone %s (%s)\n%s\n", plan.repoURL, plan.ref, out)
	if err != nil {
		return opErr(OpDeployRun, "klonen: %s", truncate(out, 500))
	}
	return nil
}

// gitEnv setzt die Umgebung des git-Aufrufs.
//
// GIT_SSH_COMMAND ist die eine Stelle in diesem Projekt, an der ein Wert
// später doch von einer Shell gelesen wird: git übergibt ihn an `sh -c`.
// Deshalb steht dort ausschließlich Text aus dem Quelltext und ein Pfad, den
// der Agent selbst aus dem geprüften Namen gebildet hat — nie etwas aus einer
// Anfrage.
//
// IdentitiesOnly, damit ssh nicht doch einen anderen Schlüssel des Benutzers
// nimmt. accept-new statt no: ein unbekannter Host wird beim ersten Mal
// akzeptiert, ein *geänderter* Fingerabdruck aber abgelehnt — das ist der
// Unterschied zwischen "ich kenne GitHub noch nicht" und "hier antwortet
// jemand anderes".
func (s *Server) gitEnv(plan *deployPlan) []string {
	env := []string{
		"GIT_TERMINAL_PROMPT=0", // sonst wartet git auf eine Passworteingabe
		"HOME=" + plan.root,
	}
	if fileExists(plan.keyPath) {
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+plan.keyPath+
			" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o BatchMode=yes")
	}
	return env
}

func (s *Server) headCommit(ctx context.Context, plan *deployPlan, target string) string {
	out, err := runAsUser(ctx, shortTimeout, plan.uid, plan.gid, target,
		nil, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(truncate(out, 40))
}

// runSteps führt die Buildschritte aus.
func (s *Server) runSteps(ctx context.Context, plan *deployPlan, target string,
	log *strings.Builder) error {

	for _, cmd := range plan.steps {
		out, err := runAsUser(ctx, deployTimeout, plan.uid, plan.gid, target,
			[]string{"HOME=" + plan.root, "NODE_ENV=production", "CI=1"},
			cmd[0], cmd[1:]...)
		fmt.Fprintf(log, "$ %s\n%s\n", strings.Join(cmd, " "), truncate(out, 4000))
		if err != nil {
			return opErr(OpDeployRun, "%s: %s", strings.Join(cmd, " "), truncate(out, 500))
		}
	}
	return nil
}

// switchCurrent legt den Symlink um — in einem Schritt.
//
// Über einen temporären Namen und rename(2): ein `rm` mit anschließendem `ln`
// hätte dazwischen einen Moment ohne current, und wer in diesem Moment eine
// Anfrage stellt, bekommt einen Fehler. rename(2) ersetzt atomar.
func (s *Server) switchCurrent(ctx context.Context, plan *deployPlan, target string) error {
	link := filepath.Join(plan.root, currentLink)
	tmp := link + ".neu"

	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return opErr(OpDeployRun, "symlink anlegen: %v", err)
	}
	if err := os.Lchown(tmp, plan.uid, plan.gid); err != nil {
		_ = os.Remove(tmp)
		return opErr(OpDeployRun, "symlink übereignen: %v", err)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return opErr(OpDeployRun, "umschalten: %v", err)
	}
	return nil
}

// pruneReleases räumt alte Stände weg und gibt zurück, wie viele.
//
// Der gerade gültige bleibt in jedem Fall stehen, auch wenn er alt ist: nach
// einem Rollback ist der neueste Stand nicht der benutzte.
func (s *Server) pruneReleases(plan *deployPlan) int {
	dir := filepath.Join(plan.root, releasesDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	aktiv, _ := filepath.EvalSymlinks(filepath.Join(plan.root, currentLink))

	var namen []string
	for _, e := range entries {
		if e.IsDir() && validRelease(e.Name()) {
			namen = append(namen, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(namen)))
	if len(namen) <= keepReleases {
		return 0
	}

	var weg int
	for _, name := range namen[keepReleases:] {
		path := filepath.Join(dir, name)
		if aktiv != "" && path == aktiv {
			continue
		}
		if err := os.RemoveAll(path); err == nil {
			weg++
		}
	}
	return weg
}

// deployKeyPath bildet den Pfad des Deploy-Keys aus dem geprüften Namen.
func (s *Server) deployKeyPath(name string) string {
	return filepath.Join(s.deployDir, name+".key")
}

// validDeployName ist dieselbe Form wie ein App-Name: aus ihm entstehen
// Dateinamen, und beides bezeichnet dieselbe Site.
func validDeployName(name string) bool {
	return templates.ValidAppName(name)
}

// validRelease erkennt ein Release-Verzeichnis an seinem Namen.
//
// Nur was dieses Muster trifft, wird beim Aufräumen gelöscht. Ein Verzeichnis,
// das jemand von Hand dorthin gelegt hat, bleibt stehen — Aufräumen darf nie
// mehr wegnehmen, als es selbst angelegt hat.
var reRelease = regexp.MustCompile(`^\d{8}-\d{6}$`)

func validRelease(name string) bool { return reRelease.MatchString(name) }

// DeployKeyParams fragt den öffentlichen Deploy-Key ab und legt ihn an, wenn es
// noch keinen gibt.
type DeployKeyParams struct {
	Name       string `json:"name"`
	SystemUser string `json:"system_user"`
}

type DeployKeyResult struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	Created   bool   `json:"created"`
}

// opDeployKey liefert den öffentlichen Schlüssel, mit dem die Site ihr
// Repository lesen darf.
//
// Der private Teil verlässt den Server nie — auch nicht über das Panel. Er
// gehört dem Systembenutzer der Site, weil der git-Aufruf unter dessen Kennung
// läuft; mehr Trennung ist nicht möglich, ohne den Klon als root laufen zu
// lassen, und das wäre deutlich schlechter.
func (s *Server) opDeployKey(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[DeployKeyParams](raw, OpDeployKey)
	if err != nil {
		return nil, err
	}
	if !validDeployName(p.Name) {
		return nil, opInputErr(OpDeployKey, "%q ist kein gültiger name", p.Name)
	}
	uid, gid, err := siteUserIDs(OpDeployKey, p.SystemUser)
	if err != nil {
		return nil, err
	}

	key := s.deployKeyPath(p.Name)
	res := DeployKeyResult{Name: p.Name}

	if !fileExists(key) {
		if err := os.MkdirAll(s.deployDir, 0o750); err != nil {
			return nil, opErr(OpDeployKey, "schlüsselverzeichnis: %v", err)
		}
		// ed25519 ohne Passphrase: ein Schlüssel mit Passphrase, die neben ihm
		// liegen müsste, ist keiner. -N "" statt gar keiner Angabe, sonst
		// fragt ssh-keygen nach und wartet.
		out, err := run(ctx, shortTimeout, "ssh-keygen",
			"-t", "ed25519", "-N", "", "-C", "voltpanel:"+p.Name, "-f", key)
		if err != nil {
			return nil, opErr(OpDeployKey, "schlüssel erzeugen: %s", truncate(out, 300))
		}
		res.Created = true
	}
	// Dem Systembenutzer übereignen: unter seiner Kennung läuft git.
	for _, f := range []string{key, key + ".pub"} {
		if err := os.Lchown(f, uid, gid); err != nil {
			return nil, opErr(OpDeployKey, "%s übereignen: %v", f, err)
		}
	}
	if err := os.Chmod(key, 0o600); err != nil {
		return nil, opErr(OpDeployKey, "rechte am schlüssel: %v", err)
	}

	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return nil, opErr(OpDeployKey, "öffentlichen schlüssel lesen: %v", err)
	}
	res.PublicKey = strings.TrimSpace(string(pub))
	return res, nil
}

// DeployListResult sind die vorhandenen Stände einer Site.
type DeployListResult struct {
	Releases []string `json:"releases"`
	Current  string   `json:"current"`
}

type DeployListParams struct {
	RootPath string `json:"root_path"`
}

// opDeployList sagt, welche Stände dastehen und welcher gilt.
func (s *Server) opDeployList(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[DeployListParams](raw, OpDeployList)
	if err != nil {
		return nil, err
	}
	root, err := jail(p.RootPath, s.roots)
	if err != nil {
		return nil, err
	}

	res := DeployListResult{Releases: []string{}}
	entries, err := os.ReadDir(filepath.Join(root, releasesDir))
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && validRelease(e.Name()) {
				res.Releases = append(res.Releases, e.Name())
			}
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(res.Releases)))

	if ziel, err := os.Readlink(filepath.Join(root, currentLink)); err == nil {
		res.Current = filepath.Base(ziel)
	}
	return res, nil
}

// DeployRollbackParams schaltet auf einen vorhandenen Stand zurück.
type DeployRollbackParams struct {
	SystemUser string `json:"system_user"`
	RootPath   string `json:"root_path"`
	Release    string `json:"release"`
}

// opDeployRollback zeigt current auf einen älteren Stand.
//
// Das ist der Grund für die ganze Struktur: ein Rollback ist kein zweiter
// Deploy, der noch einmal bauen müsste und dabei ein anderes Ergebnis liefern
// könnte, sondern das Zurückzeigen auf etwas, das schon dasteht.
func (s *Server) opDeployRollback(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[DeployRollbackParams](raw, OpDeployRollback)
	if err != nil {
		return nil, err
	}
	root, err := jail(p.RootPath, s.roots)
	if err != nil {
		return nil, err
	}
	// Der Name geht nicht als Pfad durch, sondern nur als Verzeichnisname
	// unterhalb von releases/. Ein "../.." wäre sonst ein Symlink auf ein
	// beliebiges Verzeichnis des Servers, ausgeliefert unter der Domain des
	// Kunden.
	if !validRelease(p.Release) {
		return nil, opInputErr(OpDeployRollback, "%q ist kein stand", p.Release)
	}
	uid, gid, err := siteUserIDs(OpDeployRollback, p.SystemUser)
	if err != nil {
		return nil, err
	}

	target := filepath.Join(root, releasesDir, p.Release)
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return nil, opInputErr(OpDeployRollback, "den stand %s gibt es nicht", p.Release)
	}

	plan := &deployPlan{root: root, uid: uid, gid: gid}
	if err := s.switchCurrent(context.Background(), plan, target); err != nil {
		return nil, err
	}
	return TextResult{Text: "auf " + p.Release + " zurückgeschaltet"}, nil
}
