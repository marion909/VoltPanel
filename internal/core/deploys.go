package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/gitspec"
	"github.com/marion909/voltpanel/internal/store"
)

// Git-Deploy: holen, bauen, umschalten — ausgelöst von Hand oder von einem
// Webhook.
//
// Der Webhook ist der Teil, der von außen erreichbar ist, ohne dass sich jemand
// angemeldet hat. Er hat deshalb zwei Ausweise: eine zufällige Adresse und eine
// Signatur über den Rumpf. Die Adresse allein wäre zu wenig — sie steht in den
// Einstellungen eines fremden Dienstes und in jedem Proxy-Log dazwischen.

// ErrDeployRunning sagt, dass für diese Site schon einer läuft.
var ErrDeployRunning = errors.New("für diese site läuft gerade ein deploy")

type DeployService struct {
	store   *store.Store
	agent   *agent.Client
	cfg     *config.Config
	apps    *AppService
	secrets *authn.SecretBox
	log     *slog.Logger

	// laufend hält je Site fest, dass gerade gebaut wird. Zwei Deploys
	// gleichzeitig auf demselben Verzeichnis wären zwei npm-Läufe im selben
	// node_modules — das Ergebnis wäre weder das eine noch das andere.
	mu      sync.Mutex
	laufend map[int64]bool
}

func NewDeployService(st *store.Store, ag *agent.Client, cfg *config.Config,
	secrets *authn.SecretBox, log *slog.Logger) *DeployService {

	if log == nil {
		log = slog.Default()
	}
	return &DeployService{
		store: st, agent: ag, cfg: cfg, secrets: secrets, log: log,
		apps:    NewAppService(st, ag, cfg, secrets),
		laufend: map[int64]bool{},
	}
}

// DeployInput ist, was von außen kommt. Hook-Adresse und -Geheimnis stehen
// nicht darin: die erzeugt der Server.
type DeployInput struct {
	SiteID     int64    `json:"site_id"`
	RepoURL    string   `json:"repo_url"`
	Ref        string   `json:"ref"`
	Steps      []string `json:"steps"`
	AutoDeploy bool     `json:"auto_deploy"`
}

// DeployView ist ein Deploy mit dem, was dazugehört.
type DeployView struct {
	*store.Deploy
	Domain string `json:"domain"`
	// HookURL ist die vollständige Adresse zum Eintragen beim Hoster.
	HookURL string `json:"hook_url"`
	Running bool   `json:"running"`
}

// Configure legt einen Deploy an oder ändert ihn.
//
// Beim Anlegen kommt das Geheimnis für die Signatur einmal zurück — danach nie
// wieder. Es liegt verschlüsselt, und es noch einmal zu zeigen hieße, es aus
// der Datenbank herauszugeben; wer es verliert, bekommt ein neues.
func (s *DeployService) Configure(ctx context.Context, sc store.Scope, in DeployInput) (
	*store.Deploy, string, error) {

	site, err := s.store.GetSite(ctx, sc, in.SiteID)
	if err != nil {
		return nil, "", err
	}

	vorhanden, err := s.store.DeployForSite(ctx, sc, site.ID)
	if err == nil {
		vorhanden.RepoURL, vorhanden.Ref = in.RepoURL, in.Ref
		vorhanden.Steps, vorhanden.AutoDeploy = in.Steps, in.AutoDeploy
		if err := s.store.UpdateDeploy(ctx, sc, vorhanden); err != nil {
			return nil, "", err
		}
		return vorhanden, "", nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, "", err
	}

	hookID, err := store.NewHookID()
	if err != nil {
		return nil, "", err
	}
	secret, err := store.NewHookSecret()
	if err != nil {
		return nil, "", err
	}
	enc, err := s.secrets.Encrypt(secret)
	if err != nil {
		return nil, "", err
	}

	d := &store.Deploy{
		TenantID: site.TenantID, SiteID: site.ID,
		RepoURL: in.RepoURL, Ref: in.Ref, Steps: in.Steps,
		HookID: hookID, HookSecretEnc: enc, AutoDeploy: in.AutoDeploy,
	}
	if err := s.store.CreateDeploy(ctx, sc, d); err != nil {
		return nil, "", err
	}
	return d, secret, nil
}

