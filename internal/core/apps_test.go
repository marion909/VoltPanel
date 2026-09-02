package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

// TestUmgebungLiegtVerschluesselt: in einer App-Umgebung stehen regelmäßig
// Datenbankpasswörter, und die Datenbank des Panels ist eine Datei. Wer sie in
// die Hand bekommt — ein Backup, eine Kopie, ein falsch gesetztes Recht —, soll
// die Passwörter der Apps nicht mitlesen.
//
// Geprüft wird hier und nicht über HTTP: über HTTP ging der Test durch, weil
// er die Umgebung selbst verschlüsselt hatte, statt den Dienst es tun zu
// lassen. Grün, ohne die Verschlüsselung je anzufassen.
func TestUmgebungLiegtVerschluesselt(t *testing.T) {
	env := newTestEnv(t)
	svc := NewAppService(env.store, env.agent, env.cfg, env.secrets)

	enc, err := svc.encodeEnv(map[string]string{
		"DB_PASSWORD": "streng-geheim",
		"API_TOKEN":   "auch-geheim",
	})
	if err != nil {
		t.Fatal(err)
	}
	if enc == "" {
		t.Fatal("nichts herausgekommen")
	}
	for _, geheim := range []string{"streng-geheim", "auch-geheim", "DB_PASSWORD"} {
		if strings.Contains(enc, geheim) {
			t.Errorf("%q steht im Klartext in %q", geheim, enc)
		}
	}

	zurueck, err := svc.decodeEnv(enc)
	if err != nil {
		t.Fatal(err)
	}
	if zurueck["DB_PASSWORD"] != "streng-geheim" || zurueck["API_TOKEN"] != "auch-geheim" {
		t.Errorf("was zurückkam, ist nicht was hineinging: %v", zurueck)
	}

	// Eine leere Umgebung ergibt einen leeren Wert, keinen verschlüsselten
	// Leerstring: sonst stünde in jeder Zeile ohne Umgebung Datenmüll.
	leer, err := svc.encodeEnv(nil)
	if err != nil || leer != "" {
		t.Errorf("leere Umgebung wurde zu %q (%v)", leer, err)
	}
}

// TestNamenOhneWerte: die Oberfläche soll zeigen, welche Variablen gesetzt sind
// — nicht, was darin steht. Wer sie einmal gesetzt hat, braucht sie nicht
// zurückgelesen zu bekommen, und ein übernommenes Panel-Konto erst recht nicht.
func TestNamenOhneWerte(t *testing.T) {
	env := newTestEnv(t)
	svc := NewAppService(env.store, env.agent, env.cfg, env.secrets)

	enc, err := svc.encodeEnv(map[string]string{"B_ZWEITE": "x", "A_ERSTE": "geheim"})
	if err != nil {
		t.Fatal(err)
	}
	keys := svc.envKeys(enc)
	if len(keys) != 2 || keys[0] != "A_ERSTE" || keys[1] != "B_ZWEITE" {
		t.Errorf("Namen: %v — erwartet sortiert A_ERSTE, B_ZWEITE", keys)
	}

	// Ein unlesbarer Wert gibt eine leere Liste, nicht die halbe und nicht den
	// Rohtext.
	kaputt := svc.envKeys("das ist kein geheimtext")
	if len(kaputt) != 0 {
		t.Errorf("aus unlesbarem Text wurden Namen: %v", kaputt)
	}
	raw, _ := json.Marshal(kaputt)
	if string(raw) != "[]" {
		t.Errorf("die leere Liste wird als %s serialisiert — null bricht die Oberfläche", raw)
	}
}

