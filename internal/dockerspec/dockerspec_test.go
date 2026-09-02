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
