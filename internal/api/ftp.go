package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

// handleFTPStatus sagt, ob der Dienst überhaupt eingerichtet ist. Ohne diese
// Auskunft stünde in der Oberfläche eine Liste, die nie funktionieren kann.
func (s *Server) handleFTPStatus(c echo.Context) error {
	status, err := s.ftp.Status(c.Request().Context())
	if err != nil {
		return agentError(err)
	}
	return c.JSON(http.StatusOK, status)
}

// handleFTPSetup installiert und konfiguriert Pure-FTPd.
//
// Nur für Administratoren: es holt ein Paket auf den Server und öffnet Ports.
func (s *Server) handleFTPSetup(c echo.Context) error {
	ctx := c.Request().Context()
	res, err := s.ftp.Setup(ctx)
	if err != nil {
		s.audit(ctx, currentUser(c), "ftp.setup", "service", "pure-ftpd", "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return agentError(err)
	}
	s.audit(ctx, currentUser(c), "ftp.setup", "service", "pure-ftpd", "ok", c.RealIP(),
		map[string]any{"ports": []int{res.PassiveFrom, res.PassiveTo}})
	return c.JSON(http.StatusOK, res)
}

func (s *Server) handleListFTPAccounts(c echo.Context) error {
	var siteID int64
	if raw := c.QueryParam("site_id"); raw != "" {
		siteID, _ = strconv.ParseInt(raw, 10, 64)
	}
	accounts, err := s.store.ListFTPAccounts(c.Request().Context(), s.scopeFor(c), siteID)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, accounts)
}

type createFTPRequest struct {
	SiteID   int64  `json:"site_id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Subdir   string `json:"subdir"`
	QuotaMB  int64  `json:"quota_mb"`
}

func (s *Server) handleCreateFTPAccount(c echo.Context) error {
	var req createFTPRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	account, password, err := s.ftp.Create(ctx, currentScope(c), core.CreateFTPInput{
		SiteID: req.SiteID, Username: req.Username, Password: req.Password,
		Subdir: req.Subdir, QuotaMB: req.QuotaMB,
	})
	user := currentUser(c)
	if err != nil {
		s.audit(ctx, user, "ftp.create", "ftp", req.Username, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}

	// Das Klartextpasswort steht bewusst nicht im Audit-Log.
	s.audit(ctx, user, "ftp.create", "ftp", account.Username, "ok", c.RealIP(),
		map[string]any{"site": req.SiteID, "verzeichnis": account.HomeDir})
	return c.JSON(http.StatusCreated, map[string]any{"account": account, "password": password})
}

type updateFTPRequest struct {
	Password *string `json:"password"`
	Status   *string `json:"status"`
}

func (s *Server) handleUpdateFTPAccount(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req updateFTPRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	out := map[string]any{}

	if req.Status != nil {
		if err := s.ftp.SetStatus(ctx, sc, id, *req.Status); err != nil {
			return storeError(err)
		}
		s.audit(ctx, currentUser(c), "ftp.status", "ftp", strconv.FormatInt(id, 10), "ok",
			c.RealIP(), map[string]string{"status": *req.Status})
	}
	if req.Password != nil {
		password, err := s.ftp.SetPassword(ctx, sc, id, *req.Password)
		if err != nil {
			return storeError(err)
		}
		s.audit(ctx, currentUser(c), "ftp.password", "ftp", strconv.FormatInt(id, 10), "ok",
			c.RealIP(), nil)
		out["password"] = password
	}

	account, err := s.store.GetFTPAccount(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	out["account"] = account
	return c.JSON(http.StatusOK, out)
}

// handleRevealFTPPassword gibt das hinterlegte Passwort heraus.
//
// POST und nicht GET: der Aufruf ist keine Abfrage, sondern eine Handlung, die
// im Audit-Log steht. Über eine URL ließe er sich sonst aus einer fremden Seite
// heraus auslösen.
func (s *Server) handleRevealFTPPassword(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	password, err := s.ftp.Reveal(ctx, currentScope(c), id)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "ftp.reveal", "ftp", strconv.FormatInt(id, 10), "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"password": password})
}

func (s *Server) handleDeleteFTPAccount(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx, sc := c.Request().Context(), currentScope(c)
	account, err := s.store.GetFTPAccount(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if err := s.ftp.Delete(ctx, sc, id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "ftp.delete", "ftp", account.Username, "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}

// handleFTPOrphans zeigt Zugänge, die beim Dienst stehen und im Panel nicht.
// Nur für Administratoren: die Liste geht über Mandantengrenzen hinweg.
func (s *Server) handleFTPOrphans(c echo.Context) error {
	names, err := s.ftp.Orphans(c.Request().Context(), currentScope(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, names)
}
