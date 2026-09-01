package templates

import (
	"strings"
	"testing"
)

func gueltigeApp() AppData {
	return AppData{
		Name:        "shop",
		GeneratedAt: "2026-09-01",
		User:        "site_shop",
		Group:       "site_shop",
		WorkingDir:  "/var/www/shop.example.at",
		EnvPath:     "/etc/volt/apps/shop.env",
		Command:     []string{"/usr/bin/node", "server.js"},
		Env:         []EnvVar{{Key: "PORT", Value: "3001"}},
	}
}

// TestUnitZeileBleibtEineZeile ist der Kern.
//
// Eine Unit-Datei ist zeilenweise aufgebaut. Was einen Zeilenumbruch in einen
// Wert bekommt, schreibt die nächste Direktive selbst — und `User=root` in
// einer Zeile, die als Kommandozeile gedacht war, lässt die App als root
// laufen. Alles unten ist ein Versuch, genau das zu tun.
func TestUnitZeileBleibtEineZeile(t *testing.T) {
	angriffe := map[string]func(*AppData){
		"Zeilenumbruch im Kommando": func(d *AppData) {
			d.Command = []string{"/usr/bin/node", "server.js\nUser=root"}
		},
		"Zeilenumbruch im Arbeitsverzeichnis": func(d *AppData) {
			d.WorkingDir = "/var/www/x\nUser=root"
		},
		"Zeilenumbruch im Pfad der Umgebungsdatei": func(d *AppData) {
			d.EnvPath = "/etc/volt/x.env\nExecStartPre=/bin/sh"
		},
		"Zeilenumbruch im Benutzer": func(d *AppData) {
			d.User = "site_x\nUser=root"
		},
		"Zeilenumbruch im Namen": func(d *AppData) {
			d.Name = "shop\nUser=root"
		},
		"Platzhalter im Kommando": func(d *AppData) {
			// %h wäre das Heimatverzeichnis, nicht der gemeinte Pfad.
			d.Command = []string{"%h/bin/node", "server.js"}
		},
		"Leerzeichen im Argument": func(d *AppData) {
			// systemd zerlegt ExecStart selbst; wer das nachbaut, vertut sich.
			d.Command = []string{"/usr/bin/node", "server.js --eval require('fs')"}
		},
		"relatives Programm": func(d *AppData) {
			d.Command = []string{"node", "server.js"}
		},
		"Semikolon im Argument": func(d *AppData) {
			// In einer Unit trennt ";" zwei Kommandos.
			d.Command = []string{"/usr/bin/node", ";", "/bin/sh"}
		},
		"Punkte im Arbeitsverzeichnis": func(d *AppData) {
			d.WorkingDir = "/var/www/../../etc"
		},
	}

	for name, kaputt := range angriffe {
		d := gueltigeApp()
		kaputt(&d)
		out, err := RenderApp(d)
		if err == nil {
			t.Errorf("%s wurde angenommen:\n%s", name, out)
		}
	}
}

// TestGueltigeAppWirdGerendert: sonst prüfte der Test oben nur, dass gar nichts
// durchkommt.
func TestGueltigeAppWirdGerendert(t *testing.T) {
	out, err := RenderApp(gueltigeApp())
	if err != nil {
		t.Fatalf("die gültige App wurde abgelehnt: %v", err)
	}

	muss := []string{
		"ExecStart=/usr/bin/node server.js",
		"User=site_shop",
		"WorkingDirectory=/var/www/shop.example.at",
		"EnvironmentFile=/etc/volt/apps/shop.env",
		"Description=VoltPanel App volt-app-shop",
		// Ohne diese vier ist die Unit keine Isolation, sondern nur ein Start.
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
		"ReadWritePaths=/var/www/shop.example.at",
		"CapabilityBoundingSet=",
	}
	for _, m := range muss {
		if !strings.Contains(out, m) {
			t.Errorf("%q fehlt in der Unit:\n%s", m, out)
		}
	}

	// V8 braucht schreibbaren und ausführbaren Speicher. Mit dieser Sperre
	// startet keine Node-Anwendung — eine Härtung, die das Ziel nicht laufen
	// lässt, ist keine.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "MemoryDenyWriteExecute=yes") {
			t.Error("MemoryDenyWriteExecute=yes steht in der Unit — Node startet damit nicht")
		}
	}
}

// TestUmgebungLandetNichtInDerUnit: Unit-Dateien sind für alle lesbar, und
// `systemctl show` gibt sie ohnehin heraus. In einer App-Umgebung stehen
// regelmäßig Datenbankpasswörter.
func TestUmgebungLandetNichtInDerUnit(t *testing.T) {
	d := gueltigeApp()
	d.Env = append(d.Env, EnvVar{Key: "DATABASE_URL", Value: "postgres://u:geheim@localhost/db"})

	out, err := RenderApp(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "geheim") {
		t.Errorf("das Passwort steht in der Unit:\n%s", out)
	}
	if strings.Contains(out, "Environment=") && !strings.Contains(out, "EnvironmentFile=") {
		t.Error("die Umgebung steht direkt in der Unit")
	}
}

