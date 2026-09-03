package templates

import (
	"strings"
	"testing"
)

func gueltigeAutoconfigDaten() AutoconfigData {
	return AutoconfigData{
		Domain: "kunde.example.at", Host: "mail.example.at",
		IMAPPort: 993, SMTPPort: 587,
	}
}

func gueltigerAutoconfigVhost() AutoconfigVhostData {
	return AutoconfigVhostData{
		AutoconfigHost: "autoconfig.kunde.example.at", AutodiscoverHost: "autodiscover.kunde.example.at",
		CertPath:      "/var/lib/volt/certs/autoconfig.kunde.example.at/fullchain.pem",
		KeyPath:       "/var/lib/volt/certs/autoconfig.kunde.example.at/privkey.pem",
		MozillaPath:   "/var/lib/volt/autoconfig/kunde.example.at/config-v1.1.xml",
		MicrosoftPath: "/var/lib/volt/autoconfig/kunde.example.at/autodiscover.xml",
	}
}

// xmlesc ist zu XML, was phpstr zu PHP ist: die einzige Schranke zwischen
// einem eingesetzten Wert und den fünf Zeichen, die XML als Syntax liest.
// Ein Domain- oder Hostname besteht die Validierung nie mit einem davon —
// geprüft wird die Funktion trotzdem für sich, unabhängig davon, ob ein
// gültiger Wert sie je auf die Probe stellen könnte.
func TestXMLEscEscaptSonderzeichen(t *testing.T) {
	faelle := map[string]string{
		`normal`:                 `normal`,
		`a & b`:                  `a &amp; b`,
		`<script>`:               `&lt;script&gt;`,
		`"quote"`:                `&#34;quote&#34;`,
		`it's`:                   `it&#39;s`,
		`</hostname><x>böse</x>`: `&lt;/hostname&gt;&lt;x&gt;böse&lt;/x&gt;`,
	}
	for in, want := range faelle {
		if got := xmlesc(in); got != want {
			t.Errorf("xmlesc(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

func TestRenderMozillaAutoconfigEnthaeltWerte(t *testing.T) {
	out, err := RenderMozillaAutoconfig(gueltigeAutoconfigDaten())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kunde.example.at", "mail.example.at", "993", "587",
		"SSL", "STARTTLS", "%EMAILADDRESS%"} {
		if !strings.Contains(out, want) {
			t.Errorf("mozilla-config enthält %q nicht:\n%s", want, out)
		}
	}
}

func TestRenderMicrosoftAutodiscoverEnthaeltWerte(t *testing.T) {
	out, err := RenderMicrosoftAutodiscover(gueltigeAutoconfigDaten())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mail.example.at", "993", "587", "IMAP", "SMTP"} {
		if !strings.Contains(out, want) {
			t.Errorf("microsoft-config enthält %q nicht:\n%s", want, out)
		}
	}
}

// Eine Domäne, die store.ValidMailDomain nicht besteht, darf nie in einer
// XML-Datei landen, die ein fremdes Mailprogramm ungeprüft lädt.
func TestAutoconfigDataPrueftDomaene(t *testing.T) {
	d := gueltigeAutoconfigDaten()
	d.Domain = "nicht gültig"
	if _, err := RenderMozillaAutoconfig(d); err == nil {
		t.Error("eine ungültige domäne wurde angenommen")
	}
	if _, err := RenderMicrosoftAutodiscover(d); err == nil {
		t.Error("eine ungültige domäne wurde angenommen")
	}
}

func TestAutoconfigDataPrueftPort(t *testing.T) {
	d := gueltigeAutoconfigDaten()
	d.IMAPPort = 0
	if _, err := RenderMozillaAutoconfig(d); err == nil {
		t.Error("ein port von 0 wurde angenommen")
	}
}

func TestRenderAutoconfigVhostEnthaeltBeideHosts(t *testing.T) {
	out, err := RenderAutoconfigVhost(gueltigerAutoconfigVhost())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"autoconfig.kunde.example.at", "autodiscover.kunde.example.at",
		"fullchain.pem", "privkey.pem", "config-v1.1.xml", "autodiscover.xml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("vhost enthält %q nicht:\n%s", want, out)
		}
	}
}

// Ein Pfad mit einem Zeilenumbruch oder Semikolon könnte in der erzeugten
// Nginx-Config eine zusätzliche Direktive öffnen — checkPath muss das vor dem
// Rendern abfangen, nicht erst nginx -t auf dem Server.
func TestRenderAutoconfigVhostPrueftPfade(t *testing.T) {
	d := gueltigerAutoconfigVhost()
	d.CertPath = "/var/lib/volt/certs/x; server { listen 1.2.3.4:80; }"
	if _, err := RenderAutoconfigVhost(d); err == nil {
		t.Error("ein zertifikatspfad mit ';' wurde angenommen")
	}
}
