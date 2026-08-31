package store

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateCronSchedule(t *testing.T) {
	valid := []string{
		"* * * * *",
		"0 3 * * *",
		"*/5 * * * *",
		"0,15,30,45 * * * *",
		"0 9-17 * * 1-5",
		"30 2 1 * *",
		"0 0 1 1 0",
		"0 0 * * 7",
		"*/15 0-6/2 * * *",
		"@daily", "@hourly", "@weekly", "@monthly", "@yearly", "@midnight",
	}
	for _, s := range valid {
		if err := ValidateCronSchedule(s); err != nil {
			t.Errorf("ValidateCronSchedule(%q) = %v, erwartet gültig", s, err)
		}
	}

	invalid := []struct {
		name     string
		schedule string
	}{
		// Ein sechstes Feld würde in /etc/cron.d als Benutzername gelesen —
		// der Job liefe dann unter einem fremden Konto.
		{"sechs felder", "* * * * * root"},
		{"vier felder", "* * * *"},
		{"leer", ""},
		{"nur leerzeichen", "   "},
		{"minute zu groß", "60 * * * *"},
		{"stunde zu groß", "* 24 * * *"},
		{"tag null", "* * 0 * *"},
		{"monat 13", "* * * 13 *"},
		{"wochentag 8", "* * * * 8"},
		{"rückwärtsbereich", "10-5 * * * *"},
		{"keine zahl", "abc * * * *"},
		{"leerer listeneintrag", "1,,3 * * * *"},
		{"schrittweite null", "*/0 * * * *"},
		{"unbekannte kurzform", "@reboot"},
		{"kurzform mit tippfehler", "@dayly"},
		{"kommando angehängt", "* * * * * ; rm -rf /"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCronSchedule(tc.schedule); err == nil {
				t.Fatalf("ValidateCronSchedule(%q) akzeptiert", tc.schedule)
			}
		})
	}
}

func TestCronjobValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	tenant, _, site := seedTenant(t, s, "alice")
	sys := SystemScope()

	valid := &Cronjob{
		TenantID: tenant.ID, SiteID: &site.ID, Name: "Backup nachts",
		Schedule: "0 3 * * *", Command: "/usr/bin/php /var/www/x/artisan backup",
		RunAs: site.SystemUser, Enabled: true,
	}
	if err := s.CreateCronjob(ctx, sys, valid); err != nil {
		t.Fatalf("gültiger Cronjob abgelehnt: %v", err)
	}

	invalid := []struct {
		name string
		job  Cronjob
	}{
		// Ein Zeilenumbruch im Kommando wäre eine zweite, ungeprüfte Zeile
		// in der crontab-Datei — also ein zusätzlicher, frei wählbarer Job.
		{"zeilenumbruch im kommando", Cronjob{
			TenantID: tenant.ID, Name: "job", Schedule: "* * * * *",
			Command: "echo eins\n* * * * * root rm -rf /",
		}},
		{"nullbyte im kommando", Cronjob{
			TenantID: tenant.ID, Name: "job", Schedule: "* * * * *", Command: "echo\x00x",
		}},
		{"leeres kommando", Cronjob{
			TenantID: tenant.ID, Name: "job", Schedule: "* * * * *", Command: "  ",
		}},
		{"name mit slash", Cronjob{
			TenantID: tenant.ID, Name: "../etc/cron.d/x", Schedule: "* * * * *", Command: "echo",
		}},
		{"leerer name", Cronjob{
			TenantID: tenant.ID, Name: "", Schedule: "* * * * *", Command: "echo",
		}},
		{"ungültiger zeitplan", Cronjob{
			TenantID: tenant.ID, Name: "job", Schedule: "jeden tag", Command: "echo",
		}},
		{"überlanges kommando", Cronjob{
			TenantID: tenant.ID, Name: "job", Schedule: "* * * * *",
			Command: strings.Repeat("a", 1001),
		}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			job := tc.job
			if err := s.CreateCronjob(ctx, sys, &job); err == nil {
				t.Fatalf("CreateCronjob akzeptierte %+v", tc.job)
			}
		})
	}
}

func TestCronjobTenantIsolation(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	alice, _, aliceSite := seedTenant(t, s, "alice")
	bob, bobUser, _ := seedTenant(t, s, "bob")

	job := &Cronjob{
		TenantID: alice.ID, SiteID: &aliceSite.ID, Name: "alices job",
		Schedule: "* * * * *", Command: "echo", RunAs: aliceSite.SystemUser, Enabled: true,
	}
	if err := s.CreateCronjob(ctx, SystemScope(), job); err != nil {
		t.Fatal(err)
	}

	bobScope := UserScope(bobUser.ID, bob.ID, RoleCustomer)
	if _, err := s.GetCronjob(ctx, bobScope, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Bob konnte Alices Cronjob lesen: %v", err)
	}
	if err := s.DeleteCronjob(ctx, bobScope, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Bob konnte Alices Cronjob löschen: %v", err)
	}

	jobs, err := s.ListCronjobs(ctx, bobScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("Bob sieht %d fremde Cronjobs", len(jobs))
	}
}
