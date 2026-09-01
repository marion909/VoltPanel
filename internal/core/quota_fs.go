package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

// Die Grenzen aus den Hosting-Paketen ins Dateisystem schreiben.
//
// Bis hierher wirken sie auf Anwendungsebene: eine Aktion über der Quota wird
// abgelehnt. Was am Panel vorbei schreibt — der PHP-Code der Site, ein Upload
// über FTP, ein entpacktes Archiv — merkt davon nichts. Erst der Kernel kann
// das, und dafür braucht es Project Quota.
//
// Nicht überall möglich: die Mount-Option lässt sich nicht im Betrieb setzen.
// Fehlt sie, geschieht hier nichts und die Grenzen wirken weiter wie bisher.
// Das ist der Grund, warum ein nicht anwendbarer Lauf kein Fehler ist.

// SyncDiskQuotas trägt für jeden Mandanten seine Grenze im Dateisystem nach.
//
// Ein Mandant ist ein Projekt, nicht eine Site. Wer fünf Sites hat, hat eine
// Grenze über alle fünf — genau die, die im Hosting-Paket steht.
func (s *QuotaService) SyncDiskQuotas(ctx context.Context) (applied int, hinweis string, errs []error) {
	sc := store.SystemScope()

	tenants, err := s.store.ListTenants(ctx, sc)
	if err != nil {
		return 0, "", []error{err}
	}
	sites, err := s.store.ListSites(ctx, sc)
	if err != nil {
		return 0, "", []error{err}
	}

	byTenant := make(map[int64][]string, len(tenants))
	for _, site := range sites {
		if site.RootPath == "" {
			continue
		}
		byTenant[site.TenantID] = append(byTenant[site.TenantID], site.RootPath)
	}

	for _, t := range tenants {
		if ctx.Err() != nil {
			return applied, hinweis, append(errs, ctx.Err())
		}
		dirs := byTenant[t.ID]
		if len(dirs) == 0 {
			// Ohne Verzeichnis gibt es nichts, woran eine Projektnummer hinge.
			continue
		}

		limit, err := s.diskLimitMB(ctx, sc, t.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.Slug, err))
			continue
		}

		res, err := s.agent.SetQuotaProject(ctx, t.ID, dirs, limit)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.Slug, err))
			continue
		}
		if !res.Applied {
			// Der Grund ist auf einem Server für alle Mandanten derselbe —
			// einmal gemerkt reicht, sonst steht er zwanzigmal im Log.
			if hinweis == "" {
				hinweis = res.Skipped
			}
			continue
		}
		applied++
	}
	return applied, hinweis, errs
}

// diskLimitMB ist die Grenze eines Mandanten in MB, 0 für unbegrenzt.
//
// Ein Mandant ohne Paket ist unbegrenzt, nicht gesperrt: dieselbe Auslegung wie
// überall sonst im Panel. Eine Quota, die aus einer fehlenden Zuordnung
// entsteht, wäre eine stille Sperre.
func (s *QuotaService) diskLimitMB(ctx context.Context, sc store.Scope, tenantID int64) (int64, error) {
	plan, err := s.store.PlanForTenant(ctx, sc, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if store.Unlimited(plan.DiskQuotaMB) {
		return 0, nil
	}
	return plan.DiskQuotaMB, nil
}

// FilesystemStatus sagt, ob dieser Server echte Quotas führen kann.
//
// Gefragt wird nach dem Verzeichnis der Sites: dort liegt, was begrenzt werden
// soll. Auf einem Server mit eigener Platte für /var/www ist das etwas anderes
// als die Wurzel, und genau der Unterschied entscheidet.
func (s *QuotaService) FilesystemStatus(ctx context.Context) (*agent.QuotaSupport, error) {
	return s.agent.QuotaStatus(ctx, s.cfg.SitesDir)
}
