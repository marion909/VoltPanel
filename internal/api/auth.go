package api

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/store"
)

// Nach so vielen Fehlversuchen sperrt sich das Konto selbst — unabhängig von
// der IP, damit ein verteilter Angriff nicht am Ratelimit vorbeikommt.
const (
	maxFailedLogins = 8
	accountLockSecs = 900
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

type loginResponse struct {
	User          *store.User `json:"user"`
	CSRFToken     string      `json:"csrf_token"`
	MustChangePW  bool        `json:"must_change_password"`
	TOTPRequired  bool        `json:"totp_required,omitempty"`
	SessionExpiry int64       `json:"session_expires_at"`
}

// handleAuthState sagt dem Frontend, ob überhaupt schon jemand eingerichtet ist.
func (s *Server) handleAuthState(c echo.Context) error {
	count, err := s.store.CountUsers(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"setup_required": count == 0,
		"version":        s.versionInfo(),
	})
}

// handleLogin prüft Zugangsdaten und 2FA und legt die Sitzung an.
func (s *Server) handleLogin(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	ip := c.RealIP()
	if !s.loginRate.Allow(ip) {
		return echo.NewHTTPError(http.StatusTooManyRequests,
			"zu viele anmeldeversuche, bitte kurz warten")
	}

	ctx := c.Request().Context()
	user, err := s.store.UserByEmail(ctx, req.Email)
	if err != nil {
		// Immer dieselbe Antwort wie bei falschem Passwort: sonst verrät das
		// Panel, welche E-Mail-Adressen es kennt.
		s.auditLogin(ctx, nil, req.Email, ip, "unbekannte adresse")
		return errInvalidCredentials()
	}

	if user.Locked() {
		s.auditLogin(ctx, user, req.Email, ip, "konto gesperrt")
		return echo.NewHTTPError(http.StatusForbidden,
			"konto ist nach zu vielen fehlversuchen vorübergehend gesperrt")
	}
	if !user.Active() {
		s.auditLogin(ctx, user, req.Email, ip, "konto deaktiviert")
		return echo.NewHTTPError(http.StatusForbidden, "konto ist deaktiviert")
	}

	if err := authn.VerifyPassword(req.Password, user.PasswordHash); err != nil {
		_ = s.store.NoteLoginFailure(ctx, user.ID, maxFailedLogins, accountLockSecs)
		s.auditLogin(ctx, user, req.Email, ip, "falsches passwort")
		return errInvalidCredentials()
	}

	if user.TOTPEnabled {
		if req.TOTPCode == "" {
			// Kein Fehler: das Frontend soll jetzt das Code-Feld zeigen.
			return c.JSON(http.StatusOK, loginResponse{TOTPRequired: true})
		}
		secret, err := s.secrets.Decrypt(user.TOTPSecret)
		if err != nil {
			return err
		}
		if !authn.VerifyTOTP(secret, req.TOTPCode) {
			_ = s.store.NoteLoginFailure(ctx, user.ID, maxFailedLogins, accountLockSecs)
			s.auditLogin(ctx, user, req.Email, ip, "falscher 2fa-code")
			return echo.NewHTTPError(http.StatusUnauthorized, "der code stimmt nicht")
		}
	}

	resp, err := s.startSession(c, user)
	if err != nil {
		return err
	}

	s.loginRate.Reset(ip)
	_ = s.store.NoteLoginSuccess(ctx, user.ID)
	s.audit(ctx, user, "auth.login", "user", user.Email, "ok", ip, nil)
	return c.JSON(http.StatusOK, resp)
}

