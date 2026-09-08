package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// TestCreateLiefertGroesseUndPruefsummeDesTatsaechlichenArchivs: Create und
// ExportTenant teilen sich seit dieser Umstellung den archiveWriter-Helfer
// (Datei + sha256-Hasher + gzip.Writer + tar.Writer). Dieser Test sichert
// ab, dass Result.SizeBytes/Checksum weiterhin zur tatsächlich geschriebenen
// Datei passen, nicht nur zu irgendeinem Wert.
func TestCreateLiefertGroesseUndPruefsummeDesTatsaechlichenArchivs(t *testing.T) {
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
	if err := st.CreateTenant(ctx, sys, &store.Tenant{Name: "pruefsumme", Slug: "pruefsumme"}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.BackupDir, cfg.DBPath = filepath.Join(dir, "backups"), dbPath
	svc := NewBackupService(cfg, st, slog.New(slog.DiscardHandler), nil)

	res, err := svc.Create(ctx, CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) != res.SizeBytes {
		t.Errorf("SizeBytes = %d, tatsächliche Dateigröße = %d", res.SizeBytes, len(raw))
	}
	sum := sha256.Sum256(raw)
	if want := hex.EncodeToString(sum[:]); res.Checksum != want {
		t.Errorf("Checksum = %s, tatsächliche Prüfsumme = %s", res.Checksum, want)
	}
}

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

// TestRestoreLehntArchivOhneVoltDBAb: Restore lief seit der Umstellung auf
// eachEntry über dessen Callback statt über eine eigene Schleife — dieser
// Test hält fest, dass ein Archiv ohne volt.db weiterhin abgelehnt wird und
// die laufende Datenbank dabei nicht angefasst (insbesondere nicht
// geschlossen) wird.
func TestRestoreLehntArchivOhneVoltDBAb(t *testing.T) {
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
	if err := st.CreateTenant(ctx, sys, &store.Tenant{Name: "bleibt", Slug: "bleibt"}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.BackupDir, cfg.DBPath = filepath.Join(dir, "backups"), dbPath
	svc := NewBackupService(cfg, st, slog.New(slog.DiscardHandler), nil)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("nicht die datenbank")
	if err := tw.WriteHeader(&tar.Header{
		Name: "config.yaml", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	archive := filepath.Join(dir, "ohne-voltdb.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.Restore(ctx, archive); err == nil {
		t.Fatal("Restore hat ein Archiv ohne volt.db angenommen")
	}

	// Die laufende Datenbank muss dabei unangetastet geblieben sein: weder
	// geschlossen (Close() steht erst nach dem Fund von volt.db) noch
	// überschrieben.
	if _, err := st.ListTenants(ctx, sys); err != nil {
		t.Fatalf("Datenbank nach abgelehntem Restore nicht mehr benutzbar: %v", err)
	}
}
