package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// TestImportKommtDurchDieWurzelpruefung geht den echten Weg über den Socket.
//
// MariaDB läuft in der CI nicht, der Import scheitert also — die Frage ist
// nur, woran. Scheitert er an jail(), liegt das Sicherungsverzeichnis nicht in
// den Wurzeln des Agents, und die Operation wäre auf einem echten Server
// genauso unerreichbar wie vorher. Genau das war der Fehler.
func TestImportKommtDurchDieWurzelpruefung(t *testing.T) {
	env := newTestEnv(t)
	ctx, sys := context.Background(), store.SystemScope()

	tenant, _, _ := env.seedSite(t, "impo")
	db := &store.Database{
		TenantID: tenant.ID, Name: "impo_test", Engine: "mysql",
		Charset: "utf8mb4", Collation: "utf8mb4_unicode_ci",
	}
	if err := env.store.CreateDatabase(ctx, sys, db); err != nil {
		t.Fatal(err)
	}

	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	if _, err := gz.Write([]byte("SELECT 1;\n")); err != nil {
		t.Fatal(err)
	}
	gz.Close()

	svc := NewDatabaseService(env.store, env.agent, env.cfg, nil)
	_, err := svc.Import(ctx, sys, db.ID, bytes.NewReader(packed.Bytes()))
	if err == nil {
		return // auf einer Maschine mit laufender MariaDB ist das der Erfolgsfall
	}
	for _, verboten := range []string{"außerhalb der erlaubten", "nicht erlaubt"} {
		if strings.Contains(err.Error(), verboten) {
			t.Fatalf("der Pfad kam nicht durch jail(): %v", err)
		}
	}
	t.Logf("erwarteter Fehler ohne MariaDB: %v", err)

	// Die Zwischendatei darf nicht liegen bleiben — sie enthält Kundendaten.
	entries, err := os.ReadDir(filepath.Join(env.backupDir, "imports"))
	if err != nil {
		t.Fatalf("import-verzeichnis: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d Datei(en) im Import-Verzeichnis liegen geblieben", len(entries))
	}
}
