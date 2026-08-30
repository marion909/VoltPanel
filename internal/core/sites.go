// Package core enthält die Domänenlogik: es verbindet den Datenbestand
// (store), die erzeugten Configs (templates) und die privilegierten Aktionen
// (agent) zu vollständigen Vorgängen.
package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/templates"
)

type SiteService struct {
	store *store.Store
	agent *agent.Client
	cfg   *config.Config
}

func NewSiteService(st *store.Store, ag *agent.Client, cfg *config.Config) *SiteService {
	return &SiteService{store: st, agent: ag, cfg: cfg}
}

// CreateSiteInput ist das, was die API oder die CLI übergibt.
type CreateSiteInput struct {
	Domain       string
	Aliases      []string
	Type         store.SiteType
	PHPVersion   string
	ProxyTarget  string
	DocumentRoot string
	TenantID     int64
}

// CreateSite legt eine Site vollständig an: Datenbankeintrag, Systembenutzer,
// Verzeichnisse, FPM-Pool und Vhost.
//
// Die Reihenfolge ist so gewählt, dass jeder Schritt rückgängig gemacht werden
// kann: erst die Datenbank (billig zurückzurollen), dann die Systemressourcen.
// Scheitert ein späterer Schritt, räumt cleanup() die bereits erzeugten wieder ab.
func (s *SiteService) CreateSite(ctx context.Context, sc store.Scope, in CreateSiteInput) (*store.Site, error) {
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	if !store.ValidDomain(in.Domain) {
		return nil, fmt.Errorf("%q ist kein gültiger domainname", in.Domain)
	}
	if in.TenantID == 0 {
		in.TenantID = sc.TenantID
	}
	if in.DocumentRoot == "" && in.Type != store.SiteProxy {
		in.DocumentRoot = "public"
	}

	if err := s.checkQuota(ctx, sc, in.TenantID); err != nil {
		return nil, err
	}

	site := &store.Site{
		TenantID:     in.TenantID,
		Domain:       in.Domain,
		Aliases:      in.Aliases,
		Type:         in.Type,
		SystemUser:   SystemUserName(in.Domain),
		RootPath:     filepath.Join(s.cfg.SitesDir, in.Domain),
		DocumentRoot: in.DocumentRoot,
		PHPVersion:   in.PHPVersion,
		ProxyTarget:  in.ProxyTarget,
		ForceHTTPS:   true,
	}
	if err := s.store.CreateSite(ctx, sc, site); err != nil {
		return nil, err
	}

	// Ab hier existieren Systemressourcen. Jeder Fehler räumt sie wieder ab,
	// damit ein zweiter Versuch nicht an Resten des ersten scheitert.
	if err := s.provision(ctx, sc, site); err != nil {
		s.cleanup(ctx, sc, site)
		return nil, err
	}
	return site, nil
}

func (s *SiteService) provision(ctx context.Context, sc store.Scope, site *store.Site) error {
	if err := s.agent.CreateSystemUser(ctx, site.SystemUser, site.RootPath); err != nil {
		return fmt.Errorf("systembenutzer %s: %w", site.SystemUser, err)
	}

	// tmp liegt innerhalb der Site, damit open_basedir es abdeckt: PHP-Uploads
	// und Sessions einer Site landen so nie in einem gemeinsamen /tmp.
	dirs := []string{
		site.WebRoot(),
		filepath.Join(site.RootPath, "tmp"),
		filepath.Join(site.RootPath, "tmp", "sessions"),
		filepath.Join(site.RootPath, "logs"),
	}
	for _, dir := range dirs {
		if err := s.agent.Mkdir(ctx, dir, 0o750, site.SystemUser); err != nil {
			return fmt.Errorf("verzeichnis %s: %w", dir, err)
		}
	}

	if err := s.agent.WriteFile(ctx, filepath.Join(site.WebRoot(), "index.html"),
		placeholderPage(site.Domain), 0o644, site.SystemUser); err != nil {
		return fmt.Errorf("platzhalterseite: %w", err)
	}

	if site.Type == store.SitePHP {
		if err := s.writePool(ctx, sc, site); err != nil {
			return err
		}
	}
	return s.writeVhost(ctx, sc, site)
}

