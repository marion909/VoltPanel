package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/store"
)

// --- Hosting-Pakete --------------------------------------------------------

func (s *Server) handleListPlans(c echo.Context) error {
	plans, err := s.store.ListPlans(c.Request().Context(), currentScope(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, plans)
}

type planRequest struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	MaxSites       int    `json:"max_sites"`
	MaxDatabases   int    `json:"max_databases"`
	MaxFTP         int    `json:"max_ftp"`
	MaxMailboxes   int    `json:"max_mailboxes"`
	MaxCronjobs    int    `json:"max_cronjobs"`
	DiskQuotaMB    int64  `json:"disk_quota_mb"`
	TrafficQuotaMB int64  `json:"traffic_quota_mb"`
	IsDefault      bool   `json:"is_default"`
}

func (r planRequest) toPlan() *store.Plan {
	return &store.Plan{
		Name: r.Name, Description: r.Description,
		MaxSites: r.MaxSites, MaxDatabases: r.MaxDatabases, MaxFTP: r.MaxFTP,
		MaxMailboxes: r.MaxMailboxes, MaxCronjobs: r.MaxCronjobs,
		DiskQuotaMB: r.DiskQuotaMB, TrafficQuotaMB: r.TrafficQuotaMB,
		IsDefault: r.IsDefault,
	}
}

func (s *Server) handleCreatePlan(c echo.Context) error {
	var req planRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	plan := req.toPlan()
	ctx := c.Request().Context()
	if err := s.store.CreatePlan(ctx, currentScope(c).Elevate(), plan); err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "plan.create", "plan", plan.Name, "ok", c.RealIP(),
		map[string]any{"sites": plan.MaxSites, "disk_mb": plan.DiskQuotaMB})
	return c.JSON(http.StatusCreated, plan)
}

func (s *Server) handleUpdatePlan(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req planRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	plan := req.toPlan()
	plan.ID = id

	ctx := c.Request().Context()
	if err := s.store.UpdatePlan(ctx, currentScope(c).Elevate(), plan); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "plan.update", "plan", plan.Name, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, plan)
}

func (s *Server) handleDeletePlan(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx, sc := c.Request().Context(), currentScope(c).Elevate()
	plan, err := s.store.GetPlan(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if err := s.store.DeletePlan(ctx, sc, id); err != nil {
		return storeError(err)
	}

	// Tenants, die das Paket hatten, stehen danach ohne Grenzen da. Das gehört
	// ins Log, weil es eine stille Lockerung ist.
	s.audit(ctx, currentUser(c), "plan.delete", "plan", plan.Name, "ok", c.RealIP(),
		map[string]string{"hinweis": "zugeordnete tenants sind jetzt ohne grenzen"})
	return c.NoContent(http.StatusNoContent)
}

// --- Quota-Übersicht -------------------------------------------------------

// handleQuota liefert den Verbrauch des eigenen Tenants.
func (s *Server) handleQuota(c echo.Context) error {
	sc := currentScope(c)
	status, err := s.quota.Status(c.Request().Context(), sc, sc.TenantID)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, status)
}

// handleQuotaFilesystem sagt, ob die Grenzen auch im Dateisystem stehen.
//
// Nur für Administratoren: die Antwort nennt Gerätenamen und Einhängepunkte
// des Servers. Das ist nichts, was ein Kunde über die Maschine erfahren muss,
// auf der seine Site zufällig liegt.
func (s *Server) handleQuotaFilesystem(c echo.Context) error {
	sup, err := s.quota.FilesystemStatus(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, sup)
}

// handleTenantQuota liefert den Verbrauch eines beliebigen Tenants — der Scope
// entscheidet, ob das erlaubt ist.
func (s *Server) handleTenantQuota(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	status, err := s.quota.Status(c.Request().Context(), currentScope(c), id)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, status)
}

// --- Tenants ---------------------------------------------------------------

type updateTenantRequest struct {
	Name   *string `json:"name"`
	PlanID *int64  `json:"plan_id"`
	Status *string `json:"status"`
}

func (s *Server) handleUpdateTenant(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req updateTenantRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ctx, sc := c.Request().Context(), currentScope(c).Elevate()
	tenant, err := s.store.GetTenant(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	applyIf(req.Name, &tenant.Name)
	if req.Status != nil {
		switch *req.Status {
		case store.TenantActive, store.TenantSuspended:
			// Den eigenen Mandanten nicht sperren. Seit eine Sperre die
			// Anmeldung wirklich verhindert, wäre das der kürzeste Weg, sich
			// selbst aus dem Panel auszusperren — und niemand wäre mehr da, der
			// sie zurücknehmen könnte.
			if *req.Status == store.TenantSuspended {
				if u := currentUser(c); u != nil && u.TenantID == tenant.ID {
					return echo.NewHTTPError(http.StatusBadRequest,
						"den eigenen mandanten kann man nicht sperren — "+
							"danach käme niemand mehr herein, der es zurücknimmt")
				}
			}
			tenant.Status = *req.Status
		default:
			return echo.NewHTTPError(http.StatusBadRequest,
				"status muss active oder suspended sein")
		}
	}
	if req.PlanID != nil {
		// 0 bedeutet "kein Paket" — sonst ließe sich eine Zuordnung nie lösen.
		if *req.PlanID == 0 {
			tenant.PlanID = nil
		} else {
			if _, err := s.store.GetPlan(ctx, sc, *req.PlanID); err != nil {
				return storeError(err)
			}
			tenant.PlanID = req.PlanID
		}
	}

	if err := s.store.UpdateTenant(ctx, sc, tenant); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "tenant.update", "tenant", tenant.Slug, "ok", c.RealIP(),
		map[string]any{"status": tenant.Status, "paket": tenant.PlanID})
	return c.JSON(http.StatusOK, tenant)
}

func (s *Server) handleDeleteTenant(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}

	ctx, sc := c.Request().Context(), currentScope(c).Elevate()
	if id == currentUser(c).TenantID {
		return echo.NewHTTPError(http.StatusBadRequest, "der eigene tenant lässt sich nicht löschen")
	}

	tenant, err := s.store.GetTenant(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}

	// Nur ein leerer Tenant lässt sich entfernen. Sonst würde der
	// Datenbankeintrag verschwinden, während Vhosts, Linux-Benutzer und
	// Datenbanken auf dem Server zurückbleiben.
	usage, err := s.store.UsageForTenant(ctx, sc, id)
	if err != nil {
		return storeError(err)
	}
	if usage.Sites > 0 || usage.Databases > 0 || usage.Cronjobs > 0 {
		return echo.NewHTTPError(http.StatusConflict,
			"tenant hat noch websites, datenbanken oder cronjobs — diese zuerst entfernen")
	}

	if err := s.store.DeleteTenant(ctx, sc, id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "tenant.delete", "tenant", tenant.Slug, "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}