// startSession erzeugt Session und CSRF-Token und setzt beide Cookies.
func (s *Server) startSession(c echo.Context, user *store.User) (*loginResponse, error) {
	token, hash, err := authn.NewSessionToken()
	if err != nil {
		return nil, err
	}
	csrfToken, _, err := authn.NewSessionToken()
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(s.sessionTTL())
	sess := &store.Session{
		ID: hash, UserID: user.ID, TenantID: user.TenantID,
		UserAgent: truncate(c.Request().UserAgent(), 255),
		IP:        c.RealIP(), ExpiresAt: expiresAt.Unix(),
	}
	if err := s.store.CreateSession(c.Request().Context(), sess); err != nil {
		return nil, err
	}

	// HttpOnly: kein Zugriff aus JavaScript, ein XSS kann die Sitzung nicht auslesen.
	s.setCookie(c, sessionCookie, token, expiresAt, true)
	// Das CSRF-Cookie muss lesbar sein — das Frontend schickt den Wert als Header zurück.
	s.setCookie(c, csrfCookie, csrfToken, expiresAt, false)

	return &loginResponse{
		User: user, CSRFToken: csrfToken,
		MustChangePW: user.MustChangePW, SessionExpiry: expiresAt.Unix(),
	}, nil
}

func (s *Server) handleLogout(c echo.Context) error {
	if cookie, err := c.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(c.Request().Context(), hashToken(cookie.Value))
	}
	s.clearSessionCookies(c)
	if user := currentUser(c); user != nil {
		s.audit(c.Request().Context(), user, "auth.logout", "user", user.Email, "ok", c.RealIP(), nil)
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleMe(c echo.Context) error {
	user := currentUser(c)
	tenant, err := s.store.GetTenant(c.Request().Context(), currentScope(c), user.TenantID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"user": user, "tenant": tenant, "version": s.versionInfo(),
	})
}

type changePasswordRequest struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

func (s *Server) handleChangePassword(c echo.Context) error {
	var req changePasswordRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	user := currentUser(c)
	if err := authn.VerifyPassword(req.Current, user.PasswordHash); err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "das aktuelle passwort stimmt nicht")
	}
	if err := authn.DefaultPolicy().Check(req.New); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	hash, err := authn.HashPassword(req.New)
	if err != nil {
		return err
	}
	user.PasswordHash, user.MustChangePW = hash, false

	ctx := c.Request().Context()
	if err := s.store.UpdateUser(ctx, currentScope(c), user); err != nil {
		return err
	}

	// Alle anderen Sitzungen beenden: wer das Passwort ändert, will auch
	// mögliche fremde Sitzungen loswerden.
	if err := s.store.DeleteUserSessions(ctx, user.ID); err != nil {
		return err
	}
	resp, err := s.startSession(c, user)
	if err != nil {
		return err
	}

	s.audit(ctx, user, "auth.password_change", "user", user.Email, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, resp)
}

// handleTOTPSetup liefert Secret und QR-Code. Aktiv wird 2FA erst, wenn der
// Nutzer mit handleTOTPEnable einen gültigen Code nachweist — sonst könnte er
// sich mit einem falsch gescannten Code aussperren.
func (s *Server) handleTOTPSetup(c echo.Context) error {
	user := currentUser(c)
	if user.TOTPEnabled {
		return echo.NewHTTPError(http.StatusConflict, "2fa ist bereits aktiv")
	}

	secret, qr, err := authn.NewTOTPSecret("VoltPanel", user.Email)
	if err != nil {
		return err
	}
	encrypted, err := s.secrets.Encrypt(secret)
	if err != nil {
		return err
	}

	user.TOTPSecret = encrypted
	if err := s.store.UpdateUser(c.Request().Context(), currentScope(c), user); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{"secret": secret, "qr_code": qr})
}

type totpRequest struct {
	Code string `json:"code"`
}

