package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/marion909/voltpanel/internal/version"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if _, _, err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	from, to, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("zweite Migration: %v", err)
	}
	if from != to {
		t.Fatalf("zweite Migration hat v%d auf v%d geändert, erwartet war keine Änderung", from, to)
	}

	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Gegen die Konstante prüfen, nicht gegen eine feste Zahl — sonst muss
	// dieser Test bei jeder neuen Migration angefasst werden.
	if v != version.SchemaVersion {
		t.Fatalf("SchemaVersion = %d, erwartet %d", v, version.SchemaVersion)
	}
}

// seedTenant legt einen Tenant mit einem User und einer Site an.
func seedTenant(t *testing.T, s *Store, slug string) (*Tenant, *User, *Site) {
	t.Helper()
	ctx := t.Context()
	sys := SystemScope()

	tenant := &Tenant{Name: slug, Slug: slug}
	if err := s.CreateTenant(ctx, sys, tenant); err != nil {
		t.Fatalf("CreateTenant(%s): %v", slug, err)
	}

	user := &User{
		TenantID: tenant.ID, Email: slug + "@example.at",
		PasswordHash: "x", Role: RoleCustomer,
	}
	if err := s.CreateUser(ctx, sys, user); err != nil {
		t.Fatalf("CreateUser(%s): %v", slug, err)
	}

	site := &Site{
		TenantID: tenant.ID, Domain: slug + ".example.at", Type: SiteStatic,
		SystemUser: "site_" + slug, RootPath: "/var/www/" + slug, DocumentRoot: "public",
	}
	if err := s.CreateSite(ctx, sys, site); err != nil {
		t.Fatalf("CreateSite(%s): %v", slug, err)
	}
	return tenant, user, site
}

