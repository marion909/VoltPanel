package core

import (
	"context"
	"errors"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// TestCreateDatabaseLehntFremdeSiteAb deckt den end-to-end exploitierbaren
// Fund ab: CreateDatabase prüfte nicht, dass die übergebene SiteID zum
// Mandanten der Anfrage gehört, bevor die Datenbank angelegt wird. Bob
// versucht hier, eine Datenbank mit seiner eigenen tenant_id, aber Alices
// site_id anzulegen — muss vor jedem Agent-/MySQL-Aufruf scheitern.
func TestCreateDatabaseLehntFremdeSiteAb(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	_, _, aliceSite := env.seedSite(t, "alice")
	bobTenant, bobUser, _ := env.seedSite(t, "bob")
	bobScope := store.UserScope(bobUser.ID, bobTenant.ID, store.RoleCustomer)

	svc := NewDatabaseService(env.store, env.agent, env.cfg, nil)
	_, err := svc.CreateDatabase(ctx, bobScope, CreateDatabaseInput{
		Name: "shop", SiteID: &aliceSite.ID, TenantID: bobTenant.ID,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateDatabase mit Alices site_id: %v, erwartet ErrNotFound", err)
	}
}

// TestAcmeIssueLehntFremdeSiteAb deckt dieselbe Lücke bei CertService.Issue
// ab: opts.SiteID/opts.TenantID wurden nie gegeneinander geprüft, bevor
// store.CreateCert lief — erst enableSSL hätte die Zuordnung später implizit
// geprüft, wenn der Cert-Datensatz bereits angelegt war.
func TestAcmeIssueLehntFremdeSiteAb(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	_, _, aliceSite := env.seedSite(t, "alice")
	bobTenant, bobUser, _ := env.seedSite(t, "bob")
	bobScope := store.UserScope(bobUser.ID, bobTenant.ID, store.RoleCustomer)

	svc := NewCertService(env.cfg, env.store, env.agent, nil, nil)
	_, err := svc.Issue(ctx, bobScope, IssueOptions{
		Domains: []string{"bob.example.at"}, SiteID: &aliceSite.ID, TenantID: bobTenant.ID,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Issue mit Alices site_id: %v, erwartet ErrNotFound", err)
	}
}