// writePool erzeugt den FPM-Pool einer PHP-Site und legt ihn ab.
func (s *SiteService) writePool(ctx context.Context, sc store.Scope, site *store.Site) error {
	pool := &store.PHPPool{
		TenantID:          site.TenantID,
		SiteID:            site.ID,
		PHPVersion:        site.PHPVersion,
		PoolName:          PoolName(site.Domain),
		SocketPath:        filepath.Join("/run/php", PoolName(site.Domain)+".sock"),
		PM:                "ondemand",
		MaxChildren:       10,
		MemoryLimit:       "256M",
		MaxExecutionTime:  30,
		UploadMaxFilesize: "64M",
	}
	if err := s.store.CreatePHPPool(ctx, sc, pool); err != nil {
		return fmt.Errorf("pool speichern: %w", err)
	}

	content, err := templates.RenderPool(templates.PoolData{
		Site: site, Pool: pool, LogDir: filepath.Join(s.cfg.LogDir, "sites"),
	})
	if err != nil {
		return err
	}
	if err := s.agent.WritePHPPool(ctx, pool.PHPVersion, pool.PoolName, content); err != nil {
		return fmt.Errorf("pool schreiben: %w", err)
	}
	return nil
}

// writeVhost erzeugt die Nginx-Config aus dem aktuellen Datenbankstand.
func (s *SiteService) writeVhost(ctx context.Context, sc store.Scope, site *store.Site) error {
	data := templates.SiteData{
		Site:        site,
		LogDir:      filepath.Join(s.cfg.LogDir, "sites"),
		ACMEWebroot: filepath.Join(s.cfg.DataDir, "acme"),
	}
	if site.Type == store.SitePHP {
		pool, err := s.store.PHPPoolBySite(ctx, sc, site.ID)
		if err != nil {
			return fmt.Errorf("pool zur site %s: %w", site.Domain, err)
		}
		data.SocketPath = pool.SocketPath
	}
	if site.SSLEnabled {
		data.CertPath = filepath.Join(s.cfg.CertDir, site.Domain, "fullchain.pem")
		data.KeyPath = filepath.Join(s.cfg.CertDir, site.Domain, "privkey.pem")
	}

	content, err := templates.RenderSite(data)
	if err != nil {
		return err
	}
	if err := s.agent.WriteVhost(ctx, site.Domain, content); err != nil {
		return fmt.Errorf("vhost schreiben: %w", err)
	}
	return nil
}

// Rebuild schreibt Vhost und Pool aus dem aktuellen Datenbankstand neu. Das ist
// der Weg, jede Änderung an einer Site wirksam zu machen — und zugleich die
// Reparatur, wenn eine Config von Hand verbogen wurde.
func (s *SiteService) Rebuild(ctx context.Context, sc store.Scope, siteID int64) error {
	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return err
	}
	if site.Type == store.SitePHP {
		pool, err := s.store.PHPPoolBySite(ctx, sc, site.ID)
		if err != nil {
			return err
		}
		content, err := templates.RenderPool(templates.PoolData{
			Site: site, Pool: pool, LogDir: filepath.Join(s.cfg.LogDir, "sites"),
		})
		if err != nil {
			return err
		}
		if err := s.agent.WritePHPPool(ctx, pool.PHPVersion, pool.PoolName, content); err != nil {
			return err
		}
	}
	return s.writeVhost(ctx, sc, site)
}

// DeleteSite entfernt Vhost, Pool, Systembenutzer und Datenbankeintrag.
//
// keepFiles lässt das Datenverzeichnis stehen — der Normalfall, weil ein
// versehentliches Löschen sonst die Kundendaten mitnimmt.
func (s *SiteService) DeleteSite(ctx context.Context, sc store.Scope, siteID int64, keepFiles bool) error {
	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return err
	}

	// Erst den Vhost weg — danach ist die Site nicht mehr erreichbar, auch wenn
	// ein späterer Schritt scheitert.
	var problems []string
	if err := s.agent.RemoveVhost(ctx, site.Domain); err != nil {
		problems = append(problems, "vhost: "+err.Error())
	}
	if site.Type == store.SitePHP {
		if pool, err := s.store.PHPPoolBySite(ctx, sc, site.ID); err == nil {
			if err := s.agent.RemovePHPPool(ctx, pool.PHPVersion, pool.PoolName); err != nil {
				problems = append(problems, "fpm-pool: "+err.Error())
			}
		}
	}
	if err := s.agent.DeleteSystemUser(ctx, site.SystemUser, !keepFiles); err != nil {
		problems = append(problems, "systembenutzer: "+err.Error())
	}
	if !keepFiles {
		if err := s.agent.RemovePath(ctx, site.RootPath, true); err != nil {
			problems = append(problems, "datenverzeichnis: "+err.Error())
		}
	}

	if err := s.store.DeleteSite(ctx, sc, siteID); err != nil {
		return err
	}
	if len(problems) > 0 {
		// Der Datenbankeintrag ist weg, aber der Betreiber muss erfahren, was
		// auf dem System zurückgeblieben ist.
		return fmt.Errorf("site %s entfernt, aber nicht vollständig aufgeräumt: %s",
			site.Domain, strings.Join(problems, "; "))
	}
	return nil
}

