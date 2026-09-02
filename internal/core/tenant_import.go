package core

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/version"
)

// Einen Mandanten einspielen.
//
// Der Gegenweg zum Export, und der unangenehmere: beim Einsammeln steht alles
// schon da, beim Einspielen muss es erst entstehen — und zwar in einer
// Reihenfolge, in der jede Zeile ihre Bezugspunkte schon findet.
//
// Die Nummern aus dem Bündel gelten hier nicht. Auf dem Zielserver hat der
// Mandant eine neue, seine Sites haben neue, seine Datenbanken haben neue. Was
// aufeinander zeigt, wird über eine Zuordnung umgehängt; alles, was das nicht
// tut, hinge nach dem Import an einer fremden Zeile.

// ImportResult sagt, was angekommen ist.
type ImportResult struct {
	TenantID int64  `json:"tenant_id"`
	Slug     string `json:"slug"`

	Sites int `json:"sites"`
	Users int `json:"users"`
	// Rebuilt sind die Sites, die auf diesem Server auch wirklich stehen —
	// mit Benutzer, Vhost und Pool. Sie kann kleiner sein als Sites; dann
	// steht daneben, was schiefging.
	Rebuilt   int `json:"rebuilt"`
	Databases int `json:"databases"`
	Cronjobs  int `json:"cronjobs"`
	Apps      int `json:"apps"`

	MailDomains int `json:"mail_domains"`
	Mailboxes   int `json:"mailboxes"`

	// Warnings sind Teile, die nicht mitkonnten. Ein Import, der still weniger
	// einspielt als im Bündel steht, ist schlimmer als einer, der es sagt.
	Warnings []string `json:"warnings,omitempty"`
}

// ImportTenant spielt ein Bündel ein.
//
// Der Mandant entsteht neu. Ein vorhandener wird nicht überschrieben und nicht
// ergänzt: ein halb überschriebener Mandant wäre schlimmer als gar keiner, und
// welche Hälfte gälte, wüsste danach niemand.
func (s *ExportService) ImportTenant(ctx context.Context, path, passphrase string) (
	*ImportResult, error) {

	bundle, box, err := OpenBundle(path, passphrase)
	if err != nil {
		return nil, err
	}
	if bundle.Schema > version.SchemaVersion {
		return nil, fmt.Errorf("das bündel stammt von schema %d, dieser server kennt %d — "+
			"erst aktualisieren, dann einspielen", bundle.Schema, version.SchemaVersion)
	}
	if bundle.Tenant == nil {
		return nil, errors.New("in dem bündel steckt kein mandant")
	}

	sys := store.SystemScope()
	if vorhanden, err := s.store.ListTenants(ctx, sys); err == nil {
		for _, t := range vorhanden {
			if strings.EqualFold(t.Slug, bundle.Tenant.Slug) {
				return nil, fmt.Errorf("%w: den mandanten %q gibt es auf diesem server schon",
					store.ErrConflict, t.Slug)
			}
		}
	}

	res := &ImportResult{Slug: bundle.Tenant.Slug}
	m := &idMap{
		sites: map[int64]int64{}, dbs: map[int64]int64{},
		dbUsers: map[int64]int64{}, mailDomains: map[int64]int64{},
	}

	if err := s.importTenantRow(ctx, sys, bundle, res); err != nil {
		return nil, err
	}
	if err := s.importUsers(ctx, sys, bundle, box, res); err != nil {
		return res, err
	}
	if err := s.importSites(ctx, sys, bundle, box, m, res); err != nil {
		return res, err
	}
	if err := s.importDatabases(ctx, sys, bundle, box, m, res); err != nil {
		return res, err
	}
	s.importRest(ctx, sys, bundle, box, m, res)

	// Erst die Zeilen, dann die Dateien: eine Site ohne Verzeichnis lässt sich
	// nachbauen, ein Verzeichnis ohne Site gehört niemandem.
	s.unpackFiles(ctx, path, bundle, m, res)
	s.restoreDatabases(ctx, path, bundle, res)

	// Und zuletzt der Server selbst.
	s.applySystem(ctx, res)

	return res, nil
}

