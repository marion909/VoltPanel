package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/templates"
	"golang.org/x/crypto/bcrypt"
)

// AuthUser ist ein Zugang für den Passwortschutz. Das Klartextpasswort
// existiert nur für die Dauer des Aufrufs.
type AuthUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UpdateSettingsInput sind die Änderungen an einer Site.
//
// Nur gesetzte Zeiger werden übernommen — ein PATCH ohne Feld darf nichts
// leeren. Bei den Listen bedeutet ein gesetzter, leerer Zeiger dagegen
// "alles entfernen"; anders ließen sie sich nie leeren.
type UpdateSettingsInput struct {
	Redirects  *[]store.Redirect
	DenyIPs    *[]string
	AllowIPs   *[]string
	ExtraLines *[]string

	MaxBodySize    *string
	FastCGITimeout *int

	// BasicAuthUsers ersetzt die Zugangsliste vollständig. Nil lässt sie
	// unverändert, eine leere Liste schaltet den Schutz ab.
	BasicAuthUsers *[]AuthUser
	BasicAuthRealm *string
}

// UpdateSettings übernimmt die Änderungen, schreibt den Passwortschutz und
// erzeugt den Vhost neu.
func (s *SiteService) UpdateSettings(ctx context.Context, sc store.Scope, siteID int64, in UpdateSettingsInput) (*store.Site, error) {
	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return nil, err
	}
	set := &site.Settings

	applyIfSet(in.Redirects, &set.Redirects)
	applyIfSet(in.DenyIPs, &set.DenyIPs)
	applyIfSet(in.AllowIPs, &set.AllowIPs)
	applyIfSet(in.ExtraLines, &set.ExtraLines)
	applyIfSet(in.MaxBodySize, &set.MaxBodySize)
	applyIfSet(in.FastCGITimeout, &set.FastCGITimeout)

	// Der Passwortschutz braucht eine Datei auf dem Server, nicht nur einen
	// Eintrag in der Datenbank — deshalb hier und nicht im Repository.
	if in.BasicAuthUsers != nil {
		if err := s.applyBasicAuth(ctx, site, *in.BasicAuthUsers, in.BasicAuthRealm); err != nil {
			return nil, err
		}
	} else if in.BasicAuthRealm != nil && set.BasicAuth != nil {
		set.BasicAuth.Realm = *in.BasicAuthRealm
	}

	// Zweimal geprüft: hier, damit ein Fehler eine verständliche Meldung
	// ergibt, und noch einmal beim Rendern, weil dort nichts escaped wird.
	if err := set.Validate(); err != nil {
		return nil, err
	}
	if err := s.store.UpdateSite(ctx, sc, site); err != nil {
		return nil, err
	}
	if err := s.Rebuild(ctx, sc, site.ID); err != nil {
		return nil, err
	}
	return site, nil
}

// applyBasicAuth hasht die Passwörter und legt die htpasswd-Datei an.
//
// Gehasht wird hier, nicht im Agent: so verlässt ein Klartextpasswort den
// Web-Prozess nie und kann in keinem Agent-Log landen.
func (s *SiteService) applyBasicAuth(ctx context.Context, site *store.Site, users []AuthUser, realm *string) error {
	set := &site.Settings

	if len(users) == 0 {
		if err := s.agent.RemoveHtpasswd(ctx, site.Domain); err != nil {
			return fmt.Errorf("passwortschutz entfernen: %w", err)
		}
		set.BasicAuth = nil
		return nil
	}

	entries := make([]string, 0, len(users))
	names := make([]string, 0, len(users))
	for _, u := range users {
		if len(u.Password) < 8 {
			return fmt.Errorf("passwort für %q ist kürzer als 8 zeichen", u.Username)
		}
		// bcrypt statt der historischen apr1/crypt-Formate: die sind für
		// heutige Hardware zu schnell zu brechen.
		hash, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("passwort für %q: %w", u.Username, err)
		}
		entries = append(entries, u.Username+":"+string(hash))
		names = append(names, u.Username)
	}
	sort.Strings(names)

	if _, err := s.agent.WriteHtpasswd(ctx, site.Domain, entries); err != nil {
		return fmt.Errorf("passwortschutz schreiben: %w", err)
	}

	auth := &store.BasicAuth{Enabled: true, Users: names, Realm: site.Domain}
	if set.BasicAuth != nil && set.BasicAuth.Realm != "" {
		auth.Realm = set.BasicAuth.Realm
	}
	if realm != nil && strings.TrimSpace(*realm) != "" {
		auth.Realm = *realm
	}
	set.BasicAuth = auth
	return nil
}

// UpdatePHPInput sind die Werte des FPM-Pools einer Site.
type UpdatePHPInput struct {
	PHPVersion        *string
	PM                *string
	MaxChildren       *int
	MemoryLimit       *string
	MaxExecutionTime  *int
	UploadMaxFilesize *string
	DisableFunctions  *string
	ExtraINI          *string
}

// UpdatePHP ändert die PHP-Einstellungen einer Site und schreibt Pool und
// Vhost neu.
func (s *SiteService) UpdatePHP(ctx context.Context, sc store.Scope, siteID int64, in UpdatePHPInput) (*store.PHPPool, error) {
	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return nil, err
	}
	if site.Type != store.SitePHP {
		return nil, fmt.Errorf("site %s ist keine php-site", site.Domain)
	}

	pool, err := s.store.PHPPoolBySite(ctx, sc, site.ID)
	if err != nil {
		return nil, err
	}

	applyIfSet(in.PHPVersion, &pool.PHPVersion)
	applyIfSet(in.PM, &pool.PM)
	applyIfSet(in.MaxChildren, &pool.MaxChildren)
	applyIfSet(in.MemoryLimit, &pool.MemoryLimit)
	applyIfSet(in.MaxExecutionTime, &pool.MaxExecutionTime)
	applyIfSet(in.UploadMaxFilesize, &pool.UploadMaxFilesize)
	applyIfSet(in.DisableFunctions, &pool.DisableFunctions)
	applyIfSet(in.ExtraINI, &pool.ExtraINI)

	// Vor dem Speichern rendern: eine unbrauchbare Einstellung soll gar nicht
	// erst in die Datenbank kommen.
	if _, err := templates.RenderPool(templates.PoolData{
		Site: site, Pool: pool, LogDir: s.cfg.LogDir,
	}); err != nil {
		return nil, err
	}

	// Wechselt die Version, gehört der alte Pool aufgeräumt — sonst liefe die
	// Site unter zwei Versionen gleichzeitig.
	oldVersion := site.PHPVersion
	if pool.PHPVersion != oldVersion {
		if err := s.agent.RemovePHPPool(ctx, oldVersion, pool.PoolName); err != nil {
			return nil, fmt.Errorf("alten pool entfernen: %w", err)
		}
		site.PHPVersion = pool.PHPVersion
		if err := s.store.UpdateSite(ctx, sc, site); err != nil {
			return nil, err
		}
	}

	if err := s.store.UpdatePHPPool(ctx, sc, pool); err != nil {
		return nil, err
	}
	if err := s.Rebuild(ctx, sc, site.ID); err != nil {
		return nil, err
	}
	return pool, nil
}

// applyIfSet übernimmt einen Wert nur, wenn das Feld im PATCH gesetzt war.
func applyIfSet[T any](src *T, dst *T) {
	if src != nil {
		*dst = *src
	}
}