// cleanup macht ein halb angelegtes Provisioning rückgängig. Fehler werden
// bewusst geschluckt: der eigentliche Fehler des Aufrufers ist der wichtigere.
func (s *SiteService) cleanup(ctx context.Context, sc store.Scope, site *store.Site) {
	_ = s.agent.RemoveVhost(ctx, site.Domain)
	if site.Type == store.SitePHP {
		_ = s.agent.RemovePHPPool(ctx, site.PHPVersion, PoolName(site.Domain))
	}
	_ = s.agent.DeleteSystemUser(ctx, site.SystemUser, true)
	_ = s.store.DeleteSite(ctx, sc, site.ID)
}

// checkQuota prüft das Site-Limit des Hosting-Pakets (Phase 4).
func (s *SiteService) checkQuota(ctx context.Context, sc store.Scope, tenantID int64) error {
	tenantScope, err := sc.ForTenant(tenantID)
	if err != nil {
		return err
	}
	plan, err := s.store.PlanForTenant(ctx, tenantScope, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // kein Paket zugeordnet = kein Limit
	}
	if err != nil {
		return err
	}
	if plan.MaxSites <= 0 {
		return nil
	}

	count, err := s.store.CountSites(ctx, tenantScope)
	if err != nil {
		return err
	}
	if count >= plan.MaxSites {
		return fmt.Errorf("paket %q erlaubt %d sites, %d sind bereits angelegt",
			plan.Name, plan.MaxSites, count)
	}
	return nil
}

// reNonAlnum ersetzt alles, was in einem Linux-Benutzernamen nicht vorkommen darf.
var reNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// SystemUserName leitet den Linux-Benutzer aus der Domain ab.
//
// Linux begrenzt Benutzernamen auf 32 Zeichen; lange Domains werden gekürzt und
// mit einem Kurz-Hash eindeutig gehalten, damit example-sehr-lang.at und
// example-sehr-langer.at nicht denselben Benutzer bekommen.
func SystemUserName(domain string) string {
	return "site_" + shortName(domain, 26)
}

// PoolName ist der Name des FPM-Pools und zugleich der des Sockets.
func PoolName(domain string) string {
	return "volt-" + shortName(domain, 40)
}

func shortName(domain string, maxLen int) string {
	slug := strings.Trim(reNonAlnum.ReplaceAllString(strings.ToLower(domain), "_"), "_")
	if len(slug) <= maxLen {
		return slug
	}
	// fnv-1a, kurz und ohne Krypto-Anspruch — es geht nur um Eindeutigkeit.
	var h uint32 = 2166136261
	for i := 0; i < len(domain); i++ {
		h = (h ^ uint32(domain[i])) * 16777619
	}
	return fmt.Sprintf("%s_%08x", slug[:maxLen-9], h)
}

func placeholderPage(domain string) string {
	return `<!doctype html>
<html lang="de">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + domain + `</title>
<style>
  body { font-family: system-ui, sans-serif; display: grid; place-items: center;
         min-height: 100vh; margin: 0; background: #0f1115; color: #e6e8eb; }
  main { text-align: center; padding: 2rem; }
  h1 { font-weight: 600; margin: 0 0 .5rem; }
  p { color: #8b93a1; margin: 0; }
</style>
</head>
<body>
<main>
  <h1>` + domain + `</h1>
  <p>Diese Site wurde mit VoltPanel angelegt und wartet auf Inhalte.</p>
</main>
</body>
</html>
`
}
