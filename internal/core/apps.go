package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// Apps: eine Anwendung ist eine systemd-Unit plus Reverse-Proxy.
//
// Beide Hälften gehören zusammen, und das ist der Grund für diesen Dienst. Der
// Vhost muss auf den Port zeigen, unter dem die App wirklich horcht; die App
// muss unter dem Benutzer laufen, dem die Site gehört. Wer das an zwei Stellen
// pflegt, hat irgendwann einen Vhost, der auf nichts zeigt.

type AppService struct {
	store   *store.Store
	agent   *agent.Client
	cfg     *config.Config
	sites   *SiteService
	secrets *authn.SecretBox
}

func NewAppService(st *store.Store, ag *agent.Client, cfg *config.Config,
	secrets *authn.SecretBox) *AppService {

	return &AppService{
		store: st, agent: ag, cfg: cfg,
		sites: NewSiteService(st, ag, cfg), secrets: secrets,
	}
}

// AppInput ist, was von außen kommt. Name und Port stehen nicht darin: der Name
// entsteht aus der Domain, den Port vergibt der Store.
type AppInput struct {
	SiteID int64         `json:"site_id"`
	Kind   store.AppKind `json:"kind"`

	// Für kind="native".
	Runtime string   `json:"runtime"`
	Args    []string `json:"args"`

	// Für kind="docker".
	Image         string            `json:"image"`
	Volumes       []store.AppVolume `json:"volumes"`
	MemoryMB      int64             `json:"memory_mb"`
	CPUs          string            `json:"cpus"`
	ContainerPort int               `json:"container_port"`

	Env     map[string]string `json:"env"`
	Enabled bool              `json:"enabled"`
}

// AppView ist eine App mit dem, was der Agent über sie sagt.
type AppView struct {
	*store.App
	Domain string `json:"domain"`
	// EnvKeys sind die Namen der Umgebungsvariablen ohne ihre Werte. Die Werte
	// gibt das Panel nie wieder heraus — in einer App-Umgebung stehen
	// regelmäßig Passwörter, und wer sie einmal gesetzt hat, braucht sie nicht
	// zurückgelesen zu bekommen.
	EnvKeys []string `json:"env_keys"`
	Active  bool     `json:"active"`
	Unit    string   `json:"unit"`
}

// CreateApp legt eine App an, schreibt ihre Unit und richtet den Vhost auf sie.
func (s *AppService) CreateApp(ctx context.Context, sc store.Scope, in AppInput) (*store.App, error) {
	site, err := s.store.GetSite(ctx, sc, in.SiteID)
	if err != nil {
		return nil, err
	}
	// Nur eine Proxy-Site kann eine App haben: bei einer PHP- oder Static-Site
	// zeigte der Vhost weiter auf das Verzeichnis, und die App liefe für
	// niemanden.
	if site.Type != store.SiteProxy {
		// storeError macht daraus einen 400: ein Validierungsfehler aus
		// core/store ist für den Nutzer gedacht.
		return nil, fmt.Errorf("%s ist keine proxy-site — eine app braucht einen vhost, "+
			"der auf sie zeigt", site.Domain)
	}

	envEnc, err := s.encodeEnv(in.Env)
	if err != nil {
		return nil, err
	}

	app := &store.App{
		TenantID: site.TenantID,
		SiteID:   site.ID,
		Name:     store.AppNameForDomain(site.Domain),
		EnvEnc:   envEnc,
		Enabled:  true,
	}
	applyInput(app, in)
	if err := s.store.CreateApp(ctx, sc, app); err != nil {
		return nil, err
	}

	if err := s.apply(ctx, sc, site, app); err != nil {
		// Die Zeile wieder weg: eine App in der Datenbank, deren Unit nie
		// geschrieben wurde, belegt einen Port und eine Site und läuft nie.
		_ = s.store.DeleteApp(ctx, sc, app.ID)
		return nil, err
	}
	return app, nil
}

// applyInput überträgt, was von außen kommt — und nur das.
//
// Name, Port und Mandant stehen nicht darin: die entstehen im Panel, und sie
// über ein Eingabefeld änderbar zu machen hieße, dem Kunden die Vergabe von
// Ports und Unit-Namen zu überlassen.
func applyInput(app *store.App, in AppInput) {
	app.Kind = in.Kind
	if app.Kind == "" {
		app.Kind = store.AppNative
	}
	app.Runtime, app.Args = in.Runtime, in.Args
	app.Image, app.Volumes = in.Image, in.Volumes
	app.MemoryMB, app.CPUs, app.ContainerPort = in.MemoryMB, in.CPUs, in.ContainerPort
}