func (s *Server) handleTOTPEnable(c echo.Context) error {
	var req totpRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	user := currentUser(c)
	if user.TOTPSecret == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "zuerst 2fa einrichten")
	}
	secret, err := s.secrets.Decrypt(user.TOTPSecret)
	if err != nil {
		return err
	}
	if !authn.VerifyTOTP(secret, req.Code) {
		return echo.NewHTTPError(http.StatusBadRequest, "der code stimmt nicht")
	}

	user.TOTPEnabled = true
	ctx := c.Request().Context()
	if err := s.store.UpdateUser(ctx, currentScope(c), user); err != nil {
		return err
	}
	s.audit(ctx, user, "auth.2fa_enable", "user", user.Email, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]bool{"totp_enabled": true})
}

// handleTOTPDisable verlangt einen gültigen Code — sonst könnte eine übernommene
// Sitzung den zweiten Faktor einfach abschalten.
func (s *Server) handleTOTPDisable(c echo.Context) error {
	var req totpRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "anfrage nicht lesbar")
	}

	user := currentUser(c)
	if !user.TOTPEnabled {
		return c.JSON(http.StatusOK, map[string]bool{"totp_enabled": false})
	}
	secret, err := s.secrets.Decrypt(user.TOTPSecret)
	if err != nil {
		return err
	}
	if !authn.VerifyTOTP(secret, req.Code) {
		return echo.NewHTTPError(http.StatusBadRequest, "der code stimmt nicht")
	}

	user.TOTPEnabled, user.TOTPSecret = false, ""
	ctx := c.Request().Context()
	if err := s.store.UpdateUser(ctx, currentScope(c), user); err != nil {
		return err
	}
	s.audit(ctx, user, "auth.2fa_disable", "user", user.Email, "ok", c.RealIP(), nil)
	return c.JSON(http.StatusOK, map[string]bool{"totp_enabled": false})
}

func (s *Server) setCookie(c echo.Context, name, value string, expires time.Time, httpOnly bool) {
	path := "/"
	if s.cfg.AccessPath != "" {
		path = "/" + s.cfg.AccessPath
	}
	c.SetCookie(&http.Cookie{
		Name: name, Value: value, Path: path, Expires: expires,
		HttpOnly: httpOnly,
		// Strict statt Lax: das Panel wird nie aus einer fremden Seite heraus
		// aufgerufen, also gibt es keinen Grund, das Cookie mitzuschicken.
		SameSite: http.SameSiteStrictMode,
		Secure:   c.Scheme() == "https",
	})
}

func (s *Server) clearSessionCookies(c echo.Context) {
	past := time.Unix(0, 0)
	s.setCookie(c, sessionCookie, "", past, true)
	s.setCookie(c, csrfCookie, "", past, false)
}

func errInvalidCredentials() error {
	return echo.NewHTTPError(http.StatusUnauthorized, "e-mail-adresse oder passwort stimmt nicht")
}

func (s *Server) auditLogin(ctx context.Context, user *store.User, email, ip, reason string) {
	entry := &store.AuditEntry{
		Action: "auth.login", TargetType: "user", TargetID: email,
		Actor: email, IP: ip, Result: "error", Detail: store.Detail(map[string]string{"grund": reason}),
	}
	if user != nil {
		entry.UserID, entry.TenantID = &user.ID, &user.TenantID
	}
	if err := s.store.Log(ctx, entry); err != nil {
		s.log.Error("audit-eintrag nicht gespeichert", "err", err)
	}
}

func (s *Server) audit(ctx context.Context, user *store.User, action, targetType, targetID, result, ip string, detail any) {
	entry := &store.AuditEntry{
		Action: action, TargetType: targetType, TargetID: targetID,
		Result: result, IP: ip,
	}
	if user != nil {
		entry.UserID, entry.TenantID, entry.Actor = &user.ID, &user.TenantID, user.Email
	} else {
		entry.Actor = "system"
	}
	if detail != nil {
		entry.Detail = store.Detail(detail)
	}
	if err := s.store.Log(ctx, entry); err != nil {
		s.log.Error("audit-eintrag nicht gespeichert", "action", action, "err", err)
	}
}

func hashToken(token string) string { return authn.HashToken(token) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
