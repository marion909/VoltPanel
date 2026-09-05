package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// TestTOTPCodeIstNachDemErstenGebrauchVerbraucht deckt die Replay-Lücke ab:
// wegen Skew=1 blieb derselbe gültige 2FA-Code bisher bis zu ~90 Sekunden
// lang beliebig oft gültig — hier verbraucht durch 2fa/enable und danach noch
// einmal gegen den Login versucht.
func TestTOTPCodeIstNachDemErstenGebrauchVerbraucht(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("2fa/setup: Status %d — %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}

	code, err := totp.GenerateCode(setup.Secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	rec = ts.do(http.MethodPost, "/api/v1/auth/2fa/enable", map[string]string{"code": code})
	if rec.Code != http.StatusOK {
		t.Fatalf("2fa/enable mit frischem Code: Status %d — %s", rec.Code, rec.Body.String())
	}

	// Derselbe Code, den enable gerade verbraucht hat, darf für den nächsten
	// Login-Versuch nicht noch einmal gelten — vorher wäre das durchgegangen.
	ts.session, ts.csrf = "", ""
	rec = ts.do(http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "alice@example.at", "password": testPassword, "totp_code": code,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Login mit bereits verbrauchtem 2FA-Code: Status %d, erwartet 401 (Replay möglich)", rec.Code)
	}

	// Ein frischer, noch nie gesehener Code muss weiterhin funktionieren —
	// die Sperre betrifft nur bereits verbrauchte Schritte, nicht 2FA insgesamt.
	// +30s (statt z. B. +31s) verschiebt den Zeitschritt unabhängig von der
	// Phase innerhalb des aktuellen Fensters immer um genau +1 — und bleibt
	// damit innerhalb des erlaubten Skew von 1.
	next, err := totp.GenerateCode(setup.Secret, time.Now().UTC().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	rec = ts.do(http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "alice@example.at", "password": testPassword, "totp_code": next,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Login mit frischem 2FA-Code: Status %d — %s", rec.Code, rec.Body.String())
	}
}

// TestTOTPEnableWirdGeratelimited deckt den Fund ab, dass der 6-stellige Code
// bei 2fa/enable ohne jedes Rate-Limiting geprüft wurde — bei einer gekaperten
// Session ließen sich die 1.000.000 Kombinationen in kurzer Zeit durchprobieren.
func TestTOTPEnableWirdGeratelimited(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("2fa/setup: Status %d — %s", rec.Code, rec.Body.String())
	}

	// newRateLimiter(5, time.Minute) in server.go: die ersten fünf falschen
	// Codes scheitern normal an der Code-Prüfung, der sechste muss schon am
	// Ratelimit scheitern, bevor überhaupt ein Code geprüft wird.
	for i := 0; i < 5; i++ {
		rec = ts.do(http.MethodPost, "/api/v1/auth/2fa/enable", map[string]string{"code": "000000"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("versuch %d mit falschem code: Status %d, erwartet 400", i+1, rec.Code)
		}
	}
	rec = ts.do(http.MethodPost, "/api/v1/auth/2fa/enable", map[string]string{"code": "000000"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("sechster versuch direkt danach: Status %d, erwartet 429", rec.Code)
	}
}

// TestTOTPDisableWirdGeratelimited ist dasselbe Muster für 2fa/disable —
// der Endpunkt, dessen eigener Code-Kommentar das Bedrohungsmodell benennt
// ("sonst könnte eine übernommene Sitzung den zweiten Faktor einfach
// abschalten"), aber bisher ohne Ratenbegrenzung auskam.
func TestTOTPDisableWirdGeratelimited(t *testing.T) {
	ts := newTestServer(t)
	ts.login(t, "alice@example.at")

	rec := ts.do(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("2fa/setup: Status %d — %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(setup.Secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	rec = ts.do(http.MethodPost, "/api/v1/auth/2fa/enable", map[string]string{"code": code})
	if rec.Code != http.StatusOK {
		t.Fatalf("2fa/enable mit frischem Code: Status %d — %s", rec.Code, rec.Body.String())
	}

	for i := 0; i < 5; i++ {
		rec = ts.do(http.MethodPost, "/api/v1/auth/2fa/disable", map[string]string{"code": "000000"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("versuch %d mit falschem code: Status %d, erwartet 400", i+1, rec.Code)
		}
	}
	rec = ts.do(http.MethodPost, "/api/v1/auth/2fa/disable", map[string]string{"code": "000000"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("sechster versuch direkt danach: Status %d, erwartet 429", rec.Code)
	}
}
