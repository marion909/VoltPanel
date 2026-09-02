package agent

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

// Ein Name, der auf den Metadatendienst zeigt, kommt nicht durch — auch wenn
// er daneben eine völlig unverdächtige Adresse liefert.
//
// Das ist der Kern: "eine gute Antwort genügt" wäre die falsche Regel. Wer den
// DNS-Eintrag stellt, bestimmt auch die Reihenfolge, und git nimmt nicht
// unbedingt dieselbe wie wir.
func TestCheckRepoTargetLehntJedeSchlechteAntwortAb(t *testing.T) {
	original := aufloesen
	t.Cleanup(func() { aufloesen = original })

	faelle := []struct {
		name      string
		antwort   []string
		abgelehnt bool
	}{
		{"nur öffentlich", []string{"93.184.216.34"}, false},
		{"privates gitea", []string{"10.0.0.5"}, false},
		{"metadatendienst", []string{"169.254.169.254"}, true},
		{"loopback", []string{"127.0.0.1"}, true},
		{"gut und schlecht gemischt", []string{"93.184.216.34", "169.254.169.254"}, true},
		{"schlecht zuerst", []string{"169.254.169.254", "93.184.216.34"}, true},
		{"als ipv6 verpackt", []string{"::ffff:127.0.0.1"}, true},
		{"gar keine antwort", nil, true},
	}

	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			aufloesen = func(context.Context, string) ([]netip.Addr, error) {
				out := make([]netip.Addr, 0, len(f.antwort))
				for _, s := range f.antwort {
					out = append(out, netip.MustParseAddr(s))
				}
				return out, nil
			}
			_, err := checkRepoTarget(t.Context(), "https://git.example.at/team/app.git")
			if f.abgelehnt && err == nil {
				t.Errorf("%v wurde angenommen", f.antwort)
			}
			if !f.abgelehnt && err != nil {
				t.Errorf("%v wurde abgelehnt: %v", f.antwort, err)
			}
		})
	}
}

// Steht in der Adresse schon eine IP, wird gar nicht erst aufgelöst — aber
// geprüft wird sie trotzdem.
func TestCheckRepoTargetPrueftAuchLiterale(t *testing.T) {
	original := aufloesen
	t.Cleanup(func() { aufloesen = original })
	aufloesen = func(context.Context, string) ([]netip.Addr, error) {
		t.Error("für ein literal darf nicht aufgelöst werden")
		return nil, nil
	}

	if _, err := checkRepoTarget(t.Context(), "https://169.254.169.254/x.git"); err == nil {
		t.Error("der metadatendienst als literal wurde angenommen")
	}
	ziel, err := checkRepoTarget(t.Context(), "git@10.0.0.5:team/app.git")
	if err != nil {
		t.Fatalf("ein gitea im eigenen netz wurde abgelehnt: %v", err)
	}
	if !ziel.literal || ziel.host != "10.0.0.5" {
		t.Errorf("falsch zerlegt: %+v", ziel)
	}
}

// Für https wird die geprüfte Adresse festgenagelt und keiner Umleitung
// gefolgt. Ohne beides wäre die Prüfung nur die erste Station.
func TestCloneArgsNageltDieAdresseFest(t *testing.T) {
	plan := &deployPlan{ref: "main", repoURL: "https://git.example.at/team/app.git"}
	ziel := &repoTarget{
		scheme: "https", host: "git.example.at",
		addrs: []netip.Addr{
			netip.MustParseAddr("93.184.216.34"),
			netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946"),
		},
	}
	args := strings.Join(cloneArgs(plan, ziel, "/srv/x/releases/20260902-120000"), " ")

	for _, will := range []string{
		"http.followRedirects=false",
		"http.curloptResolve=git.example.at:443:93.184.216.34",
		"http.curloptResolve=git.example.at:443:2606:2800:220:1:248:1893:25c8:1946",
	} {
		if !strings.Contains(args, will) {
			t.Errorf("%q fehlt in der kommandozeile:\n%s", will, args)
		}
	}
	// Die Adresse selbst kommt zuletzt und hinter "--".
	if !strings.HasSuffix(args, "-- https://git.example.at/team/app.git /srv/x/releases/20260902-120000") {
		t.Errorf("die adresse steht nicht am ende hinter --:\n%s", args)
	}

	// Ein eigener Port wird übernommen.
	ziel.port = "8443"
	args = strings.Join(cloneArgs(plan, ziel, "/tmp/x"), " ")
	if !strings.Contains(args, "http.curloptResolve=git.example.at:8443:93.184.216.34") {
		t.Errorf("der port fehlt beim festnageln:\n%s", args)
	}
}

// ssh löst selbst auf, und curl-Einstellungen gehen es nichts an. Sie trotzdem
// mitzugeben wäre wirkungslos — und würde vortäuschen, die Lücke sei zu.
func TestCloneArgsOhneCurlBeiSSH(t *testing.T) {
	plan := &deployPlan{ref: "main", repoURL: "git@github.com:marion909/VoltPanel.git"}
	ziel := &repoTarget{
		scheme: "ssh", host: "github.com",
		addrs: []netip.Addr{netip.MustParseAddr("140.82.121.4")},
	}
	args := strings.Join(cloneArgs(plan, ziel, "/tmp/x"), " ")
	if strings.Contains(args, "curloptResolve") || strings.Contains(args, "followRedirects") {
		t.Errorf("http-einstellungen bei einem ssh-klon:\n%s", args)
	}
}
