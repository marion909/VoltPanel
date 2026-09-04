// Package api ist die HTTP-Schicht des Panels: Routen, Middlewares und die
// Auslieferung des eingebetteten Frontends.
package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/metrics"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/webui"
)

type Server struct {
	echo      *echo.Echo
	cfg       *config.Config
	store     *store.Store
	agent     *agent.Client
	metrics   *metrics.Collector
	sites     *core.SiteService
	databases *core.DatabaseService
	files     *core.FileService
	ftp       *core.FTPService
	cron      *core.CronService
	apps      *core.AppService
	mail      *core.MailService
	plugins   *core.PluginService
	appstore  *core.AppStoreService
	webmail   *core.WebmailService
	deploys   *core.DeployService
	quota     *core.QuotaService
	certs     *core.CertService
	backups   *core.BackupService
	secrets   *authn.SecretBox
	log       *slog.Logger
	loginRate *rateLimiter
	// logins hält die Zuordnung Anmeldedomain → Mandant im Speicher.
	logins    *loginDomains
	devOrigin string
}

type Options struct {
	Config    *config.Config
	Store     *store.Store
	Agent     *agent.Client
	Metrics   *metrics.Collector
	Secrets   *authn.SecretBox
	Logger    *slog.Logger
	DevOrigin string // erlaubt den Vite-Dev-Server als Origin
}

func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	// Echos Standard-Handler verrät bei einem Panic Interna; unser Handler
	// protokolliert vollständig und antwortet knapp.
	e.HTTPErrorHandler = errorHandler(opts.Logger)

	// Ohne expliziten IPExtractor fällt Echo auf ein Verhalten zurück, das
	// X-Forwarded-For/X-Real-IP ungeprüft vertraut — unabhängig davon, ob
	// überhaupt ein Reverse-Proxy davorsteht. Beides hängt an c.RealIP():
	// die IP-Whitelist (middleware.go) und das Login-Ratelimit (auth.go).
	// Ohne diese Zeile könnte ein externer Angreifer beide über einen
	// beliebigen, selbst gesetzten Header umgehen. trust_proxy ist Standard
	// aus, weil volt-web TLS meist selbst terminiert (siehe docs/release.md);
	// wer bewusst einen Reverse-Proxy davorstellt, setzt trust_proxy: true
	// und bekommt die Herkunft aus X-Forwarded-For, sonst nie.
	if opts.Config != nil && opts.Config.TrustProxy {
		e.IPExtractor = echo.ExtractIPFromXFFHeader()
	} else {
		e.IPExtractor = echo.ExtractIPDirect()
	}

	s := &Server{
		echo:      e,
		cfg:       opts.Config,
		store:     opts.Store,
		agent:     opts.Agent,
		metrics:   opts.Metrics,
		secrets:   opts.Secrets,
		sites:     core.NewSiteService(opts.Store, opts.Agent, opts.Config),
		databases: core.NewDatabaseService(opts.Store, opts.Agent, opts.Config, opts.Secrets),
		files:     core.NewFileService(opts.Store, opts.Agent, opts.Config),
		ftp:       core.NewFTPService(opts.Store, opts.Agent, opts.Config, opts.Secrets),
		cron:      core.NewCronService(opts.Store, opts.Agent, opts.Config),
		quota:     core.NewQuotaService(opts.Store, opts.Agent, opts.Config, opts.Logger),
		certs:     core.NewCertService(opts.Config, opts.Store, opts.Agent, opts.Secrets, opts.Logger),
		backups:   core.NewBackupService(opts.Config, opts.Store, opts.Logger, opts.Secrets),
		log:       opts.Logger,
		devOrigin: opts.DevOrigin,
		// Fünf Fehlversuche je Minute und IP. Der Zähler in der users-Tabelle
		// sperrt zusätzlich das Konto; dieser hier bremst schon davor.
		apps:      core.NewAppService(opts.Store, opts.Agent, opts.Config, opts.Secrets),
		mail:      core.NewMailService(opts.Store, opts.Agent, opts.Config, opts.Secrets),
		plugins:   core.NewPluginService(opts.Store, opts.Agent),
		appstore:  core.NewAppStoreService(opts.Store, opts.Agent, opts.Config, opts.Secrets),
		webmail:   core.NewWebmailService(opts.Store, opts.Agent, opts.Config, opts.Secrets),
		deploys:   core.NewDeployService(opts.Store, opts.Agent, opts.Config, opts.Secrets, opts.Logger),
		loginRate: newRateLimiter(5, time.Minute),
		logins:    newLoginDomains(opts.Store),
	}

	s.setupMiddleware(opts.DevOrigin)
	s.setupRoutes()
	return s, nil
}

