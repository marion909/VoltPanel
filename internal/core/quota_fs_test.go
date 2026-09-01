package core

import (
	"context"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// TestOhneProjectQuotaKeinFehler ist die Zusage, an der der ganze Zusatz hängt:
// ein Server, der keine Project Quota führt, darf davon nichts merken.
//
// Die Grenzen wirken dort weiter auf Anwendungsebene. Ein Fehler pro Mandant
// und Stunde im Log wäre das Gegenteil von hilfreich — und ein Betreiber, der
// Warnungen gewohnheitsmäßig überliest, übersieht die nächste echte.
func TestOhneProjectQuotaKeinFehler(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	alice, _, _ := env.seedSite(t, "alice")
	seedPlan(t, env, alice, &store.Plan{Name: "Klein", DiskQuotaMB: 500})
	env.seedSite(t, "bob")

	quota := NewQuotaService(env.store, env.agent, env.cfg, nil)

	applied, hinweis, errs := quota.SyncDiskQuotas(ctx)
	if len(errs) > 0 {
		t.Fatalf("ein Server ohne Project Quota meldet Fehler: %v", errs)
	}
	if applied != 0 {
		t.Errorf("%d Mandanten gesetzt, obwohl das Dateisystem keine Quota führt", applied)
	}
	if hinweis == "" {
		t.Error("kein Hinweis darauf, warum nichts geschah")
	}
}

// TestMandantOhnePaketIstUnbegrenzt: 0 heißt im ganzen Panel "unbegrenzt", und
// eine fehlende Zuordnung darf keine stille Sperre werden. Im Dateisystem wäre
// sie besonders unangenehm — dort schreibt der Kernel den Fehler, nicht das
// Panel, und niemand sähe, woher er kommt.
func TestMandantOhnePaketIstUnbegrenzt(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sys := store.SystemScope()

	ohne, _, _ := env.seedSite(t, "ohne")
	mit, _, _ := env.seedSite(t, "mit")
	seedPlan(t, env, mit, &store.Plan{Name: "Groß", DiskQuotaMB: 2048})
	unbegrenzt, _, _ := env.seedSite(t, "unbegrenzt")
	seedPlan(t, env, unbegrenzt, &store.Plan{Name: "Offen", DiskQuotaMB: 0})

	quota := NewQuotaService(env.store, env.agent, env.cfg, nil)

	cases := map[string]struct {
		tenant int64
		want   int64
	}{
		"ohne Paket":        {ohne.ID, 0},
		"mit Grenze":        {mit.ID, 2048},
		"Paket ohne Grenze": {unbegrenzt.ID, 0},
	}
	for name, c := range cases {
		got, err := quota.diskLimitMB(ctx, sys, c.tenant)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: Grenze %d MB, erwartet %d MB", name, got, c.want)
		}
	}
}

// TestSitesEinesMandantenBildenEinProjekt: die Quota gilt je Mandant. Wer fünf
// Sites hat, hat eine Grenze über alle fünf — nicht fünfmal dieselbe.
//
// Geprüft wird an der Meldung des Agents: er bekommt die Verzeichnisse eines
// Mandanten in einem Aufruf, und nur so kann er sie an eine Projektnummer
// hängen.
func TestSitesEinesMandantenBildenEinProjekt(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sys := store.SystemScope()

	tenant, _, erste := env.seedSite(t, "viele")
	zweite := &store.Site{
		TenantID: tenant.ID, Domain: "zweite.example.at", Type: store.SiteStatic,
		SystemUser: erste.SystemUser, RootPath: env.sitesDir + "/zweite.example.at",
		DocumentRoot: "public",
	}
	if err := env.store.CreateSite(ctx, sys, zweite); err != nil {
		t.Fatal(err)
	}

	res, err := env.agent.SetQuotaProject(ctx, tenant.ID,
		[]string{erste.RootPath, zweite.RootPath}, 100)
	if err != nil {
		t.Fatalf("der Agent lehnte den Aufruf ab: %v", err)
	}
	if res.ProjectID == 0 {
		t.Error("Projektnummer 0 — die trägt jede Datei ohne Projekt")
	}
	if res.Tenant != tenant.ID {
		t.Errorf("Antwort für Mandant %d statt %d", res.Tenant, tenant.ID)
	}
	// Ohne Project Quota im Dateisystem geschieht nichts — aber mit einer
	// Begründung, nicht stillschweigend.
	if !res.Applied && res.Skipped == "" {
		t.Error("nichts geschah und nichts sagt warum")
	}
}

// TestQuotaNurInDenWurzeln: ein Pfad außerhalb der Wurzeln des Agents bekäme
// sonst die Projektnummer eines Mandanten — und sein Verbrauch zählte dort mit.
func TestQuotaNurInDenWurzeln(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	tenant, _, _ := env.seedSite(t, "grenze")

	for _, pfad := range []string{"/etc", "/", "/var/lib/mysql", env.sitesDir + "/../../etc"} {
		_, err := env.agent.SetQuotaProject(ctx, tenant.ID, []string{pfad}, 100)
		if err == nil {
			t.Errorf("%s wurde angenommen", pfad)
		}
	}
}
