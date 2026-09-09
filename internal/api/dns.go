package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/core"
)

func (s *Server) handleListDNSZones(c echo.Context) error {
	zones, err := s.dns.ListZones(c.Request().Context(), currentScope(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, zones)
}

func (s *Server) handleListDNSRecords(c echo.Context) error {
	provider := c.QueryParam("provider")
	if provider == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "provider fehlt")
	}
	records, err := s.dns.ListRecords(c.Request().Context(), currentScope(c), provider, c.Param("zoneId"))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, records)
}

type dnsRecordRequest struct {
	Provider string `json:"provider"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority"`
	// OldValue identifiziert bei PUT den zu ersetzenden Wert — nötig, weil es
	// keine providerübergreifend stabile Record-ID gibt (siehe dns_provider.go).
	OldValue string `json:"old_value"`
}

func (s *Server) handleCreateDNSRecord(c echo.Context) error {
	var req dnsRecordRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	if req.Provider == "" || req.Type == "" || req.Name == "" || req.Value == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "provider, type, name und value sind pflicht")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	rec := recordFromRequest(req)
	if err := s.dns.CreateRecord(ctx, sc, req.Provider, c.Param("zoneId"), rec); err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "dns.record.create", "zone", c.Param("zoneId"), "ok", c.RealIP(),
		map[string]string{"typ": req.Type, "name": req.Name})
	return c.JSON(http.StatusCreated, rec)
}

func (s *Server) handleUpdateDNSRecord(c echo.Context) error {
	var req dnsRecordRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	if req.Provider == "" || req.Type == "" || req.Name == "" || req.Value == "" || req.OldValue == "" {
		return echo.NewHTTPError(http.StatusBadRequest,
			"provider, type, name, value und old_value sind pflicht")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	rec := recordFromRequest(req)
	if err := s.dns.UpdateRecord(ctx, sc, req.Provider, c.Param("zoneId"), req.OldValue, rec); err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "dns.record.update", "zone", c.Param("zoneId"), "ok", c.RealIP(),
		map[string]string{"typ": req.Type, "name": req.Name})
	return c.JSON(http.StatusOK, rec)
}

// handleDeleteDNSRecord liest die Adressierung aus Query-Parametern statt aus
// einem Rumpf — DELETE-Anfragen mit Body sind unüblich, und ein fetch()
// braucht dafür keinen Sonderfall.
func (s *Server) handleDeleteDNSRecord(c echo.Context) error {
	provider, recType := c.QueryParam("provider"), c.QueryParam("type")
	name, value := c.QueryParam("name"), c.QueryParam("value")
	if provider == "" || recType == "" || name == "" || value == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "provider, type, name und value sind pflicht")
	}

	ctx, sc := c.Request().Context(), currentScope(c)
	rec := core.DNSRecord{Name: name, Type: recType, Value: value}
	if err := s.dns.DeleteRecord(ctx, sc, provider, c.Param("zoneId"), rec); err != nil {
		return storeError(err)
	}

	s.audit(ctx, currentUser(c), "dns.record.delete", "zone", c.Param("zoneId"), "ok", c.RealIP(),
		map[string]string{"typ": recType, "name": name})
	return c.NoContent(http.StatusNoContent)
}

func recordFromRequest(req dnsRecordRequest) core.DNSRecord {
	return core.DNSRecord{
		Name: req.Name, Type: req.Type, Value: req.Value, TTL: req.TTL, Priority: req.Priority,
	}
}
