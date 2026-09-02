package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/templates"
)

func appCall(t *testing.T, srv *Server, op Op, params any) error {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	switch op {
	case OpAppWrite:
		_, err = srv.opAppWrite(t.Context(), raw)
	case OpAppRemove:
		_, err = srv.opAppRemove(t.Context(), raw)
	case OpAppStatus:
		_, err = srv.opAppStatus(t.Context(), raw)
	}
	return err
}

// TestAppNameBleibtUnterUnseremPraefix ist die Trennung zur Dienst-Whitelist.
//
// Der Unit-Name entsteht aus dem App-Namen mit festem Präfix. Käme er aus der
// Anfrage, wäre "meine App neu starten" ein Weg an der Whitelist vorbei zu
// jedem Dienst des Servers — `systemctl stop ssh` über einen Endpunkt, den ein
// Kunde bedienen darf.
func TestAppNameBleibtUnterUnseremPraefix(t *testing.T) {
	srv, _ := testServer(t)

	// Eine App darf "ssh" heißen. Sie wird dann volt-app-ssh, und genau
	// deshalb ist der Name harmlos: der Agent fasst nie den Dienst an, der so
	// heißt. Geprüft wird also nicht, ob der Name abgelehnt wird, sondern
	// welche Unit dabei herauskommt.
	for _, name := range []string{"ssh", "nginx", "volt-web", "cron"} {
		raw, err := json.Marshal(AppNameParams{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		out, err := srv.opAppStatus(t.Context(), raw)
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		res, ok := out.(AppResult)
		if !ok {
			t.Fatalf("unerwartete Antwort: %T", out)
		}
		if res.Unit != "volt-app-"+name {
			t.Errorf("die App %q spricht die Unit %q an", name, res.Unit)
		}
		if res.UnitPath != "/etc/systemd/system/volt-app-"+name+".service" {
			t.Errorf("die App %q schreibt nach %q", name, res.UnitPath)
		}
	}

	// Was gar kein Name sein kann, wird abgewiesen — bevor daraus ein Pfad
	// wird.
	for _, name := range []string{
		"../../ssh", "shop.service", "shop app", "SHOP", "", "-shop", "shop/../x",
		"volt-app-shop", // das Präfix setzt der Agent, nicht der Aufrufer
	} {
		if err := appCall(t, srv, OpAppStatus, AppNameParams{Name: name}); err == nil {
			t.Errorf("der App-Name %q wurde angenommen", name)
		}
	}
}

// TestNurUnsereUnitsSindDienste: die Dienst-Whitelist muss App-Units kennen —
// sonst kann der Agent die Unit, die er selbst geschrieben hat, nicht starten.
// Und sie darf nichts darüber hinaus dazubekommen.
func TestNurUnsereUnitsSindDienste(t *testing.T) {
	if err := checkService(templates.UnitName("shop")); err != nil {
		t.Errorf("die eigene App-Unit gilt nicht als Dienst: %v", err)
	}
	for _, fremd := range []string{
		"volt-app-", "volt-app-x", "volt-app-.service", "volt-app-../ssh",
		"volt-appssh", "volt-app-SHOP",
	} {
		if err := checkService(fremd); err == nil {
			t.Errorf("%q ging durch die Whitelist", fremd)
		}
	}
}

// TestAppLaeuftNieAlsSystemkonto: eine App mit der UID 0 wäre ein Rootprozess,
// den ein Kunde selbst gestartet hat — mit einem Kommando, das er bestimmt.
func TestAppLaeuftNieAlsSystemkonto(t *testing.T) {
	srv, _ := testServer(t)

	for _, user := range []string{"root", "www-data", "mysql", "nobody", "daemon", "volt"} {
		err := appCall(t, srv, OpAppWrite, AppParams{
			Name: "shop", SystemUser: user,
			WorkingDir: srv.roots[0] + "/shop.example.at",
			Runtime:    "node", Args: []string{"server.js"},
		})
		if err == nil {
			t.Errorf("eine App unter %q wurde angenommen", user)
			continue
		}
		if strings.Contains(err.Error(), "kein systembenutzer einer site") ||
			strings.Contains(err.Error(), "reservierter systembenutzer") ||
			strings.Contains(err.Error(), "gibt es nicht") {
			continue
		}
		t.Errorf("%q abgelehnt, aber aus dem falschen Grund: %v", user, err)
	}
}

// TestAppVerzeichnisBleibtInDenWurzeln: das Arbeitsverzeichnis wird
// ReadWritePaths der Unit. Ein Pfad außerhalb der Wurzeln machte genau das
// Verzeichnis schreibbar, das die Härtung schützen soll.
func TestAppVerzeichnisBleibtInDenWurzeln(t *testing.T) {
	srv, _ := testServer(t)

	for _, dir := range []string{"/etc", "/", "/etc/systemd/system", srv.roots[0] + "/../../etc"} {
		err := appCall(t, srv, OpAppWrite, AppParams{
			Name: "shop", SystemUser: "site_shop", WorkingDir: dir,
			Runtime: "node", Args: []string{"server.js"},
		})
		if err == nil {
			t.Errorf("das Arbeitsverzeichnis %q wurde angenommen", dir)
			continue
		}
		// Der Grund muss die Wurzelprüfung sein, nicht irgendein späterer
		// Fehler. Auf einem Rechner ohne die Systembenutzer scheitert der
		// Aufruf ohnehin — ein Test auf "irgendein Fehler" wäre grün, auch
		// wenn jail() gar nicht mehr gerufen würde.
		if !strings.Contains(err.Error(), "außerhalb der erlaubten verzeichnisse") {
			t.Errorf("%q abgelehnt, aber aus dem falschen Grund: %v", dir, err)
		}
	}
}

// TestNurBekannteLaufzeitumgebungen: der Web-Prozess nennt die Umgebung, nicht
// ihren Pfad. Stünde dort ein Pfad, könnte ein übernommenes Panel jedes
// Programm des Servers als ExecStart eintragen — als Benutzer der Site zwar,
// aber "nie sh -c" ist eine Regel dieses Projekts, und eine Regel, die für
// jeden Aufruf gilt außer diesem einen, gilt nicht.
func TestNurBekannteLaufzeitumgebungen(t *testing.T) {
	srv, _ := testServer(t)
	for _, name := range []string{
		"", "sh", "bash", "/bin/sh", "python3", "NODE", "node ",
		// Auch die Node-Fassungen sind Namen, keine Pfade.
		"node22/../../../bin/sh", "node-22", "nodeXX", "node0",
		"/opt/volt/node/node22/bin/node",
	} {
		if path, err := srv.runtimePath(name); err == nil {
			t.Errorf("die Laufzeitumgebung %q wurde angenommen: %s", name, path)
		}
	}

	// Und jeder Schlüssel, den appRuntimes nennt, muss in der Binary-Whitelist
	// stehen — sonst wäre die Umgebung geschriebener, aber unerreichbarer Code:
	// run() lehnte den Aufruf ab, und auffallen könnte das erst auf einem
	// Server, auf dem node wirklich liegt.
	for runtime, keys := range appRuntimes {
		for _, key := range keys {
			if allowedBinaries[key] == "" {
				t.Errorf("%s verweist auf %q, das nicht in allowedBinaries steht", runtime, key)
			}
		}
	}
}

// TestPfadeKommenAusDemNamen: käme der Pfad der Unit aus der Anfrage, wäre
// "eine App schreiben" ein Weg, jede Datei des Servers durch eine systemd-Unit
// zu ersetzen — und die nächste davon läuft als root.
func TestPfadeKommenAusDemNamen(t *testing.T) {
	srv, _ := testServer(t)

	unit := srv.appUnitPath("shop")
	if unit != "/etc/systemd/system/volt-app-shop.service" {
		t.Errorf("Unit-Pfad ist %q", unit)
	}
	env := srv.appEnvPath("shop")
	if !strings.HasSuffix(env, "/shop.env") || !strings.HasPrefix(env, srv.appDir) {
		t.Errorf("Umgebungspfad ist %q, erwartet unter %q", env, srv.appDir)
	}

	// In AppParams gibt es kein Feld für einen der beiden Pfade. Das ist keine
	// Kleinigkeit, sondern der Grund, warum die beiden Zeilen oben halten.
	raw, _ := json.Marshal(map[string]any{
		"name": "shop", "unit_path": "/etc/systemd/system/ssh.service",
		"env_path": "/etc/shadow", "system_user": "site_shop",
	})
	var p AppParams
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "shop" {
		t.Fatalf("der Name kam nicht an: %+v", p)
	}
}
