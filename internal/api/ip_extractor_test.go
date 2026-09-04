package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLoginRatelimitIgnoriertXForwardedForOhneTrustProxy hält fest, dass ein
// Angreifer ohne explizites trust_proxy: true (der Standard) das Login-
// Ratelimit nicht über einen selbst gesetzten X-Forwarded-For-Header umgehen
// kann. Vorher hatte Echo keinen IPExtractor gesetzt und fiel auf ein
// Verhalten zurück, das dem Header unabhängig davon vertraute, ob überhaupt
// ein Reverse-Proxy davorstand — c.RealIP() und damit sowohl die IP-
// Whitelist als auch dieses Ratelimit hätten sich beliebig oft neu
// ausschöpfen lassen, indem jeder Versuch einen neuen Header-Wert schickt.
func TestLoginRatelimitIgnoriertXForwardedForOhneTrustProxy(t *testing.T) {
	ts := newTestServer(t)

	attempt := func(forwardedFor string) int {
		body, _ := json.Marshal(map[string]string{"email": "alice@example.at", "password": "falsch"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		// httptest.NewRequest setzt für jede Anfrage dieselbe RemoteAddr
		// ("192.0.2.1:1234") — genau das simuliert hier denselben tatsächlichen
		// Absender hinter wechselnden, selbst behaupteten Herkünften.
		req.Header.Set("X-Forwarded-For", forwardedFor)
		rec := httptest.NewRecorder()
		ts.server.echo.ServeHTTP(rec, req)
		return rec.Code
	}

	// newRateLimiter(5, time.Minute) in server.go: fünf Fehlversuche sind das
	// Limit. Jeder Versuch behauptet über X-Forwarded-For eine andere
	// Herkunft — würde Echo dem Header vertrauen, sähe jeder Versuch wie eine
	// neue IP aus und das Ratelimit griffe nie.
	for i := 0; i < 5; i++ {
		if code := attempt(fmt.Sprintf("203.0.113.%d", i)); code != http.StatusUnauthorized {
			t.Fatalf("Versuch %d mit X-Forwarded-For: Status %d, erwartet 401", i, code)
		}
	}
	if code := attempt("203.0.113.99"); code != http.StatusTooManyRequests {
		t.Fatalf("sechster Versuch mit gefälschtem X-Forwarded-For: Status %d, erwartet 429 (Ratelimit umgangen)", code)
	}
}
