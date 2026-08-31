package core

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestJoinInsideBlocksEscapes ist das Tenant-Gefängnis des Dateimanagers.
// Fällt einer dieser Fälle um, kann ein Kunde im Verzeichnis eines anderen
// lesen und schreiben — der Agent würde es durchlassen, weil beide unter
// /var/www liegen.
func TestJoinInsideBlocksEscapes(t *testing.T) {
	const root = "/var/www/example.at"

	denied := []struct {
		name string
		rel  string
	}{
		{"einfaches ..", "../andere-site.at/public"},
		{"tiefes ..", "public/../../../etc/passwd"},
		{"nur punkte", "../../.."},
		{"nullbyte", "public\x00/../../etc"},
		{"präfix-verwechslung", "../example.at-evil/datei"},
	}
	for _, tc := range denied {
		t.Run("verweigert: "+tc.name, func(t *testing.T) {
			got, err := joinInside(root, tc.rel)
			if err == nil {
				t.Fatalf("joinInside(%q, %q) = %q, erwartet war eine Ablehnung", root, tc.rel, got)
			}
		})
	}

	allowed := map[string]string{
		"":                      root,
		".":                     root,
		"/":                     root,
		"public":                root + "/public",
		"/public":               root + "/public",
		"public/index.php":      root + "/public/index.php",
		"public/../public/x":    root + "/public/x",
		"a/b/c/d.txt":           root + "/a/b/c/d.txt",
		"datei mit leerzeichen": root + "/datei mit leerzeichen",
	}
	for rel, want := range allowed {
		t.Run("erlaubt: "+rel, func(t *testing.T) {
			got, err := joinInside(root, rel)
			if err != nil {
				t.Fatalf("joinInside(%q, %q) verweigert: %v", root, rel, err)
			}
			if got != want {
				t.Fatalf("joinInside(%q, %q) = %q, erwartet %q", root, rel, got, want)
			}
		})
	}
}

// TestJoinInsideNeverLeavesRoot prüft die Eigenschaft direkt, statt einzelne
// Eingaben aufzuzählen: was auch immer hereinkommt, das Ergebnis liegt in der
// Wurzel oder es gibt einen Fehler.
func TestJoinInsideNeverLeavesRoot(t *testing.T) {
	const root = "/var/www/site.at"

	inputs := []string{
		"..", "../..", "../../..", "./../.", "a/../..", "a/b/../../..",
		"/..", "//..", "/./../", "....//", "..;/etc", "%2e%2e/etc",
		"a/./b/../../../etc", strings.Repeat("../", 50) + "etc/passwd",
		strings.Repeat("a/", 100), "\\..\\..",
	}
	for _, rel := range inputs {
		got, err := joinInside(root, rel)
		if err != nil {
			continue
		}
		if got != root && !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Errorf("joinInside(%q, %q) = %q verlässt die Wurzel", root, rel, got)
		}
	}
}

func TestRelativeTo(t *testing.T) {
	const root = "/var/www/example.at"
	cases := map[string]string{
		root:                       "",
		root + "/public":           "public",
		root + "/public/index.php": "public/index.php",
		root + "/a/b/c":            "a/b/c",
	}
	for abs, want := range cases {
		if got := relativeTo(root, abs); got != want {
			t.Errorf("relativeTo(%q, %q) = %q, erwartet %q", root, abs, got, want)
		}
	}
}