// largeBody hebt die Größengrenze für die Routen an, auf denen Nutzdaten
// ankommen. Der Wert deckt sich mit maxUploadBytes im Dateimanager.
var largeBody = middleware.BodyLimit("512M")

func (s *Server) setupMiddleware(devOrigin string) {
	s.echo.Use(middleware.Recover())
	s.echo.Use(requestLogger(s.log))
	s.echo.Use(securityHeaders())
	// Global eng: eine JSON-Anfrage an dieses Panel ist nie groß. Die beiden
	// Wege, auf denen wirklich Daten ankommen — Datei-Upload und SQL-Import —
	// heben die Grenze an ihrer Route selbst an. Ohne das griff hier still die
	// 32 MiB, obwohl der Upload 512 MiB annehmen wollte.
	s.echo.Use(middleware.BodyLimit("32M"))

	if len(s.cfg.IPWhitelist) > 0 {
		s.echo.Use(ipWhitelist(s.cfg.IPWhitelist, s.log))
	}
	if devOrigin != "" {
		s.echo.Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins:     []string{devOrigin},
			AllowCredentials: true,
			AllowHeaders:     []string{echo.HeaderContentType, csrfHeader},
		}))
	}
}

func (s *Server) setupRoutes() {
	// Ohne Präfix hängt alles direkt unter /. Mit access_path liegt das Panel
	// unter einem nicht erratbaren Pfad — ein Scanner findet es dann nicht.
	root := s.echo.Group("")
	if s.cfg.AccessPath != "" {
		root = s.echo.Group("/" + s.cfg.AccessPath)
	}

	// "/<präfix>" ohne Schrägstrich passt auf keine Route der Gruppe: die
	// registriert "/<präfix>/*", und das verlangt den Schrägstrich. Ohne
	// diese Weiterleitung beantwortet das Panel genau die URL mit 404, die
	// der Installer ausgibt.
	if s.cfg.AccessPath != "" {
		s.echo.GET("/"+strings.Trim(s.cfg.AccessPath, "/"), func(c echo.Context) error {
			return c.Redirect(http.StatusMovedPermanently, s.frontendBase(c))
		})
	}

	// Vor dem Routing: auf der Anmeldedomain eines Mandanten liegt das Panel
	// unter "/", ohne den Zugriffspfad des Betreibers.
	s.echo.Pre(s.tenantDomainRoot)

	root.GET("/healthz", s.handleHealth)

	api := root.Group("/api/v1")

	// Öffentlich: Login und der Zustand der Ersteinrichtung.
	api.GET("/auth/state", s.handleAuthState)
	api.GET("/auth/branding", s.handleBranding)
	api.POST("/auth/login", s.handleLogin)

	// Alles Weitere braucht eine gültige Session.
	auth := api.Group("", s.requireSession)
	auth.POST("/auth/logout", s.handleLogout)
	auth.GET("/auth/me", s.handleMe)
	auth.POST("/auth/password", s.handleChangePassword)
	auth.POST("/auth/2fa/setup", s.handleTOTPSetup)
	auth.POST("/auth/2fa/enable", s.handleTOTPEnable)
	auth.POST("/auth/2fa/disable", s.handleTOTPDisable)

	auth.GET("/system/info", s.handleSystemInfo)
	auth.GET("/system/metrics", s.handleMetricsSnapshot)
	auth.GET("/system/metrics/stream", s.handleMetricsStream)
	auth.GET("/system/services", s.handleServices)

	// Firewall und Fail2ban betreffen den ganzen Server, nicht eine Site.
	auth.GET("/system/firewall", s.handleFirewallStatus, s.requireRole(store.RoleAdmin))
	auth.POST("/system/firewall", s.handleFirewallRule, s.requireRole(store.RoleAdmin))
	auth.GET("/system/fail2ban", s.handleFail2banStatus, s.requireRole(store.RoleAdmin))
	auth.POST("/system/fail2ban/unban", s.handleUnban, s.requireRole(store.RoleAdmin))
	// Nachinstallieren, was das Panel verwaltet. Der Pfad nennt eine
	// Fähigkeit, keinen Paketnamen — welche Pakete dazugehören, weiß der
	// Agent, und nur er.
	auth.GET("/system/features", s.handleFeatures, s.requireRole(store.RoleAdmin))
	auth.POST("/system/features/:name", s.handleInstallFeature, s.requireRole(store.RoleAdmin))
	auth.GET("/system/portscan", s.handlePortScanStatus, s.requireRole(store.RoleAdmin))
	auth.POST("/system/portscan", s.handlePortScanSet, s.requireRole(store.RoleAdmin))

	// Plugins: server-weite Fähigkeiten aus einem festen Katalog — kein
	// offenes Repository, keine Fremdcode-Ausführung. Siehe
	// internal/core/plugins.go.
	auth.GET("/plugins", s.handleListPlugins, s.requireRole(store.RoleAdmin))
	auth.POST("/plugins/:id/install", s.handleInstallPlugin, s.requireRole(store.RoleAdmin))
	auth.POST("/plugins/:id/uninstall", s.handleUninstallPlugin, s.requireRole(store.RoleAdmin))
	auth.POST("/plugins/:id/set", s.handleSetPlugin, s.requireRole(store.RoleAdmin))

	// Webmail: eine einzige, server-weite Installation — siehe
	// internal/core/webmail.go.
	auth.GET("/webmail", s.handleWebmailStatus, s.requireRole(store.RoleAdmin))
	auth.POST("/webmail", s.handleWebmailInstall, s.requireRole(store.RoleAdmin))
	auth.DELETE("/webmail", s.handleWebmailUninstall, s.requireRole(store.RoleAdmin))

	// Mail. Eine Maildomäne gehört einem Mandanten, deshalb entscheidet der
	// Scope — wie bei Sites. Nur das Einrichten des Mailspeichers und der
	// Zustandsbericht bleiben beim Administrator: das eine legt einen
	// Systembenutzer an, das andere sagt etwas über die Maschine.
	auth.GET("/mail/status", s.handleMailStatus, s.requireRole(store.RoleAdmin))
	auth.GET("/mail/spamstats", s.handleMailSpamStats, s.requireRole(store.RoleAdmin))
	auth.POST("/mail/setup", s.handleMailSetup, s.requireRole(store.RoleAdmin))
	auth.GET("/mail/check", s.handleMailCheck)
	auth.GET("/mail/settings", s.handleMailSettings)
	auth.GET("/mail/domains", s.handleListMailDomains)
	auth.POST("/mail/domains", s.handleCreateMailDomain)
	auth.PATCH("/mail/domains/:id", s.handleUpdateMailDomain)
	auth.DELETE("/mail/domains/:id", s.handleDeleteMailDomain)
	auth.GET("/mail/domains/:id/dkim", s.handleDKIM)
	auth.POST("/mail/domains/:id/dkim", s.handleEnableDKIM)
	auth.POST("/mail/domains/:id/dns", s.handlePublishDNS)
	auth.POST("/mail/domains/:id/autoconfig", s.handlePublishAutoconfig)
	auth.GET("/mail/mailboxes", s.handleListMailboxes)
	auth.POST("/mail/mailboxes", s.handleCreateMailbox)
	auth.PATCH("/mail/mailboxes/:id", s.handleUpdateMailbox)
	auth.GET("/mail/mailboxes/:id/password", s.handleRevealMailbox)
	auth.DELETE("/mail/mailboxes/:id", s.handleDeleteMailbox)
	auth.GET("/mail/aliases", s.handleListMailAliases)
	auth.POST("/mail/aliases", s.handleCreateMailAlias)
	auth.DELETE("/mail/aliases/:id", s.handleDeleteMailAlias)
	auth.POST("/system/services/:name/:action", s.handleServiceAction, s.requireRole(store.RoleAdmin))

	// Den Stand darf jeder Angemeldete sehen — er steht als Hinweis in der
	// Oberfläche. Auslösen darf ihn nur, wer auch Dienste neu starten darf:
	// das Update startet das Panel neu und tauscht beide Binaries.
	// Erweiterungen gelten systemweit je PHP-Version. Sie zu installieren
	// heißt, ein Paket auf den Server zu holen — das darf nur, wer auch
	// Dienste steuern darf.
	auth.GET("/system/php/:version/extensions", s.handlePHPExtensions, s.requireRole(store.RoleAdmin))
	auth.POST("/system/php/:version/extensions/install", s.handlePHPExtensionInstall, s.requireRole(store.RoleAdmin))
	auth.POST("/system/php/:version/extensions/toggle", s.handlePHPExtensionToggle, s.requireRole(store.RoleAdmin))

	// Die Prozessliste ist gefiltert: ein Kunde sieht nur die Prozesse seiner
	// eigenen Sites. Beenden lassen sich ohnehin nur diese.
	auth.GET("/system/processes", s.handleProcesses)
	auth.POST("/system/processes/stop", s.handleStopProcess)

	// Das Zertifikat des Panels betrifft den Server, nicht einen Mandanten.
	auth.GET("/system/panel-certificate", s.handlePanelCertStatus, s.requireRole(store.RoleAdmin))
	auth.POST("/system/panel-certificate", s.handleIssuePanelCert, s.requireRole(store.RoleAdmin))

	auth.GET("/system/update", s.handleUpdateStatus)
	auth.POST("/system/update", s.handleUpdateStart, s.requireRole(store.RoleAdmin))

	auth.GET("/sites", s.handleListSites)
	auth.POST("/sites", s.handleCreateSite)

	// App-Store: ein Klick, eine fertige Website — dieselbe Berechtigung wie
	// beim Anlegen einer Site direkt, siehe internal/core/appstore.go.
	auth.GET("/appstore", s.handleAppStoreCatalog)
	auth.POST("/appstore/wordpress", s.handleInstallWordPress)
	auth.GET("/sites/:id", s.handleGetSite)
	auth.PATCH("/sites/:id", s.handleUpdateSite)
	auth.DELETE("/sites/:id", s.handleDeleteSite)
	auth.POST("/sites/:id/rebuild", s.handleRebuildSite)
	auth.GET("/sites/:id/logs", s.handleSiteLogs)
	auth.GET("/sites/:id/settings", s.handleGetSiteSettings)
	auth.PATCH("/sites/:id/settings", s.handleUpdateSiteSettings)
	auth.GET("/sites/:id/php", s.handleGetSitePHP)
	auth.PATCH("/sites/:id/php", s.handleUpdateSitePHP)
	auth.POST("/sites/:id/certificate", s.handleIssueCert)

	// Terminal: eine Shell als Systembenutzer der Site. Vorerst nur für
	// Administratoren — mehr gibt es damit zwar nicht als über einen Cronjob
	// derselben Site, aber eine Shell macht das Umsehen auf dem Server eben
	// deutlich bequemer. Die Freigabe für Kunden ist eine eigene Entscheidung.
	auth.GET("/sites/:id/terminal", s.handleTerminal, s.requireRole(store.RoleAdmin))

	auth.GET("/certs", s.handleListCerts)
	auth.DELETE("/certs/:id", s.handleDeleteCert)

	// Dateimanager: immer site-gebunden, nie mit absolutem Pfad.
	auth.GET("/sites/:id/files", s.handleFileList)
	auth.GET("/sites/:id/files/read", s.handleFileRead)
	auth.GET("/sites/:id/files/download", s.handleFileDownload)
	auth.POST("/sites/:id/files/write", s.handleFileWrite)
	auth.POST("/sites/:id/files/mkdir", s.handleFileMkdir)
	auth.POST("/sites/:id/files/delete", s.handleFileDelete)
	auth.POST("/sites/:id/files/move", s.handleFileMove)
	auth.POST("/sites/:id/files/copy", s.handleFileCopy)
	auth.POST("/sites/:id/files/chmod", s.handleFileChmod)
	auth.POST("/sites/:id/files/archive", s.handleFileArchive)
	auth.POST("/sites/:id/files/extract", s.handleFileExtract)
	auth.POST("/sites/:id/files/upload", s.handleFileUpload, largeBody)

	// FTP: die Zugänge hängen an einer Site und laufen unter deren
	// Systembenutzer. Einrichten darf nur, wer auch Dienste steuern darf —
	// es holt ein Paket auf den Server und öffnet Ports.
	auth.GET("/ftp", s.handleListFTPAccounts)
	auth.POST("/ftp", s.handleCreateFTPAccount)
	auth.PATCH("/ftp/:id", s.handleUpdateFTPAccount)
	auth.POST("/ftp/:id/reveal", s.handleRevealFTPPassword)
	auth.DELETE("/ftp/:id", s.handleDeleteFTPAccount)
	auth.GET("/ftp/status", s.handleFTPStatus)
	auth.POST("/ftp/setup", s.handleFTPSetup, s.requireRole(store.RoleAdmin))
	auth.GET("/ftp/orphans", s.handleFTPOrphans, s.requireRole(store.RoleAdmin))

	auth.GET("/databases", s.handleListDatabases)
	auth.POST("/databases", s.handleCreateDatabase)
	auth.DELETE("/databases/:id", s.handleDeleteDatabase)
	auth.POST("/databases/:id/dump", s.handleDumpDatabase)
	auth.GET("/databases/:id/dump/download", s.handleDownloadDump)
	auth.POST("/databases/:id/import", s.handleImportDatabase, largeBody)
	auth.POST("/databases/:id/query", s.handleRunQuery)
	auth.GET("/databases/:id/users", s.handleListDBUsers)
	auth.POST("/databases/:id/users", s.handleCreateDBUser)
	auth.PATCH("/db-users/:id", s.handleUpdateDBUser)
	auth.POST("/db-users/:id/reveal", s.handleRevealDBUserPassword)
	auth.DELETE("/db-users/:id", s.handleDeleteDBUser)
	auth.GET("/db-users/:id/hosts", s.handleListRemoteHosts)
	auth.POST("/db-users/:id/hosts", s.handleAddRemoteHost)
	auth.DELETE("/db-hosts/:id", s.handleRemoveRemoteHost)
	auth.GET("/databases-remote", s.handleRemoteStatus)
	auth.POST("/databases-remote", s.handleSetRemoteAccess, s.requireRole(store.RoleAdmin))

	auth.GET("/backups", s.handleListBackups)
	auth.POST("/backups", s.handleCreateBackup, s.requireRole(store.RoleAdmin))
	auth.GET("/backup-targets", s.handleListTargets)
	auth.POST("/backup-targets", s.handleCreateTarget)
	auth.PATCH("/backup-targets/:id", s.handleUpdateTarget)
	auth.DELETE("/backup-targets/:id", s.handleDeleteTarget)
	auth.POST("/backup-targets/:id/test", s.handleTestTarget)
	auth.POST("/backup-targets/:id/upload", s.handleUploadBackup, s.requireRole(store.RoleAdmin))

	auth.GET("/cronjobs", s.handleListCronjobs)
	auth.POST("/cronjobs", s.handleCreateCronjob)
	auth.PATCH("/cronjobs/:id", s.handleUpdateCronjob)
	auth.DELETE("/cronjobs/:id", s.handleDeleteCronjob)
	auth.GET("/cronjobs/:id/log", s.handleCronjobLog)

	// Git-Deploy. Der Webhook steht weiter unten, außerhalb dieser Gruppe.
	auth.GET("/deploys", s.handleListDeploys)
	auth.GET("/deploys/steps", s.handleDeploySteps)
	auth.POST("/deploys", s.handleConfigureDeploy)
	auth.POST("/deploys/:id/run", s.handleRunDeploy)
	auth.GET("/deploys/:id/releases", s.handleDeployReleases)
	auth.POST("/deploys/:id/rollback", s.handleDeployRollback)
	auth.GET("/deploys/:id/key", s.handleDeployKey)
	auth.DELETE("/deploys/:id", s.handleDeleteDeploy)

	// Apps: eine Anwendung ist eine systemd-Unit plus Reverse-Proxy.
	auth.GET("/apps", s.handleListApps)
	auth.GET("/apps/runtimes", s.handleAppRuntimes)
	auth.GET("/apps/docker", s.handleDockerStatus, s.requireRole(store.RoleAdmin))
	auth.GET("/apps/node", s.handleNodeVersions)
	auth.POST("/apps/node", s.handleInstallNode, s.requireRole(store.RoleAdmin))
	auth.DELETE("/apps/node/:major", s.handleRemoveNode, s.requireRole(store.RoleAdmin))
	auth.POST("/apps/pull", s.handlePullImage, s.requireRole(store.RoleAdmin))
	auth.GET("/apps/stats", s.handleAppStats)
	auth.GET("/apps/images", s.handleImages, s.requireRole(store.RoleAdmin))
	auth.POST("/apps/images/remove", s.handleRemoveImage, s.requireRole(store.RoleAdmin))
	auth.GET("/apps/:id/logs", s.handleAppLogs)
	auth.POST("/apps", s.handleCreateApp)
	auth.PATCH("/apps/:id", s.handleUpdateApp)
	auth.DELETE("/apps/:id", s.handleDeleteApp)

	auth.GET("/tenants", s.handleListTenants)
	auth.POST("/tenants", s.handleCreateTenant, s.requireRole(store.RoleAdmin))
	auth.PATCH("/tenants/:id", s.handleUpdateTenant, s.requireRole(store.RoleAdmin))
	auth.DELETE("/tenants/:id", s.handleDeleteTenant, s.requireRole(store.RoleAdmin))
	auth.GET("/tenants/:id/quota", s.handleTenantQuota)
	auth.PUT("/tenants/:id/cloudflare", s.handleSetCloudflareToken, s.requireRole(store.RoleReseller))
	auth.PUT("/tenants/:id/login-domain", s.handleSetLoginDomain, s.requireRole(store.RoleReseller))
	auth.POST("/tenants/:id/login-domain/cert", s.handleIssueLoginDomainCert,
		s.requireRole(store.RoleReseller))

	auth.GET("/quota", s.handleQuota)
	auth.GET("/quota/filesystem", s.handleQuotaFilesystem, s.requireRole(store.RoleAdmin))
	auth.GET("/plans", s.handleListPlans)
	auth.POST("/plans", s.handleCreatePlan, s.requireRole(store.RoleAdmin))
	auth.PATCH("/plans/:id", s.handleUpdatePlan, s.requireRole(store.RoleAdmin))
	auth.DELETE("/plans/:id", s.handleDeletePlan, s.requireRole(store.RoleAdmin))

	auth.GET("/users", s.handleListUsers)
	auth.POST("/users", s.handleCreateUser, s.requireRole(store.RoleReseller))
	auth.DELETE("/users/:id", s.handleDeleteUser, s.requireRole(store.RoleReseller))

	auth.GET("/audit", s.handleAudit)

	// Der Webhook liegt außerhalb des Zugriffspfads und außerhalb der
	// Sitzungsprüfung. Sein Ausweis ist die Signatur über den Rumpf.
	//
	// Außerhalb des Pfads, weil seine Adresse in den Einstellungen eines
	// fremden Dienstes landet — GitHub kennt den Zugriffspfad des Betreibers
	// nicht und soll ihn auch nicht erfahren.
	s.echo.POST("/hooks/deploy/:hook", s.handleDeployHook)

	s.mountFrontend(root)
}

