package api

import (
	"net/http"
	"testing"
)

// TestFTPZugangBleibtImMandanten: ein Zugang entsteht immer an einer Site, und
// die Site wird im Zugriffsbereich des Aufrufers aufgelöst. Eine fremde Site
// ist deshalb nicht von einer nicht vorhandenen zu unterscheiden.
func TestFTPZugangBleibtImMandanten(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at")

	// Site 1 gehört Alice.
	rec := ts.do(http.MethodPost, "/api/v1/ftp", map[string]any{
		"site_id": 1, "username": "uebernahme",
	})
	if rec.Code != http.StatusNotFound {
		t.Errorf("FTP-Zugang auf fremder Site: Status %d, erwartet 404 — %s",
			rec.Code, rec.Body.String())
	}

	for _, path := range []string{"/api/v1/ftp/1", "/api/v1/ftp/1/reveal"} {
		method := http.MethodPost
		if path == "/api/v1/ftp/1" {
			method = http.MethodDelete
		}
		rec := ts.do(method, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s als fremder Mandant: Status %d, erwartet 404", method, path, rec.Code)
		}
	}
}

// TestFTPEinrichtenNurFuerAdministratoren: das Einrichten holt ein Paket auf
// den Server und öffnet Ports in der Firewall. Das ist keine Kundenaktion.
func TestFTPEinrichtenNurFuerAdministratoren(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at")

	for _, r := range []struct {
		method, path string
	}{
		{http.MethodPost, "/api/v1/ftp/setup"},
		{http.MethodGet, "/api/v1/ftp/orphans"},
	} {
		rec := ts.do(r.method, r.path, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s als Kunde: Status %d, erwartet 403 — %s",
				r.method, r.path, rec.Code, rec.Body.String())
		}
	}
}

// TestFTPListeBrauchtEineSitzung: die Liste nennt Benutzernamen und Pfade.
func TestFTPListeBrauchtEineSitzung(t *testing.T) {
	ts := newTestServer(t)

	rec := ts.do(http.MethodGet, "/api/v1/ftp", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("FTP-Liste ohne Sitzung: Status %d, erwartet 401", rec.Code)
	}
}
