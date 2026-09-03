package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/templates"
)

// Webmail: die eine, server-weite Roundcube-Installation.
//
// Anders als ein Plugin (internal/core/plugins.go, apt-Paket plus Dienst) und
// anders als ein App-Store-Eintrag (internal/core/appstore.go, eine ganz
// gewöhnliche Site mit tenant_id) gehört Webmail keinem — jedes Postfach
// jedes Mandanten soll sich anmelden können, genau wie bei Postfix und
// Dovecot selbst. SiteService und DatabaseService verlangen beide eine echte
// tenant_id (harter Fremdschlüssel in sites/databases); eine erfundene
// "System"-Tenant-Zeile wäre die größere Verrenkung, dazu eine, die aus jeder
// mandantenbezogenen Liste wieder herausgefiltert werden müsste. Stattdessen:
// dieselben rohen Bausteine, die SiteService/DatabaseService selbst benutzen
// (agent.CreateSystemUser, agent.CreateDatabase, templates.RenderPool über
// einen nie gespeicherten *store.Site/*store.PHPPool) — direkt aufgerufen,
// ohne eine Site- oder Datenbank-Zeile.

const (
	webmailSystemUser = "volt-webmail"
	webmailPoolName   = "webmail"
	webmailDBName     = "volt_webmail"
	webmailDBUser     = "volt_webmail"
)

type WebmailService struct {
	store   *store.Store
	agent   *agent.Client
	cfg     *config.Config
	secrets *authn.SecretBox
	certs   *CertService
}

func NewWebmailService(st *store.Store, ag *agent.Client, cfg *config.Config,
	secrets *authn.SecretBox) *WebmailService {

	return &WebmailService{
		store: st, agent: ag, cfg: cfg, secrets: secrets,
		certs: NewCertService(cfg, st, ag, secrets, nil),
	}
}

// Status sagt, ob Webmail installiert ist — ohne das Datenbankpasswort.
func (s *WebmailService) Status(ctx context.Context) (*store.Webmail, error) {
	w, err := s.store.GetWebmail(ctx)
	if err != nil {
		return nil, err
	}
	w.DBPasswordEnc = ""
	return w, nil
}

// InstallWebmailInput ist, was ein Klick im Panel liefert.
type InstallWebmailInput struct {
	PHPVersion string
	TenantID   int64 // wessen Cloudflare-Token das Zertifikat holt
}

