package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

// Mail: Domänen, Postfächer, Weiterleitungen.
//
// Anders als Firewall und Dienste ist das keine Serversache: eine Maildomäne
// gehört einem Mandanten, und ein Kunde soll seine eigenen Postfächer anlegen
// können. Der Scope macht die Arbeit — dieselbe Regel wie bei Sites.
//
// Zwei Ausnahmen bleiben beim Administrator: das Einrichten des Mailspeichers
// (es legt einen Systembenutzer an) und der Zustandsbericht (er sagt etwas
// über die Maschine, nicht über den Mandanten).

func (s *Server) handleMailStatus(c echo.Context) error {
	st, err := s.mail.Status(c.Request().Context())
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, st)
}

func (s *Server) handleMailSetup(c echo.Context) error {
	ctx := c.Request().Context()
	out, err := s.mail.Setup(ctx)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.setup", "server", "", "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"log": out})
}

// handleMailCheck prüft, was der Zustellbarkeit im Weg steht.
//
// Im Scope des Aufrufers: geprüft werden die eigenen Domänen. Die Befunde über
// den Server selbst — PTR, TLS, offenes Relay — stehen trotzdem darin, und das
// ist richtig so: sie betreffen jeden, der über diesen Server sendet.
// handleMailSettings sagt, was in ein Mailprogramm gehört.
//
// Für jeden Angemeldeten: wer ein Postfach hat, braucht die Angaben. Sie sagen
// nichts über andere Mandanten — Servername und Ports sind für alle dieselben.
func (s *Server) handleMailSettings(c echo.Context) error {
	return c.JSON(http.StatusOK, s.mail.Settings(c.Request().Context()))
}

func (s *Server) handleMailCheck(c echo.Context) error {
	res, err := s.mail.Check(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) handleListMailDomains(c echo.Context) error {
	list, err := s.store.ListMailDomains(c.Request().Context(), s.scopeFor(c))
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, list)
}

func (s *Server) handleCreateMailDomain(c echo.Context) error {
	var req struct {
		Domain string `json:"domain"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()
	sc := s.scopeFor(c)

	// Der Mandant kommt aus dem Scope, nicht aus der Anfrage. Ein Feld dafür
	// wäre der kürzeste Weg, eine Domäne bei einem fremden anzulegen.
	tenantID := sc.TenantID
	if tenantID == 0 {
		return echo.NewHTTPError(http.StatusBadRequest,
			"eine maildomäne gehört einem mandanten — als Betreiber über dessen Zugang anlegen")
	}

	d, err := s.mail.CreateDomain(ctx, sc, tenantID, req.Domain)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.domain.create", "domain", d.Domain, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusCreated, d)
}

func (s *Server) handleUpdateMailDomain(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req struct {
		Active   *bool   `json:"active"`
		CatchAll *string `json:"catch_all"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()
	d, err := s.mail.SetDomain(ctx, s.scopeFor(c), id, req.Active, req.CatchAll)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.domain.update", "domain", d.Domain, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, d)
}

func (s *Server) handleDeleteMailDomain(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	hinweis, err := s.mail.DeleteDomain(ctx, s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.domain.delete", "id", c.Param("id"), "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"hinweis": hinweis})
}

// handleEnableDKIM erzeugt einen Signaturschlüssel für eine Domäne.
//
// Die Antwort ist der DNS-Eintrag, nicht der Schlüssel. Der private Teil
// verlässt das Panel nie über HTTP — nur über den Socket zum Agent, der ihn in
// eine Datei schreibt, die OpenDKIM lesen darf.
func (s *Server) handleEnableDKIM(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	info, err := s.mail.EnableDKIM(ctx, s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.dkim.enable", "domain", info.Domain, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, info)
}

// handleDKIM liefert den DNS-Eintrag noch einmal.
func (s *Server) handleDKIM(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	info, err := s.mail.DKIMOf(c.Request().Context(), s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, info)
}

func (s *Server) handleListMailboxes(c echo.Context) error {
	domainID, _ := queryID(c, "domain_id")
	list, err := s.store.ListMailboxes(c.Request().Context(), s.scopeFor(c), domainID)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, list)
}

func (s *Server) handleCreateMailbox(c echo.Context) error {
	var req struct {
		DomainID  int64  `json:"domain_id"`
		LocalPart string `json:"local_part"`
		Password  string `json:"password"`
		QuotaMB   int64  `json:"quota_mb"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()
	m, err := s.mail.CreateMailbox(ctx, s.scopeFor(c), req.DomainID,
		req.LocalPart, req.Password, req.QuotaMB)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.mailbox.create", "address", m.Address, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusCreated, m)
}

func (s *Server) handleUpdateMailbox(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var req struct {
		Password string `json:"password"`
		QuotaMB  *int64 `json:"quota_mb"`
		Active   *bool  `json:"active"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()
	if err := s.mail.SetMailbox(ctx, s.scopeFor(c), id, req.Password, req.QuotaMB, req.Active); err != nil {
		return storeError(err)
	}
	// Das Passwort steht nicht im Audit-Log, nur dass eines gesetzt wurde.
	s.audit(ctx, currentUser(c), "mail.mailbox.update", "id", c.Param("id"), "ok", c.RealIP(),
		map[string]bool{"passwort_geaendert": req.Password != ""})
	return c.NoContent(http.StatusNoContent)
}

// handleRevealMailbox gibt das Passwort heraus.
//
// Wie bei FTP und Datenbanken: ein Mailkonto wird in einem Mailprogramm
// eingetragen, und "wie war noch mein Passwort" ist dort eine echte Frage.
// Der Abruf steht im Audit-Log — wer ihn nicht selbst ausgelöst hat, sieht es.
func (s *Server) handleRevealMailbox(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	pw, err := s.mail.Reveal(ctx, s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.mailbox.reveal", "id", c.Param("id"), "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"password": pw})
}

func (s *Server) handleDeleteMailbox(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	hinweis, err := s.mail.DeleteMailbox(ctx, s.scopeFor(c), id)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.mailbox.delete", "id", c.Param("id"), "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]string{"hinweis": hinweis})
}

func (s *Server) handleListMailAliases(c echo.Context) error {
	domainID, _ := queryID(c, "domain_id")
	list, err := s.store.ListMailAliases(c.Request().Context(), s.scopeFor(c), domainID)
	if err != nil {
		return storeError(err)
	}
	return c.JSON(http.StatusOK, list)
}

func (s *Server) handleCreateMailAlias(c echo.Context) error {
	var req struct {
		DomainID    int64  `json:"domain_id"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}
	ctx := c.Request().Context()
	a, err := s.mail.CreateAlias(ctx, s.scopeFor(c), req.DomainID, req.Source, req.Destination)
	if err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.alias.create", "source", a.Source, "ok", c.RealIP(),
		map[string]string{"ziel": a.Destination})
	return c.JSON(http.StatusCreated, a)
}

func (s *Server) handleDeleteMailAlias(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := s.mail.DeleteAlias(ctx, s.scopeFor(c), id); err != nil {
		return storeError(err)
	}
	s.audit(ctx, currentUser(c), "mail.alias.delete", "id", c.Param("id"), "ok", c.RealIP(), nil)
	return c.NoContent(http.StatusNoContent)
}

// queryID liest eine optionale ID aus der Anfrage. 0 heißt "alle".
func queryID(c echo.Context, name string) (int64, error) {
	roh := c.QueryParam(name)
	if roh == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(roh, 10, 64)
	if err != nil || id < 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, name+" ist keine zahl")
	}
	return id, nil
}
