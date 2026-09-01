package api

import (
	"net/http"
	"testing"
)

// TestAbfrageBleibtImMandanten ist die Kernzusage des SQL-Browsers.
//
// Der Aufrufer nennt eine Datenbank-ID, nie einen Datenbanknamen. Der Name wird
// im Zugriffsbereich des Mandanten nachgeschlagen. Käme er aus der Anfrage,
// wäre "führe diese Abfrage aus" der direkte Weg in die Datenbank eines anderen
// Kunden — und die ID-Prüfung wäre reine Zierde.
func TestAbfrageBleibtImMandanten(t *testing.T) {
	ts := newTestServer(t)
	seedFremdeDatenbank(t, ts)
	ts.login(t, "bob@example.at")

	rec := ts.do(http.MethodPost, "/api/v1/databases/1/query",
		map[string]any{"statement": "SELECT * FROM kunden"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("Abfrage gegen eine fremde Datenbank: Status %d, erwartet 404 — %s",
			rec.Code, rec.Body.String())
	}
}

// TestAbfrageNimmtKeinenDatenbanknamenEntgegen: selbst wenn jemand einen
// mitschickt, ändert er nichts. Das Feld existiert in der Anfrage nicht.
//
// Der Test prüft das über die Wirkung: mit der ID einer eigenen Datenbank
// kommt die Anfrage bis zum Agent (der hier nicht läuft, also 503). Wäre der
// mitgeschickte Name maßgeblich, käme stattdessen ein anderer Fehler.
func TestAbfrageNimmtKeinenDatenbanknamenEntgegen(t *testing.T) {
	ts := newTestServer(t)
	seedFremdeDatenbank(t, ts)
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodPost, "/api/v1/databases/1/query", map[string]any{
		"statement": "SELECT 1",
		"database":  "mysql",
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("eigene Datenbank: Status %d, erwartet 503 (Agent läuft im Test nicht) — %s",
			rec.Code, rec.Body.String())
	}
}

// TestAbfrageBrauchtEineSitzung: ohne Anmeldung gar nichts.
func TestAbfrageBrauchtEineSitzung(t *testing.T) {
	ts := newTestServer(t)

	rec := ts.do(http.MethodPost, "/api/v1/databases/1/query",
		map[string]any{"statement": "SELECT 1"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Abfrage ohne Sitzung: Status %d, erwartet 401", rec.Code)
	}
}
