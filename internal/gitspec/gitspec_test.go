package gitspec

import (
	"strings"
	"testing"
)

// TestGitAdresseIstKeinKommando ist der Kern.
//
// git legt eine Adresse selbst noch einmal aus, und mehrere Formen tun dabei
// etwas anderes als "irgendwo etwas herunterladen". Jede Zeile unten ist ein
// dokumentierter Weg, aus einer Repository-Adresse ein Kommando zu machen.
func TestGitAdresseIstKeinKommando(t *testing.T) {
	angriffe := map[string]string{
		"ext-Transport führt ein Kommando aus":       "ext::sh -c whoami",
		"ext-Transport ohne Leerzeichen":             "ext::sh",
		"Option statt Adresse":                       "--upload-pack=/bin/sh",
		"Option mit einem Bindestrich":               "-u/bin/sh",
		"ssh-Option im Hostnamen (CVE-2017-1000117)": "ssh://-oProxyCommand=id/repo.git",
		// Ohne Gleichheitszeichen: sonst scheitert die Adresse schon am
		// Zeichenvorrat des Hostnamens, und die Regel, die hier zählt — ein
		// Hostname beginnt nicht mit einem Bindestrich —, bliebe ungeprüft.
		"kurze ssh-Option im Hostnamen":   "ssh://-x/repo.git",
		"Bindestrich am Hostanfang":       "ssh://-host.example.at/repo.git",
		"Kurzform mit Optionshost":        "git@-oProxyCommand=id:repo.git",
		"lokale Datei":                    "file:///etc",
		"lokaler Pfad ohne Schema":        "/etc/shadow",
		"unverschlüsseltes git-Protokoll": "git://host/repo.git",
		"unverschlüsseltes http":          "http://host/repo.git",
		"Zeilenumbruch":                   "https://host/repo.git\nrm -rf /",
		"Leerzeichen":                     "https://host/repo.git --upload-pack=id",
		"Backtick":                        "https://host/`id`.git",
		"Semikolon":                       "https://host/a;id.git",
		"Punkte im Pfad":                  "https://host/../../etc/passwd",
		"Passwort in der Adresse":         "https://user:geheim@host/repo.git",
		"leer":                            "",
	}

	for name, url := range angriffe {
		if got, err := NormalizeURL(url); err == nil {
			t.Errorf("%s: %q wurde angenommen als %q", name, url, got)
		}
	}
}

// TestGitAdresseWirdNeuGebaut: sonst prüfte der Test oben nur, dass gar nichts
// durchkommt. Was herauskommt, muss außerdem aus geprüften Teilen bestehen —
// nicht die Eingabe sein, die zufällig keinen Angriff enthielt.
func TestGitAdresseWirdNeuGebaut(t *testing.T) {
	gut := map[string]string{
		"https://github.com/marion909/VoltPanel.git": "https://github.com/marion909/VoltPanel.git",
		"https://github.com/marion909/VoltPanel":     "https://github.com/marion909/VoltPanel",
		"git@github.com:marion909/VoltPanel.git":     "git@github.com:marion909/VoltPanel.git",
		"ssh://git@github.com/marion909/repo.git":    "ssh://git@github.com/marion909/repo.git",
		"ssh://git@gitea.example.at:2222/x/y.git":    "ssh://git@gitea.example.at:2222/x/y.git",
		// Führender Schrägstrich in der Kurzform: derselbe Ort, andere
		// Schreibweise. Was herauskommt, ist die kanonische.
		"git@github.com:/marion909/repo.git": "git@github.com:marion909/repo.git",
		"  https://github.com/a/b.git  ":     "https://github.com/a/b.git",
	}
	for in, want := range gut {
		got, err := NormalizeURL(in)
		if err != nil {
			t.Errorf("%q wurde abgelehnt: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q → %q, erwartet %q", in, got, want)
		}
	}
}

// TestBranchNameIstKeineOption: der Name wird ein Argument von `git checkout`.
// Ein führender Bindestrich wäre dort eine Option.
func TestBranchNameIstKeineOption(t *testing.T) {
	for _, gut := range []string{"main", "master", "release/2026-09", "v1.2.3", "feature/a_b"} {
		if !ValidRef(gut) {
			t.Errorf("%q wurde abgelehnt", gut)
		}
	}
	for _, schlecht := range []string{
		"", "-b", "--force", "main..dev", "main@{1}", "main.lock", "main/",
		"a//b", "main branch", "main\nrm", "haupt;id", "..",
	} {
		if ValidRef(schlecht) {
			t.Errorf("%q ging durch", schlecht)
		}
	}
}

