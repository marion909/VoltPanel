package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

const (
	sessionCookie = "volt_session"
	csrfCookie    = "volt_csrf"
	csrfHeader    = "X-CSRF-Token"

	ctxUser  = "volt_user"
	ctxScope = "volt_scope"
)

// securityHeaders setzt die Kopfzeilen, die den Browser absichern.
func securityHeaders() echo.MiddlewareFunc {
	// Die CSP erlaubt bewusst kein 'unsafe-eval' und keine fremden Hosts: das
	// Frontend liegt vollständig im Binary, es gibt nichts nachzuladen.
	// 'unsafe-inline' für Styles ist nötig, weil Vue Inline-Styles setzt.
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"font-src 'self' data:; " +
		"connect-src 'self' ws: wss:; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Response().Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			if c.Scheme() == "https" {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			return next(c)
		}
	}
}

func requestLogger(log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)

			status := c.Response().Status
			if err != nil {
				var he *echo.HTTPError
				if errors.As(err, &he) {
					status = he.Code
				} else {
					status = http.StatusInternalServerError
				}
			}
			// Erfolgreiche Anfragen nur auf Debug — sonst füllt der
			// Metrik-Poll des Dashboards das Log.
			level := slog.LevelDebug
			if status >= 400 {
				level = slog.LevelWarn
			}
			log.Log(c.Request().Context(), level, "http",
				"methode", c.Request().Method, "pfad", c.Request().URL.Path,
				"status", status, "dauer", time.Since(start), "ip", c.RealIP())
			return err
		}
	}
}

// ipWhitelist sperrt das Panel auf bestimmte Adressen ein (Phase 8).
func ipWhitelist(allowed []string, log *slog.Logger) echo.MiddlewareFunc {
	var nets []*net.IPNet
	var ips []net.IP
	for _, entry := range allowed {
		if _, n, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, n)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			ips = append(ips, ip)
			continue
		}
		log.Warn("eintrag in der ip-whitelist ist ungültig und wird ignoriert", "eintrag", entry)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			remote := net.ParseIP(c.RealIP())
			if remote != nil {
				for _, ip := range ips {
					if ip.Equal(remote) {
						return next(c)
					}
				}
				for _, n := range nets {
					if n.Contains(remote) {
						return next(c)
					}
				}
			}
			log.Warn("zugriff durch ip-whitelist abgelehnt", "ip", c.RealIP())
			return echo.NewHTTPError(http.StatusForbidden, "zugriff von dieser adresse nicht erlaubt")
		}
	}
}

// requireSession prüft Session-Cookie und CSRF-Token.
func (s *Server) requireSession(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		cookie, err := c.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			return echo.NewHTTPError(http.StatusUnauthorized, "nicht angemeldet")
		}

		ctx := c.Request().Context()
		sess, err := s.store.GetSession(ctx, hashToken(cookie.Value))
		if err != nil {
			s.clearSessionCookies(c)
			return echo.NewHTTPError(http.StatusUnauthorized, "sitzung abgelaufen")
		}

		user, err := s.store.GetUser(ctx, store.SystemScope(), sess.UserID)
		if err != nil || !user.Active() {
			_ = s.store.DeleteSession(ctx, sess.ID)
			s.clearSessionCookies(c)
			return echo.NewHTTPError(http.StatusUnauthorized, "konto nicht mehr aktiv")
		}

		if err := s.checkCSRF(c); err != nil {
			return err
		}

		c.Set(ctxUser, user)
		c.Set(ctxScope, store.UserScope(user.ID, user.TenantID, user.Role))

		// Gleitendes Ablaufdatum, aber nur einmal je Stunde schreiben — sonst
		// löst jeder Metrik-Poll einen Schreibvorgang aus.
		if remaining := time.Until(time.Unix(sess.ExpiresAt, 0)); remaining < s.sessionTTL()-time.Hour {
			_ = s.store.TouchSession(ctx, sess.ID, time.Now().Add(s.sessionTTL()).Unix())
		}

		// Der Agent protokolliert, wer eine Root-Aktion ausgelöst hat.
		c.SetRequest(c.Request().WithContext(agent.WithActor(ctx, user.Email)))
		return next(c)
	}
}

