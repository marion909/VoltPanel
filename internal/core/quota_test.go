package core

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// seedPlan legt ein Paket an und ordnet es einem Tenant zu.
func seedPlan(t *testing.T, env *testEnv, tenant *store.Tenant, plan *store.Plan) {
	t.Helper()
	ctx := context.Background()
	sys := store.SystemScope()

	if err := env.store.CreatePlan(ctx, sys, plan); err != nil {
		t.Fatal(err)
	}
	tenant.PlanID = &plan.ID
	if err := env.store.UpdateTenant(ctx, sys, tenant); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaCountLimits(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sys := store.SystemScope()

	tenant, _, _ := env.seedSite(t, "alice") // Site 1 existiert damit bereits
	seedPlan(t, env, tenant, &store.Plan{Name: "Klein", MaxSites: 1, MaxDatabases: 2})

	quota := NewQuotaService(env.store, env.agent, env.cfg, nil)

	t.Run("erreichte grenze blockt", func(t *testing.T) {
		err := quota.CheckCount(ctx, sys, tenant.ID, ResourceSites)
		if !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("erwartet ErrQuotaExceeded, bekam %v", err)
		}
		// Die Meldung muss sagen, welches Paket und welche Grenze greift.
		if !strings.Contains(err.Error(), "Klein") || !strings.Contains(err.Error(), "websites") {
			t.Fatalf("wenig hilfreiche Meldung: %v", err)
		}
	})

	t.Run("noch freie grenze laesst durch", func(t *testing.T) {
		if err := quota.CheckCount(ctx, sys, tenant.ID, ResourceDatabases); err != nil {
			t.Fatalf("CheckCount für Datenbanken: %v", err)
		}
	})

	t.Run("null bedeutet unbegrenzt", func(t *testing.T) {
		// MaxCronjobs ist im Paket nicht gesetzt — das muss "kein Limit"
		// heißen und nicht "nichts erlaubt", sonst wäre ein lückenhaft
		// gepflegtes Paket eine Sperre.
		if err := quota.CheckCount(ctx, sys, tenant.ID, ResourceCronjobs); err != nil {
			t.Fatalf("nicht gesetzte Grenze blockt: %v", err)
		}
	})

	t.Run("ohne paket keine grenze", func(t *testing.T) {
		bob, _, _ := env.seedSite(t, "bob")
		if err := quota.CheckCount(ctx, sys, bob.ID, ResourceSites); err != nil {
			t.Fatalf("Tenant ohne Paket wird begrenzt: %v", err)
		}
	})
}

func TestQuotaDiskLimit(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sys := store.SystemScope()

	tenant, _, site := env.seedSite(t, "alice")
	seedPlan(t, env, tenant, &store.Plan{Name: "Klein", DiskQuotaMB: 1})

	quota := NewQuotaService(env.store, env.agent, env.cfg, nil)

	t.Run("unter der grenze erlaubt", func(t *testing.T) {
		if err := quota.CheckDisk(ctx, sys, tenant.ID, 100*1024); err != nil {
			t.Fatalf("CheckDisk unter der Grenze: %v", err)
		}
	})

	t.Run("ueber der grenze blockt", func(t *testing.T) {
		if err := quota.CheckDisk(ctx, sys, tenant.ID, 2*1024*1024); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("erwartet ErrQuotaExceeded, bekam %v", err)
		}
	})

	t.Run("gemessener verbrauch zaehlt mit", func(t *testing.T) {
		// 900 KiB belegt, 1 MiB erlaubt: 200 KiB mehr passen nicht.
		if err := env.store.RecordSiteUsage(ctx, site.ID, 900*1024, 10); err != nil {
			t.Fatal(err)
		}
		if err := quota.CheckDisk(ctx, sys, tenant.ID, 200*1024); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("belegter Platz wird nicht mitgezählt: %v", err)
		}
		if err := quota.CheckDisk(ctx, sys, tenant.ID, 50*1024); err != nil {
			t.Fatalf("passender Rest wird abgelehnt: %v", err)
		}
	})
}

// TestQuotaBlocksUpload prüft die Durchsetzung dort, wo sie zählt.
func TestQuotaBlocksUpload(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sys := store.SystemScope()

	tenant, _, site := env.seedSite(t, "alice")
	seedPlan(t, env, tenant, &store.Plan{Name: "Klein", DiskQuotaMB: 1})

	payload := bytes.Repeat([]byte("x"), 2*1024*1024)
	_, err := env.files.Upload(ctx, sys, site.ID, "public/zugross.bin",
		bytes.NewReader(payload), UploadOptions{Size: int64(len(payload))})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("Upload über der Quota: erwartet ErrQuotaExceeded, bekam %v", err)
	}

	// Ein passender Upload muss weiterhin durchgehen.
	small := bytes.Repeat([]byte("y"), 100*1024)
	if _, err := env.files.Upload(ctx, sys, site.ID, "public/klein.bin",
		bytes.NewReader(small), UploadOptions{Size: int64(len(small))}); err != nil {
		t.Fatalf("Upload unter der Quota abgelehnt: %v", err)
	}
}

