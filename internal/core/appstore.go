package core

import (
	"context"
	"fmt"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/templates"
)

// App-Store: ein Klick, eine fertige Website.
//
// Anders als ein Plugin (internal/core/plugins.go, das den Server selbst
// erweitert) erzeugt ein App-Store-Eintrag eine ganz gewöhnliche Site mit
// ganz gewöhnlicher Datenbank — über dieselben Dienste, über die auch ein
// Kunde sie von Hand anlegt. Danach unterscheidet sich nichts mehr: die neue
// Site steht in der normalen Site-Liste, die Datenbank in der normalen
// Datenbankliste, und es gibt keine fortlaufende Buchführung eines
// "installierten App-Store-Eintrags", die aus dem Ruder laufen könnte.
//
// WordPress ist der erste und bisher einzige Eintrag.

type AppStoreService struct {
	store *store.Store
	agent *agent.Client
	cfg   *config.Config
	sites *SiteService
	dbs   *DatabaseService
}

func NewAppStoreService(st *store.Store, ag *agent.Client, cfg *config.Config,
	secrets *authn.SecretBox) *AppStoreService {

	return &AppStoreService{
		store: st, agent: ag, cfg: cfg,
		sites: NewSiteService(st, ag, cfg),
		dbs:   NewDatabaseService(st, ag, cfg, secrets),
	}
}

// InstallWordPressInput ist, was ein Klick im Panel liefert.
type InstallWordPressInput struct {
	Domain     string
	PHPVersion string
	TenantID   int64
}

// InstallWordPressResult beschreibt, was entstanden ist.
type InstallWordPressResult struct {
	Site     *store.Site     `json:"site"`
	Database *store.Database `json:"database"`
	// DBPassword steht genau einmal hier — danach nur noch verschlüsselt in
	// der Datenbank, wie jedes andere Datenbankpasswort in diesem Panel.
	DBPassword string `json:"db_password"`
}

// InstallWordPress legt Site und Datenbank an und packt den WordPress-Kern
// hinein.
//
// Die Reihenfolge ist dieselbe Vorsicht wie bei CreateSite selbst: erst, was
// sich billig zurückrollen lässt, dann das, was auf dem Server bleibt. Ein
// Fehlschlag auf halbem Weg lässt bewusst stehen, was schon entstanden ist —
// eine Site ohne Datenbank oder eine Site ohne WordPress-Dateien ist ein
// Zustand, den derselbe Aufruf reparieren kann, sobald das Hindernis (ein
// voller Datenpfad, ein kurzzeitig nicht erreichbares wordpress.org) weg ist.
// Ein Rückbau würde hier mehr zerstören als reparieren — dieselbe Abwägung
// wie in DatabaseService.CreateDatabase.
func (s *AppStoreService) InstallWordPress(ctx context.Context, sc store.Scope,
	in InstallWordPressInput) (*InstallWordPressResult, error) {

	site, err := s.sites.CreateSite(ctx, sc, CreateSiteInput{
		Domain: in.Domain, Type: store.SitePHP, PHPVersion: in.PHPVersion,
		TenantID: in.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("site anlegen: %w", err)
	}
	res := &InstallWordPressResult{Site: site}

	dbInput := CreateDatabaseInput{
		Name: wordpressDBBaseName(in.Domain), SiteID: &site.ID,
		TenantID: in.TenantID, WithUser: true,
	}
	dbRes, err := s.dbs.CreateDatabase(ctx, sc, dbInput)
	if err != nil {
		return res, fmt.Errorf("site %s angelegt, aber datenbank fehlgeschlagen: %w",
			site.Domain, err)
	}
	res.Database, res.DBPassword = dbRes.Database, dbRes.Password

	if _, err := s.agent.InstallWordPress(ctx, agent.WordPressInstallParams{
		WebRoot: site.WebRoot(), SystemUser: site.SystemUser, WebGroup: s.cfg.WebGroup,
	}); err != nil {
		return res, fmt.Errorf("site und datenbank angelegt, wordpress-kern aber nicht "+
			"ausgepackt: %w", err)
	}

	geheim, err := templates.NewWordPressSecrets()
	if err != nil {
		return res, fmt.Errorf("wordpress ausgepackt, aber schlüssel nicht erzeugt: %w", err)
	}
	geheim.DBName, geheim.DBUser, geheim.DBPassword = dbRes.Database.Name, dbRes.User.Username, dbRes.Password

	conf, err := templates.RenderWordPressConfig(geheim)
	if err != nil {
		return res, fmt.Errorf("wp-config.php: %w", err)
	}
	if err := s.agent.WriteFileGroup(ctx, site.WebRoot()+"/wp-config.php", conf,
		0o640, site.SystemUser, s.cfg.WebGroup); err != nil {
		return res, fmt.Errorf("wp-config.php schreiben: %w", err)
	}

	return res, nil
}

// wordpressDBBaseName leitet den Datenbanknamen aus der Domain her, nicht aus
// einer Eingabe — dieselbe Herleitung wie beim Namen einer App
// (store.AppNameForDomain). Ohne sie konkurrierten zwei
// WordPress-Installationen desselben Mandanten um denselben, festen Namen
// "wordpress" — CreateDatabase prefixt zwar noch mit dem Mandanten, aber
// nicht mit der Site.
func wordpressDBBaseName(domain string) string {
	return "wp_" + store.AppNameForDomain(domain)
}
