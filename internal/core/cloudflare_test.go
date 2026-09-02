package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Ein vorhandener SPF-Eintrag bleibt stehen.
//
// Er zählt womöglich einen Newsletter-Versand oder ein CRM auf. Ihn durch
// "v=spf1 mx -all" zu ersetzen sperrte die alle aus — und zwar lautlos, bis
// sich jemand wundert, dass seine Rechnungen nicht mehr ankommen.
func TestSPFBleibtStehen(t *testing.T) {
	var geschrieben []string
	srv := cloudflareAttrappe(t, map[string][]cfRecord{
		"example.at": {{ID: "r1", Type: "TXT", Name: "example.at",
			Content: "v=spf1 include:sendgrid.net -all"}},
	}, &geschrieben)
	defer srv.Close()

	cf := newCloudflareClient("token")
	cf.http = srv.Client()
	alt := cloudflareBasis(t, srv.URL)
	defer alt()

	zone, err := cf.zoneID(context.Background(), "example.at")
	if err != nil {
		t.Fatal(err)
	}
	records, err := cf.txtRecords(context.Background(), zone, "example.at")
	if err != nil {
		t.Fatal(err)
	}
	if vorhandenesSPF(records) == "" {
		t.Fatal("der vorhandene spf-eintrag wurde nicht gefunden")
	}
	if len(geschrieben) != 0 {
		t.Errorf("es wurde geschrieben, obwohl nur gelesen werden sollte: %v", geschrieben)
	}
}

// Ein DKIM-Eintrag, der schon richtig dasteht, wird nicht noch einmal
// geschrieben. Jede Änderung ist eine neue Verbreitung im DNS.
func TestSetzeTXTSchreibtNichtUmsonst(t *testing.T) {
	var geschrieben []string
	srv := cloudflareAttrappe(t, map[string][]cfRecord{
		"volt._domainkey.example.at": {{ID: "r2", Type: "TXT",
			Name: "volt._domainkey.example.at", Content: "v=DKIM1; p=abc"}},
	}, &geschrieben)
	defer srv.Close()

	cf := newCloudflareClient("token")
	cf.http = srv.Client()
	alt := cloudflareBasis(t, srv.URL)
	defer alt()

	ctx := context.Background()
	if err := cf.setzeTXT(ctx, "zone1", "volt._domainkey.example.at", "v=DKIM1; p=abc"); err != nil {
		t.Fatal(err)
	}
	if len(geschrieben) != 0 {
		t.Errorf("derselbe wert wurde noch einmal geschrieben: %v", geschrieben)
	}

	// Ein anderer Wert ersetzt den alten — und zwar über PUT auf die
	// vorhandene Kennung, nicht als zweiter Eintrag daneben.
	if err := cf.setzeTXT(ctx, "zone1", "volt._domainkey.example.at", "v=DKIM1; p=xyz"); err != nil {
		t.Fatal(err)
	}
	if len(geschrieben) != 1 || !strings.HasPrefix(geschrieben[0], "PUT ") {
		t.Errorf("erwartet ein PUT, bekommen: %v", geschrieben)
	}
	if !strings.Contains(geschrieben[0], "/r2") {
		t.Errorf("es wurde nicht der vorhandene eintrag geändert: %v", geschrieben)
	}
}

// Die Zone einer Subdomäne ist die übergeordnete. Wer nur nach dem vollen
// Namen fragt, findet nichts und meldet "keine Zone" — obwohl es eine gibt.
func TestZoneIDFindetDieUebergeordneteZone(t *testing.T) {
	var geschrieben []string
	srv := cloudflareAttrappe(t, nil, &geschrieben)
	defer srv.Close()

	cf := newCloudflareClient("token")
	cf.http = srv.Client()
	alt := cloudflareBasis(t, srv.URL)
	defer alt()

	zone, err := cf.zoneID(context.Background(), "mail.sub.example.at")
	if err != nil {
		t.Fatal(err)
	}
	if zone != "zone-example-at" {
		t.Errorf("zone = %q", zone)
	}
}

// Die Meldung von Cloudflare wird weitergereicht. "invalid API token" ist eine
// Auskunft, "Fehler 400" ist keine.
func TestCloudflareFehlerWirdWeitergereicht(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  []map[string]any{{"code": 6003, "message": "Invalid request headers"}},
		})
	}))
	defer srv.Close()

	cf := newCloudflareClient("falsch")
	cf.http = srv.Client()
	alt := cloudflareBasis(t, srv.URL)
	defer alt()

	_, err := cf.zoneID(context.Background(), "example.at")
	if err == nil {
		t.Fatal("ein abgelehnter token wurde angenommen")
	}
	if !strings.Contains(err.Error(), "Invalid request headers") {
		t.Errorf("die meldung von cloudflare fehlt: %v", err)
	}
}

// cloudflareAttrappe ist ein Server, der sich wie die API verhält — soweit
// dieses Paket sie benutzt.
func cloudflareAttrappe(t *testing.T, txt map[string][]cfRecord, geschrieben *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		antwort := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
		}

		// Die Reihenfolge zählt: /zones/<id>/dns_records fängt auch mit
		// "/zones" an und trägt ebenfalls ein name=. Wer zuerst auf das
		// Präfix prüft, beantwortet jede Abfrage als Zonensuche — genau
		// darauf bin ich hier hereingefallen.
		switch {
		case strings.Contains(r.URL.Path, "/dns_records"):
			if r.Method != http.MethodGet {
				*geschrieben = append(*geschrieben, r.Method+" "+r.URL.Path)
				antwort(map[string]string{"id": "neu"})
				return
			}
			antwort(txt[r.URL.Query().Get("name")])

		case strings.HasPrefix(r.URL.Path, "/zones"):
			name := r.URL.Query().Get("name")
			if name == "example.at" {
				antwort([]map[string]string{{"id": "zone-example-at", "name": name}})
				return
			}
			antwort([]map[string]string{})

		default:
			t.Errorf("unerwarteter aufruf: %s %s", r.Method, r.URL)
			antwort(map[string]string{})
		}
	}))
}

// cloudflareBasis biegt die Adresse auf die Attrappe um und gibt zurück, was
// sie wieder zurücksetzt.
func cloudflareBasis(t *testing.T, url string) func() {
	t.Helper()
	alt := cloudflareAPIBase
	cloudflareAPIBase = url
	return func() { cloudflareAPIBase = alt }
}