// UpdateApp ändert Laufzeit, Argumente oder Umgebung und schreibt neu.
//
// env == nil lässt die Umgebung, wie sie ist. Eine leere Map löscht sie. Der
// Unterschied ist nötig, weil das Panel die Werte nie zurückliest: ohne ihn
// löschte jedes Speichern der übrigen Felder die ganze Umgebung.
func (s *AppService) UpdateApp(ctx context.Context, sc store.Scope, id int64,
	in AppInput) (*store.App, error) {

	app, err := s.store.GetApp(ctx, sc, id)
	if err != nil {
		return nil, err
	}
	site, err := s.store.GetSite(ctx, sc, app.SiteID)
	if err != nil {
		return nil, err
	}

	applyInput(app, in)
	app.Enabled = in.Enabled
	if in.Env != nil {
		if app.EnvEnc, err = s.encodeEnv(in.Env); err != nil {
			return nil, err
		}
	}
	if err := s.store.UpdateApp(ctx, sc, app); err != nil {
		return nil, err
	}
	if err := s.apply(ctx, sc, site, app); err != nil {
		return nil, err
	}
	return app, nil
}

// DeleteApp hält die App an und räumt sie weg.
//
// Der Vhost bleibt stehen und zeigt auf einen Port, auf dem nichts mehr horcht.
// Das ist Absicht: die Site gehört dem Kunden, und sie einfach umzustellen
// hieße, eine Entscheidung für ihn zu treffen. Nginx meldet dann 502, und das
// ist die richtige Auskunft — hier läuft gerade nichts.
func (s *AppService) DeleteApp(ctx context.Context, sc store.Scope, id int64) error {
	app, err := s.store.GetApp(ctx, sc, id)
	if err != nil {
		return err
	}
	if err := s.entferne(ctx, app); err != nil {
		return err
	}
	return s.store.DeleteApp(ctx, sc, app.ID)
}

// applyForSite ist apply unter einem Namen, den auch der Deploy-Dienst ruft:
// nach einem neuen Stand muss die Unit neu geschrieben und gestartet werden,
// sonst liefe weiter der alte Code aus dem alten Verzeichnis.
func (s *AppService) applyForSite(ctx context.Context, sc store.Scope, site *store.Site,
	app *store.App) error {
	return s.apply(ctx, sc, site, app)
}

// apply schreibt Unit und Vhost. Beides oder nichts.
func (s *AppService) apply(ctx context.Context, sc store.Scope, site *store.Site,
	app *store.App) error {

	// Erst der Vhost: er zeigt auf den Port, den der Store vergeben hat. Ohne
	// diesen Schritt liefe die App, und niemand käme an sie heran.
	target := "http://127.0.0.1:" + strconv.Itoa(app.Port)
	if site.ProxyTarget != target {
		site.ProxyTarget = target
		if err := s.store.UpdateSite(ctx, sc, site); err != nil {
			return err
		}
		if err := s.sites.Rebuild(ctx, sc, site.ID); err != nil {
			return err
		}
	}

	if !app.Enabled {
		// Abgeschaltet heißt: weg, nicht bloß gestoppt. Eine Unit oder ein
		// Container, die dastehen und nicht laufen sollen, kämen beim nächsten
		// Neustart des Servers von selbst wieder.
		return s.entferne(ctx, app)
	}

	env, err := s.decodeEnv(app.EnvEnc)
	if err != nil {
		return err
	}

	if app.Kind == store.AppDocker {
		return s.applyContainer(ctx, site, app, env)
	}

	// PORT gehört dazu, ohne dass jemand ihn einträgt: fast jede Node-Anwendung
	// liest ihn, und wer ihn selbst setzen müsste, könnte ihn falsch setzen —
	// dann horchte die App woanders als der Vhost hinsieht.
	env["PORT"] = strconv.Itoa(app.Port)
	if _, gesetzt := env["NODE_ENV"]; !gesetzt {
		env["NODE_ENV"] = "production"
	}

	_, err = s.agent.WriteApp(ctx, agent.AppParams{
		Name:       app.Name,
		SystemUser: site.SystemUser,
		WorkingDir: s.workingDir(ctx, site),
		Runtime:    app.Runtime,
		Args:       app.Args,
		Env:        env,
	})
	return err
}

// applyContainer startet die App als Container.
//
// Die Umgebung zuerst: `docker run` liest die Datei beim Start, und wenn sie
// dann noch nicht dasteht, startet der Container ohne sie — mit einer
// Anwendung, die ihre Datenbank nicht findet und nicht sagt, warum.
func (s *AppService) applyContainer(ctx context.Context, site *store.Site,
	app *store.App, env map[string]string) error {

	vols := make([]agent.ContainerVolume, 0, len(app.Volumes))
	for _, v := range app.Volumes {
		vols = append(vols, agent.ContainerVolume{
			Source: v.Source, Target: v.Target, ReadOnly: v.ReadOnly,
		})
	}
	p := agent.ContainerParams{
		Name:       app.Name,
		SystemUser: site.SystemUser,
		RootPath:   site.RootPath,
		Image:      app.Image,
		Env:        env,
		HostPort:   app.Port,
		Port:       app.ContainerPort,
		Volumes:    vols,
		MemoryMB:   app.MemoryMB,
		CPUs:       app.CPUs,
	}
	if len(env) > 0 {
		if err := s.agent.WriteContainerEnv(ctx, p); err != nil {
			return err
		}
	}
	_, err := s.agent.RunContainer(ctx, p)
	return err
}