// Install richtet Roundcube ein: Systembenutzer, Datenbank samt Benutzer,
// FPM-Pool, die Dateien selbst, Zertifikat, Vhost — in dieser Reihenfolge,
// weil jeder Schritt auf dem vorherigen aufbaut und der Vhost erst zuletzt
// etwas Erreichbares ergibt.
//
// Kein Rückbau bei einem Fehler auf halbem Weg, aus demselben Grund wie bei
// AppStoreService.InstallWordPress: was schon entstanden ist, ist ein
// Zustand, den ein erneuter Versuch reparieren kann, sobald das Hindernis
// weg ist. Anders als dort ist ein zweiter Versuch hier aber kein
// automatischer — Install lehnt ab, wenn schon eine Zeile in der
// webmail-Tabelle steht. Wer nach einem gescheiterten Versuch neu ansetzen
// will, ruft erst Uninstall.
func (s *WebmailService) Install(ctx context.Context, sc store.Scope, in InstallWebmailInput) (
	*store.Webmail, error) {

	if s.cfg.PanelDomain == "" {
		return nil, errors.New("es ist keine panel-domain konfiguriert — panel_domain in der config.yaml setzen")
	}
	if _, err := s.store.GetWebmail(ctx); err == nil {
		return nil, errors.New("webmail ist schon installiert — erst entfernen, dann neu einrichten")
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	hostname := "webmail." + s.cfg.PanelDomain
	webRoot := s.cfg.SiteRoot("_webmail")

	if err := s.agent.CreateSystemUser(ctx, webmailSystemUser, webRoot); err != nil {
		return nil, fmt.Errorf("systembenutzer: %w", err)
	}

	dbPassword, err := authn.GeneratePassword(24)
	if err != nil {
		return nil, err
	}
	if err := s.agent.CreateDatabase(ctx, webmailDBName, "", ""); err != nil {
		return nil, fmt.Errorf("datenbank anlegen: %w", err)
	}
	if err := s.agent.CreateDBUser(ctx, agent.MySQLUserParams{
		Username: webmailDBUser, HostPattern: "localhost",
		Database: webmailDBName, Grants: "ALL", Password: dbPassword,
	}); err != nil {
		return nil, fmt.Errorf("datenbankbenutzer anlegen: %w", err)
	}

	// Ein in-memory Site/PHPPool, nie gespeichert — RenderPool liest nur
	// Domain, SystemUser, RootPath und die Pool-Felder, keines davon braucht
	// eine echte tenant_id.
	site := &store.Site{Domain: hostname, SystemUser: webmailSystemUser, RootPath: webRoot}
	pool := &store.PHPPool{
		PHPVersion: in.PHPVersion, PoolName: webmailPoolName,
		SocketPath: filepath.Join("/run/php", webmailPoolName+".sock"),
		PM:         "ondemand", MaxChildren: 6, MemoryLimit: "256M",
		MaxExecutionTime: 60, UploadMaxFilesize: "32M",
	}
	poolContent, err := templates.RenderPool(templates.PoolData{
		Site: site, Pool: pool, LogDir: filepath.Join(s.cfg.LogDir, "sites"),
	})
	if err != nil {
		return nil, fmt.Errorf("php-pool: %w", err)
	}
	if err := s.agent.WritePHPPool(ctx, pool.PHPVersion, pool.PoolName, poolContent); err != nil {
		return nil, fmt.Errorf("php-pool schreiben: %w", err)
	}

	// 993/587 sind keine Einstellungen, sondern das, was mail.setup
	// eingerichtet hat — dieselben festen Werte wie in MailService.Settings.
	// Roundcube verbindet sich mit localhost, nicht mit dem öffentlichen
	// Mailhostnamen; der ist hier ohne Belang.
	if _, err := s.agent.InstallWebmail(ctx, agent.WebmailInstallParams{
		WebRoot: webRoot, SystemUser: webmailSystemUser, WebGroup: s.cfg.WebGroup,
		DBName: webmailDBName, DBUser: webmailDBUser, DBPassword: dbPassword,
		IMAPPort: 993, SMTPPort: 587,
	}); err != nil {
		return nil, fmt.Errorf("roundcube: %w", err)
	}

	cert, err := s.certs.Issue(ctx, sc, IssueOptions{
		Domains: []string{hostname}, TenantID: in.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("zertifikat: %w", err)
	}

	vhost, err := templates.RenderWebmailVhost(templates.WebmailVhostData{
		Hostname: hostname, CertPath: cert.CertPath, KeyPath: cert.KeyPath,
		WebRoot: webRoot, LogDir: filepath.Join(s.cfg.LogDir, "sites"),
		SocketPath: pool.SocketPath,
	})
	if err != nil {
		return nil, fmt.Errorf("vhost: %w", err)
	}
	if err := s.agent.WriteVhost(ctx, hostname, vhost); err != nil {
		return nil, fmt.Errorf("vhost schreiben: %w", err)
	}

	encPassword, err := s.secrets.Encrypt(dbPassword)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetWebmail(ctx, hostname, in.PHPVersion, webmailDBName, webmailDBUser, encPassword); err != nil {
		return nil, err
	}
	return s.store.GetWebmail(ctx)
}

// Uninstall nimmt Vhost, Datenbank, Dateien und Systembenutzer wieder herunter.
//
// Die Reihenfolge ist umgekehrt zu Install: erst der Vhost, damit niemand
// mehr auf eine Installation trifft, die schon halb abgebaut ist. Jeder
// Schritt läuft weiter, auch wenn ein früherer scheitert — sonst bliebe ein
// Rest stehen, den kein zweiter Versuch mehr findet, weil die Zeile in der
// Datenbank schon weg ist.
func (s *WebmailService) Uninstall(ctx context.Context) error {
	w, err := s.store.GetWebmail(ctx)
	if err != nil {
		return err
	}

	var errs []error
	if err := s.agent.RemoveVhost(ctx, w.Hostname); err != nil {
		errs = append(errs, fmt.Errorf("vhost: %w", err))
	}
	if err := s.agent.RemovePHPPool(ctx, w.PHPVersion, webmailPoolName); err != nil {
		errs = append(errs, fmt.Errorf("php-pool: %w", err))
	}
	if err := s.agent.DropDBUser(ctx, w.DBUser, "localhost"); err != nil {
		errs = append(errs, fmt.Errorf("datenbankbenutzer: %w", err))
	}
	if err := s.agent.DropDatabase(ctx, w.DBName); err != nil {
		errs = append(errs, fmt.Errorf("datenbank: %w", err))
	}
	if err := s.agent.DeleteSystemUser(ctx, webmailSystemUser, true); err != nil {
		errs = append(errs, fmt.Errorf("systembenutzer: %w", err))
	}
	if err := s.store.DeleteWebmail(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
