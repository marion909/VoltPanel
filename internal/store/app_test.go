package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestAppNameKommtAusDerDomain: der Name wird ein Unit- und ein Dateiname auf
// der Maschine und muss über alle Mandanten hinweg eindeutig sein.
//
// Eingegeben wäre er zweierlei Ärger. Erstens müsste die Fehlermeldung „schon
// vergeben" lauten — und verriete damit einem Mandanten, welche Namen ein
// anderer benutzt. Zweitens wäre er eine zweite Wahrheit neben der Domain.
func TestAppNameKommtAusDerDomain(t *testing.T) {
	cases := map[string]string{
		"shop.example.at":      "shop-example-at",
		"SHOP.Example.AT":      "shop-example-at",
		"shop.example.at.":     "shop-example-at",
		"xn--brse-5qa.example": "xn--brse-5qa-example",
	}
	for domain, want := range cases {
		if got := AppNameForDomain(domain); got != want {
			t.Errorf("%s → %q, erwartet %q", domain, got, want)
		}
	}

	// Was herauskommt, muss immer ein gültiger Name sein — sonst schlägt das
	// Anlegen fehl an einer Stelle, an der niemand einen Namen eingegeben hat.
	for _, domain := range []string{
		"a.at",
		"1234.example.at",
		"eine-wirklich-sehr-lange-domain-eins.example.at",
		"eine-wirklich-sehr-lange-domain-zwei.example.at",
		strings.Repeat("x", 60) + ".at",
	} {
		got := AppNameForDomain(domain)
		if !reAppName.MatchString(got) {
			t.Errorf("%s → %q, das ist kein gültiger App-Name", domain, got)
		}
	}
}

// TestLangeDomainsBleibenUnterscheidbar: ohne das Stück Prüfsumme fielen zwei
// lange Domains auf denselben Namen — und die zweite Site überschriebe die Unit
// der ersten.
func TestLangeDomainsBleibenUnterscheidbar(t *testing.T) {
	// Die beiden müssen sich erst *nach* der Kürzungsgrenze unterscheiden —
	// sonst prüft der Test nur, dass Kürzen den Anfang stehen lässt, und wäre
	// auch ohne Prüfsumme grün. Genau darauf bin ich hereingefallen.
	a := AppNameForDomain("eine-wirklich-sehr-lange-domain-eins.example.at")
	b := AppNameForDomain("eine-wirklich-sehr-lange-domain-zwei.example.at")
	if a == b {
		t.Errorf("zwei Domains teilen sich den Namen %q", a)
	}
	// Und derselbe Name für dieselbe Domain, jedes Mal: sonst zeigte der Vhost
	// nach einem Neuschreiben auf eine Unit, die es nicht gibt.
	if AppNameForDomain("shop.example.at") != AppNameForDomain("shop.example.at") {
		t.Error("derselbe Name kommt zweimal verschieden heraus")
	}
}

func seedAppSite(t *testing.T, st *Store, slug string) *Site {
	t.Helper()
	ctx, sys := context.Background(), SystemScope()

	tenant := &Tenant{Name: slug, Slug: slug}
	if err := st.CreateTenant(ctx, sys, tenant); err != nil {
		t.Fatal(err)
	}
	site := &Site{
		TenantID: tenant.ID, Domain: slug + ".example.at", Type: SiteProxy,
		SystemUser: "site_" + slug, RootPath: "/var/www/" + slug, DocumentRoot: "public",
		// Das Ziel bekommt erst die App, wenn sie ihren Port hat. Bis dahin
		// muss trotzdem eines dastehen — der Store nimmt keine Proxy-Site ohne.
		ProxyTarget: "http://127.0.0.1:1",
	}
	if err := st.CreateSite(ctx, sys, site); err != nil {
		t.Fatal(err)
	}
	return site
}