// applySystem legt an, was auf diesem Server noch fehlt.
//
// Bis hierher stand der Mandant nur in der Datenbank und auf der Platte: die
// Zeilen sind da, die Dateien sind da, die Datenbanken laufen. Was fehlte,
// war alles, was der Server selbst führt — Linux-Benutzer, Vhost, FPM-Pool,
// Cron-Dateien, FTP-Zugänge, Units. Dafür stand am Ende des Imports ein
// Hinweis, man möge `volt site rebuild --all` laufen lassen.
//
// Ein Hinweis ist kein Zustand. Wer ihn übersieht, hat einen Mandanten, der
// in der Oberfläche vollständig aussieht und dessen Websites 502 liefern —
// und der Zusammenhang zum Import ist dann längst nicht mehr sichtbar. Also
// macht der Import es selbst.
//
// Fehler brechen nichts ab. Zeilen, Dateien und Datenbanken liegen schon; ein
// Abbruch hier ließe einen halb eingespielten Mandanten zurück, und das wäre
// schlechter als einer, bei dem eine Site nachgebaut werden muss. Was nicht
// ging, steht in den Warnungen — ein Import, der still weniger herstellt als
// er soll, ist schlimmer als einer, der es sagt.
func (s *ExportService) applySystem(ctx context.Context, res *ImportResult) {
	// Im Scope des eingespielten Mandanten, nicht im Systemscope: dieselbe
	// Regel wie überall sonst, und sie beantwortet hier auch die Frage, was
	// überhaupt dazugehört.
	sc := store.Scope{TenantID: res.TenantID, Role: store.RoleOwner}

	sites, err := s.store.ListSites(ctx, sc)
	if err != nil {
		res.Warnings = append(res.Warnings, "sites nachbauen: "+err.Error())
		return
	}

	siteSvc := NewSiteService(s.store, s.agent, s.cfg)
	nachSite := make(map[int64]*store.Site, len(sites))
	for _, site := range sites {
		nachSite[site.ID] = site
		// Der Benutzer, die Rechte, der Vhost und der FPM-Pool — alles, was
		// `volt site rebuild` herstellt, und zwar über denselben Weg. Ein
		// zweiter, hier nachgebauter Ablauf wäre die Stelle, an der Import
		// und Rebuild verschiedene Ergebnisse liefern.
		if err := siteSvc.Rebuild(ctx, sc, site.ID); err != nil {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s nachbauen: %v — `volt site rebuild %s` wiederholt es",
					site.Domain, err, site.Domain))
			continue
		}
		res.Rebuilt++
	}

	s.applyCronjobs(ctx, sc, res)
	s.applyFTP(ctx, sc, nachSite, res)
	s.applyApps(ctx, sc, nachSite, res)

	// Und die Mail-Dateien. Sie gelten für den ganzen Server, deshalb schreibt
	// der Dienst sie vollständig neu — mit dem eingespielten Mandanten darin.
	if res.MailDomains > 0 {
		mail := NewMailService(s.store, s.agent, s.cfg, s.secrets)
		if _, err := mail.Apply(ctx); err != nil {
			res.Warnings = append(res.Warnings,
				"die mail-dateien konnten nicht geschrieben werden: "+err.Error()+
					" — `volt mail apply` wiederholt es, sobald der mailspeicher steht")
		}
	}
}

// applyCronjobs schreibt die Dateien in /etc/cron.d.
func (s *ExportService) applyCronjobs(ctx context.Context, sc store.Scope, res *ImportResult) {
	jobs, err := s.store.ListCronjobs(ctx, sc)
	if err != nil {
		res.Warnings = append(res.Warnings, "cronjobs: "+err.Error())
		return
	}
	cron := NewCronService(s.store, s.agent, s.cfg)
	for _, job := range jobs {
		if err := cron.apply(ctx, job); err != nil {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("cronjob %s: %v — er steht in der Datenbank, läuft aber nicht",
					job.Name, err))
		}
	}
}