// Run führt einen Deploy aus.
//
// Läuft im Vordergrund und dauert Minuten — der Aufrufer entscheidet, ob er
// wartet. Über HTTP tut er das nicht: dort wird gestartet und der Zustand
// später abgefragt, sonst liefe die Anfrage in jeden Zeitüberlauf, den es
// zwischen Browser und Server gibt.
func (s *DeployService) Run(ctx context.Context, d *store.Deploy) error {
	if !s.beginne(d.SiteID) {
		return ErrDeployRunning
	}
	defer s.beende(d.SiteID)
	return s.ausfuehren(ctx, d)
}

// ausfuehren macht die eigentliche Arbeit. Setzt voraus, dass die
// Laufend-Sperre für d.SiteID bereits gehalten wird — Run und RunAsync
// reservieren sie jeweils selbst, damit sie das auch synchron, vor dem Start
// einer Goroutine, tun können.
func (s *DeployService) ausfuehren(ctx context.Context, d *store.Deploy) error {
	site, err := s.store.GetSite(ctx, store.SystemScope(), d.SiteID)
	if err != nil {
		return err
	}
	if err := s.store.RecordDeployRun(ctx, d.ID, d.LastRelease, d.LastCommit, "running", ""); err != nil {
		s.log.Warn("deploy-verlauf nicht gespeichert", "err", err)
	}

	res, err := s.agent.Deploy(ctx, agent.DeployParams{
		Name:       store.AppNameForDomain(site.Domain),
		SystemUser: site.SystemUser,
		RootPath:   site.RootPath,
		RepoURL:    d.RepoURL,
		Ref:        d.Ref,
		Steps:      d.Steps,
	})
	if err != nil {
		// Das Protokoll gehört auch dann gespeichert — gerade dann. Ein
		// Deploy, der nur "fehlgeschlagen" meldet, zwingt zur Shell, und die
		// hat der Kunde nicht.
		log := err.Error()
		if res != nil && res.Log != "" {
			log = res.Log + "\n" + err.Error()
		}
		if err := s.store.RecordDeployRun(ctx, d.ID, "", "", "failed", log); err != nil {
			s.log.Warn("deploy-verlauf nicht gespeichert", "err", err)
		}
		return err
	}

	// Die App zeigt jetzt auf einen neuen Stand: Unit neu schreiben und
	// starten. Ohne das liefe weiter der alte Code aus dem alten Verzeichnis.
	if app, err := s.store.AppForSite(ctx, store.SystemScope(), site.ID); err == nil {
		if err := s.apps.applyForSite(ctx, store.SystemScope(), site, app); err != nil {
			if err := s.store.RecordDeployRun(ctx, d.ID, res.Release, res.Commit, "failed",
				res.Log+"\napp neu starten: "+err.Error()); err != nil {
				s.log.Warn("deploy-verlauf nicht gespeichert", "err", err)
			}
			return err
		}
	}

	if err := s.store.RecordDeployRun(ctx, d.ID, res.Release, res.Commit, "ok", res.Log); err != nil {
		s.log.Warn("deploy-verlauf nicht gespeichert", "err", err)
	}
	return nil
}

