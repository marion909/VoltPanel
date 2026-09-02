package gitspec

import "testing"

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
