package core

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// TestWebPermissionsLetTheServerIn prüft am echten Dateisystem, was auf dem
// Server schiefging: nginx läuft als eigene Gruppe und kam nicht in das
// Verzeichnis der Site.
//
// Die Gruppe des Webservers ist hier die des Testbenutzers — der Agent lehnt
// ein chown auf einen fremden Benutzer zu Recht ab, wenn er nicht als root
// läuft, und ein stillschweigend übersprungenes chown würde den Test wertlos
// machen.
func TestWebPermissionsLetTheServerIn(t *testing.T) {
	env := newTestEnv(t)

	group := testPrimaryGroup(t)
	env.cfg.WebGroup = group

	site := &store.Site{
		Domain: "perm.example.at", SystemUser: env.systemUser,
		RootPath: filepath.Join(env.sitesDir, "perm.example.at"), DocumentRoot: "public",
	}

	svc := NewSiteService(env.store, env.agent, env.cfg)
	if err := svc.applyWebPermissions(context.Background(), site); err != nil {
		t.Fatalf("rechte setzen: %v", err)
	}

	root, err := os.Stat(site.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	// Der Webserver muss durchqueren können, sonst endet jede Anfrage in
	// "stat() failed (13: Permission denied)".
	if root.Mode().Perm()&0o050 == 0 {
		t.Errorf("Wurzel %o — die Gruppe kann nicht hinein", root.Mode().Perm())
	}
	// Und niemand sonst: die Trennung zwischen den Sites hängt daran.
	if root.Mode().Perm()&0o007 != 0 {
		t.Errorf("Wurzel %o ist für alle zugänglich", root.Mode().Perm())
	}

	web, err := os.Stat(site.WebRoot())
	if err != nil {
		t.Fatal(err)
	}
	if web.Mode()&os.ModeSetgid == 0 {
		t.Error("dem Dokumentenstamm fehlt setgid — was PHP dort anlegt, verlässt die Gruppe")
	}
	if web.Mode().Perm()&0o007 != 0 {
		t.Errorf("Dokumentenstamm %o ist für alle zugänglich", web.Mode().Perm())
	}
}

func testPrimaryGroup(t *testing.T) string {
	t.Helper()
	me, err := user.Current()
	if err != nil {
		t.Skip("aktueller Benutzer nicht ermittelbar")
	}
	g, err := user.LookupGroupId(me.Gid)
	if err != nil {
		t.Skipf("Gruppe %s nicht auflösbar", me.Gid)
	}
	return g.Name
}
