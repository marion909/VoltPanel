package store

import (
	"context"
	"errors"
	"testing"
)

// TestZweiterPoolFuerDieselbeSiteWirdAbgelehnt deckt den Fund ab, dass
// CreatePHPPool vor dem Insert nicht prüfte, ob für die Site schon ein Pool
// existiert. Zwei nahezu gleichzeitige Aufrufe für dieselbe Site mit
// unterschiedlichem pool_name konnten zwei Zeilen mit derselben site_id
// anlegen — PHPPoolBySite liefert ohne ORDER BY/LIMIT die erste
// Treffer-Zeile, welche das war, war unbestimmt; die andere blieb im Panel
// unsichtbar, existierte aber als laufender PHP-FPM-Pool weiter.
func TestZweiterPoolFuerDieselbeSiteWirdAbgelehnt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tenant, _, site := seedTenant(t, s, "alice")

	erster := &PHPPool{
		TenantID: tenant.ID, SiteID: site.ID, PHPVersion: "8.3",
		PoolName: "alice_erster", SocketPath: "/run/php/alice1.sock",
	}
	if err := s.CreatePHPPool(ctx, SystemScope(), erster); err != nil {
		t.Fatalf("erster Pool: %v", err)
	}

	zweiter := &PHPPool{
		TenantID: tenant.ID, SiteID: site.ID, PHPVersion: "8.3",
		PoolName: "alice_zweiter", SocketPath: "/run/php/alice2.sock",
	}
	if err := s.CreatePHPPool(ctx, SystemScope(), zweiter); !errors.Is(err, ErrConflict) {
		t.Fatalf("zweiter Pool für dieselbe Site: %v, erwartet ErrConflict", err)
	}

	pool, err := s.PHPPoolBySite(ctx, SystemScope(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pool.PoolName != "alice_erster" {
		t.Errorf("PHPPoolBySite liefert %q, erwartet den zuerst angelegten Pool", pool.PoolName)
	}
}
