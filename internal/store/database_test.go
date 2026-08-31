package store

import (
	"errors"
	"strings"
	"testing"
)

func TestDatabaseNameValidation(t *testing.T) {
	valid := []string{"alice_wordpress", "shop_db", "a12", "kunde_1_wp"}
	for _, name := range valid {
		if !ValidDBName(name) {
			t.Errorf("ValidDBName(%q) = false, erwartet true", name)
		}
	}

	// Diese Namen landen als Identifier in DDL-Anweisungen, die sich nicht
	// parametrisieren lassen — deshalb die enge Zeichenmenge.
	invalid := []string{
		"", "ab", "mysql; DROP DATABASE x", "with space", "UPPER",
		"backtick`name", "quote'name", "1beginnt_mit_ziffer", "-dash",
		"dots.in.name", strings.Repeat("a", 49), "schön",
	}
	for _, name := range invalid {
		if ValidDBName(name) {
			t.Errorf("ValidDBName(%q) = true, erwartet false", name)
		}
	}
}

func TestDBUserNameValidation(t *testing.T) {
	for _, name := range []string{"alice_wp", "shop", "u12345"} {
		if !ValidDBUser(name) {
			t.Errorf("ValidDBUser(%q) = false, erwartet true", name)
		}
	}
	// MySQL begrenzt Benutzernamen auf 32 Zeichen; wir bleiben bei 31.
	for _, name := range []string{"", "ab", "root'@'%", "with space", strings.Repeat("a", 32)} {
		if ValidDBUser(name) {
			t.Errorf("ValidDBUser(%q) = true, erwartet false", name)
		}
	}
}

func TestGrantSetValidation(t *testing.T) {
	for _, g := range []string{"ALL", "all", "READONLY", "readwrite"} {
		if !ValidGrantSet(g) {
			t.Errorf("ValidGrantSet(%q) = false, erwartet true", g)
		}
	}
	for _, g := range []string{"", "SUPER", "ALL PRIVILEGES WITH GRANT OPTION", "DROP"} {
		if ValidGrantSet(g) {
			t.Errorf("ValidGrantSet(%q) = true, erwartet false", g)
		}
	}
}

func TestDatabaseTenantIsolation(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	alice, _, _ := seedTenant(t, s, "alice")
	bob, bobUser, _ := seedTenant(t, s, "bob")
	sys := SystemScope()

	aliceDB := &Database{TenantID: alice.ID, Name: "alice_wordpress"}
	if err := s.CreateDatabase(ctx, sys, aliceDB); err != nil {
		t.Fatal(err)
	}
	dbUser := &DBUser{
		TenantID: alice.ID, DatabaseID: aliceDB.ID,
		Username: "alice_wp", HostPattern: "localhost", Grants: "ALL",
	}
	if err := s.CreateDBUser(ctx, sys, dbUser); err != nil {
		t.Fatal(err)
	}

	bobScope := UserScope(bobUser.ID, bob.ID, RoleCustomer)

	if _, err := s.GetDatabase(ctx, bobScope, aliceDB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Bob konnte Alices Datenbank lesen: %v", err)
	}
	if err := s.DeleteDatabase(ctx, bobScope, aliceDB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Bob konnte Alices Datenbank löschen: %v", err)
	}
	if _, err := s.GetDBUser(ctx, bobScope, dbUser.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Bob konnte Alices Datenbankbenutzer lesen: %v", err)
	}

	// Auch die Benutzerliste einer fremden Datenbank muss leer bleiben.
	users, err := s.ListDBUsers(ctx, bobScope, aliceDB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("Bob sieht %d Benutzer einer fremden Datenbank", len(users))
	}

	dbs, err := s.ListDatabases(ctx, bobScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 0 {
		t.Fatalf("Bob sieht %d fremde Datenbanken", len(dbs))
	}
}

func TestDuplicateDatabaseConflicts(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	alice, _, _ := seedTenant(t, s, "alice")
	sys := SystemScope()

	first := &Database{TenantID: alice.ID, Name: "shared_name"}
	if err := s.CreateDatabase(ctx, sys, first); err != nil {
		t.Fatal(err)
	}
	// MySQL kennt keine Mandanten: derselbe Name darf kein zweites Mal
	// vergeben werden, auch nicht an einen anderen Tenant.
	bob, _, _ := seedTenant(t, s, "bob")
	second := &Database{TenantID: bob.ID, Name: "shared_name"}
	if err := s.CreateDatabase(ctx, sys, second); !errors.Is(err, ErrConflict) {
		t.Fatalf("erwartet ErrConflict, bekam %v", err)
	}
}