// TestKeinAufrufNachInnen: `git clone` holt von einer Adresse, die der Kunde
// bestimmt. Zeigt sie auf den Server selbst oder auf den Metadatendienst der
// Cloud, ist das ein Aufruf von innen, den jemand von außen ausgelöst hat — und
// die Antwort landet im Protokoll des Deploys, wo er sie lesen kann.
func TestKeinAufrufNachInnen(t *testing.T) {
	// Diese hier fängt die Adressprüfung.
	nachInnen := []string{
		"https://169.254.169.254/latest/meta-data", // AWS, Azure, GCP
		"https://127.0.0.1/x.git",
		"https://127.0.0.1:8080/x.git",
		"https://0.0.0.0/x.git",
		"https://224.0.0.1/x.git",
		"git@169.254.169.254:x.git",
	}
	for _, url := range nachInnen {
		got, err := NormalizeURL(url)
		if err == nil {
			t.Errorf("%q wurde angenommen als %q", url, got)
			continue
		}
		// Auf den Grund geprüft: sonst wäre der Test auch dann grün, wenn nur
		// die Formprüfung zufällig zuschlägt — und die Adressprüfung könnte
		// ersatzlos verschwinden, ohne dass es auffällt.
		if !strings.Contains(err.Error(), "server selbst") &&
			!strings.Contains(err.Error(), "link-local") &&
			!strings.Contains(err.Error(), "multicast") &&
			!strings.Contains(err.Error(), "keine adresse") {
			t.Errorf("%q abgelehnt, aber nicht von der Adressprüfung: %v", url, err)
		}
	}

	// Und diese hier fängt schon die Formprüfung: der Doppelpunkt steht nicht
	// im Zeichenvorrat eines Hostnamens, IPv6-Literale kommen also gar nicht
	// bis zur Adressprüfung. Eine Einschränkung, keine Lücke — aufgeschrieben,
	// weil ich sie zuerst für eine Adressprüfung gehalten habe.
	for _, url := range []string{
		"https://[fe80::1]/x.git",
		"https://[::1]/x.git",
		"https://[::ffff:127.0.0.1]/x.git",
	} {
		if got, err := NormalizeURL(url); err == nil {
			t.Errorf("%q wurde angenommen als %q", url, got)
		}
	}

	// Ein selbst betriebenes Gitea im eigenen Netz ist der Normalfall in genau
	// der Art von Umgebung, für die dieses Panel gedacht ist.
	nachDrinnen := []string{
		"https://10.0.0.5/x/y.git",
		"https://192.168.1.20:3000/x/y.git",
		"git@172.16.0.9:x/y.git",
		"https://gitea.intern/x/y.git",
	}
	for _, url := range nachDrinnen {
		if _, err := NormalizeURL(url); err != nil {
			t.Errorf("%q wurde abgelehnt: %v", url, err)
		}
	}
}

// Endpoint muss den Doppelpunkt der Kurzform als Pfadtrenner lesen, nicht als
// Port. "git@github.com:marion909/VoltPanel.git" hat keinen Port "marion909" —
// und wer ihn dafür hält, löst hinterher den falschen Namen auf.
func TestEndpoint(t *testing.T) {
	faelle := []struct {
		in                 string
		scheme, host, port string
	}{
		{"https://github.com/marion909/VoltPanel.git", "https", "github.com", ""},
		{"https://git.example.at:8443/team/app.git", "https", "git.example.at", "8443"},
		{"ssh://git@git.example.at/team/app.git", "ssh", "git.example.at", ""},
		{"ssh://git@git.example.at:2222/team/app.git", "ssh", "git.example.at", "2222"},
		{"git@github.com:marion909/VoltPanel.git", "ssh", "github.com", ""},
		{"git@10.0.0.5:team/app.git", "ssh", "10.0.0.5", ""},
	}
	for _, f := range faelle {
		scheme, host, port, err := Endpoint(f.in)
		if err != nil {
			t.Errorf("Endpoint(%q) = %v", f.in, err)
			continue
		}
		if scheme != f.scheme || host != f.host || port != f.port {
			t.Errorf("Endpoint(%q) = %q %q %q, erwartet %q %q %q",
				f.in, scheme, host, port, f.scheme, f.host, f.port)
		}
	}
}