// RunAsync stößt einen Deploy an und kommt sofort zurück.
//
// Die Sperre wird synchron reserviert, bevor die Goroutine überhaupt startet
// — sonst könnten zwei nahezu gleichzeitige Aufrufe (z. B. zwei
// Webhook-Zustellungen) beide den Laufend-Check passieren, bevor eine der
// beiden Goroutinen die Sperre tatsächlich setzt.
//
// Eigener Context: der der Anfrage endet, sobald der Browser die Antwort hat,
// und ein Build, der mitten im npm-Lauf abgebrochen wird, hinterlässt ein
// halbes node_modules.
func (s *DeployService) RunAsync(d *store.Deploy) error {
	if !s.beginne(d.SiteID) {
		return ErrDeployRunning
	}
	go func() {
		defer s.beende(d.SiteID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := s.ausfuehren(ctx, d); err != nil {
			s.log.Warn("deploy fehlgeschlagen", "site", d.SiteID, "err", err)
		}
	}()
	return nil
}

func (s *DeployService) beginne(siteID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.laufend[siteID] {
		return false
	}
	s.laufend[siteID] = true
	return true
}

func (s *DeployService) beende(siteID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.laufend, siteID)
}

func (s *DeployService) istLaufend(siteID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.laufend[siteID]
}

// HandleHook nimmt einen Webhook entgegen.
//
// Die Antwort ist für jeden Fehlerfall dieselbe: der Aufrufer soll nicht
// unterscheiden können, ob es die Adresse nicht gibt, die Signatur nicht passt
// oder der Branch ein anderer ist. Sonst wäre der Endpunkt ein Weg,
// Hook-Adressen durch Ausprobieren zu finden.
func (s *DeployService) HandleHook(ctx context.Context, hookID string,
	headers map[string]string, body []byte) (string, error) {

	d, err := s.store.DeployByHookID(ctx, hookID)
	if err != nil {
		return "", ErrHookAbgelehnt
	}
	secret, err := s.secrets.Decrypt(d.HookSecretEnc)
	if err != nil {
		return "", ErrHookAbgelehnt
	}
	if !VerifyHookSignature(secret, body, headers) {
		return "", ErrHookAbgelehnt
	}

	// Ab hier ist der Aufrufer ausgewiesen: er kennt das Geheimnis. Was jetzt
	// noch schiefgehen kann, darf und soll er erfahren.
	if !d.AutoDeploy {
		return "automatischer deploy ist abgeschaltet", nil
	}
	if ref := refFromPayload(body); ref != "" && ref != d.Ref {
		// Ein Push auf einen anderen Branch ist kein Fehler. Ohne diese Zeile
		// würde jeder Feature-Branch die Produktion überschreiben.
		return "anderer branch (" + ref + "), kein deploy", nil
	}
	if err := s.RunAsync(d); err != nil {
		return "", err
	}
	return "deploy gestartet", nil
}

// ErrHookAbgelehnt ist die eine Antwort für alles, was vor der Signaturprüfung
// oder an ihr scheitert.
var ErrHookAbgelehnt = errors.New("abgelehnt")

// VerifyHookSignature prüft die Signatur eines Webhooks.
//
// Drei Formen, weil die drei üblichen Hoster sich nicht einig sind:
//
//	X-Hub-Signature-256: sha256=<hex>   GitHub, Gitea
//	X-Gitea-Signature:   <hex>          ältere Gitea-Fassungen
//	X-Gitlab-Token:      <geheimnis>    GitLab schickt es im Klartext
//
// Verglichen wird überall in konstanter Zeit. Ein gewöhnlicher Vergleich bricht
// beim ersten falschen Zeichen ab, und aus der Laufzeit ließe sich das
// Geheimnis Zeichen für Zeichen erraten.
func VerifyHookSignature(secret string, body []byte, headers map[string]string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	erwartet := mac.Sum(nil)

	if h := headers["X-Hub-Signature-256"]; h != "" {
		// Mit Präfix, nicht ohne. GitHub und Gitea schicken immer "sha256=",
		// und eine Kopfzeile ohne es kommt von etwas, das sich nur so nennt.
		// Streng zu sein kostet hier nichts.
		roh, ok := strings.CutPrefix(h, "sha256=")
		return ok && gleich(roh, erwartet)
	}
	if h := headers["X-Gitea-Signature"]; h != "" {
		return gleich(h, erwartet)
	}
	if h := headers["X-Gitlab-Token"]; h != "" {
		return subtle.ConstantTimeCompare([]byte(h), []byte(secret)) == 1
	}
	// Ohne Signatur keine Annahme. Ein Webhook ohne Geheimnis wäre eine
	// Adresse, die jeder auslösen kann, der sie einmal in einem Log gesehen hat.
	return false
}