// TestQuotaMeasure misst echten Verbrauch über den Agent.
func TestQuotaMeasure(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sys := store.SystemScope()

	tenant, _, site := env.seedSite(t, "alice")
	payload := bytes.Repeat([]byte("z"), 300*1024)
	if _, err := env.files.Upload(ctx, sys, site.ID, "public/daten.bin",
		bytes.NewReader(payload), UploadOptions{}); err != nil {
		t.Fatal(err)
	}

	quota := NewQuotaService(env.store, env.agent, env.cfg, nil)
	measured, errs := quota.Measure(ctx)
	if len(errs) > 0 {
		t.Fatalf("Measure: %v", errs[0])
	}
	if measured != 1 {
		t.Fatalf("Measure hat %d Sites gemessen, erwartet 1", measured)
	}

	updated, err := env.store.GetSite(ctx, sys, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Belegte Blöcke, nicht nominelle Größe — deshalb nur die Größenordnung prüfen.
	if updated.DiskBytes < 250*1024 {
		t.Fatalf("gemessen wurden %d bytes, erwartet mindestens 250 KiB", updated.DiskBytes)
	}
	if updated.DiskFiles < 1 {
		t.Fatalf("gemessen wurden %d Dateien", updated.DiskFiles)
	}
	if updated.DiskMeasuredAt == nil {
		t.Fatal("kein Messzeitpunkt gesetzt")
	}

	status, err := quota.Status(ctx, sys, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Usage.DiskBytes != updated.DiskBytes {
		t.Fatalf("Status meldet %d, Site hat %d", status.Usage.DiskBytes, updated.DiskBytes)
	}
	// Ohne Paket darf keine Auslastung suggeriert werden.
	for _, e := range status.Entries {
		if e.Limit == 0 && e.Percent != -1 {
			t.Fatalf("%s ohne Grenze meldet %.0f%% Auslastung", e.Resource, e.Percent)
		}
	}
}

// TestQuotaStatusZeigtPostfaecher deckt den Fund ab, dass die
// Quota-Übersicht (Status) Sites/Datenbanken/Cronjobs/FTP/Disk/Traffic
// listete, aber keinen Eintrag für Postfächer — obwohl CheckCount das
// Postfach-Limit längst über genau diese Ressource durchsetzt. Ein
// Kunde/Reseller, dessen Postfach-Limit erreicht ist, bekam beim Anlegen
// eine Fehlermeldung, sah aber auf der Quota-Übersicht keine Postfach-Zeile,
// die das erklärt hätte.
func TestQuotaStatusZeigtPostfaecher(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sys := store.SystemScope()

	tenant, _, _ := env.seedSite(t, "alice")
	seedPlan(t, env, tenant, &store.Plan{Name: "Klein", MaxMailboxes: 5})

	dom := &store.MailDomain{TenantID: tenant.ID, Domain: "alice.at", Active: true}
	if err := env.store.CreateMailDomain(ctx, sys, dom); err != nil {
		t.Fatal(err)
	}
	if err := env.store.CreateMailbox(ctx, sys, &store.Mailbox{
		TenantID: tenant.ID, DomainID: dom.ID, LocalPart: "post",
		PasswordEnc: "verschlüsselt", QuotaMB: 100, Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	quota := NewQuotaService(env.store, env.agent, env.cfg, nil)
	status, err := quota.Status(ctx, sys, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Usage.Mailboxes != 1 {
		t.Errorf("Usage.Mailboxes = %d, erwartet 1", status.Usage.Mailboxes)
	}

	var gefunden bool
	for _, e := range status.Entries {
		if e.Resource == ResourceMailboxes {
			gefunden = true
			if e.Used != 1 || e.Limit != 5 {
				t.Errorf("Postfach-Eintrag: used=%d limit=%d, erwartet 1/5", e.Used, e.Limit)
			}
		}
	}
	if !gefunden {
		t.Fatal("kein Eintrag für ResourceMailboxes in der Quota-Übersicht")
	}
}

// TestQuotaTenantIsolation: der Verbrauch eines fremden Tenants bleibt verborgen.
func TestQuotaTenantIsolation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	alice, _, _ := env.seedSite(t, "alice")
	bob, bobUser, _ := env.seedSite(t, "bob")
	bobScope := store.UserScope(bobUser.ID, bob.ID, store.RoleCustomer)

	quota := NewQuotaService(env.store, env.agent, env.cfg, nil)
	if _, err := quota.Status(ctx, bobScope, alice.ID); err == nil {
		t.Fatal("Bob konnte Alices Verbrauch abrufen")
	}

	// Der eigene geht.
	if _, err := quota.Status(ctx, bobScope, bob.ID); err != nil {
		t.Fatalf("eigener Verbrauch nicht abrufbar: %v", err)
	}
}
