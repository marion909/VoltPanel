package core

import (
	"regexp"
	"strings"
	"testing"
)

// Linux begrenzt Benutzernamen auf 32 Zeichen. Eine lange Domain muss deshalb
// gekürzt werden — ohne dabei zwei verschiedene Domains auf denselben Benutzer
// abzubilden, sonst teilen sich zwei Kunden ein Konto.
func TestSystemUserName(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z_][a-z0-9_-]{1,31}$`)

	domains := []string{
		"example.at",
		"www.example.at",
		"sehr-langer-name.example.at",
		"eine-ausgesprochen-lange-subdomain.eines-langen-kundennamens.example.at",
		"a.at",
		"XN--MNCHEN-3YA.de",
	}

	seen := map[string]string{}
	for _, d := range domains {
		got := SystemUserName(d)

		if len(got) > 32 {
			t.Errorf("SystemUserName(%q) = %q ist %d Zeichen lang, Linux erlaubt 32", d, got, len(got))
		}
		if !valid.MatchString(got) {
			t.Errorf("SystemUserName(%q) = %q ist kein gültiger Linux-Benutzername", d, got)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("SystemUserName(%q) und SystemUserName(%q) ergeben beide %q", d, other, got)
		}
		seen[got] = d
	}
}

// Zwei lange Domains mit gleichem Präfix dürfen sich nicht denselben Benutzer
// teilen — genau der Fall, für den der Hash im Namen steht.
func TestSystemUserNameDistinguishesLongPrefixes(t *testing.T) {
	a := SystemUserName("eine-sehr-lange-subdomain-hier.example.at")
	b := SystemUserName("eine-sehr-lange-subdomain-hier.example.de")
	if a == b {
		t.Fatalf("zwei verschiedene Domains ergeben denselben Benutzer %q", a)
	}
}

func TestPoolName(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

	for _, d := range []string{"example.at", "www.example.at", strings.Repeat("lang", 20) + ".at"} {
		got := PoolName(d)
		if !valid.MatchString(got) {
			t.Errorf("PoolName(%q) = %q ist kein gültiger Poolname", d, got)
		}
		if len(got) > 64 {
			t.Errorf("PoolName(%q) = %q ist %d Zeichen lang", d, got, len(got))
		}
	}
}

// Der Name muss über Aufrufe hinweg gleich bleiben: er landet im Dateisystem
// und in der Datenbank, ein Wechsel würde die Zuordnung zerreißen.
func TestNamesAreStable(t *testing.T) {
	for i := 0; i < 5; i++ {
		if got := SystemUserName("example.at"); got != "site_example_at" {
			t.Fatalf("SystemUserName ist nicht stabil: %q", got)
		}
		if got := PoolName("example.at"); got != "volt-example_at" {
			t.Fatalf("PoolName ist nicht stabil: %q", got)
		}
	}
}