// mountFrontend liefert das eingebettete SPA aus. Unbekannte Pfade bekommen
// index.html, damit der Vue-Router auch beim direkten Aufruf einer Unterseite greift.
//
// Die Dateien gehen bewusst direkt über http.ServeContent raus statt über einen
// http.FileServer: der kanonisiert "/index.html" per 301 auf "./" und dreht sich
// dabei mit unserem Fallback im Kreis.
func (s *Server) mountFrontend(g *echo.Group) {
	fsys, err := webui.FS()
	if err != nil {
		s.log.Error("frontend nicht einbettbar", "err", err)
		return
	}
	if !webui.Built() {
		s.log.Warn("kein gebautes frontend eingebettet — das panel liefert nur die api aus")
	}

	g.GET("/*", func(c echo.Context) error {
		upath := strings.TrimPrefix(c.Param("*"), "/")

		// Kein SPA-Fallback für API-Pfade: ein Tippfehler in einer Route soll
		// als 404 auffallen und nicht stillschweigend HTML zurückgeben.
		if strings.HasPrefix(upath, "api/") {
			return echo.NewHTTPError(http.StatusNotFound, "unbekannter endpunkt")
		}

		if upath != "" {
			if err := serveAsset(c, fsys, upath); err == nil {
				return nil
			}
			// Dasselbe für Assets: eine fehlende .js-Datei als HTML auszuliefern
			// erzeugt im Browser einen Syntaxfehler statt eines klaren 404.
			if strings.HasPrefix(upath, "assets/") {
				return echo.NewHTTPError(http.StatusNotFound, "datei nicht gefunden")
			}
		}
		// Unbekannter Pfad oder Wurzel: die App entscheidet, was sie anzeigt.
		if err := s.serveIndex(c, fsys); err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "frontend nicht verfügbar")
		}
		return nil
	})
}

