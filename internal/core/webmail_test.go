package core

import (
	"context"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

func webmailService(env *testEnv) *WebmailService {
	return NewWebmailService(env.store, env.agent, env.cfg, env.secrets)
}

// Ohne panel_domain gibt es keinen Namen, unter dem Webmail überhaupt
// erreichbar wäre (webmail.<panel_domain>) — dieselbe Vorbedingung wie beim
// Zertifikat der Panel-Domain selbst (handleIssuePanelCert).
func TestWebmailInstallOhnePanelDomain(t *testing.T) {
	env := newTestEnv(t)
	env.cfg.PanelDomain = ""
	svc := webmailService(env)

	_, err := svc.Install(context.Background(), store.SystemScope(), InstallWebmailInput{PHPVersion: "8.3"})
	if err == nil {
		t.Fatal("ohne panel-domain wurde die installation angenommen")
	}
	if !strings.Contains(err.Error(), "panel-domain") {
		t.Errorf("unerwartete meldung: %v", err)
	}
}

// Eine zweite Installation lehnt Install ab, statt über eine bestehende
// hinwegzuschreiben — sonst verlöre ein zweiter, versehentlicher Klick das
// schon gesetzte Datenbankpasswort, ohne dass Datenbank oder Dateien
// tatsächlich neu entstünden.
func TestWebmailInstallLehntZweiteInstallationAb(t *testing.T) {
	env := newTestEnv(t)
	env.cfg.PanelDomain = "panel.example.at"
	svc := webmailService(env)
	ctx := context.Background()

	if err := env.store.SetWebmail(ctx, "webmail.panel.example.at", "8.3",
		"volt_webmail", "volt_webmail", "verschluesselt"); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Install(ctx, store.SystemScope(), InstallWebmailInput{PHPVersion: "8.3"})
	if err == nil {
		t.Fatal("eine zweite installation wurde angenommen")
	}
	if !strings.Contains(err.Error(), "schon installiert") {
		t.Errorf("unerwartete meldung: %v", err)
	}
}

// Status gibt das Datenbankpasswort nie heraus — es ist reine
// Interna für eine mögliche Neuerstellung von config.inc.php, kein Wert,
// den ein Administrator je braucht oder sehen soll.
func TestWebmailStatusOhnePasswort(t *testing.T) {
	env := newTestEnv(t)
	svc := webmailService(env)
	ctx := context.Background()

	if err := env.store.SetWebmail(ctx, "webmail.panel.example.at", "8.3",
		"volt_webmail", "volt_webmail", "verschluesselt-geheim"); err != nil {
		t.Fatal(err)
	}

	w, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if w.DBPasswordEnc != "" {
		t.Error("das verschlüsselte passwort steht im status")
	}
}
