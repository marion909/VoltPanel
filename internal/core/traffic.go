package core

import (
	"context"
	"fmt"
	"time"

	"github.com/marion909/voltpanel/internal/agent"
)

// Traffic aus den Nginx-Access-Logs.
//
// Die Spalten sites.traffic_bytes und traffic_period stehen seit dem
// Quota-Schema, und AddSiteTraffic schreibt sie fort. Aufgerufen hat die
// Funktion nie jemand: es gab nichts, was die Logs gelesen hätte.
//
// Gezählt wird im selben Takt wie der Plattenverbrauch. Nicht bei jeder
// Anfrage — das wäre ein Dateizugriff pro Seitenaufruf für eine Zahl, die
// niemand auf die Sekunde genau braucht.

// billingPeriod ist der Abrechnungszeitraum als "2026-09".
//
// Der Monatswechsel setzt den Zähler zurück; das erledigt AddSiteTraffic
// anhand dieses Werts. UTC, damit der Wechsel nicht von der Zeitzone des
// Servers abhängt — ein Umzug würde sonst einen Monat verdoppeln oder
// verschlucken.
func billingPeriod(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// CollectTraffic liest die Access-Logs aller Sites und schreibt die Zähler fort.
//
// Zurück kommt, wie viele Sites gezählt wurden, und die Fehler einzelner Sites.
// Ein Fehler bei einer Site darf die übrigen nicht aufhalten: eine Logdatei mit
// falschen Rechten wäre sonst der Grund, warum der ganze Server keine Zahlen
// mehr bekommt.
func (s *QuotaService) CollectTraffic(ctx context.Context) (int, []error) {
	cursors, err := s.store.TrafficCursors(ctx)
	if err != nil {
		return 0, []error{err}
	}
	if len(cursors) == 0 {
		return 0, nil
	}

	// Der Agent liest die Dateien — das Logverzeichnis gehört ihm, nicht dem
	// Web-Prozess. Der Lesestand geht mit und kommt fortgeschrieben zurück.
	bySite := make(map[string]int64, len(cursors))
	req := make([]agent.TrafficCursor, 0, len(cursors))
	for _, c := range cursors {
		bySite[c.Domain] = c.SiteID
		req = append(req, agent.TrafficCursor{
			Domain: c.Domain, Offset: c.Offset, Inode: c.Inode,
		})
	}

	res, err := s.agent.Traffic(ctx, req)
	if err != nil {
		return 0, []error{err}
	}

	period := billingPeriod(time.Now())
	var errs []error
	var counted int

	for _, f := range res.Files {
		siteID, ok := bySite[f.Domain]
		if !ok {
			// Der Agent hat etwas zurückgegeben, wonach nicht gefragt wurde.
			continue
		}
		if f.Error != "" {
			errs = append(errs, fmt.Errorf("%s: %s", f.Domain, f.Error))
			continue
		}

		if f.Bytes > 0 {
			if err := s.store.AddSiteTraffic(ctx, siteID, f.Bytes, period); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", f.Domain, err))
				continue
			}
		}
		// Der Lesestand auch dann, wenn nichts dazukam: sonst wird beim
		// nächsten Lauf dieselbe Stelle noch einmal gelesen, und nach einer
		// Rotation läge er auf einer Datei, die es nicht mehr gibt.
		if err := s.store.SetTrafficCursor(ctx, siteID, f.Offset, f.Inode); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Domain, err))
			continue
		}

		if f.Rotated {
			s.log.Debug("access-log rotiert", "domain", f.Domain)
		}
		if f.Skipped > 0 && f.Requests == 0 {
			// Nur unlesbare Zeilen: das Format passt nicht zu dem, was hier
			// erwartet wird. Eine Warnung, kein Fehler — gezählt wird
			// weiterhin, nur eben nichts.
			s.log.Warn("access-log in unbekanntem format",
				"domain", f.Domain, "zeilen", f.Skipped)
		}
		counted++
	}
	return counted, errs
}