// checkCSRF sichert schreibende Anfragen ab (Double-Submit-Cookie).
//
// Der Wert steht sowohl im Cookie als auch im Header; eine fremde Seite kann
// das Cookie zwar mitschicken lassen, aber den Header nicht setzen.
func (s *Server) checkCSRF(c echo.Context) error {
	switch c.Request().Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}

	cookie, err := c.Cookie(csrfCookie)
	if err != nil || cookie.Value == "" {
		return echo.NewHTTPError(http.StatusForbidden, "csrf-token fehlt")
	}
	if header := c.Request().Header.Get(csrfHeader); header == "" || !secureEqual(header, cookie.Value) {
		return echo.NewHTTPError(http.StatusForbidden, "csrf-token stimmt nicht")
	}
	return nil
}

// requireRole verlangt mindestens die angegebene Rolle.
func (s *Server) requireRole(min store.Role) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			user := currentUser(c)
			if user == nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "nicht angemeldet")
			}
			if roleRank(user.Role) < roleRank(min) {
				return echo.NewHTTPError(http.StatusForbidden,
					"dafür fehlt die berechtigung (nötig: "+string(min)+")")
			}
			return next(c)
		}
	}
}

// roleRank ordnet die Rollen. Owner steht über Admin, Admin über Reseller,
// Reseller über Kunde.
func roleRank(r store.Role) int {
	switch r {
	case store.RoleOwner:
		return 4
	case store.RoleAdmin:
		return 3
	case store.RoleReseller:
		return 2
	case store.RoleCustomer:
		return 1
	}
	return 0
}

func currentUser(c echo.Context) *store.User {
	u, _ := c.Get(ctxUser).(*store.User)
	return u
}

// currentScope liefert den Tenant-Scope der Anfrage. Fehlt er, kommt der
// Nullwert zurück — und der lässt im store-Paket keine Query zu.
func currentScope(c echo.Context) store.Scope {
	sc, _ := c.Get(ctxScope).(store.Scope)
	return sc
}

func (s *Server) sessionTTL() time.Duration {
	return time.Duration(s.cfg.SessionTTLMin) * time.Minute
}

// rateLimiter begrenzt Versuche je Schlüssel (hier: je IP) in einem Zeitfenster.
type rateLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	buckets map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, buckets: make(map[string][]time.Time)}
}

// Allow zählt einen Versuch und sagt, ob er noch im Rahmen liegt.
func (r *rateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-r.window)
	kept := r.buckets[key][:0]
	for _, t := range r.buckets[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= r.limit {
		r.buckets[key] = kept
		return false
	}
	r.buckets[key] = append(kept, time.Now())
	return true
}

// Reset löscht den Zähler nach einem erfolgreichen Login.
func (r *rateLimiter) Reset(key string) {
	r.mu.Lock()
	delete(r.buckets, key)
	r.mu.Unlock()
}

// Cleanup entfernt abgelaufene Einträge, damit die Map nicht unbegrenzt wächst.
func (r *rateLimiter) Cleanup(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-r.window)
			r.mu.Lock()
			for key, times := range r.buckets {
				kept := times[:0]
				for _, t := range times {
					if t.After(cutoff) {
						kept = append(kept, t)
					}
				}
				if len(kept) == 0 {
					delete(r.buckets, key)
				} else {
					r.buckets[key] = kept
				}
			}
			r.mu.Unlock()
		}
	}
}

// errorHandler antwortet einheitlich als JSON und hält Interna zurück.
func errorHandler(log *slog.Logger) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}

		status, message := http.StatusInternalServerError, "interner fehler"
		var he *echo.HTTPError
		if errors.As(err, &he) {
			status = he.Code
			if msg, ok := he.Message.(string); ok {
				message = msg
			} else {
				message = http.StatusText(status)
			}
		} else {
			// Unerwartete Fehler vollständig ins Log, aber nicht in die Antwort:
			// ein SQL-Fehler im Browser verrät die Struktur der Datenbank.
			log.Error("unbehandelter fehler", "pfad", c.Request().URL.Path, "err", err)
		}

		if c.Request().Method == http.MethodHead {
			_ = c.NoContent(status)
			return
		}
		_ = c.JSON(status, map[string]string{"error": message})
	}
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
