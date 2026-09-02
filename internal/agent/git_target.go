package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/marion909/voltpanel/internal/gitspec"
	"github.com/marion909/voltpanel/internal/transfer"
)

// Wohin ein `git clone` wirklich geht.
//
// gitspec sieht nur die Adresse an. Steht dort eine IP, wird sie beurteilt;
// steht dort ein Name, bleibt offen, worauf er zeigt — und damit stand der
// bequemste Weg offen: ein Name, den der Kunde selbst auf 169.254.169.254
// zeigen lässt. Der Metadatendienst antwortet, git schreibt die Antwort ins
// Protokoll, und das Protokoll steht im Panel.
//
// Deshalb wird hier aufgelöst, bevor git läuft, und jede Adresse dahinter
// gegen dieselbe Regel gehalten wie ein Backup-Ziel — transfer.CheckAddr, nicht
// eine zweite, ähnliche Liste.
//
// Was das nicht kann: zwischen dieser Auflösung und der von git liegt ein
// Moment, in dem ein DNS-Eintrag mit kurzer Lebensdauer die Antwort wechseln
// kann. Für https wird deshalb gepinnt (siehe cloneArgs) — dort löst git gar
// nicht mehr selbst auf. Für ssh bleibt die Lücke; sie zu schließen hieße,
// ssh die Verbindung aus der Hand zu nehmen.

// repoTarget ist das geprüfte Ziel einer Repository-Adresse.
type repoTarget struct {
	scheme string
	host   string
	port   string
	// addrs sind die aufgelösten Adressen, alle geprüft. Bei einem Literal
	// steht genau eine darin.
	addrs []netip.Addr
	// literal sagt, ob der Host schon eine IP war. Dann gibt es nichts zu
	// pinnen — die Adresse steht ja da.
	literal bool
}

// checkRepoTarget löst den Hostnamen auf und prüft jede Adresse.
//
// Abgelehnt wird, sobald *eine* der Antworten nicht zulässig ist. Nicht "eine
// gute genügt": wer den DNS-Eintrag stellt, bestimmt auch die Reihenfolge, und
// git nimmt nicht unbedingt dieselbe wie wir.
func checkRepoTarget(ctx context.Context, repoURL string) (*repoTarget, error) {
	scheme, host, port, err := gitspec.Endpoint(repoURL)
	if err != nil {
		return nil, opInputErr(OpDeployRun, "%v", err)
	}
	t := &repoTarget{scheme: scheme, host: host, port: port}

	if addr, err := netip.ParseAddr(host); err == nil {
		t.literal = true
		if err := transfer.CheckAddr(addr); err != nil {
			return nil, opInputErr(OpDeployRun, "%s: %v", host, err)
		}
		t.addrs = []netip.Addr{addr}
		return t, nil
	}

	addrs, err := aufloesen(ctx, host)
	if err != nil {
		return nil, opErr(OpDeployRun, "%s lässt sich nicht auflösen: %v", host, err)
	}
	if len(addrs) == 0 {
		return nil, opErr(OpDeployRun, "%s löst auf keine adresse auf", host)
	}
	for _, addr := range addrs {
		if addr.Is4In6() {
			addr = addr.Unmap()
		}
		if err := transfer.CheckAddr(addr); err != nil {
			return nil, opInputErr(OpDeployRun,
				"%s zeigt auf %s, und %v — ein repository liegt dort nicht",
				host, addr, err)
		}
		t.addrs = append(t.addrs, addr)
	}
	return t, nil
}

// aufloesen ist die Namensauflösung — als Variable, damit der Test sie
// ersetzen kann. Was hier geprüft wird, ist eine Regel über Antworten des DNS;
// sie an echtem DNS zu prüfen hieße, den Test von einem fremden Namen abhängig
// zu machen, der heute so und morgen anders auflöst.
var aufloesen = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// cloneArgs baut die Kommandozeile des Klons.
//
// Zwei Einstellungen stehen darin, die nicht offensichtlich sind:
//
// http.followRedirects=false. Ohne das folgt git einer Umleitung der ersten
// Anfrage — und eine Umleitung führt, wohin der andere will. Die geprüfte
// Adresse wäre damit nur die erste Station. Der Preis ist sichtbar: ein
// umbenanntes Repository muss mit seiner neuen Adresse eingetragen werden,
// statt still weiterzulaufen.
//
// http.curloptResolve. Damit steht die Adresse fest, bevor git sie braucht:
// git löst den Namen nicht mehr selbst auf, sondern nimmt die hier geprüfte.
// Das schließt die Lücke zwischen unserer Auflösung und seiner — genau die,
// durch die ein Eintrag mit kurzer Lebensdauer sonst passt.
func cloneArgs(plan *deployPlan, t *repoTarget, target string) []string {
	args := []string{
		"clone", "--depth", "1", "--single-branch",
		"--branch", plan.ref, "--config", "advice.detachedHead=false",
	}

	if t.scheme == "https" {
		args = append(args, "--config", "http.followRedirects=false")
		if !t.literal {
			port := t.port
			if port == "" {
				port = "443"
			}
			for _, addr := range t.addrs {
				args = append(args, "--config",
					fmt.Sprintf("http.curloptResolve=%s:%s:%s", t.host, port, addr))
			}
		}
	}

	// "--" vor der Adresse: ohne das läse git eine Adresse, die mit einem
	// Bindestrich beginnt, als Option. NormalizeURL schließt das schon aus;
	// beides zusammen heißt, dass hier auch dann nichts passiert, wenn dort
	// einmal etwas durchrutscht.
	return append(args, "--", plan.repoURL, target)
}

// hostFuerLog ist der Name mit den Adressen dahinter, für das Deploy-Protokoll.
func (t *repoTarget) hostFuerLog() string {
	if t.literal {
		return t.host
	}
	teile := make([]string, 0, len(t.addrs))
	for _, a := range t.addrs {
		teile = append(teile, a.String())
	}
	return fmt.Sprintf("%s (%s)", t.host, strings.Join(teile, ", "))
}
