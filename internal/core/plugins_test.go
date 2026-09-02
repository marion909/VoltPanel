package core

import (
	"context"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/agent"
)

// Erwartet wird nicht nur "irgendein Fehler", sondern die eigene Meldung
// "kein bekanntes plugin". Der Grund: env.agent zeigt in diesen Tests auf
// einen echten, aber unbedienten Unix-Socket — jeder Aufruf, der wirklich bis
// zum Agent durchgeht, scheitert dort ohnehin, ob die Prüfung im Panel nun
// greift oder nicht. Ein Test, der nur "err != nil" prüft, wäre also auch
// dann grün, wenn die eigentliche Schranke fehlte, und genau darauf bin ich
// beim ersten Versuch hereingefallen: er blieb grün, selbst nachdem ich die
// Prüfung testweise entfernt hatte.
const nichtImKatalog = "kein bekanntes plugin"

func mussLehnenWegenKatalog(t *testing.T, err error, was string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s wurde angenommen, obwohl es nicht im katalog steht", was)
	}
	if !strings.Contains(err.Error(), nichtImKatalog) {
		t.Fatalf("%s wurde abgelehnt, aber aus dem falschen grund: %v", was, err)
	}
}

func TestPluginInstallLehntUnbekanntesAb(t *testing.T) {
	env := newTestEnv(t)
	svc := NewPluginService(env.store, env.agent)

	_, err := svc.Install(context.Background(), "nicht-im-katalog")
	mussLehnenWegenKatalog(t, err, `"nicht-im-katalog"`)
}

func TestPluginUninstallLehntUnbekanntesAb(t *testing.T) {
	env := newTestEnv(t)
	svc := NewPluginService(env.store, env.agent)

	_, err := svc.Uninstall(context.Background(), "nicht-im-katalog")
	mussLehnenWegenKatalog(t, err, `"nicht-im-katalog"`)
}

func TestPluginSetEnabledLehntUnbekanntesAb(t *testing.T) {
	env := newTestEnv(t)
	svc := NewPluginService(env.store, env.agent)

	err := svc.SetEnabled(context.Background(), "nicht-im-katalog", true)
	mussLehnenWegenKatalog(t, err, `"nicht-im-katalog"`)
}

// "docker" ist eine gültige Fähigkeit des Agents (siehe die
// Nachinstallieren-Knöpfe bei Apps/Firewall/Mail), aber kein Eintrag im
// Plugin-Katalog. Ein Plugin-Install, das nur beim Agent nachfragt statt beim
// eigenen Katalog, ließe sich damit für alles missbrauchen, was der Agent
// kennt — nicht nur für das, was als Plugin gedacht ist.
func TestPluginInstallPrueftDenEigenenKatalogNichtNurDenAgent(t *testing.T) {
	env := newTestEnv(t)
	svc := NewPluginService(env.store, env.agent)

	_, err := svc.Install(context.Background(), "docker")
	mussLehnenWegenKatalog(t, err, `"docker"`)
}

// List zeigt den ganzen Katalog, auch die Einträge, die noch nie installiert
// wurden — sonst gäbe es in der Oberfläche nichts zum Anklicken.
func TestPluginListZeigtDenGanzenKatalog(t *testing.T) {
	env := newTestEnv(t)
	svc := NewPluginService(env.store, env.agent)

	views, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != len(Catalog()) {
		t.Fatalf("%d einträge, erwartet %d (der ganze katalog)", len(views), len(Catalog()))
	}
	for _, v := range views {
		if v.Installed {
			t.Errorf("%s gilt als installiert, ohne dass etwas installiert wurde", v.ID)
		}
	}
}

// Jeder Katalogeintrag mit einem Dienst muss auf einen Namen zeigen, den der
// Agent auch wirklich anfassen darf (siehe agent.ServiceAllowed) — sonst
// scheitert jede Installation dieses Plugins an derselben Stelle, und zwar
// erst beim ersten Klick in der Oberfläche, nicht beim Bauen.
func TestKatalogDiensteSindWhitelistKompatibel(t *testing.T) {
	for _, p := range Catalog() {
		if p.Service == "" {
			continue
		}
		if !agent.ServiceAllowed(p.Service) {
			t.Errorf("%s: Dienst %q steht nicht in der Whitelist des Agents (allowedServices)",
				p.ID, p.Service)
		}
	}
}