// entferne räumt weg, was zu dieser App gehört — je nach Art.
func (s *AppService) entferne(ctx context.Context, app *store.App) error {
	if app.Kind == store.AppDocker {
		return s.agent.ContainerAction(ctx, app.Name, "remove")
	}
	return s.agent.RemoveApp(ctx, app.Name)
}

// workingDir ist das Verzeichnis, in dem die App läuft.
//
// Gibt es einen Deploy, ist es der gültige Stand — <root>/current. Sonst die
// Wurzel der Site selbst, wie bei einer App, deren Code über FTP hochgeladen
// wird.
//
// Der Unterschied ist nicht kosmetisch: zeigte die Unit auf die Wurzel, liefe
// nach einem Deploy weiter der alte Code, denn dort liegt er noch. Und der
// Symlink wird beim Start aufgelöst, deshalb schreibt der Deploy die Unit
// danach neu.
func (s *AppService) workingDir(ctx context.Context, site *store.Site) string {
	if _, err := s.store.DeployForSite(ctx, store.SystemScope(), site.ID); err == nil {
		return site.RootPath + "/current"
	}
	return site.RootPath
}

// ListApps liefert die Apps eines Mandanten samt Zustand vom Agent.
func (s *AppService) ListApps(ctx context.Context, sc store.Scope) ([]AppView, error) {
	apps, err := s.store.ListApps(ctx, sc)
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

	out := make([]AppView, 0, len(apps))
	for _, app := range apps {
		view := AppView{App: app, Domain: domain[app.SiteID], Unit: "volt-app-" + app.Name}
		view.EnvKeys = s.envKeys(app.EnvEnc)
		// Ein Agent, der gerade nicht antwortet, darf die Liste nicht leeren:
		// dann steht eben "läuft nicht" da, und das ist ehrlicher als ein
		// Fehler statt der ganzen Übersicht.
		view.Active = s.laeuft(ctx, app)
		out = append(out, view)
	}
	return out, nil
}

// laeuft fragt den Agent, ob die App wirklich läuft.
//
// Ein Agent, der gerade nicht antwortet, ergibt "läuft nicht". Das ist
// ehrlicher als ein Fehler statt der ganzen Übersicht — und in der Sache
// meistens richtig.
func (s *AppService) laeuft(ctx context.Context, app *store.App) bool {
	if app.Kind == store.AppDocker {
		liste, err := s.agent.Containers(ctx)
		if err != nil {
			return false
		}
		for _, c := range liste {
			if c.Name == "volt-"+app.Name {
				return c.State == "running"
			}
		}
		return false
	}
	st, err := s.agent.AppStatus(ctx, app.Name)
	return err == nil && st.Active
}

// DockerStatus sagt, ob Docker läuft und wie sicher es steht.
func (s *AppService) DockerStatus(ctx context.Context) (*agent.DockerStatus, error) {
	return s.agent.DockerStatusOf(ctx)
}

// PullImage holt ein Image vorab. Getrennt vom Start, damit ein Tippfehler im
// Image-Namen als solcher auffällt und nicht als Container, der nicht startet.
func (s *AppService) PullImage(ctx context.Context, image string) (string, error) {
	return s.agent.PullImage(ctx, image)
}

// ContainerLogs liefert die letzten Zeilen eines Containers.
func (s *AppService) ContainerLogs(ctx context.Context, sc store.Scope, id int64,
	lines int) (string, error) {

	app, err := s.store.GetApp(ctx, sc, id)
	if err != nil {
		return "", err
	}
	if app.Kind != store.AppDocker {
		return "", fmt.Errorf("%s läuft nicht als container", app.Name)
	}
	return s.agent.ContainerLogs(ctx, app.Name, lines)
}

// Runtimes sagt, welche Laufzeitumgebungen der Server hat.
func (s *AppService) Runtimes(ctx context.Context) ([]agent.RuntimeInfo, error) {
	return s.agent.AppRuntimes(ctx)
}

// encodeEnv verschlüsselt die Umgebung. In ihr stehen regelmäßig
// Datenbankpasswörter, und die Datenbank des Panels ist eine Datei.
func (s *AppService) encodeEnv(env map[string]string) (string, error) {
	if len(env) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return s.secrets.Encrypt(string(raw))
}

func (s *AppService) decodeEnv(enc string) (map[string]string, error) {
	out := map[string]string{}
	if enc == "" {
		return out, nil
	}
	raw, err := s.secrets.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("umgebung entschlüsseln: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("umgebung lesen: %w", err)
	}
	return out, nil
}

// envKeys sind die Namen ohne die Werte. Ein Fehler beim Entschlüsseln gibt
// eine leere Liste — nicht die Werte, und nicht die halbe Liste.
func (s *AppService) envKeys(enc string) []string {
	env, err := s.decodeEnv(enc)
	if err != nil {
		return []string{}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
