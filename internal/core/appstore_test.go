package core

import (
	"context"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

func seedPlainTenant(t *testing.T, env *testEnv, slug string) int64 {
	t.Helper()
	tenant := &store.Tenant{Name: slug, Slug: slug}
	if err := env.store.CreateTenant(context.Background(), store.SystemScope(), tenant); err != nil {
		t.Fatal(err)
	}
	return tenant.ID
}

// Zwei WordPress-Installationen desselben Mandanten dürfen sich nicht um
// denselben Datenbanknamen streiten — der Name muss aus der Domain kommen,
// nicht aus einem festen Wort wie "wordpress". CreateDatabase prefixt zwar
// noch mit dem Mandanten, aber nicht mit der Site; ohne diese Herleitung
// scheiterte die zweite Installation eines Mandanten mit zwei
// WordPress-Sites an "existiert bereits".
func TestWordpressDBBaseNameUnterscheidetDomains(t *testing.T) {
	a := wordpressDBBaseName("blog.alice.example.at")
	b := wordpressDBBaseName("shop.alice.example.at")
	if a == b {
		t.Errorf("blog.alice.example.at und shop.alice.example.at ergeben denselben namen %q", a)
	}
	if !store.ValidNameInput(a) || !store.ValidNameInput(b) {
		t.Errorf("die abgeleiteten namen sind keine gültige eingabe für CreateDatabase: %q, %q", a, b)
	}
	// Dieselbe Domain ergibt jedes Mal denselben Namen — sonst zeigte ein
	// erneuter Aufruf (etwa nach einem fehlgeschlagenen ersten Versuch) auf
	// eine andere Datenbank als der vorherige.
	if wordpressDBBaseName("blog.alice.example.at") != a {
		t.Error("derselbe domainname ergibt zweimal verschiedene datenbanknamen")
	}
}

// InstallWordPress darf nie eine Site oder Datenbank zurückgeben, die es in
// der Datenbank tatsächlich nicht gibt — ein Ergebnis, das mehr behauptet, als
// tatsächlich entstanden ist, wäre irreführender als ein leeres. Was genau
// gelingt, hängt in dieser Testumgebung vom Agent ab (kein echter
// Systembenutzer, keine echte nginx-Installation) — geprüft wird deshalb die
// Zusage, die unabhängig davon gilt, wo der Ablauf stehen bleibt.
func TestInstallWordPressErgebnisIstNieErfunden(t *testing.T) {
	env := newTestEnv(t)
	svc := NewAppStoreService(env.store, env.agent, env.cfg, env.secrets)
	tenantID := seedPlainTenant(t, env, "alice")
	sc := store.Scope{TenantID: tenantID, Role: store.RoleOwner}

	res, err := svc.InstallWordPress(context.Background(), sc, InstallWordPressInput{
		Domain: "blog.alice.example.at", PHPVersion: "8.3", TenantID: tenantID,
	})
	if res == nil {
		if err == nil {
			t.Fatal("weder ergebnis noch fehler — das darf nicht sein")
		}
		return
	}
	if res.Site != nil {
		if _, gerr := env.store.GetSite(context.Background(), sc, res.Site.ID); gerr != nil {
			t.Errorf("res.Site verweist auf %d, aber GetSite findet sie nicht: %v",
				res.Site.ID, gerr)
		}
	}
	if res.Database != nil {
		if _, gerr := env.store.GetDatabase(context.Background(), sc, res.Database.ID); gerr != nil {
			t.Errorf("res.Database verweist auf %d, aber GetDatabase findet sie nicht: %v",
				res.Database.ID, gerr)
		}
		// Eine Datenbank ohne Site wäre verwaist — sie zählt auf niemandes
		// Quota und ihr SiteID-Verweis zeigt ins Leere.
		if res.Site == nil {
			t.Error("es gibt eine datenbank, aber keine site dazu")
		}
	}
}