// serveIndex liefert die App aus und trägt dabei ihren Ort ein.
//
// Das Panel läuft hinter einem zufälligen Pfadpräfix, der erst bei der
// Installation entsteht — zur Bauzeit kann ihn niemand kennen. Das <base>-Tag
// ist die Stelle, an der er ankommt: Vue Router liest es, fetch löst seine
// relativen Adressen dagegen auf, und die Asset-Verweise ebenso.
//
// Es muss ein Tag sein und darf keine Ersetzung der Asset-Pfade werden: eine
// Unterseite wie /<präfix>/sites/5 liegt zwei Ebenen tiefer, relative
// Adressen ohne <base> zeigten von dort ins Leere.
func (s *Server) serveIndex(c echo.Context, fsys http.FileSystem) error {
	f, err := fsys.Open("/index.html")
	if err != nil {
		return err
	}
	defer f.Close()

	raw, err := io.ReadAll(f)
	if err != nil {
		return err
	}

	html := string(raw)
	if !strings.Contains(html, "<base ") {
		tag := fmt.Sprintf("<base href=%q>", s.frontendBase(c))
		switch {
		case strings.Contains(html, "<head>"):
			html = strings.Replace(html, "<head>", "<head>"+tag, 1)
		default:
			// Ohne <head> ist die Datei nicht das, was wir gebaut haben —
			// dann lieber vorne anhängen als still das Falsche ausliefern.
			html = tag + html
		}
	}

	c.Response().Header().Set("Cache-Control", "no-cache, must-revalidate")
	return c.HTMLBlob(http.StatusOK, []byte(html))
}