// applyFTP legt die virtuellen Zugänge wieder an.
//
// Das Passwort steht verschlüsselt in der Zeile, mit dem Schlüssel *dieses*
// Servers — der Import hat es beim Einspielen umgeschlüsselt. Pure-FTPd
// braucht es im Klartext, also einmal aufschließen. Wer diese Zeile liest und
// stutzt: das ist genau der Grund, warum das Bündel eine Passphrase hat.
func (s *ExportService) applyFTP(ctx context.Context, sc store.Scope,
	sites map[int64]*store.Site, res *ImportResult) {

	ftp := NewFTPService(s.store, s.agent, s.cfg, s.secrets)
	for siteID, site := range sites {
		accounts, err := s.store.ListFTPAccounts(ctx, sc, siteID)
		if err != nil {
			res.Warnings = append(res.Warnings, "ftp-zugänge: "+err.Error())
			continue
		}
		for _, a := range accounts {
			passwort, err := s.secrets.Decrypt(a.PasswordEnc)
			if err != nil {
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("ftp-zugang %s: das passwort lässt sich nicht lesen (%v) — "+
						"im Panel ein neues setzen", a.Username, err))
				continue
			}
			if err := ftp.apply(ctx, sc, a, site.SystemUser, passwort); err != nil {
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("ftp-zugang %s: %v", a.Username, err))
			}
		}
	}
}

// applyApps schreibt die Units und richtet den Vhost auf sie.
func (s *ExportService) applyApps(ctx context.Context, sc store.Scope,
	sites map[int64]*store.Site, res *ImportResult) {

	apps, err := s.store.ListApps(ctx, sc)
	if err != nil {
		res.Warnings = append(res.Warnings, "apps: "+err.Error())
		return
	}
	svc := NewAppService(s.store, s.agent, s.cfg, s.secrets)
	for _, app := range apps {
		site, ok := sites[app.SiteID]
		if !ok {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("app %s: die zugehörige site fehlt", app.Name))
			continue
		}
		if err := svc.apply(ctx, sc, site, app); err != nil {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("app %s: %v — sie steht in der Datenbank, läuft aber nicht",
					app.Name, err))
		}
	}
}

// idMap hält fest, welche Nummer aus dem Bündel welcher auf diesem Server
// entspricht.
type idMap struct {
	sites       map[int64]int64
	dbs         map[int64]int64
	dbUsers     map[int64]int64
	mailDomains map[int64]int64
}

func (s *ExportService) importTenantRow(ctx context.Context, sys store.Scope,
	b *TenantBundle, res *ImportResult) error {

	t := &store.Tenant{
		Name: b.Tenant.Name, Slug: b.Tenant.Slug, Status: b.Tenant.Status,
	}
	// Die Anmeldedomain kommt nicht mit: sie zeigt auf den alten Server, und
	// bis der DNS-Eintrag umgestellt ist, wäre sie eine Adresse, unter der
	// niemand ankommt. Sie steht schnell wieder drin.
	if b.Plan != nil {
		if id, err := s.planID(ctx, sys, b.Plan); err == nil {
			t.PlanID = &id
		} else {
			res.Warnings = append(res.Warnings, "paket: "+err.Error())
		}
	}
	if err := s.store.CreateTenant(ctx, sys, t); err != nil {
		return fmt.Errorf("mandant anlegen: %w", err)
	}
	res.TenantID = t.ID

	// Das Cloudflare-Token wird erst in importRest nachgetragen: dort liegt
	// ohnehin alles, was einen zweiten Durchgang über die Zeile braucht.
	return nil
}

// planID sucht ein gleichnamiges Paket oder legt es an.
//
// Über den Namen, nicht über die Nummer: Pakete sind serverweit, und auf dem
// Zielserver gibt es "Klein" vielleicht schon — nur mit anderer Nummer und
// womöglich anderen Grenzen. Dann gilt das dortige, denn es ist das, was der
// Betreiber dort verkauft.
func (s *ExportService) planID(ctx context.Context, sys store.Scope, p *store.Plan) (int64, error) {
	plans, err := s.store.ListPlans(ctx, sys)
	if err != nil {
		return 0, err
	}
	for _, vorhanden := range plans {
		if strings.EqualFold(vorhanden.Name, p.Name) {
			return vorhanden.ID, nil
		}
	}
	neu := *p
	neu.ID, neu.IsDefault = 0, false
	if err := s.store.CreatePlan(ctx, sys, &neu); err != nil {
		return 0, err
	}
	return neu.ID, nil
}

