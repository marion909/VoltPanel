package store

import "testing"

// TestAddSiteTrafficAndCursorSchreibtBeidesAtomar deckt den Fund ab, dass
// CollectTraffic Byte-Zuwachs und Lesestand früher in zwei getrennten
// UPDATE-Anweisungen schrieb: schlug die zweite (der Cursor) nach der ersten
// (die Bytes) fehl, las der nächste Lauf denselben Log-Bereich erneut und
// zählte dieselben Bytes ein zweites Mal. AddSiteTrafficAndCursor macht
// beides in einer Anweisung.
//
// traffic_requests läuft in derselben Anweisung mit demselben
// Zeitraum-Rhythmus wie traffic_bytes — hier mitgeprüft, statt eines eigenen
// Tests, weil es exakt dieselbe CASE-WHEN-Logik ist, nur eine zweite Spalte.
func TestAddSiteTrafficAndCursorSchreibtBeidesAtomar(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	_, _, site := seedTenant(t, s, "alice")

	if err := s.AddSiteTrafficAndCursor(ctx, site.ID, 1000, 10, "2026-08", 500, 7); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSite(ctx, SystemScope(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrafficBytes != 1000 || got.TrafficRequests != 10 || got.TrafficPeriod != "2026-08" {
		t.Fatalf("traffic_bytes/requests/period = %d/%d/%s, erwartet 1000/10/2026-08",
			got.TrafficBytes, got.TrafficRequests, got.TrafficPeriod)
	}

	cursors, err := s.TrafficCursors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var gefunden bool
	for _, c := range cursors {
		if c.SiteID != site.ID {
			continue
		}
		gefunden = true
		if c.Offset != 500 || c.Inode != 7 {
			t.Fatalf("Cursor = %d/%d, erwartet 500/7", c.Offset, c.Inode)
		}
	}
	if !gefunden {
		t.Fatal("kein Cursor für die Site gefunden")
	}

	// Ein zweiter Aufruf im selben Zeitraum zählt dazu, statt zu ersetzen.
	if err := s.AddSiteTrafficAndCursor(ctx, site.ID, 500, 5, "2026-08", 900, 8); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSite(ctx, SystemScope(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrafficBytes != 1500 || got.TrafficRequests != 15 {
		t.Fatalf("traffic_bytes/requests nach zweitem Aufruf = %d/%d, erwartet 1500/15",
			got.TrafficBytes, got.TrafficRequests)
	}

	// Der Monatswechsel setzt beide Zähler zurück, statt aufzuaddieren.
	if err := s.AddSiteTrafficAndCursor(ctx, site.ID, 42, 3, "2026-09", 100, 1); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSite(ctx, SystemScope(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrafficBytes != 42 || got.TrafficRequests != 3 || got.TrafficPeriod != "2026-09" {
		t.Fatalf("nach Monatswechsel: %d/%d/%s, erwartet 42/3/2026-09",
			got.TrafficBytes, got.TrafficRequests, got.TrafficPeriod)
	}
}
