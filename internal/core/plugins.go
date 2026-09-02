package core

import (
	"context"
	"fmt"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

// Plugins: Fähigkeiten des Servers, nicht eines Mandanten.
//
// Phase 7 der Roadmap nennt zwei Dinge in einem Namen: "App Store und
// Plugin-System". Beides ist gebaut, aber als zwei verschiedene Mechanismen,
// weil sie zwei verschiedene Dinge sind.
//
//   - Ein Plugin (diese Datei) erweitert den Server selbst — ein zusätzlicher
//     Dienst, den das Panel danach mitverwaltet. Es hat einen Ein/Aus-Zustand
//     und lebt server-weit, wie Docker oder die Firewall.
//   - Ein App-Store-Eintrag (internal/core/appstore.go) erzeugt eine ganz
//     gewöhnliche Site mit ganz gewöhnlicher Datenbank — danach unterscheidet
//     sich nichts mehr von einer Site, die jemand von Hand angelegt hat, und
//     es gibt keine fortlaufende Buchführung darüber.
//
// Was hier bewusst *nicht* gebaut ist: ein offenes Repository, aus dem
// irgendjemand ein signiertes Paket hochlädt und der Agent es als root
// ausführt. Der Katalog unten ist der ganze Bestand — jeder Eintrag im
// Quelltext dieses Programms, geprüft wie jede andere Zeile hier. Ein
// Fremd-Plugin mit eigenem Installationsskript wäre die Art Entscheidung, die
// sich nicht zurücknehmen lässt, wenn sie einmal zu großzügig war: "signiert"
// beweist nur, wer es geschrieben hat, nicht, dass es harmlos ist.
//
// Installiert wird deshalb nie mit einem mitgelieferten Skript, sondern mit
// genau denselben Bausteinen, die der Agent für alles andere auch benutzt —
// eine feste Paketliste (internal/agent/ops_feature.go) und die
// Dienst-Whitelist. Ein Plugin, das mehr braucht als "ein apt-Paket, ein
// Dienst", gehört nicht in diesen Katalog, sondern in den Kern.

// PluginDef ist ein Eintrag im Katalog.
type PluginDef struct {
	// ID ist zugleich der Name in der featurePakete-Liste des Agents — eine
	// zweite Übersetzungstabelle wäre eine zweite Stelle, an der beide
	// auseinanderlaufen können.
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Service ist der systemd-Dienst, den dieses Plugin mitbringt — leer,
	// wenn es keinen eigenen hat. Er muss in der Dienst-Whitelist des Agents
	// stehen; Catalog() prüft das beim Programmstart.
	Service string `json:"service"`
}

// Catalog ist der ganze Bestand.
func Catalog() []PluginDef {
	return []PluginDef{
		{
			ID:   "redis",
			Name: "Redis",
			Description: "Schlüssel-Wert-Speicher für Sitzungen, Caches und Warteschlangen. " +
				"Bindet nur an 127.0.0.1 — erreichbar für Apps auf diesem Server, nicht von außen.",
			Service: "redis-server",
		},
	}
}

// PluginView ist ein Katalogeintrag mit dem Zustand auf diesem Server.
type PluginView struct {
	PluginDef
	// Installed sagt, ob das Panel diesen Eintrag je installiert hat — nicht,
	// ob der Dienst gerade läuft. Das steht in Active.
	Installed bool `json:"installed"`
	Enabled   bool `json:"enabled"`
	Active    bool `json:"active"`
}

type PluginService struct {
	store *store.Store
	agent *agent.Client
}

func NewPluginService(st *store.Store, ag *agent.Client) *PluginService {
	return &PluginService{store: st, agent: ag}
}

func findPlugin(id string) (PluginDef, bool) {
	for _, p := range Catalog() {
		if p.ID == id {
			return p, true
		}
	}
	return PluginDef{}, false
}

// List stellt den Katalog dem tatsächlichen Zustand gegenüber.
//
// Der Dienststatus kommt vom Agent, nicht aus der eigenen Zeile: die Zeile
// sagt nur, was das Panel zuletzt veranlasst hat. Ob der Dienst wirklich
// läuft, weiß allein systemd — dieselbe Vorsicht wie bei jeder anderen
// Statusauskunft in diesem Panel.
func (s *PluginService) List(ctx context.Context) ([]PluginView, error) {
	rows, err := s.store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	zustand := make(map[string]*store.Plugin, len(rows))
	for _, r := range rows {
		zustand[r.ID] = r
	}

	catalog := Catalog()
	out := make([]PluginView, 0, len(catalog))
	for _, def := range catalog {
		v := PluginView{PluginDef: def}
		if row, ok := zustand[def.ID]; ok {
			v.Installed = true
			v.Enabled = row.Enabled
		}
		if v.Installed && def.Service != "" {
			// Ein Agent, der gerade nicht antwortet, macht aus "läuft" kein
			// "läuft nicht" in der Datenbank — nur die Anzeige bleibt für
			// diesen Aufruf ehrlicher: "aktiv" ist dann unbekannt, nicht falsch
			// behauptet.
			if st, err := s.agent.ServiceStatus(ctx, def.Service); err == nil {
				v.Active = st.Active
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Install holt das Plugin nach und schaltet seinen Dienst ein.
func (s *PluginService) Install(ctx context.Context, id string) (string, error) {
	def, ok := findPlugin(id)
	if !ok {
		return "", fmt.Errorf("%q ist kein bekanntes plugin", id)
	}

	out, err := s.agent.InstallFeature(ctx, def.ID)
	if err != nil {
		return "", err
	}
	if err := s.store.SetPlugin(ctx, def.ID, true, "{}"); err != nil {
		return out, err
	}
	return out, nil
}

// Uninstall entfernt das Paket wieder und die Zeile dazu.
//
// Die Zeile geht auch dann weg, wenn das Entfernen des Pakets scheitert
// (etwa weil ein anderer Dienst davon abhängt) — sonst zeigte die Oberfläche
// "installiert", obwohl apt bereits mitten in der Deinstallation war und der
// Dienst nicht mehr sauber läuft. Der Fehler wird trotzdem gemeldet, nicht
// verschluckt.
func (s *PluginService) Uninstall(ctx context.Context, id string) (string, error) {
	def, ok := findPlugin(id)
	if !ok {
		return "", fmt.Errorf("%q ist kein bekanntes plugin", id)
	}

	out, err := s.agent.UninstallFeature(ctx, def.ID)
	delErr := s.store.DeletePlugin(ctx, def.ID)
	if err != nil {
		return out, err
	}
	return out, delErr
}

// SetEnabled schaltet den Dienst eines installierten Plugins ein oder aus.
func (s *PluginService) SetEnabled(ctx context.Context, id string, enabled bool) error {
	def, ok := findPlugin(id)
	if !ok {
		return fmt.Errorf("%q ist kein bekanntes plugin", id)
	}
	if def.Service == "" {
		return fmt.Errorf("%s hat keinen eigenen dienst zum ein- oder ausschalten", def.Name)
	}

	action := "disable"
	if enabled {
		action = "enable"
	}
	if _, err := s.agent.ServiceAction(ctx, action, def.Service); err != nil {
		return err
	}
	// Ein Neustart bzw. Stopp gehört dazu — "enabled" heißt für den Dienst
	// selbst nur "beim nächsten Systemstart", nicht "jetzt". Wer den Knopf im
	// Panel drückt, meint aber "jetzt".
	laufAction := "stop"
	if enabled {
		laufAction = "start"
	}
	if _, err := s.agent.ServiceAction(ctx, laufAction, def.Service); err != nil {
		return err
	}

	row, err := s.store.GetPlugin(ctx, def.ID)
	if err != nil {
		return err
	}
	return s.store.SetPlugin(ctx, def.ID, enabled, row.Config)
}