// isLoginDomain sagt, ob ein Name die Anmeldedomain eines Mandanten ist.
//
// Gerufen im TLS-Handshake, deshalb über den Zwischenspeicher und mit knapper
// Frist: eine hängende Abfrage darf keine Verbindung aufhalten.
func (s *Server) isLoginDomain(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.logins.lookup(ctx, host) != nil
}

// frontendBase ist der Pfad, unter dem die App liegt, immer mit Schrägstrich
// am Ende — ohne ihn löst der Browser relative Adressen gegen das
// übergeordnete Verzeichnis auf.
//
// Auf der Anmeldedomain eines Mandanten ist das "/": dort ergänzt
// tenantDomainRoot den Zugriffspfad nach innen, und der Browser darf ihn nicht
// zu sehen bekommen. Stünde er im <base href>, hinge er an jeder Adresse, die
// die App bildet — und der Kunde kennte den Weg zum Panel des Betreibers.
func (s *Server) frontendBase(c echo.Context) string {
	if _, ok := c.Get(loginTenantKey).(*store.Tenant); ok {
		return "/"
	}
	if s.cfg.AccessPath == "" {
		return "/"
	}
	return "/" + strings.Trim(s.cfg.AccessPath, "/") + "/"
}

// serveAsset schreibt eine Datei aus dem eingebetteten Dateisystem in die
// Antwort. Verzeichnisse gelten als "nicht gefunden", damit der Aufrufer auf
// index.html zurückfällt.
func serveAsset(c echo.Context, fsys http.FileSystem, name string) error {
	f, err := fsys.Open("/" + name)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return errors.New("kein auslieferbares objekt")
	}

	// Gehashte Assets sind unveränderlich; index.html darf nie im Cache
	// festhängen, sonst zeigt der Browser nach einem Update die alte App.
	if name == "index.html" {
		c.Response().Header().Set("Cache-Control", "no-cache, must-revalidate")
	} else {
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	http.ServeContent(c.Response(), c.Request(), info.Name(), info.ModTime(), f)
	return nil
}

