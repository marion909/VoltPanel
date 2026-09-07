package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// internal/core/ftp.go hatte bisher keine eigene Testdatei (nur
// agent/ftp_test.go, api/ftp_test.go, transfer/ftp_test.go existierten) —
// weder für den Symlink-Fall noch für sonst irgendeine Funktionalität
// dieser Datei. FTPService.Create teilt sich joinInside mit FileService
// (files.go); derselbe in v0.4.31 behobene Symlink-Fund betrifft also auch
// das FTP-Home-Verzeichnis.

func ftpService(env *testEnv) *FTPService {
	return NewFTPService(env.store, env.agent, env.cfg, env.secrets)
}

// TestFTPCreateLehntFremdeSiteAb: eine site_id, die nicht dem eigenen
// Mandanten gehört, muss scheitern, bevor überhaupt ein Zugang entsteht.
func TestFTPCreateLehntFremdeSiteAb(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	_, _, aliceSite := env.seedSite(t, "alice")
	bobTenant, bobUser, _ := env.seedSite(t, "bob")
	bobScope := store.UserScope(bobUser.ID, bobTenant.ID, store.RoleCustomer)

	svc := ftpService(env)
	if _, _, err := svc.Create(ctx, bobScope, CreateFTPInput{
		SiteID: aliceSite.ID, Username: "zugang",
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Create mit Alices site_id: %v, erwartet ErrNotFound", err)
	}
}

// TestFTPCreateLehntSymlinkAufFremdeSiteAb deckt dieselbe Lücke wie
// files.go: ein Symlink innerhalb der eigenen Site auf eine fremde Site
// darf nicht als FTP-Home durchgehen — das Home-Verzeichnis läge dann,
// symlink-aufgelöst, im Verzeichnis eines fremden Mandanten.
func TestFTPCreateLehntSymlinkAufFremdeSiteAb(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	_, _, aliceSite := env.seedSite(t, "alice")
	bobTenant, bobUser, bobSite := env.seedSite(t, "bob")
	bobScope := store.UserScope(bobUser.ID, bobTenant.ID, store.RoleCustomer)

	// Bob legt in seiner eigenen Site einen Symlink auf Alices Verzeichnis
	// an — kein besonderes Privileg nötig.
	link := filepath.Join(bobSite.RootPath, "fremd")
	if err := os.Symlink(aliceSite.RootPath, link); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(link)

	svc := ftpService(env)
	if account, _, err := svc.Create(ctx, bobScope, CreateFTPInput{
		SiteID: bobSite.ID, Username: "zugang", Subdir: "fremd",
	}); err == nil {
		t.Fatalf("Create über den Symlink auf Alices Site wurde angenommen: HomeDir=%q", account.HomeDir)
	}
}

// TestFTPBuildNameTraegtMandantenpraefix: buildName ist der reine,
// agent-freie Teil von Create — der volle Rundlauf braucht einen echten,
// site-präfigierten Linux-Benutzer (agent/ops_ftp.go: siteUserIDs verlangt
// den sitePrefix), den es in dieser Sandbox nicht gibt. Was sich ohne den
// Agent prüfen lässt, wird hier geprüft: der Name bekommt den
// Mandantenpräfix, zwei Kunden können sich also nicht denselben Namen
// greifen.
func TestFTPBuildNameTraegtMandantenpraefix(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	tenant, user, site := env.seedSite(t, "alice")
	sc := store.UserScope(user.ID, tenant.ID, store.RoleOwner)

	svc := ftpService(env)
	name, err := svc.buildName(ctx, sc, site, "kunde")
	if err != nil {
		t.Fatal(err)
	}
	if name == "kunde" {
		t.Errorf("Name %q trägt kein Mandantenpräfix", name)
	}
	if !strings.Contains(name, "kunde") {
		t.Errorf("Name %q enthält den Wunschnamen nicht mehr", name)
	}
}