func (s *ExportService) importUsers(ctx context.Context, sys store.Scope,
	b *TenantBundle, box *authn.SecretBox, res *ImportResult) error {

	for _, u := range b.Users {
		neu := &store.User{
			TenantID: res.TenantID, Email: u.Email, DisplayName: u.DisplayName,
			PasswordHash: u.PasswordHash, Role: u.Role, Locale: u.Locale,
			Status: u.Status, TOTPEnabled: u.TOTPEnabled,
		}
		// Das TOTP-Geheimnis aus dem Bündel in die Verschlüsselung dieses
		// Servers überführen. Ohne das könnte sich niemand mit zweitem Faktor
		// anmelden — und das Konto wäre gesperrt statt umgezogen.
		if klar, ok := bundleSecret(b, box, fmt.Sprintf("user.%d.totp", u.ID)); ok {
			enc, err := s.secrets.Encrypt(klar)
			if err != nil {
				return fmt.Errorf("totp von %s: %w", u.Email, err)
			}
			neu.TOTPSecret = enc
		}
		if err := s.store.CreateUser(ctx, sys, neu); err != nil {
			// Eine schon vorhandene Adresse ist der häufigste Fall und kein
			// Grund, den ganzen Import zu verwerfen.
			res.Warnings = append(res.Warnings, "benutzer "+u.Email+": "+err.Error())
			continue
		}
		res.Users++
	}
	return nil
}

func (s *ExportService) importSites(ctx context.Context, sys store.Scope,
	b *TenantBundle, box *authn.SecretBox, m *idMap, res *ImportResult) error {

	for _, site := range b.Sites {
		neu := *site
		neu.ID, neu.TenantID = 0, res.TenantID
		// Der Pfad entsteht neu aus dem Verzeichnis dieses Servers: der alte
		// zeigte auf den alten Server, wo /var/www auch woanders liegen konnte.
		neu.RootPath = filepath.Join(s.cfg.SitesDir, site.Domain)

		if err := s.store.CreateSite(ctx, sys, &neu); err != nil {
			res.Warnings = append(res.Warnings, "site "+site.Domain+": "+err.Error())
			continue
		}
		m.sites[site.ID] = neu.ID
		res.Sites++

		if pool, ok := b.Pools[site.ID]; ok {
			p := *pool
			p.ID, p.TenantID, p.SiteID = 0, res.TenantID, neu.ID
			if err := s.store.CreatePHPPool(ctx, sys, &p); err != nil {
				res.Warnings = append(res.Warnings, "pool von "+site.Domain+": "+err.Error())
			}
		}
		for _, a := range b.FTP[site.ID] {
			acc := *a
			siteID := neu.ID
			acc.ID, acc.TenantID, acc.SiteID = 0, res.TenantID, &siteID
			acc.PasswordEnc = s.rekey(b, box, fmt.Sprintf("ftp.%d.password", a.ID), res)
			if err := s.store.CreateFTPAccount(ctx, sys, &acc); err != nil {
				res.Warnings = append(res.Warnings, "ftp "+a.Username+": "+err.Error())
			}
		}
	}
	return nil
}

func (s *ExportService) importDatabases(ctx context.Context, sys store.Scope,
	b *TenantBundle, box *authn.SecretBox, m *idMap, res *ImportResult) error {

	for _, db := range b.Databases {
		neu := *db
		neu.ID, neu.TenantID = 0, res.TenantID
		if db.SiteID != nil {
			if id, ok := m.sites[*db.SiteID]; ok {
				neu.SiteID = &id
			} else {
				neu.SiteID = nil
			}
		}
		if err := s.store.CreateDatabase(ctx, sys, &neu); err != nil {
			res.Warnings = append(res.Warnings, "datenbank "+db.Name+": "+err.Error())
			continue
		}
		m.dbs[db.ID] = neu.ID
		res.Databases++

		for _, u := range b.DBUsers[db.ID] {
			usr := *u
			usr.ID, usr.TenantID, usr.DatabaseID = 0, res.TenantID, neu.ID
			usr.PasswordEnc = s.rekey(b, box, fmt.Sprintf("dbuser.%d.password", u.ID), res)
			if err := s.store.CreateDBUser(ctx, sys, &usr); err != nil {
				res.Warnings = append(res.Warnings, "datenbankbenutzer "+u.Username+": "+err.Error())
				continue
			}
			m.dbUsers[u.ID] = usr.ID

			for _, h := range b.Remotes[u.ID] {
				host := *h
				host.ID, host.TenantID, host.DBUserID = 0, res.TenantID, usr.ID
				if err := s.store.CreateRemoteHost(ctx, sys, &host); err != nil {
					res.Warnings = append(res.Warnings,
						"herkunft "+h.Host+" von "+u.Username+": "+err.Error())
				}
			}
		}
	}
	return nil
}

