package store

import (
	"fmt"
	"testing"
)

// TestUsageForTenantZaehltAlleTabellen sät in jeder der sieben Zähltabellen
// eine andere Anzahl Zeilen und prüft, dass UsageForTenant jede Zahl der
// richtigen Spalte zuordnet. Die Funktion wurde von acht Einzelabfragen
// (eine je Tabelle) auf ein einziges Statement mit sieben Subselects
// umgestellt — unterschiedliche Zählstände je Tabelle sichern ab, dass ein
// vertauschtes Scan-Ziel auffiele, statt zufällig gleich auszusehen.
func TestUsageForTenantZaehltAlleTabellen(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	tenant, _, site := seedTenant(t, s, "alice")
	sys := SystemScope()

	for i := range 2 {
		if err := s.CreateDatabase(ctx, sys, &Database{
			TenantID: tenant.ID, Name: fmt.Sprintf("alice_db%d", i),
		}); err != nil {
			t.Fatalf("CreateDatabase: %v", err)
		}
	}
	for i := range 3 {
		if err := s.CreateCronjob(ctx, sys, &Cronjob{
			TenantID: tenant.ID, SiteID: &site.ID, Name: fmt.Sprintf("Job %d", i),
			Schedule: "0 3 * * *", Command: "/usr/bin/true", RunAs: site.SystemUser, Enabled: true,
		}); err != nil {
			t.Fatalf("CreateCronjob: %v", err)
		}
	}
	for i := range 4 {
		if err := s.CreateFTPAccount(ctx, sys, &FTPAccount{
			TenantID: tenant.ID, SiteID: &site.ID, Username: fmt.Sprintf("alice_ftp%d", i),
			HomeDir: "/var/www/alice", UID: 1001, GID: 1001,
		}); err != nil {
			t.Fatalf("CreateFTPAccount: %v", err)
		}
	}
	var mailDomain *MailDomain
	for i := range 6 {
		dom := &MailDomain{TenantID: tenant.ID, Domain: fmt.Sprintf("alice%d.example.at", i), Active: true}
		if err := s.CreateMailDomain(ctx, sys, dom); err != nil {
			t.Fatalf("CreateMailDomain: %v", err)
		}
		mailDomain = dom
	}
	for i := range 5 {
		if err := s.CreateMailbox(ctx, sys, &Mailbox{
			TenantID: tenant.ID, DomainID: mailDomain.ID, LocalPart: fmt.Sprintf("box%d", i),
		}); err != nil {
			t.Fatalf("CreateMailbox: %v", err)
		}
	}
	for range 7 {
		if err := s.CreateCert(ctx, sys, &Cert{
			TenantID: tenant.ID, SiteID: &site.ID, Domains: []string{"alice.example.at"},
		}); err != nil {
			t.Fatalf("CreateCert: %v", err)
		}
	}
	for i := range 8 {
		if err := s.CreateBackupTarget(ctx, sys, &BackupTarget{
			TenantID: tenant.ID, Name: fmt.Sprintf("Offsite %d", i), Kind: "s3",
			Endpoint: "s3.example.at", Region: "eu", Bucket: fmt.Sprintf("alice-backups-%d", i),
		}); err != nil {
			t.Fatalf("CreateBackupTarget: %v", err)
		}
	}

	usage, err := s.UsageForTenant(ctx, sys, tenant.ID)
	if err != nil {
		t.Fatalf("UsageForTenant: %v", err)
	}

	want := TenantUsage{
		TenantID: tenant.ID, Sites: 1, Databases: 2, Cronjobs: 3,
		FTPAccounts: 4, Mailboxes: 5, MailDomains: 6, Certs: 7, BackupTargets: 8,
	}
	got := *usage
	got.DiskBytes, got.DiskFiles, got.TrafficBytes = 0, 0, 0
	if got != want {
		t.Fatalf("UsageForTenant = %+v, erwartet %+v", got, want)
	}
}