func gleich(hexSig string, erwartet []byte) bool {
	roh, err := hex.DecodeString(strings.TrimSpace(hexSig))
	if err != nil {
		return false
	}
	return hmac.Equal(roh, erwartet)
}

// refFromPayload liest den Branch aus dem Rumpf, soweit einer darin steht.
//
// Nur das eine Feld: der Rumpf kommt von außen, und alles, was hier mehr
// gelesen wird, ist mehr Angriffsfläche für nichts.
func refFromPayload(body []byte) string {
	var p struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return ""
	}
	return strings.TrimPrefix(p.Ref, "refs/heads/")
}

// List liefert die Deploys eines Mandanten samt Domain und Hook-Adresse.
func (s *DeployService) List(ctx context.Context, sc store.Scope) ([]DeployView, error) {
	deploys, err := s.store.ListDeploys(ctx, sc)
	if err != nil {
		return nil, err
	}
	sites, err := s.store.ListSites(ctx, sc)
	if err != nil {
		return nil, err
	}
	domain := make(map[int64]string, len(sites))
	for _, site := range sites {
		domain[site.ID] = site.Domain
	}

	out := make([]DeployView, 0, len(deploys))
	for _, d := range deploys {
		out = append(out, DeployView{
			Deploy: d, Domain: domain[d.SiteID],
			HookURL: s.HookURL(d.HookID), Running: s.istLaufend(d.SiteID),
		})
	}
	return out, nil
}

// HookURL baut die Adresse, die beim Hoster einzutragen ist.
//
// Ohne den Zugriffspfad des Panels: der Endpunkt liegt außerhalb, weil er sich
// über sein eigenes Geheimnis ausweist und die Verborgenheit des Pfads nicht
// braucht. Und weil diese Adresse in den Einstellungen eines fremden Dienstes
// landet, wo der Zugriffspfad des Betreibers nichts verloren hat.
func (s *DeployService) HookURL(hookID string) string {
	host := s.cfg.PanelDomain
	if host == "" {
		host = "<panel-domain>"
	}
	return fmt.Sprintf("https://%s/hooks/deploy/%s", host, hookID)
}

// Releases sagt, welche Stände dastehen und welcher gilt.
func (s *DeployService) Releases(ctx context.Context, sc store.Scope, siteID int64) (
	*agent.DeployListResult, error) {

	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return nil, err
	}
	return s.agent.DeployList(ctx, site.RootPath)
}

// Rollback schaltet auf einen vorhandenen Stand zurück und startet die App neu.
func (s *DeployService) Rollback(ctx context.Context, sc store.Scope, siteID int64,
	release string) error {

	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return err
	}
	if err := s.agent.DeployRollback(ctx, site.SystemUser, site.RootPath, release); err != nil {
		return err
	}
	if app, err := s.store.AppForSite(ctx, sc, site.ID); err == nil {
		return s.apps.applyForSite(ctx, sc, site, app)
	}
	return nil
}

// DeployKey liefert den öffentlichen Schlüssel zum Eintragen beim Hoster.
func (s *DeployService) DeployKey(ctx context.Context, sc store.Scope, siteID int64) (
	*agent.DeployKeyResult, error) {

	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return nil, err
	}
	return s.agent.DeployKey(ctx, store.AppNameForDomain(site.Domain), site.SystemUser)
}

// StepNames sind die möglichen Buildschritte. Die Oberfläche bietet sie an,
// statt ein Textfeld zu zeigen.
func (s *DeployService) StepNames() []string { return gitspec.StepNames() }