// importRest spielt ein, was an keiner Nummer mehr hängt oder nur an einer
// Site.
func (s *ExportService) importRest(ctx context.Context, sys store.Scope,
	b *TenantBundle, box *authn.SecretBox, m *idMap, res *ImportResult) {
	s.importMail(ctx, sys, b, box, m, res)

	for _, c := range b.Cronjobs {
		job := *c
		job.ID, job.TenantID = 0, res.TenantID
		if c.SiteID != nil {
			if id, ok := m.sites[*c.SiteID]; ok {
				job.SiteID = &id
			} else {
				job.SiteID = nil
			}
		}
		if err := s.store.CreateCronjob(ctx, sys, &job); err != nil {
			res.Warnings = append(res.Warnings, "cronjob "+c.Name+": "+err.Error())
			continue
		}
		res.Cronjobs++
	}

	for _, a := range b.Apps {
		id, ok := m.sites[a.SiteID]
		if !ok {
			res.Warnings = append(res.Warnings, "app "+a.Name+": die site fehlt")
			continue
		}
		app := *a
		app.ID, app.TenantID, app.SiteID = 0, res.TenantID, id
		// Der Port wird neu vergeben: der alte ist auf diesem Server
		// vielleicht belegt, und zwei Apps auf demselben Port sind zwei
		// Sites, von denen eine nicht startet.
		app.Port = 0
		app.EnvEnc = s.rekey(b, box, fmt.Sprintf("app.%d.env", a.ID), res)
		if err := s.store.CreateApp(ctx, sys, &app); err != nil {
			res.Warnings = append(res.Warnings, "app "+a.Name+": "+err.Error())
			continue
		}
		res.Apps++
	}

	for _, d := range b.Deploys {
		id, ok := m.sites[d.SiteID]
		if !ok {
			continue
		}
		dep := *d
		dep.ID, dep.TenantID, dep.SiteID = 0, res.TenantID, id
		// Neue Hook-Adresse und neues Geheimnis: die alten stehen in den
		// Einstellungen eines fremden Dienstes und zeigen auf den alten
		// Server. Sie mitzunehmen hieße, zwei Server auf denselben Webhook
		// hören zu lassen.
		if dep.HookID, _ = store.NewHookID(); dep.HookID == "" {
			continue
		}
		secret, err := store.NewHookSecret()
		if err != nil {
			continue
		}
		if dep.HookSecretEnc, err = s.secrets.Encrypt(secret); err != nil {
			continue
		}
		if err := s.store.CreateDeploy(ctx, sys, &dep); err != nil {
			res.Warnings = append(res.Warnings, "deploy: "+err.Error())
			continue
		}
		res.Warnings = append(res.Warnings,
			"deploy für site "+fmt.Sprint(id)+": webhook-adresse und -geheimnis sind neu, "+
				"beim hoster nachtragen")
	}

	for _, t := range b.Targets {
		target := *t
		target.ID, target.TenantID = 0, res.TenantID
		target.SecretEnc = s.rekey(b, box, fmt.Sprintf("target.%d.secret", t.ID), res)
		if err := s.store.CreateBackupTarget(ctx, sys, &target); err != nil {
			res.Warnings = append(res.Warnings, "backup-ziel "+t.Name+": "+err.Error())
		}
	}

	// Das Cloudflare-Token des Mandanten.
	if klar, ok := bundleSecret(b, box, "tenant.cloudflare_token"); ok {
		enc, err := s.secrets.Encrypt(klar)
		if err == nil {
			if t, err := s.store.GetTenant(ctx, sys, res.TenantID); err == nil {
				t.CloudflareToken = enc
				if err := s.store.UpdateTenant(ctx, sys, t); err != nil {
					res.Warnings = append(res.Warnings, "cloudflare-token: "+err.Error())
				}
			}
		}
	}
}

// rekey holt ein Geheimnis aus dem Bündel und legt es unter dem Schlüssel
// dieses Servers wieder ab.
func (s *ExportService) rekey(b *TenantBundle, box *authn.SecretBox, key string,
	res *ImportResult) string {

	klar, ok := bundleSecret(b, box, key)
	if !ok {
		return ""
	}
	enc, err := s.secrets.Encrypt(klar)
	if err != nil {
		res.Warnings = append(res.Warnings, key+": "+err.Error())
		return ""
	}
	return enc
}

