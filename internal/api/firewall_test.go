package api

import (
	"net/http"
	"testing"
)

// TestFirewallNurFuerAdmins: eine Regel in der Firewall betrifft den ganzen
// Server, nicht eine Site. Und die Liste der gesperrten Adressen sagt einem
// Kunden nichts über seinen Mandanten, aber allerlei über die anderen — wer
// gerade ausgesperrt ist, aus welchem Netz Anmeldeversuche kommen, wie viele.
func TestFirewallNurFuerAdmins(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "bob@example.at") // Kunde

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/system/firewall", nil},
		{http.MethodPost, "/api/v1/system/firewall",
			map[string]any{"action": "allow", "port": 22, "proto": "tcp"}},
		{http.MethodGet, "/api/v1/system/fail2ban", nil},
		{http.MethodPost, "/api/v1/system/fail2ban/unban",
			map[string]any{"jail": "sshd", "ip": "1.2.3.4"}},
	}
	for _, tc := range cases {
		rec := ts.do(tc.method, tc.path, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s als Kunde: Status %d, erwartet 403 — %s",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// Ohne Anmeldung erst recht nicht.
	ts.session, ts.csrf = "", ""
	for _, tc := range cases {
		rec := ts.do(tc.method, tc.path, tc.body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s ohne Anmeldung: Status %d, erwartet 401", tc.method, tc.path, rec.Code)
		}
	}
}