// TestPortWirdVergebenUndBleibtEindeutig: zwei Apps auf demselben Port wären
// zwei Sites, von denen eine nicht startet — mit "Address already in use" und
// ohne dass jemand den Zusammenhang sieht.
func TestPortWirdVergebenUndBleibtEindeutig(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()

	vergeben := map[int]bool{}
	for _, slug := range []string{"eins", "zwei", "drei"} {
		site := seedAppSite(t, st, slug)
		app := &App{TenantID: site.TenantID, SiteID: site.ID,
			Name: AppNameForDomain(site.Domain), Runtime: "node", Args: []string{"server.js"}}
		if err := st.CreateApp(ctx, sys, app); err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		if app.Port < appPortFrom || app.Port > appPortTo {
			t.Errorf("%s bekam den Port %d, außerhalb von %d–%d",
				slug, app.Port, appPortFrom, appPortTo)
		}
		if vergeben[app.Port] {
			t.Errorf("der Port %d wurde zweimal vergeben", app.Port)
		}
		vergeben[app.Port] = true
	}
}

// TestFreiwerdenderPortWirdWiederverwendet: ohne das wandert die Vergabe mit
// jedem Anlegen und Löschen nach oben, bis der Bereich zu Ende ist — obwohl
// fast nichts belegt ist.
func TestFreiwerdenderPortWirdWiederverwendet(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()

	eins := seedAppSite(t, st, "eins")
	a := &App{TenantID: eins.TenantID, SiteID: eins.ID, Name: "eins", Runtime: "node"}
	if err := st.CreateApp(ctx, sys, a); err != nil {
		t.Fatal(err)
	}
	zwei := seedAppSite(t, st, "zwei")
	b := &App{TenantID: zwei.TenantID, SiteID: zwei.ID, Name: "zwei", Runtime: "node"}
	if err := st.CreateApp(ctx, sys, b); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteApp(ctx, sys, a.ID); err != nil {
		t.Fatal(err)
	}

	drei := seedAppSite(t, st, "drei")
	c := &App{TenantID: drei.TenantID, SiteID: drei.ID, Name: "drei", Runtime: "node"}
	if err := st.CreateApp(ctx, sys, c); err != nil {
		t.Fatal(err)
	}
	if c.Port != a.Port {
		t.Errorf("der freigewordene Port %d wurde nicht wiederverwendet, sondern %d vergeben",
			a.Port, c.Port)
	}
}

// TestEineAppJeSite: mehr als eine hieße, dass der Vhost auf einen von zwei
// Ports zeigt und niemand sagen kann, auf welchen.
func TestEineAppJeSite(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()

	site := seedAppSite(t, st, "eins")
	for i, name := range []string{"erste", "zweite"} {
		err := st.CreateApp(ctx, sys, &App{
			TenantID: site.TenantID, SiteID: site.ID, Name: name, Runtime: "node"})
		if i == 0 && err != nil {
			t.Fatalf("die erste App wurde abgelehnt: %v", err)
		}
		if i == 1 {
			if err == nil {
				t.Error("die zweite App für dieselbe Site wurde angenommen")
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
			}
		}
	}
}

// TestAppBleibtImMandanten: dieselbe Zusage wie überall — jede Abfrage erzwingt
// tenant_id.
func TestAppBleibtImMandanten(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()

	site := seedAppSite(t, st, "eins")
	app := &App{TenantID: site.TenantID, SiteID: site.ID, Name: "eins", Runtime: "node"}
	if err := st.CreateApp(ctx, sys, app); err != nil {
		t.Fatal(err)
	}
	fremd := Scope{TenantID: site.TenantID + 999, Role: RoleOwner}

	if _, err := st.GetApp(ctx, fremd, app.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetApp aus fremdem Mandanten: %v", err)
	}
	if _, err := st.AppForSite(ctx, fremd, site.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppForSite aus fremdem Mandanten: %v", err)
	}
	if err := st.DeleteApp(ctx, fremd, app.ID); err == nil {
		t.Error("DeleteApp aus fremdem Mandanten war erfolgreich")
	}
	liste, err := st.ListApps(ctx, fremd)
	if err != nil {
		t.Fatal(err)
	}
	if len(liste) != 0 {
		t.Errorf("ListApps zeigt %d fremde Apps", len(liste))
	}
}