// unpackFiles legt die Dateien der Sites an ihren neuen Platz.
// importMail spielt Domänen, Postfächer und Weiterleitungen ein.
//
// Die Reihenfolge ist zwingend: erst die Domäne, dann was an ihr hängt. Die
// Nummern aus dem Bündel gelten hier nicht — was aufeinander zeigt, wird
// umgehängt, sonst hinge es an einer fremden Zeile.
func (s *ExportService) importMail(ctx context.Context, sys store.Scope,
	b *TenantBundle, box *authn.SecretBox, m *idMap, res *ImportResult) {

	for _, d := range b.MailDomains {
		dom := *d
		dom.ID, dom.TenantID = 0, res.TenantID
		if enc, ok := bundleSecret(b, box, fmt.Sprintf("maildomain.%d.dkim", d.ID)); ok {
			// Umgeschlüsselt auf diesen Server. Bliebe der Schlüssel zurück,
			// unterschriebe der neue Server mit einem anderen — und der
			// DNS-Eintrag zeigte weiter auf den alten.
			neu, err := s.secrets.Encrypt(enc)
			if err != nil {
				res.Warnings = append(res.Warnings,
					"dkim-schlüssel von "+d.Domain+": "+err.Error())
			} else {
				dom.DKIMPrivate = neu
			}
		}
		if err := s.store.CreateMailDomain(ctx, sys, &dom); err != nil {
			res.Warnings = append(res.Warnings, "maildomäne "+d.Domain+": "+err.Error())
			continue
		}
		m.mailDomains[d.ID] = dom.ID
		res.MailDomains++
	}

	for _, mb := range b.Mailboxes {
		id, ok := m.mailDomains[mb.DomainID]
		if !ok {
			res.Warnings = append(res.Warnings, "postfach "+mb.Address+": die domäne fehlt")
			continue
		}
		box2 := *mb
		box2.ID, box2.TenantID, box2.DomainID = 0, res.TenantID, id
		if enc, ok := bundleSecret(b, box, fmt.Sprintf("mailbox.%d.password", mb.ID)); ok {
			neu, err := s.secrets.Encrypt(enc)
			if err != nil {
				res.Warnings = append(res.Warnings, "passwort von "+mb.Address+": "+err.Error())
			} else {
				box2.PasswordEnc = neu
			}
		}
		if err := s.store.CreateMailbox(ctx, sys, &box2); err != nil {
			res.Warnings = append(res.Warnings, "postfach "+mb.Address+": "+err.Error())
			continue
		}
		res.Mailboxes++
	}

	for _, a := range b.MailAliases {
		id, ok := m.mailDomains[a.DomainID]
		if !ok {
			res.Warnings = append(res.Warnings, "weiterleitung "+a.Source+": die domäne fehlt")
			continue
		}
		alias := *a
		alias.ID, alias.TenantID, alias.DomainID = 0, res.TenantID, id
		if err := s.store.CreateMailAlias(ctx, sys, &alias); err != nil {
			res.Warnings = append(res.Warnings, "weiterleitung "+a.Source+": "+err.Error())
		}
	}
}

func (s *ExportService) unpackFiles(ctx context.Context, archive string,
	b *TenantBundle, m *idMap, res *ImportResult) {

	// Nur Domains, deren Site wirklich angelegt wurde: ein Verzeichnis ohne
	// Site gehört niemandem und zählt auf niemandes Quota.
	erlaubt := map[string]string{}
	for _, site := range b.Sites {
		if _, ok := m.sites[site.ID]; ok {
			erlaubt[site.Domain] = filepath.Join(s.cfg.SitesDir, site.Domain)
		}
	}

	// Und die Post: dieselbe Regel, nur ein anderes Verzeichnis. Nur Domänen,
	// die wirklich angelegt wurden — ein Maildir ohne Domäne bekäme nie Post
	// und läge nur da.
	erlaubteMail := map[string]string{}
	for _, d := range b.MailDomains {
		if _, ok := m.mailDomains[d.ID]; ok {
			erlaubteMail[d.Domain] = filepath.Join(mailRoot, d.Domain)
		}
	}

	if err := s.eachEntry(archive, func(h *tar.Header, r io.Reader) error {
		name := strings.TrimPrefix(filepath.ToSlash(h.Name), "./")

		if rest, ok := strings.CutPrefix(name, exportMailDir+"/"); ok {
			domain, sub, ok := strings.Cut(rest, "/")
			if !ok || sub == "" {
				return nil
			}
			root, ok := erlaubteMail[domain]
			if !ok {
				return nil
			}
			return writeUnder(root, sub, h, r)
		}

		rest, ok := strings.CutPrefix(name, exportSitesDir+"/")
		if !ok {
			return nil
		}
		domain, sub, ok := strings.Cut(rest, "/")
		if !ok || sub == "" {
			return nil
		}
		root, ok := erlaubt[domain]
		if !ok {
			return nil
		}
		return writeUnder(root, sub, h, r)
	}); err != nil {
		res.Warnings = append(res.Warnings, "dateien: "+err.Error())
	}
}

