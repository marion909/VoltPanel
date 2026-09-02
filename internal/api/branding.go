package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/store"
)

// Eigene Anmeldeseite und Domain für den Kundenbereich.
//
// Bis hierher bekommt ein Kunde dieselbe Adresse genannt wie der Betreiber —
// samt dessen zufälligem Zugriffspfad, der genau deshalb zufällig ist. Über
// eine eigene Domain je Mandant führt der Weg an die Anmeldung, die zu ihm
// gehört, und das Panel liegt dort unter "/", ohne den Pfad des Betreibers.
//
// Die Zusage, an der alles hängt: unter dieser Domain kommt nur herein, wer zu
// diesem Mandanten gehört. Sonst wäre die Domain des Kunden ein zweiter Eingang
// zum Konto des Betreibers, nur mit anderem Namen darüber — und ein bequemer
// Ort, um dort eine Anmeldung zu fälschen.

// loginTenantKey liegt im Kontext der Anfrage, sobald der Host zu einem
// Mandanten gehört.
const loginTenantKey = "volt.login_tenant"

// loginHost ist der Hostname, unter dem die Anfrage ankam.
//
// Genommen wird ausschließlich Request.Host. X-Forwarded-Host bleibt außen vor:
// den Kopf setzt, wer die Anfrage schickt, und er entschiede hier, als welcher
// Mandant die Anmeldeseite auftritt. Der optionale Reverse-Proxy vor dem Panel
// reicht den echten Host durch (proxy_set_header Host $host).
func loginHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

// loginDomains hält die Zuordnung Domain → Mandant im Speicher.
//
// Gebraucht wird sie bei jeder Anfrage, auch für jede Bilddatei; geändert wird
// sie, wenn jemand eine Domain einträgt. Deshalb ein kurz gültiger
// Zwischenspeicher über alle Domains statt einer Abfrage je Host: die Menge ist
// durch die Zahl der Mandanten begrenzt, und ein Host aus der Anfrage kann sie
// nicht aufblähen.
type loginDomains struct {
	store *store.Store
	ttl   time.Duration

	mu      sync.RWMutex
	byHost  map[string]*store.Tenant
	fetched time.Time
}

func newLoginDomains(st *store.Store) *loginDomains {
	return &loginDomains{store: st, ttl: 30 * time.Second}
}

// lookup liefert den Mandanten zu einem Host, oder nil.
func (l *loginDomains) lookup(ctx context.Context, host string) *store.Tenant {
	if host == "" {
		return nil
	}
	l.mu.RLock()
	fresh := time.Since(l.fetched) < l.ttl
	t := l.byHost[host]
	l.mu.RUnlock()
	if fresh {
		return t
	}
	return l.refresh(ctx)[host]
}

func (l *loginDomains) refresh(ctx context.Context) map[string]*store.Tenant {
	tenants, err := l.store.LoginDomains(ctx)
	if err != nil {
		// Bei einem Fehler bleibt der alte Stand stehen. Eine leere Zuordnung
		// hieße: jede Anmeldeseite eines Kunden fällt für die Dauer des
		// Fehlers auf das Panel des Betreibers zurück.
		l.mu.RLock()
		defer l.mu.RUnlock()
		return l.byHost
	}

	byHost := make(map[string]*store.Tenant, len(tenants))
	for _, t := range tenants {
		if clean, err := store.NormalizeLoginDomain(t.LoginDomain); err == nil && clean != "" {
			byHost[clean] = t
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.byHost, l.fetched = byHost, time.Now()
	return byHost
}

// invalidate erzwingt, dass die nächste Anfrage neu liest. Wird nach jeder
// Änderung gerufen — sonst dauerte es bis zu einer halben Minute, bis eine
// frisch eingetragene Domain wirkt, und der Betreiber hielte sie für kaputt.
func (l *loginDomains) invalidate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fetched = time.Time{}
}

// loginTenant sagt, ob die Anfrage über die Anmeldedomain eines Mandanten kam.
func (s *Server) loginTenant(c echo.Context) *store.Tenant {
	if t, ok := c.Get(loginTenantKey).(*store.Tenant); ok {
		return t
	}
	host := loginHost(c.Request())
	// Die Domain des Panels gehört dem Betreiber, auch wenn jemand sie
	// zusätzlich bei einem Mandanten einträgt.
	if host == "" || strings.EqualFold(host, s.cfg.PanelDomain) {
		return nil
	}
	return s.logins.lookup(c.Request().Context(), host)
}

// tenantDomainRoot legt das Panel auf einer Anmeldedomain unter "/" ab.
//
// Der Zugriffspfad ist das Geheimnis des Betreibers. Ein Kunde soll ihn nicht
// brauchen — und erst recht nicht erfahren, weil man ihm sonst mit jeder
// Weiterleitung verriete, wo das Panel des Betreibers liegt.
//
// Deshalb wird der Pfad hier nach innen ergänzt statt nach außen umgeleitet:
// die Anfrage trifft dieselben Routen, aber die Adresse in der Leiste des
// Browsers bleibt "/".
func (s *Server) tenantDomainRoot(next echo.HandlerFunc) echo.HandlerFunc {
	prefix := "/" + strings.Trim(s.cfg.AccessPath, "/")

	return func(c echo.Context) error {
		if s.cfg.AccessPath == "" {
			return next(c)
		}
		r := c.Request()
		// Wer den Pfad schon nennt, bekommt ihn nicht zweimal.
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			return next(c)
		}
		// Der Webhook liegt bewusst außerhalb des Zugriffspfads — auch auf
		// einer Kundendomain. Ihm den Pfad voranzustellen hieße, ihn dort
		// unerreichbar zu machen.
		if strings.HasPrefix(r.URL.Path, "/hooks/") {
			return next(c)
		}
		t := s.loginTenant(c)
		if t == nil {
			return next(c)
		}
		c.Set(loginTenantKey, t)
		r.URL.Path = prefix + r.URL.Path
		return next(c)
	}
}

// handleBranding sagt der Anmeldeseite, für wen sie da ist.
//
// Öffentlich, denn gefragt wird, bevor sich jemand angemeldet hat. Zurück kommt
// nur, was auf einer Anmeldeseite ohnehin steht: der Name des Mandanten. Nichts
// über sein Paket, seine Sites oder seine Leute.
func (s *Server) handleBranding(c echo.Context) error {
	t := s.loginTenant(c)
	if t == nil {
		return c.JSON(http.StatusOK, map[string]any{"tenant": nil})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"tenant": map[string]any{"name": t.Name, "slug": t.Slug},
	})
}
