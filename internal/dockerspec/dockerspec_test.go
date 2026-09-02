package dockerspec

import (
	"strings"
	"testing"
)

// TestImageIstKeinSchalter: docker läse einen Namen, der mit einem Bindestrich
// beginnt, als Schalter.
func TestImageIstKeinSchalter(t *testing.T) {
	gut := []string{
		"nginx", "nginx:1.27-alpine", "docker.io/library/nginx:latest",
		"ghcr.io/marion909/voltpanel:v1.2.3", "registry.example.at:5000/x/y:tag",
		"nginx@sha256:" + strings.Repeat("a", 64),
	}
	for _, s := range gut {
		if err := ValidImage(s); err != nil {
			t.Errorf("%q wurde abgelehnt: %v", s, err)
		}
	}
	schlecht := []string{
		"", "--privileged", "-v/:/host", "nginx --privileged", "nginx;id",
		"nginx\nrm -rf /", "nginx `id`", "nginx$(id)", "ng inx",
		"nginx@sha256:kurz", strings.Repeat("x", 400),
	}
	for _, s := range schlecht {
		if err := ValidImage(s); err == nil {
			t.Errorf("%q ging durch", s)
		}
	}
}

// TestNurEigeneContainer: ohne das Präfix wäre "meinen Container anhalten" ein
// Weg, jeden Container des Servers anzuhalten — auch den, in dem jemand anderes
// seine Datenbank betreibt.
func TestNurEigeneContainer(t *testing.T) {
	if got := ContainerName("shop"); got != "volt-shop" {
		t.Errorf("ContainerName = %q", got)
	}
	for _, eigen := range []string{"volt-shop", "volt-a-b-c", "volt-x1y"} {
		if !ContainerNameOwned(eigen) {
			t.Errorf("%q gilt nicht als eigener Container", eigen)
		}
	}
	for _, fremd := range []string{
		"", "postgres", "kunde-datenbank", "volt", "volt-", "volt-X",
		"voltshop", "volt-shop/x", "volt-../etc", "VOLT-shop",
	} {
		if ContainerNameOwned(fremd) {
			t.Errorf("%q gilt als eigener Container", fremd)
		}
	}
}

// Entfernen darf man ein Image über seinen Namen oder über seine Kennung.
// Alles andere ist ein Schalter für docker, kein Image.
func TestValidImageRef(t *testing.T) {
	gut := []string{
		"nginx:1.27",
		"nginx",
		"registry.example.at:5000/team/app:2026-08-30",
		"a1b2c3d4e5f6",
		// Zu kurz für eine Kennung, aber ein zulässiger Repository-Name —
		// und deshalb erlaubt. Docker liest ihn genauso.
		"a1b2c3",
		"sha256:" + strings.Repeat("ab", 32),
		strings.Repeat("f", 64),
	}
	for _, s := range gut {
		if err := ValidImageRef(s); err != nil {
			t.Errorf("ValidImageRef(%q) = %v, sollte gehen", s, err)
		}
	}

	schlecht := []string{
		"",
		"-f",
		"--force",
		"--all",
		"nginx; rm -rf /",
		"nginx && docker rmi -f alles",
		"nginx $(id)",
		"<none>:<none>", // steht so in der Ausgabe, ist aber kein Image
		"nginx:1.27 --force",
	}
	for _, s := range schlecht {
		if err := ValidImageRef(s); err == nil {
			t.Errorf("ValidImageRef(%q) = nil, sollte abgelehnt werden", s)
		}
	}
}

// "nginx" und "nginx:latest" sind dasselbe Image. Wer das nicht ergänzt, hält
// ein benutztes Image für unbenutzt — und entfernt es.
func TestNormalizeRef(t *testing.T) {
	faelle := map[string]string{
		"nginx":                             "nginx:latest",
		"nginx:1.27":                        "nginx:1.27",
		"team/app":                          "team/app:latest",
		"team/app:2":                        "team/app:2",
		"registry.example.at:5000/app":      "registry.example.at:5000/app:latest",
		"registry.example.at:5000/app:2026": "registry.example.at:5000/app:2026",
		"nginx@sha256:" + strings.Repeat("cd", 32): "nginx@sha256:" + strings.Repeat("cd", 32),
	}
	for in, will := range faelle {
		if got := NormalizeRef(in); got != will {
			t.Errorf("NormalizeRef(%q) = %q, erwartet %q", in, got, will)
		}
	}
}
