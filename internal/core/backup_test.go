package core

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// TestRestoreErsetztDenVorherigenStand deckt zweierlei ab: dass Restore
// tatsächlich auf den Stand zum Zeitpunkt von Create zurückspielt (nicht bloß
// eine zweite Kopie danebenlegt), und dass dabei keine .tmp-Datei liegen
// bleibt — Restore schreibt seit diesem Fix über eine temporäre Datei plus
// os.Rename an s.cfg.DBPath, statt die scharfe volt.db direkt zu überschreiben.
func TestRestoreErsetztDenVorherigenStand(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "volt.db")
	ctx := context.Background()
	sys := store.SystemScope()

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.CreateTenant(ctx, sys, &store.Tenant{Name: "vor-backup", Slug: "vor-backup"}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.BackupDir, cfg.DBPath = filepath.Join(dir, "backups"), dbPath
	svc := NewBackupService(cfg, st, slog.New(slog.DiscardHandler), nil)

	res, err := svc.Create(ctx, CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Nach dem Backup angelegt — muss durch Restore wieder verschwinden.
	if err := st.CreateTenant(ctx, sys, &store.Tenant{Name: "nach-backup", Slug: "nach-backup"}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Restore(ctx, res.Path); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if _, err := os.Stat(dbPath + ".tmp"); err == nil {
		t.Fatal("Restore hat eine .tmp-Datei zurückgelassen")
	}

	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("wiederhergestellte datenbank öffnen: %v", err)
	}
	defer st2.Close()

	tenants, err := st2.ListTenants(ctx, sys)
	if err != nil {
		t.Fatal(err)
	}
	var haveVor, haveNach bool
	for _, ten := range tenants {
		switch ten.Slug {
		case "vor-backup":
			haveVor = true
		case "nach-backup":
			haveNach = true
		}
	}
	if !haveVor {
		t.Error("Tenant von vor dem Backup fehlt nach dem Restore")
	}
	if haveNach {
		t.Error("Tenant von nach dem Backup ist nach dem Restore noch da — Restore hat nicht ersetzt")
	}
}