// TestUnitTauglicheArgumente: der Store schützt die Datenbank vor Unsinn, der
// Agent den Server vor dem Store. Wer sich auf eine der beiden Prüfungen
// verlässt, hat die andere umsonst.
func TestUnitTauglicheArgumente(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()
	site := seedAppSite(t, st, "eins")

	schlecht := [][]string{
		{"server.js\nUser=root"},
		{"server.js", "--eval require('fs')"},
		{"%h/server.js"},
		{";"},
	}
	for _, args := range schlecht {
		err := st.CreateApp(ctx, sys, &App{
			TenantID: site.TenantID, SiteID: site.ID, Name: "eins",
			Runtime: "node", Args: args})
		if err == nil {
			t.Errorf("die Argumente %q wurden angenommen", args)
		}
	}
	for _, runtime := range []string{"", "sh", "bash", "python3"} {
		err := st.CreateApp(ctx, sys, &App{
			TenantID: site.TenantID, SiteID: site.ID, Name: "eins", Runtime: runtime})
		if err == nil {
			t.Errorf("die Laufzeitumgebung %q wurde angenommen", runtime)
		}
	}
}

// TestContainerAppWirdGeprueft: der Store prüft mit denselben Funktionen, die
// der Agent unmittelbar vor dem docker-Aufruf anwendet. Dieselben, nicht
// ähnliche — eine nachgebaute Prüfung ist die Stelle, an der die beiden
// auseinanderlaufen.
func TestContainerAppWirdGeprueft(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()
	site := seedAppSite(t, st, "eins")

	basis := App{
		TenantID: site.TenantID, SiteID: site.ID, Name: "eins",
		Kind: AppDocker, Image: "nginx:1.27-alpine", ContainerPort: 8080,
	}

	// So geht es durch.
	gut := basis
	if err := st.CreateApp(ctx, sys, &gut); err != nil {
		t.Fatalf("eine gültige Container-App wurde abgelehnt: %v", err)
	}
	if err := st.DeleteApp(ctx, sys, gut.ID); err != nil {
		t.Fatal(err)
	}

	cases := map[string]func(*App){
		"Image als Schalter":     func(a *App) { a.Image = "--privileged" },
		"Image mit Leerzeichen":  func(a *App) { a.Image = "nginx --net=host" },
		"kein Image":             func(a *App) { a.Image = "" },
		"Port fehlt":             func(a *App) { a.ContainerPort = 0 },
		"Port zu groß":           func(a *App) { a.ContainerPort = 70000 },
		"CPU-Angabe mit Anhang":  func(a *App) { a.CPUs = "1 --privileged" },
		"absolute Volume-Quelle": func(a *App) { a.Volumes = []AppVolume{{Source: "/etc", Target: "/x"}} },
		"Volume aus der Wurzel":  func(a *App) { a.Volumes = []AppVolume{{Source: "..", Target: "/x"}} },
		"proc überdecken":        func(a *App) { a.Volumes = []AppVolume{{Source: "x", Target: "/proc"}} },
		"Doppelpunkt im Ziel":    func(a *App) { a.Volumes = []AppVolume{{Source: "x", Target: "/a:/b"}} },
		"unbekannte Art":         func(a *App) { a.Kind = "podman" },
	}
	for name, kaputt := range cases {
		a := basis
		kaputt(&a)
		if err := st.CreateApp(ctx, sys, &a); err == nil {
			t.Errorf("%s wurde angenommen", name)
			_ = st.DeleteApp(ctx, sys, a.ID)
		}
	}
}

// TestNativeAppBleibtDieVoreinstellung: die Spalte kind kam später dazu. Eine
// Zeile ohne sie ist eine native App — sonst wären alle bestehenden Apps nach
// der Migration von unbekannter Art.
func TestNativeAppBleibtDieVoreinstellung(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()
	site := seedAppSite(t, st, "eins")

	a := &App{TenantID: site.TenantID, SiteID: site.ID, Name: "eins", Runtime: "node"}
	if err := st.CreateApp(ctx, sys, a); err != nil {
		t.Fatal(err)
	}
	if a.Kind != AppNative {
		t.Errorf("Art ist %q, erwartet %q", a.Kind, AppNative)
	}
	gelesen, err := st.GetApp(ctx, sys, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gelesen.Kind != AppNative {
		t.Errorf("gelesen als %q", gelesen.Kind)
	}
}
