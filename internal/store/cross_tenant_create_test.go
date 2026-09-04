package store

import (
	"errors"
	"testing"
)

// TestCreateFunktionenPruefenFremdschluesselGegenTenant deckt eine Lücke ab,
// die mehrere Create*-Funktionen gemeinsam hatten: sc.owns(tenantID) der
// neuen Zeile wurde geprüft, aber nie, dass eine mitgegebene Fremd-ID
// (site_id, database_id, db_user_id, target_id) tatsächlich demselben
// Mandanten gehört wie die neue Zeile selbst. Bob versucht hier für jede
// betroffene Funktion, eine neue Zeile mit seiner eigenen tenant_id, aber
// einer Fremd-ID von Alice anzulegen — jeder Versuch muss mit ErrNotFound
// scheitern, analog zum bereits vorhandenen Muster in repo_mail.go.
func TestCreateFunktionenPruefenFremdschluesselGegenTenant(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	sys := SystemScope()

	alice, _, aliceSite := seedTenant(t, s, "alice")
	bob, bobUser, _ := seedTenant(t, s, "bob")
	bobScope := UserScope(bobUser.ID, bob.ID, RoleCustomer)

	aliceDB := &Database{TenantID: alice.ID, Name: "alice_shop"}
	if err := s.CreateDatabase(ctx, sys, aliceDB); err != nil {
		t.Fatal(err)
	}
	aliceDBUser := &DBUser{
		TenantID: alice.ID, DatabaseID: aliceDB.ID,
		Username: "alice_u", HostPattern: "localhost", Grants: "ALL",
	}
	if err := s.CreateDBUser(ctx, sys, aliceDBUser); err != nil {
		t.Fatal(err)
	}
	aliceTarget := &BackupTarget{
		TenantID: alice.ID, Name: "alice-s3", Kind: "s3",
		Endpoint: "https://s3.example.at", Region: "eu-central-1", Bucket: "alice",
	}
	if err := s.CreateBackupTarget(ctx, sys, aliceTarget); err != nil {
		t.Fatal(err)
	}

	t.Run("CreateDatabase mit Alices site_id", func(t *testing.T) {
		d := &Database{TenantID: bob.ID, SiteID: &aliceSite.ID, Name: "bob_shop"}
		if err := s.CreateDatabase(ctx, bobScope, d); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateDBUser mit Alices database_id", func(t *testing.T) {
		u := &DBUser{
			TenantID: bob.ID, DatabaseID: aliceDB.ID,
			Username: "bob_u", HostPattern: "localhost", Grants: "ALL",
		}
		if err := s.CreateDBUser(ctx, bobScope, u); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateRemoteHost mit Alices db_user_id", func(t *testing.T) {
		h := &DBRemoteHost{TenantID: bob.ID, DBUserID: aliceDBUser.ID, Host: "203.0.113.5"}
		if err := s.CreateRemoteHost(ctx, bobScope, h); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateApp mit Alices site_id", func(t *testing.T) {
		a := &App{TenantID: bob.ID, SiteID: aliceSite.ID, Name: "bobapp"}
		if err := s.CreateApp(ctx, bobScope, a); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateCronjob mit Alices site_id", func(t *testing.T) {
		c := &Cronjob{TenantID: bob.ID, SiteID: &aliceSite.ID, Name: "job", Schedule: "* * * * *", Command: "true"}
		if err := s.CreateCronjob(ctx, bobScope, c); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateCert mit Alices site_id", func(t *testing.T) {
		c := &Cert{TenantID: bob.ID, SiteID: &aliceSite.ID, Domains: []string{"bob.example.at"}}
		if err := s.CreateCert(ctx, bobScope, c); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateFTPAccount mit Alices site_id", func(t *testing.T) {
		a := &FTPAccount{TenantID: bob.ID, SiteID: &aliceSite.ID, Username: "bobftp", HomeDir: "/var/www/bob"}
		if err := s.CreateFTPAccount(ctx, bobScope, a); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateDeploy mit Alices site_id", func(t *testing.T) {
		d := &Deploy{TenantID: bob.ID, SiteID: aliceSite.ID, RepoURL: "https://example.at/x.git", Ref: "main"}
		if err := s.CreateDeploy(ctx, bobScope, d); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreatePHPPool mit Alices site_id", func(t *testing.T) {
		p := &PHPPool{TenantID: bob.ID, SiteID: aliceSite.ID, PHPVersion: "8.3", PoolName: "bobpool", SocketPath: "/run/php/bob.sock"}
		if err := s.CreatePHPPool(ctx, bobScope, p); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateBackup mit Alices site_id", func(t *testing.T) {
		b := &Backup{TenantID: bob.ID, SiteID: &aliceSite.ID, Kind: "files", Destination: "local"}
		if err := s.CreateBackup(ctx, bobScope, b); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateBackup mit Alices database_id", func(t *testing.T) {
		b := &Backup{TenantID: bob.ID, DatabaseID: &aliceDB.ID, Kind: "database", Destination: "local"}
		if err := s.CreateBackup(ctx, bobScope, b); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})

	t.Run("CreateBackup mit Alices target_id", func(t *testing.T) {
		b := &Backup{TenantID: bob.ID, TargetID: &aliceTarget.ID, Kind: "files", Destination: "remote"}
		if err := s.CreateBackup(ctx, bobScope, b); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, erwartet ErrNotFound", err)
		}
	})
}
