package agent

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// notARoot benennt Verzeichnis-Optionen, die bewusst keine Datei-Wurzel sind.
// Ein Eintrag hier heißt: der Agent soll in diesem Verzeichnis keine Datei
// anfassen dürfen.
var notARoot = map[string]bool{
	// AppDir hält die Umgebungsdateien der Apps. Dorthin kommt nie ein Pfad
	// aus einer Anfrage: der Web-Prozess nennt den Namen einer App, den Pfad
	// bildet der Agent daraus. Stünde das Verzeichnis in roots, wäre es über
	// jeden datei-basierten Endpunkt erreichbar — und in den Dateien darin
	// stehen die Passwörter der Apps.
	"AppDir": true,
}

// TestJedesVerzeichnisIstEineWurzel hält die Wurzelliste an den Optionen fest.
//
// Der Anlass: mysql.dump und mysql.import gab es von Anfang an, das
// Sicherungsverzeichnis stand aber nicht in roots. Beide Operationen waren
// damit geschriebener, aber nicht erreichbarer Code — jail() ließ den Pfad nie
// durch, und auffallen konnte das erst auf einem echten Server.
func TestJedesVerzeichnisIstEineWurzel(t *testing.T) {
	opts := ServerOptions{SocketPath: filepath.Join(t.TempDir(), "agent.sock")}

	// Jede Verzeichnis-Option bekommt einen eigenen Wert. Was danach nicht in
	// roots steht, ist nicht freigegeben.
	v := reflect.ValueOf(&opts).Elem()
	marker := map[string]string{}
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if !strings.HasSuffix(name, "Dir") || notARoot[name] {
			continue
		}
		marker[name] = "/volt-test/" + strings.ToLower(name)
		v.Field(i).SetString(marker[name])
	}
	if len(marker) == 0 {
		t.Fatal("keine Verzeichnis-Optionen gefunden — prüft der Test noch, was er soll?")
	}

	srv, err := NewServer(opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	for name, path := range marker {
		if !slices.Contains(srv.roots, path) {
			t.Errorf("%s fehlt in roots — der Agent kann dort keine Datei anfassen", name)
		}
	}
}
