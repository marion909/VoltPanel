// Package api ist die HTTP-Schicht des Panels: Routen, Middlewares und die
// Auslieferung des eingebetteten Frontends.
package api

import (
	"context"
	"errors"
	"fmt"
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
	cron      *core.CronService
	secrets   *authn.SecretBox
	log       *slog.Logger
	loginRate *rateLimiter
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
		cron:      core.NewCronService(opts.Store, opts.Agent, opts.Config),
		log:       opts.Logger,
		devOrigin: opts.DevOrigin,
		// Fünf Fehlversuche je Minute und IP. Der Zähler in der users-Tabelle
		// sperrt zusätzlich das Konto; dieser hier bremst schon davor.
		loginRate: newRateLimiter(5, time.Minute),
	}

	s.setupMiddleware(opts.DevOrigin)
	s.setupRoutes()
	return s, nil
}

func (s *Server) setupMiddleware(devOrigin string) {
	s.echo.Use(middleware.Recover())
	s.echo.Use(requestLogger(s.log))
	s.echo.Use(securityHeaders())
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

	root.GET("/healthz", s.handleHealth)

	api := root.Group("/api/v1")

	// Öffentlich: Login und der Zustand der Ersteinrichtung.
	api.GET("/auth/state", s.handleAuthState)
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
	auth.POST("/system/services/:name/:action", s.handleServiceAction, s.requireRole(store.RoleAdmin))

	auth.GET("/sites", s.handleListSites)
	auth.POST("/sites", s.handleCreateSite)
	auth.GET("/sites/:id", s.handleGetSite)
	auth.PATCH("/sites/:id", s.handleUpdateSite)
	auth.DELETE("/sites/:id", s.handleDeleteSite)
	auth.POST("/sites/:id/rebuild", s.handleRebuildSite)
	auth.GET("/sites/:id/logs", s.handleSiteLogs)

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
	auth.POST("/sites/:id/files/upload", s.handleFileUpload)

	auth.GET("/databases", s.handleListDatabases)
	auth.POST("/databases", s.handleCreateDatabase)
	auth.DELETE("/databases/:id", s.handleDeleteDatabase)
	auth.POST("/databases/:id/dump", s.handleDumpDatabase)
	auth.GET("/databases/:id/users", s.handleListDBUsers)
	auth.POST("/databases/:id/users", s.handleCreateDBUser)
	auth.PATCH("/db-users/:id", s.handleUpdateDBUser)
	auth.POST("/db-users/:id/reveal", s.handleRevealDBUserPassword)
	auth.DELETE("/db-users/:id", s.handleDeleteDBUser)

	auth.GET("/cronjobs", s.handleListCronjobs)
	auth.POST("/cronjobs", s.handleCreateCronjob)
	auth.PATCH("/cronjobs/:id", s.handleUpdateCronjob)
	auth.DELETE("/cronjobs/:id", s.handleDeleteCronjob)
	auth.GET("/cronjobs/:id/log", s.handleCronjobLog)

	auth.GET("/tenants", s.handleListTenants)
	auth.POST("/tenants", s.handleCreateTenant, s.requireRole(store.RoleAdmin))

	auth.GET("/users", s.handleListUsers)
	auth.POST("/users", s.handleCreateUser, s.requireRole(store.RoleReseller))
	auth.DELETE("/users/:id", s.handleDeleteUser, s.requireRole(store.RoleReseller))

	auth.GET("/audit", s.handleAudit)

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
		if err := serveAsset(c, fsys, "index.html"); err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "frontend nicht verfügbar")
		}
		return nil
	})
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

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("panel erreichbar", "adresse", addr, "pfad", "/"+s.cfg.AccessPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