// writeUnder schreibt einen Eintrag unterhalb von root.
//
// Dieselbe Sorgfalt wie beim Node-Archiv: ein Bündel ist Eingabe, auch wenn es
// von einem eigenen Server stammt. Wer es in die Hand bekommt, kann darin
// stehen lassen, was er will.
func writeUnder(root, sub string, h *tar.Header, r io.Reader) error {
	if strings.HasPrefix(sub, "/") || strings.Contains(sub, "..") {
		return fmt.Errorf("das bündel enthält den pfad %q", h.Name)
	}
	ziel := filepath.Join(root, filepath.FromSlash(sub))

	switch h.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(ziel, 0o750)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(ziel), 0o750); err != nil {
			return err
		}
		mode := os.FileMode(0o640)
		if h.Mode&0o111 != 0 {
			mode = 0o750
		}
		f, err := os.OpenFile(ziel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(f, r); err != nil {
			return err
		}
		return os.Chmod(ziel, mode)
	case tar.TypeSymlink:
		if strings.HasPrefix(h.Linkname, "/") {
			return fmt.Errorf("das bündel enthält den symlink %q → %q", h.Name, h.Linkname)
		}
		if !unterhalbRoot(root, filepath.Join(filepath.Dir(ziel), h.Linkname)) {
			return fmt.Errorf("das bündel enthält den symlink %q → %q", h.Name, h.Linkname)
		}
		if err := os.MkdirAll(filepath.Dir(ziel), 0o750); err != nil {
			return err
		}
		_ = os.Remove(ziel)
		return os.Symlink(h.Linkname, ziel)
	}
	// Geräte, Sockets, Hardlinks: in einem Site-Verzeichnis hat davon nichts
	// etwas verloren.
	return nil
}

func unterhalbRoot(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// restoreDatabases spielt die Auszüge ein.
func (s *ExportService) restoreDatabases(ctx context.Context, archive string,
	b *TenantBundle, res *ImportResult) {

	if len(b.Databases) == 0 {
		return
	}
	if s.agent == nil {
		res.Warnings = append(res.Warnings, "datenbanken nicht eingespielt: kein agent verfügbar")
		return
	}
	namen := map[string]bool{}
	for _, db := range b.Databases {
		namen[db.Name] = true
	}

	err := s.eachEntry(archive, func(h *tar.Header, r io.Reader) error {
		name := strings.TrimPrefix(filepath.ToSlash(h.Name), "./")
		datei, ok := strings.CutPrefix(name, exportDBDir+"/")
		if !ok || h.Typeflag != tar.TypeReg {
			return nil
		}
		db := strings.TrimSuffix(datei, ".sql")
		// Nur Auszüge zu Datenbanken, die im Bündel stehen und eben angelegt
		// wurden. Ein Dateiname im Archiv bestimmt nicht, welche Datenbank auf
		// diesem Server überschrieben wird.
		if !namen[db] {
			return nil
		}

		tmp, err := os.CreateTemp(s.cfg.BackupDir, ".volt-restore-*.sql")
		if err != nil {
			return err
		}
		path := tmp.Name()
		defer os.Remove(path)
		if _, err := io.Copy(tmp, r); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := s.agent.ImportDatabase(ctx, db, path); err != nil {
			res.Warnings = append(res.Warnings, "datenbank "+db+": "+err.Error())
		}
		return nil
	})
	if err != nil {
		res.Warnings = append(res.Warnings, "datenbanken: "+err.Error())
	}
}

// eachEntry läuft einmal durch das Archiv.
func (s *ExportService) eachEntry(archive string, fn func(*tar.Header, io.Reader) error) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
}
