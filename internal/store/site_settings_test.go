package store

import (
	"strings"
	"testing"
)

func TestSiteSettingsAcceptsValidInput(t *testing.T) {
	s := SiteSettings{
		Redirects: []Redirect{
			{From: "/alt", To: "/neu", Code: 301},
			{From: "/shop", To: "https://shop.example.at", Code: 302},
			{From: "/", To: "/start", Code: 308},
		},
		DenyIPs:  []string{"203.0.113.5", "198.51.100.0/24", "2001:db8::1"},
		AllowIPs: []string{"10.0.0.0/8"},
		ExtraLines: []string{
			"add_header X-Robots-Tag noindex;",
			"rewrite ^/alt/(.*)$ /neu/$1 permanent;",
			"# ein kommentar",
		},
		BasicAuth:      &BasicAuth{Enabled: true, Realm: "Interner Bereich", Users: []string{"admin", "test-user"}},
		MaxBodySize:    "128M",
		FastCGITimeout: 120,
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("gültige Einstellungen abgelehnt: %v", err)
	}
}

// TestSiteSettingsRejectsInjection deckt die Eingaben ab, die in einer
// Nginx-Config Schaden anrichten würden. text/template escaped nichts —
// diese Prüfung ist die einzige Abwehr.
func TestSiteSettingsRejectsInjection(t *testing.T) {
	cases := []struct {
		name     string
		settings SiteSettings
	}{
		{"zusatzzeile bricht den block auf", SiteSettings{
			ExtraLines: []string{"} server { listen 8080; root /;"},
		}},
		{"zusatzzeile mit location-block", SiteSettings{
			ExtraLines: []string{"location /admin { auth_basic off; }"},
		}},
		{"zusatzzeile mit zeilenumbruch", SiteSettings{
			ExtraLines: []string{"add_header X 1;\n    root /etc;"},
		}},
		{"zusatzzeile ohne semikolon", SiteSettings{
			ExtraLines: []string{"add_header X 1"},
		}},
		{"zusatzzeile mit include", SiteSettings{
			ExtraLines: []string{"include /etc/passwd;"},
		}},
		{"zusatzzeile mit nullbyte", SiteSettings{
			ExtraLines: []string{"add_header X 1;\x00"},
		}},
		{"weiterleitung auf javascript-url", SiteSettings{
			Redirects: []Redirect{{From: "/x", To: "javascript:alert(1)", Code: 301}},
		}},
		{"weiterleitung mit direktive im ziel", SiteSettings{
			Redirects: []Redirect{{From: "/x", To: "https://a.at; root /etc", Code: 301}},
		}},
		{"weiterleitung mit unerlaubtem code", SiteSettings{
			Redirects: []Redirect{{From: "/x", To: "/y", Code: 200}},
		}},
		{"weiterleitung ohne fuehrenden slash", SiteSettings{
			Redirects: []Redirect{{From: "x", To: "/y", Code: 301}},
		}},
		{"ip-sperre mit direktive", SiteSettings{
			DenyIPs: []string{"1.2.3.4;\n    root /;"},
		}},
		{"unsinnige ip", SiteSettings{DenyIPs: []string{"kein.host"}}},
		{"unsinniges netz", SiteSettings{DenyIPs: []string{"10.0.0.0/99"}}},
		{"anfragegroesse unsinnig", SiteSettings{MaxBodySize: "viel"}},
		{"negatives zeitlimit", SiteSettings{FastCGITimeout: -1}},
		{"zeitlimit zu gross", SiteSettings{FastCGITimeout: 99999}},
		{"benutzername mit doppelpunkt", SiteSettings{
			BasicAuth: &BasicAuth{Enabled: true, Users: []string{"admin:x"}},
		}},
		{"passwortschutz ohne benutzer", SiteSettings{
			BasicAuth: &BasicAuth{Enabled: true},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := tc.settings
			if err := settings.Validate(); err == nil {
				t.Fatalf("Validate akzeptierte %+v", tc.settings)
			}
		})
	}
}

// TestSiteSettingsReportsAllProblems: wer fünf Weiterleitungen einträgt, soll
// nicht fünfmal speichern müssen, um alle Tippfehler zu finden.
func TestSiteSettingsReportsAllProblems(t *testing.T) {
	s := SiteSettings{
		Redirects: []Redirect{
			{From: "kein-slash", To: "/a", Code: 301},
			{From: "/b", To: "/c", Code: 999},
		},
		DenyIPs:     []string{"unsinn"},
		MaxBodySize: "viel",
	}

	err := s.Validate()
	if err == nil {
		t.Fatal("Validate akzeptierte mehrere fehlerhafte Werte")
	}
	if n := strings.Count(err.Error(), ";"); n < 3 {
		t.Fatalf("nur %d Trennzeichen in der Meldung — es sollten alle vier Probleme auftauchen:\n%v", n, err)
	}
}

func TestSiteSettingsRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	tenant, _, _ := seedTenant(t, st, "alice")
	sys := SystemScope()

	site := &Site{
		TenantID: tenant.ID, Domain: "settings.example.at", Type: SiteStatic,
		SystemUser: "site_x", RootPath: "/var/www/x", DocumentRoot: "public",
		Settings: SiteSettings{
			Redirects:   []Redirect{{From: "/alt", To: "/neu", Code: 301}},
			DenyIPs:     []string{"203.0.113.5"},
			ExtraLines:  []string{"add_header X-Test 1;"},
			MaxBodySize: "256M",
		},
	}
	if err := st.CreateSite(ctx, sys, site); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetSite(ctx, sys, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Settings.Redirects) != 1 || got.Settings.Redirects[0].To != "/neu" {
		t.Fatalf("Weiterleitungen nicht erhalten: %+v", got.Settings.Redirects)
	}
	if got.Settings.MaxBodySize != "256M" {
		t.Fatalf("MaxBodySize = %q", got.Settings.MaxBodySize)
	}
	if len(got.Settings.DenyIPs) != 1 {
		t.Fatalf("IP-Sperren nicht erhalten: %+v", got.Settings.DenyIPs)
	}
}

// TestSiteRejectsInvalidSettings: die Prüfung greift auch über das Repository.
func TestSiteRejectsInvalidSettings(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	tenant, _, _ := seedTenant(t, st, "alice")

	site := &Site{
		TenantID: tenant.ID, Domain: "boese.example.at", Type: SiteStatic,
		SystemUser: "site_x", RootPath: "/var/www/x",
		Settings: SiteSettings{ExtraLines: []string{"} server { listen 9999;"}},
	}
	if err := st.CreateSite(ctx, SystemScope(), site); err == nil {
		t.Fatal("CreateSite akzeptierte eine Zusatzzeile, die den Block aufbricht")
	}
}

// TestTenantTokenNotSerialized: der Cloudflare-Token darf nie im JSON stehen.
func TestTenantTokenNotSerialized(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	sys := SystemScope()

	tenant := &Tenant{Name: "Alice", Slug: "alice", CloudflareToken: "verschluesselter-wert"}
	if err := st.CreateTenant(ctx, sys, tenant); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetTenant(ctx, sys, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CloudflareToken != "verschluesselter-wert" {
		t.Fatalf("Token nicht gespeichert: %q", got.CloudflareToken)
	}

	encoded, err := got.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "verschluesselter-wert") {
		t.Fatalf("der Token steht im JSON: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"has_cloudflare_token":true`) {
		t.Fatalf("das abgeleitete Feld fehlt: %s", encoded)
	}
}
