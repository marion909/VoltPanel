package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

func (s *Server) handleListDatabases(c echo.Context) error {
	ctx, sc := c.Request().Context(), s.scopeFor(c)

	// Die Größen kommen vom Server, nicht aus der Datenbank — sonst zeigt das
	// Panel Werte von vorgestern. Ein Fehler dabei ist kein Grund, die Liste
	// zurückzuhalten.
	if err := s.databases.SyncSizes(ctx); err != nil {
		s.log.Debug("datenbankgrößen nicht abrufbar", "err", err)
	}

	dbs, err := s.store.ListDatabases(ctx, sc)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, dbs)
}

type createDatabaseRequest struct {
	Name      string `json:"name"`
	SiteID    *int64 `json:"site_id"`
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
	WithUser  bool   `json:"with_user"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	TenantID  int64  `json:"tenant_id"`
}

func (s *Server) handleCreateDatabase(c echo.Context) error {
	var req createDatabaseRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	res, err := s.databases.CreateDatabase(ctx, currentScope(c), core.CreateDatabaseInput{
		Name: req.Name, SiteID: req.SiteID, Charset: req.Charset, Collation: req.Collation,
		WithUser: req.WithUser, Username: req.Username, Password: req.Password,
		TenantID: req.TenantID,
	})

	user := currentUser(c)
	if err != nil {
		s.audit(ctx, user, "database.create", "database", req.Name, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}

	// Das Klartextpasswort steht bewusst nicht im Audit-Log.
	s.audit(ctx, user, "database.create", "database", res.Database.Name, "ok", c.RealIP(),
		map[string]bool{"mit_benutzer": req.WithUser})
	return c.JSON(http.StatusCreated, res)
}

func (s *Server) handleDeleteDatabase(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	db, err := s.store.GetDatabase(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	if err := s.databases.DeleteDatabase(ctx, sc, id); err != nil {
		s.audit(ctx, currentUser(c), "database.delete", "database", db.Name, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "database.delete", "database", db.Name, "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleDumpDatabase(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	path, size, err := s.databases.Dump(ctx, currentScope(c), id)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "database.dump", "database", strconv.FormatInt(id, 10),
		"ok", c.RealIP(), map[string]int64{"bytes": size})
	return c.JSON(http.StatusOK, map[string]any{"path": path, "size_bytes": size})
}

// --- Datenbankbenutzer -----------------------------------------------------

func (s *Server) handleListDBUsers(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	users, err := s.store.ListDBUsers(c.Request().Context(), currentScope(c), id)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, users)
}

type createDBUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Grants   string `json:"grants"`
}

func (s *Server) handleCreateDBUser(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req createDBUserRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx := c.Request().Context()
	user, password, err := s.databases.CreateUser(ctx, currentScope(c), id,
		req.Username, req.Password, req.Grants)
	if err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "database.user_create", "db_user", user.Username, "ok", c.RealIP(),
		map[string]string{"rechte": user.Grants})
	return c.JSON(http.StatusCreated, map[string]any{"user": user, "password": password})
}

type dbUserPatchRequest struct {
	Grants   *string `json:"grants"`
	Password *string `json:"password"`
}

func (s *Server) handleUpdateDBUser(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req dbUserPatchRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	out := map[string]any{}

	if req.Grants != nil {
		if err := s.databases.SetGrants(ctx, sc, id, *req.Grants); err != nil {
			return storeError(err)
		}
		s.audit(ctx, currentUser(c), "database.user_grants", "db_user",
			strconv.FormatInt(id, 10), "ok", c.RealIP(), map[string]string{"rechte": *req.Grants})
	}
	if req.Password != nil {
		password, err := s.databases.SetPassword(ctx, sc, id, *req.Password)
		if err != nil {
			return storeError(err)
		}
		out["password"] = password
		s.audit(ctx, currentUser(c), "database.user_password", "db_user",
			strconv.FormatInt(id, 10), "ok", c.RealIP(), nil)
	}
	return c.JSON(http.StatusOK, out)
}

// handleRevealDBUserPassword gibt das gespeicherte Passwort heraus.
//
// Bewusst ein eigener Endpunkt statt eines Feldes in der Liste: so taucht jeder
// Abruf einzeln im Audit-Log auf.
func (s *Server) handleRevealDBUserPassword(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	password, err := s.databases.RevealPassword(ctx, currentScope(c), id)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "database.user_reveal", "db_user",
		strconv.FormatInt(id, 10), "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"password": password})
}

func (s *Server) handleDeleteDBUser(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	user, err := s.store.GetDBUser(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if err := s.databases.DeleteUser(ctx, sc, id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "database.user_delete", "db_user", user.Username, "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}