// TestCrossTenantAccessDenied ist die IDOR-Testsuite aus Phase 4: jede
// Leseoperation wird mit einer fremden ID aufgerufen und muss scheitern.
func TestCrossTenantAccessDenied(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	alice, aliceUser, aliceSite := seedTenant(t, s, "alice")
	bob, bobUser, bobSite := seedTenant(t, s, "bob")

	// Bobs Scope: Kunde, streng auf seinen Tenant begrenzt.
	bobScope := UserScope(bobUser.ID, bob.ID, RoleCustomer)

	t.Run("GetSite mit fremder ID", func(t *testing.T) {
		if got, err := s.GetSite(ctx, bobScope, aliceSite.ID); err == nil {
			t.Fatalf("Bob konnte Alices Site %q lesen", got.Domain)
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatalf("erwartet ErrNotFound, bekam %v", err)
		}
	})

	t.Run("SiteByDomain mit fremder Domain", func(t *testing.T) {
		if _, err := s.SiteByDomain(ctx, bobScope, aliceSite.Domain); !errors.Is(err, ErrNotFound) {
			t.Fatalf("erwartet ErrNotFound, bekam %v", err)
		}
	})

	t.Run("GetUser mit fremder ID", func(t *testing.T) {
		if _, err := s.GetUser(ctx, bobScope, aliceUser.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("erwartet ErrNotFound, bekam %v", err)
		}
	})

	t.Run("GetTenant mit fremder ID", func(t *testing.T) {
		if _, err := s.GetTenant(ctx, bobScope, alice.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("erwartet ErrForbidden, bekam %v", err)
		}
	})

	t.Run("DeleteSite mit fremder ID", func(t *testing.T) {
		if err := s.DeleteSite(ctx, bobScope, aliceSite.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("erwartet ErrNotFound, bekam %v", err)
		}
		// Und Alices Site steht noch.
		if _, err := s.GetSite(ctx, SystemScope(), aliceSite.ID); err != nil {
			t.Fatalf("Alices Site wurde gelöscht: %v", err)
		}
	})

	t.Run("UpdateSite mit umgebogener tenant_id", func(t *testing.T) {
		// Der klassische Angriff: fremdes Objekt laden und die eigene ID eintragen.
		stolen := *aliceSite
		stolen.TenantID = bob.ID
		stolen.Domain = "uebernommen.example.at"
		if err := s.UpdateSite(ctx, bobScope, &stolen); !errors.Is(err, ErrNotFound) {
			t.Fatalf("erwartet ErrNotFound, bekam %v", err)
		}
		got, err := s.GetSite(ctx, SystemScope(), aliceSite.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Domain != aliceSite.Domain {
			t.Fatalf("Alices Domain wurde auf %q geändert", got.Domain)
		}
	})

	t.Run("CreateSite in fremdem Tenant", func(t *testing.T) {
		intruder := &Site{
			TenantID: alice.ID, Domain: "eindringling.example.at", Type: SiteStatic,
			SystemUser: "x", RootPath: "/var/www/x",
		}
		if err := s.CreateSite(ctx, bobScope, intruder); !errors.Is(err, ErrForbidden) {
			t.Fatalf("erwartet ErrForbidden, bekam %v", err)
		}
	})

	t.Run("ListSites zeigt nur eigene", func(t *testing.T) {
		sites, err := s.ListSites(ctx, bobScope)
		if err != nil {
			t.Fatal(err)
		}
		if len(sites) != 1 || sites[0].ID != bobSite.ID {
			t.Fatalf("ListSites lieferte %d sites, erwartet genau Bobs eigene", len(sites))
		}
	})

	t.Run("ListUsers zeigt nur eigene", func(t *testing.T) {
		users, err := s.ListUsers(ctx, bobScope)
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 1 || users[0].ID != bobUser.ID {
			t.Fatalf("ListUsers lieferte %d user, erwartet genau Bob", len(users))
		}
	})

	t.Run("ListTenants zeigt nur den eigenen", func(t *testing.T) {
		tenants, err := s.ListTenants(ctx, bobScope)
		if err != nil {
			t.Fatal(err)
		}
		if len(tenants) != 1 || tenants[0].ID != bob.ID {
			t.Fatalf("ListTenants lieferte %d tenants, erwartet genau Bobs eigenen", len(tenants))
		}
	})

	t.Run("Audit-Log zeigt nur eigene Einträge", func(t *testing.T) {
		if err := s.Log(ctx, &AuditEntry{TenantID: &alice.ID, Action: "site.create", Actor: "alice"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Log(ctx, &AuditEntry{TenantID: &bob.ID, Action: "site.create", Actor: "bob"}); err != nil {
			t.Fatal(err)
		}
		entries, err := s.ListAudit(ctx, bobScope, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Actor != "bob" {
				t.Fatalf("Bob sieht Audit-Eintrag von %q", e.Actor)
			}
		}
	})
}

// TestZeroScopeFailsClosed: ein vergessener Scope darf nicht alles freigeben.
func TestZeroScopeFailsClosed(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	seedTenant(t, s, "alice")

	var zero Scope
	if _, err := s.ListSites(ctx, zero); !errors.Is(err, ErrNoTenant) {
		t.Fatalf("ListSites mit leerem Scope: erwartet ErrNoTenant, bekam %v", err)
	}
	if _, err := s.ListUsers(ctx, zero); !errors.Is(err, ErrNoTenant) {
		t.Fatalf("ListUsers mit leerem Scope: erwartet ErrNoTenant, bekam %v", err)
	}
	if _, err := s.GetSite(ctx, zero, 1); !errors.Is(err, ErrNoTenant) {
		t.Fatalf("GetSite mit leerem Scope: erwartet ErrNoTenant, bekam %v", err)
	}
}

// TestCustomerCannotElevate: Elevate und ForTenant sind Rollen-gebunden.
func TestCustomerCannotElevate(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	alice, _, aliceSite := seedTenant(t, s, "alice")
	bob, bobUser, _ := seedTenant(t, s, "bob")

	bobScope := UserScope(bobUser.ID, bob.ID, RoleCustomer)

	if elevated := bobScope.Elevate(); elevated.IsSystem() {
		t.Fatal("Kunde konnte sich auf System-Scope heben")
	}
	if _, err := bobScope.ForTenant(alice.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ForTenant für Kunde: erwartet ErrForbidden, bekam %v", err)
	}

	// Auch nach dem Versuch bleibt Alices Site unerreichbar.
	if _, err := s.GetSite(ctx, bobScope.Elevate(), aliceSite.ID); err == nil {
		t.Fatal("Kunde konnte nach Elevate() auf fremde Site zugreifen")
	}
}

// TestAdminCanCrossTenant: die Gegenprobe — Owner/Admin dürfen es.
func TestAdminCanCrossTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	alice, _, aliceSite := seedTenant(t, s, "alice")
	_, _, _ = seedTenant(t, s, "bob")

	admin := UserScope(1, alice.ID, RoleAdmin).Elevate()
	sites, err := s.ListSites(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("Admin sieht %d von 2 Sites", len(sites))
	}
	if _, err := s.GetSite(ctx, admin, aliceSite.ID); err != nil {
		t.Fatalf("Admin kann Alices Site nicht lesen: %v", err)
	}
}

func TestSiteValidationRejectsBadInput(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	tenant, _, _ := seedTenant(t, s, "alice")
	sys := SystemScope()

	bad := []struct {
		name string
		site Site
	}{
		{"leere domain", Site{TenantID: tenant.ID, Type: SiteStatic}},
		{"domain mit slash", Site{TenantID: tenant.ID, Domain: "a/b.at", Type: SiteStatic}},
		{"unbekannter typ", Site{TenantID: tenant.ID, Domain: "x.example.at", Type: "wordpress"}},
		{"php ohne version", Site{TenantID: tenant.ID, Domain: "x.example.at", Type: SitePHP}},
		{"proxy ohne ziel", Site{TenantID: tenant.ID, Domain: "x.example.at", Type: SiteProxy}},
		{"document_root mit ..", Site{TenantID: tenant.ID, Domain: "x.example.at",
			Type: SiteStatic, DocumentRoot: "../../etc"}},
		{"absoluter document_root", Site{TenantID: tenant.ID, Domain: "x.example.at",
			Type: SiteStatic, DocumentRoot: "/etc"}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			site := tc.site
			if err := s.CreateSite(ctx, sys, &site); err == nil {
				t.Fatalf("CreateSite akzeptierte %+v", tc.site)
			}
		})
	}
}

func TestDuplicateDomainConflicts(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	tenant, _, _ := seedTenant(t, s, "alice")

	dup := &Site{TenantID: tenant.ID, Domain: "alice.example.at", Type: SiteStatic,
		SystemUser: "x", RootPath: "/var/www/x"}
	if err := s.CreateSite(ctx, SystemScope(), dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("erwartet ErrConflict, bekam %v", err)
	}
}

func TestLoginLockout(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	_, user, _ := seedTenant(t, s, "alice")

	for i := 0; i < 5; i++ {
		if err := s.NoteLoginFailure(ctx, user.ID, 5, 900); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.UserByEmail(ctx, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Locked() {
		t.Fatalf("User nach 5 Fehlversuchen nicht gesperrt (failed=%d)", got.FailedLogins)
	}

	if err := s.NoteLoginSuccess(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.UserByEmail(ctx, user.Email); got.Locked() {
		t.Fatal("Sperre wurde nach erfolgreichem Login nicht aufgehoben")
	}
}

func TestBackupProducesUsableCopy(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedTenant(t, s, "alice")

	dst := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	copy, err := Open(dst)
	if err != nil {
		t.Fatalf("Backup nicht lesbar: %v", err)
	}
	defer copy.Close()

	sites, err := copy.ListSites(ctx, SystemScope())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("Backup enthält %d Sites, erwartet 1", len(sites))
	}
}
