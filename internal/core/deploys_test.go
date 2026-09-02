package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sig(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookOhneSignaturWirdAbgelehnt: der Endpunkt liegt außerhalb der
// Sitzungsprüfung und außerhalb des Zugriffspfads. Seine Adresse steht in den
// Einstellungen eines fremden Dienstes und in jedem Proxy-Log dazwischen — die
// Adresse allein kann deshalb nicht der Ausweis sein.
func TestWebhookOhneSignaturWirdAbgelehnt(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)

	if VerifyHookSignature("geheim", body, map[string]string{}) {
		t.Error("ohne jede Kopfzeile angenommen")
	}
	if VerifyHookSignature("geheim", body, map[string]string{"X-GitHub-Event": "push"}) {
		t.Error("mit einer beliebigen Kopfzeile angenommen")
	}
}

// TestWebhookSignaturMussStimmen prüft die drei Formen, auf die sich die
// üblichen Hoster nicht einigen konnten.
func TestWebhookSignaturMussStimmen(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	gut := sig("geheim", body)

	richtig := []map[string]string{
		{"X-Hub-Signature-256": "sha256=" + gut},
		{"X-Gitea-Signature": gut},
		{"X-Gitlab-Token": "geheim"},
	}
	for i, h := range richtig {
		if !VerifyHookSignature("geheim", body, h) {
			t.Errorf("Form %d wurde abgelehnt: %v", i, h)
		}
	}

	falsch := []map[string]string{
		{"X-Hub-Signature-256": "sha256=" + sig("anderes", body)},
		{"X-Hub-Signature-256": gut},                          // ohne Präfix
		{"X-Hub-Signature-256": "sha256=" + gut[:len(gut)-2]}, // gekürzt
		{"X-Hub-Signature-256": "sha256=keinhex"},
		{"X-Gitea-Signature": sig("anderes", body)},
		{"X-Gitlab-Token": "falsch"},
		{"X-Gitlab-Token": "geheim2"},
		{"X-Gitlab-Token": "gehei"},
	}
	for i, h := range falsch {
		if VerifyHookSignature("geheim", body, h) {
			t.Errorf("falsche Signatur %d wurde angenommen: %v", i, h)
		}
	}
}

// TestSignaturDecktDenRumpf: die Signatur muss über den tatsächlich
// empfangenen Rumpf gehen. Sonst reichte eine einmal mitgelesene Signatur, um
// beliebige Inhalte nachzuschieben — etwa einen anderen Branch.
func TestSignaturDecktDenRumpf(t *testing.T) {
	original := []byte(`{"ref":"refs/heads/main"}`)
	gefaelscht := []byte(`{"ref":"refs/heads/angreifer"}`)
	h := map[string]string{"X-Hub-Signature-256": "sha256=" + sig("geheim", original)}

	if !VerifyHookSignature("geheim", original, h) {
		t.Fatal("der echte Rumpf wurde abgelehnt")
	}
	if VerifyHookSignature("geheim", gefaelscht, h) {
		t.Error("ein anderer Rumpf mit derselben Signatur wurde angenommen")
	}
}

// TestBranchAusDemRumpf: ohne diese Prüfung überschriebe jeder Push auf einen
// Feature-Branch die Produktion.
func TestBranchAusDemRumpf(t *testing.T) {
	cases := map[string]string{
		`{"ref":"refs/heads/main"}`:            "main",
		`{"ref":"refs/heads/release/2026-09"}`: "release/2026-09",
		`{"ref":"refs/tags/v1"}`:               "refs/tags/v1",
		`{"zen":"ping"}`:                       "", // GitHubs ping-Ereignis
		`kein json`:                            "",
		``:                                     "",
	}
	for body, want := range cases {
		if got := refFromPayload([]byte(body)); got != want {
			t.Errorf("%s → %q, erwartet %q", body, got, want)
		}
	}
}

// TestHookAdresseOhneZugriffspfad: die Adresse landet in den Einstellungen
// eines fremden Dienstes. Der Zugriffspfad des Betreibers hat dort nichts
// verloren.
func TestHookAdresseOhneZugriffspfad(t *testing.T) {
	env := newTestEnv(t)
	env.cfg.PanelDomain = "panel.example.at"
	env.cfg.AccessPath = "68f5131fbe68d76d9c61588f"

	svc := NewDeployService(env.store, env.agent, env.cfg, env.secrets, nil)
	url := svc.HookURL("0123456789abcdef0123456789abcdef")

	if got, want := url, "https://panel.example.at/hooks/deploy/0123456789abcdef0123456789abcdef"; got != want {
		t.Errorf("HookURL = %q, erwartet %q", got, want)
	}
	if contains(url, env.cfg.AccessPath) {
		t.Errorf("der Zugriffspfad steht in der Hook-Adresse: %s", url)
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