// TestUmgebungsdateiBleibtZeilenweise: ein Zeilenumbruch im Wert schriebe die
// nächste Zeile selbst — und damit eine Variable, die niemand gesetzt hat.
func TestUmgebungsdateiBleibtZeilenweise(t *testing.T) {
	for _, wert := range []string{
		"geheim\nADMIN=1",
		"geheim\r\nADMIN=1",
		"geheim\x00ADMIN=1",
	} {
		d := gueltigeApp()
		d.Env = []EnvVar{{Key: "TOKEN", Value: wert}}
		if out, err := RenderAppEnv(d); err == nil {
			t.Errorf("%q wurde angenommen:\n%s", wert, out)
		}
	}

	for _, key := range []string{"lower case", "MIT-BINDESTRICH", "1FUEHRENDE", "", "A B"} {
		d := gueltigeApp()
		d.Env = []EnvVar{{Key: key, Value: "x"}}
		if _, err := RenderAppEnv(d); err == nil {
			t.Errorf("der Name %q wurde angenommen", key)
		}
	}
}

// TestUmgebungswertBleibtErhalten: was hineingeht, muss wieder herauskommen.
// Ein Wert mit Anführungszeichen, Backslash oder Doppelkreuz ist nichts
// Besonderes — in einem Passwort steht so etwas ständig.
func TestUmgebungswertBleibtErhalten(t *testing.T) {
	d := gueltigeApp()
	d.Env = []EnvVar{
		{Key: "MIT_RAUTE", Value: "abc#def"},
		{Key: "MIT_ANFUEHRUNG", Value: `sag "hallo"`},
		{Key: "MIT_BACKSLASH", Value: `C:\pfad\zu`},
		{Key: "MIT_LEERZEICHEN", Value: "eins zwei"},
	}
	out, err := RenderAppEnv(d)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"MIT_ANFUEHRUNG":  `MIT_ANFUEHRUNG="sag \"hallo\""`,
		"MIT_BACKSLASH":   `MIT_BACKSLASH="C:\\pfad\\zu"`,
		"MIT_RAUTE":       `MIT_RAUTE="abc#def"`,
		"MIT_LEERZEICHEN": `MIT_LEERZEICHEN="eins zwei"`,
	}
	for key, zeile := range want {
		if !strings.Contains(out, zeile) {
			t.Errorf("%s steht nicht als %s in:\n%s", key, zeile, out)
		}
	}
}

// TestUmgebungIstStabilSortiert: zweimal dasselbe Schreiben muss dieselbe Datei
// ergeben. Sonst meldet jeder Durchlauf eine Änderung und startet die App neu.
func TestUmgebungIstStabilSortiert(t *testing.T) {
	a := gueltigeApp()
	a.Env = []EnvVar{{Key: "B", Value: "2"}, {Key: "A", Value: "1"}, {Key: "C", Value: "3"}}
	b := gueltigeApp()
	b.Env = []EnvVar{{Key: "C", Value: "3"}, {Key: "A", Value: "1"}, {Key: "B", Value: "2"}}

	erste, err := RenderAppEnv(a)
	if err != nil {
		t.Fatal(err)
	}
	zweite, err := RenderAppEnv(b)
	if err != nil {
		t.Fatal(err)
	}
	if erste != zweite {
		t.Errorf("dieselbe Umgebung in anderer Reihenfolge ergibt eine andere Datei:\n%s\n---\n%s",
			erste, zweite)
	}
}

// TestUnitNameGehoertUns: der Name entscheidet, welche Unit der Agent anfassen
// darf. Ein Name ohne Präfix wäre ein Weg, an eine fremde Unit zu kommen.
func TestUnitNameGehoertUns(t *testing.T) {
	if got := UnitName("shop"); got != "volt-app-shop" {
		t.Errorf("UnitName = %q", got)
	}
	if got := AppNameFromUnit("volt-app-shop.service"); got != "shop" {
		t.Errorf("AppNameFromUnit = %q", got)
	}
	for _, fremd := range []string{"ssh", "ssh.service", "nginx", "volt-web.service", ""} {
		if got := AppNameFromUnit(fremd); got != "" {
			t.Errorf("%q gilt als App-Unit: %q", fremd, got)
		}
	}
	for _, schlecht := range []string{"", "a", "ab", "-shop", "shop-", "Shop", "shop.service",
		"../etc", "shop app", strings.Repeat("x", 33),
		// Das Präfix setzt der Agent. Ein Name, der es schon trägt, weckt die
		// falsche Erwartung, der Aufrufer täte es.
		"volt-app-shop"} {
		if ValidAppName(schlecht) {
			t.Errorf("%q gilt als App-Name", schlecht)
		}
	}
}
