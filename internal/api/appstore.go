package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

// App-Store: ein Klick, eine fertige Website. WordPress ist der erste
// Eintrag — siehe internal/core/appstore.go für die Abgrenzung zu einem
// Plugin (internal/core/plugins.go).
//
// Kein eigener requireRole hier: wer eine Site anlegen darf (die normale
// Regel bei /sites), soll auch diesen abgekürzten Weg dorthin nutzen dürfen —
// am Ende entsteht dieselbe Art Site, nur mit weniger Handgriffen.

// handleAppStoreCatalog sagt, was sich mit einem Klick installieren lässt.
//
// Bisher ein einziger, fester Eintrag. Trotzdem ein eigener Endpunkt statt
// eines hartkodierten Eintrags im Frontend: eine Oberfläche, die den Katalog
// abfragt, ändert sich nicht mit, wenn hier ein zweiter Eintrag dazukommt.
func (s *Server) handleAppStoreCatalog(c echo.Context) error {
	return c.JSON(http.StatusOK, []map[string]string{
		{
			"id":          "wordpress",
			"name":        "WordPress",
			"description": "Website und Blog mit dem verbreitetsten CMS. Site, Datenbank und der WordPress-Kern entstehen in einem Schritt — den letzten Teil (Titel, erstes Konto) übernimmt WordPress' eigener Installer im Browser.",
		},
	})
}

type installWordPressRequest struct {
	Domain     string `json:"domain"`
	PHPVersion string `json:"php_version"`
	TenantID   int64  `json:"tenant_id"`
}

// handleInstallWordPress legt Site, Datenbank und den WordPress-Kern an.
func (s *Server) handleInstallWordPress(c echo.Context) error {
	var req installWordPressRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	sc := currentScope(c)
	// Die elevierte Scope aus ForTenant muss auch tatsächlich verwendet
	// werden — sonst bliebe InstallWordPress mit dem ursprünglichen sc
	// unterwegs und scheiterte an sc.owns(req.TenantID), obwohl ForTenant den
	// Zugriff gerade erlaubt hat.
	if req.TenantID != 0 && req.TenantID != sc.TenantID {
		elevated, err := sc.ForTenant(req.TenantID)
		if err != nil {
			return storeError(err)
		}
		sc = elevated
	}

	ctx := c.Request().Context()
	res, err := s.appstore.InstallWordPress(ctx, sc, core.InstallWordPressInput{
		Domain: req.Domain, PHPVersion: req.PHPVersion, TenantID: req.TenantID,
	})

	user := currentUser(c)
	if err != nil {
		s.audit(ctx, user, "appstore.wordpress", "domain", req.Domain, "error", c.RealIP(),
			map[string]string{"fehler": err.Error()})
		// res kann trotz Fehler eine Site oder Datenbank enthalten — beides
		// steht bereits im Panel und ist kein Grund, die Antwort zu
		// verschweigen. Der Fehlertext bleibt trotzdem der führende Teil der
		// Antwort: er sagt, was noch fehlt.
		if res == nil {
			return storeError(err)
		}
		return c.JSON(http.StatusConflict, map[string]any{
			"error": err.Error(), "site": res.Site, "database": res.Database,
		})
	}

	s.audit(ctx, user, "appstore.wordpress", "domain", req.Domain, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusCreated, res)
}
