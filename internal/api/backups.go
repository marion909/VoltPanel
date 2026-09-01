package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
)

// archiveInfo ist ein lokal liegendes Archiv.
type archiveInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	ModTime   int64  `json:"mod_time"`
}

// handleListBackups zeigt die lokalen Archive und die Einträge aus der
// Datenbank — ersteres ist der Bestand, letzteres die Geschichte.
func (s *Server) handleListBackups(c echo.Context) error {
	ctx := c.Request().Context()

	entries, err := s.store.ListBackups(ctx, s.scopeFor(c), 100)
	if err != nil {
		return storeError(err)
	}

	// Die Dateien liegen serverweit in einem Verzeichnis, ohne Mandant im
	// Namen. Sie sind deshalb Administratoren vorbehalten; ein Kunde sieht
	// die Einträge, die seinem Mandanten gehören.
	archives := []archiveInfo{}
	if hasRoleAtLeast(c, store.RoleAdmin) {
		infos, err := s.backups.ListArchives()
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		for _, i := range infos {
			archives = append(archives, archiveInfo{
				Name: i.Name(), SizeBytes: i.Size(), ModTime: i.ModTime().Unix(),
			})
		}
	}

	return c.JSON(http.StatusOK, map[string]any{
		"entries":  entries,
		"archives": archives,
	})
}

type createBackupRequest struct {
	IncludeConfig bool     `json:"include_config"`
	SiteDomains   []string `json:"site_domains"`
}

// handleCreateBackup erzeugt ein Archiv.
//
// Nur für Administratoren: das Archiv enthält die Panel-Datenbank, und die
// gehört allen Mandanten gemeinsam.
func (s *Server) handleCreateBackup(c echo.Context) error {
	var req createBackupRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, user := c.Request().Context(), currentUser(c)
	res, err := s.backups.Create(ctx, core.CreateOptions{
		IncludeConfig: req.IncludeConfig,
		SiteDomains:   req.SiteDomains,
		TenantID:      user.TenantID,
	})
	if err != nil {
		s.audit(ctx, user, "backup.create", "backup", "", "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	s.audit(ctx, user, "backup.create", "backup", res.Path, "ok", c.RealIP(),
		map[string]any{"bytes": res.SizeBytes, "dauer_ms": res.Duration.Milliseconds()})
	return c.JSON(http.StatusCreated, map[string]any{
		"path": res.Path, "size_bytes": res.SizeBytes, "checksum": res.Checksum,
	})
}

// --- Ziele -----------------------------------------------------------------

func (s *Server) handleListTargets(c echo.Context) error {
	targets, err := s.store.ListBackupTargets(c.Request().Context(), currentScope(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, targets)
}

type targetRequest struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Bucket   string `json:"bucket"`
	// Secret leer heisst "unverändert lassen" — nicht "löschen". Ohne diese
	// Unterscheidung verlöre jedes Speichern des Formulars die Zugangsdaten.
	Secret     string `json:"secret"`
	Username   string `json:"username"`
	BasePath   string `json:"base_path"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	UseTLS     *bool  `json:"use_tls"`
	SkipVerify bool   `json:"skip_verify"`
	PathStyle  bool   `json:"path_style"`
	Enabled    *bool  `json:"enabled"`
}

func (r targetRequest) toInput(tenantID int64) core.TargetInput {
	// Verschlüsselung und Aktiv sind an, wenn nichts anderes gesagt wird. Eine
	// Voreinstellung, die schweigend das Unsicherere wählt, wäre eine Falle.
	useTLS, enabled := true, true
	if r.UseTLS != nil {
		useTLS = *r.UseTLS
	}
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return core.TargetInput{
		Name: r.Name, Kind: r.Kind, Endpoint: r.Endpoint, Region: r.Region,
		Bucket: r.Bucket, Secret: r.Secret, Username: r.Username,
		BasePath: r.BasePath, Host: r.Host, Port: r.Port,
		UseTLS: useTLS, SkipVerify: r.SkipVerify, PathStyle: r.PathStyle,
		Enabled: enabled, TenantID: tenantID,
	}
}

func (s *Server) handleCreateTarget(c echo.Context) error {
	var req targetRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, user := c.Request().Context(), currentUser(c)
	target, err := s.backups.CreateTarget(ctx, currentScope(c), req.toInput(user.TenantID))
	if err != nil {
		return storeError(err)
	}

	// Das Geheimnis steht nicht im Log — der Rest schon: wohin Sicherungen
	// gehen dürfen, ist eine Frage, die hinterher gestellt wird.
	s.audit(ctx, user, "backup.target_create", "backup_target", target.Name, "ok", c.RealIP(),
		map[string]any{"art": target.Kind, "ziel": targetLabel(target)})
	return c.JSON(http.StatusCreated, target)
}

func (s *Server) handleUpdateTarget(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req targetRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, user := c.Request().Context(), currentUser(c)
	target, err := s.backups.UpdateTarget(ctx, currentScope(c), id, req.toInput(user.TenantID))
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, user, "backup.target_update", "backup_target", target.Name, "ok", c.RealIP(),
		map[string]any{"art": target.Kind, "ziel": targetLabel(target)})
	return c.JSON(http.StatusOK, target)
}

func (s *Server) handleDeleteTarget(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx, sc := c.Request().Context(), currentScope(c)

	target, err := s.store.GetBackupTarget(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeleteBackupTarget(ctx, sc, id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "backup.target_delete", "backup_target", target.Name,
		"ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}

// handleTestTarget prüft ein Ziel, ohne etwas abzulegen.
func (s *Server) handleTestTarget(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := s.backups.TestTarget(ctx, currentScope(c), id); err != nil {
		// Kein 500: fast jeder Fehler hier ist eine falsche Angabe des
		// Kunden — ein Tippfehler im Bucket, ein abgelaufener Schlüssel.
		return storeError(err)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

type uploadRequest struct {
	Filename string `json:"filename"`
}

// handleUploadBackup schickt ein vorhandenes Archiv an ein Ziel.
//
// Nur für Administratoren: die Archive liegen serverweit in einem Verzeichnis
// und enthalten die Panel-Datenbank.
func (s *Server) handleUploadBackup(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req uploadRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, user := c.Request().Context(), currentUser(c)
	res, err := s.backups.Upload(ctx, currentScope(c), id, req.Filename)
	if err != nil {
		s.audit(ctx, user, "backup.upload", "backup_target", strconv.FormatInt(id, 10),
			"error", c.RealIP(), map[string]string{
				"archiv": req.Filename, "fehler": err.Error(),
			})
		return storeError(err)
	}

	s.audit(ctx, user, "backup.upload", "backup_target", res.Target, "ok", c.RealIP(),
		map[string]any{"archiv": req.Filename, "ablage": res.RemotePath, "bytes": res.SizeBytes})
	return c.JSON(http.StatusOK, res)
}

// targetLabel beschreibt ein Ziel in einer Zeile — für das Audit-Log.
func targetLabel(t *store.BackupTarget) string {
	if t.Kind == "ftp" {
		return t.Host + ":" + strconv.Itoa(t.Port) + "/" + t.BasePath
	}
	return t.Bucket + "." + t.Endpoint + "/" + t.BasePath
}