// TestNodeFassungWirdNichtUnterAppsWeggezogen: eine Fassung stillschweigend zu
// entfernen, während drei Sites darauf laufen, wäre die unangenehmere Art, es
// zu erfahren — die Apps starten nach dem nächsten Neustart nicht mehr, und im
// Panel steht nur "läuft nicht".
func TestNodeFassungWirdNichtUnterAppsWeggezogen(t *testing.T) {
	env := newTestEnv(t)
	ctx, sys := t.Context(), store.SystemScope()
	svc := NewAppService(env.store, env.agent, env.cfg, env.secrets)

	_, _, site := env.seedSite(t, "shop")
	site.Type = store.SiteProxy
	site.ProxyTarget = "http://127.0.0.1:1"
	if err := env.store.UpdateSite(ctx, sys, site); err != nil {
		t.Fatal(err)
	}
	app := &store.App{
		TenantID: site.TenantID, SiteID: site.ID, Name: "shop",
		Kind: store.AppNative, Runtime: "node22", Args: []string{"server.js"},
	}
	if err := env.store.CreateApp(ctx, sys, app); err != nil {
		t.Fatal(err)
	}

	err := svc.RemoveNode(ctx, sys, 22)
	if err == nil {
		t.Fatal("node22 wurde entfernt, obwohl eine App darauf läuft")
	}
	if !strings.Contains(err.Error(), "shop") {
		t.Errorf("die Meldung nennt die App nicht: %v", err)
	}

	// Eine Fassung, die niemand benutzt, darf weg. Ohne laufenden Agent
	// scheitert der Aufruf danach — aber nicht mehr an dieser Prüfung.
	if err := svc.RemoveNode(ctx, sys, 20); err != nil &&
		strings.Contains(err.Error(), "wird von") {
		t.Errorf("node20 galt als benutzt: %v", err)
	}
}

// Ein Image, auf das noch eine App zeigt, darf nicht wegzuräumen sein.
//
// Docker selbst schützt nur, was einen vorhandenen Container hat. Eine App,
// deren Container gerade nicht existiert — gestoppt, weggeräumt, oder der
// Server neu aufgesetzt —, wäre für Docker unsichtbar und ihr Image frei; beim
// nächsten Start fehlte es, und der Grund läge Tage zurück.
func TestNutzerVonFindetAppsMitStillschweigendemTag(t *testing.T) {
	apps := []*store.App{
		{Name: "shop", Kind: store.AppDocker, Image: "nginx"},
		{Name: "blog", Kind: store.AppDocker, Image: "nginx:1.27"},
		{Name: "api", Kind: store.AppDocker, Image: "registry.example.at:5000/team/api:3"},
		{Name: "alt", Kind: store.AppDocker, Image: "a1b2c3d4e5f6"},
		// Eine native App hat kein Image und darf nichts blockieren.
		{Name: "worker", Kind: store.AppNative, Runtime: "node22"},
	}

	faelle := []struct {
		img  agent.ImageInfo
		will []string
	}{
		// "nginx" meint "nginx:latest" — das ist der Fall, der ohne
		// NormalizeRef durchginge und ein benutztes Image freigäbe.
		{agent.ImageInfo{ID: "111111111111", Repo: "nginx", Tag: "latest", Ref: "nginx:latest"},
			[]string{"shop"}},
		{agent.ImageInfo{ID: "222222222222", Repo: "nginx", Tag: "1.27", Ref: "nginx:1.27"},
			[]string{"blog"}},
		{agent.ImageInfo{ID: "333333333333", Repo: "registry.example.at:5000/team/api",
			Tag: "3", Ref: "registry.example.at:5000/team/api:3"}, []string{"api"}},
		// Über die Kennung angelegt: dieselbe App, anderer Weg.
		{agent.ImageInfo{ID: "a1b2c3d4e5f6789", Repo: "<none>", Tag: "<none>",
			Ref: "a1b2c3d4e5f6789", Dangling: true}, []string{"alt"}},
		// Und ein Image, das wirklich niemand braucht.
		{agent.ImageInfo{ID: "999999999999", Repo: "redis", Tag: "7", Ref: "redis:7"}, nil},
	}

	for _, f := range faelle {
		got := nutzerVon(f.img, apps)
		if strings.Join(got, ",") != strings.Join(f.will, ",") {
			t.Errorf("nutzerVon(%s) = %v, erwartet %v", f.img.Ref, got, f.will)
		}
	}
}