// Start bindet den Listener und bedient Anfragen bis zum Context-Ende.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.ListenAddr, s.cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.echo,
		ReadHeaderTimeout: 10 * time.Second,
		// Kein WriteTimeout: die Metrik-WebSockets laufen dauerhaft.
		IdleTimeout: 120 * time.Second,
	}

	scheme := "http"
	if s.cfg.TLSEnabled {
		tlsCfg, err := panelTLS(s.cfg, s.log, s.isLoginDomain)
		if err != nil {
			return fmt.Errorf("panel-tls: %w", err)
		}
		srv.TLSConfig = tlsCfg
		scheme = "https"
	}

	errCh := make(chan error, 1)
	go func() {
		host := s.cfg.PanelDomain
		if host == "" {
			host = s.cfg.ListenAddr
		}
		s.log.Info("panel erreichbar",
			"url", fmt.Sprintf("%s://%s:%d/%s", scheme, host, s.cfg.Port, s.cfg.AccessPath))

		var err error
		if s.cfg.TLSEnabled {
			// Zertifikat und Schlüssel stecken in der TLSConfig.
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func (s *Server) handleHealth(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()

	status := map[string]any{"status": "ok", "agent": "ok"}
	if err := s.agent.Healthy(ctx); err != nil {
		// Das Panel selbst antwortet noch, kann aber nichts ausführen —
		// das muss ein Monitoring unterscheiden können.
		status["status"], status["agent"] = "degraded", err.Error()
		return c.JSON(http.StatusServiceUnavailable, status)
	}
	return c.JSON(http.StatusOK, status)
}
