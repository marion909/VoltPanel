package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeFakeMysqldump legt ein ausführbares Shell-Skript an und hängt
// allowedBinaries["mysqldump"] testweise daran — echtes mysqldump und ein
// laufendes MariaDB sind für diesen Test nicht nötig, nur sein Exitcode und
// das, was es auf stdout schreibt.
func writeFakeMysqldump(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mysqldump")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	original := allowedBinaries["mysqldump"]
	t.Cleanup(func() { allowedBinaries["mysqldump"] = original })
	allowedBinaries["mysqldump"] = path
}

func noLeftoverTempFiles(t *testing.T, dir string) {
	t.Helper()
	reste, err := filepath.Glob(filepath.Join(dir, ".mysqldump-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reste) != 0 {
		t.Fatalf("temporäre Dumpdateien blieben liegen: %v", reste)
	}
}

// TestOpMySQLDumpLaesstAltenDumpBeiFehlschlagUnangetastet deckt die Lücke ab,
// die opMySQLDump vorher hatte: die Zieldatei wurde mit O_TRUNC sofort
// geleert, bevor mysqldump lief — schlug der Dump danach fehl, blieb eine
// leere Datei zurück statt des vorherigen funktionierenden Dumps.
func TestOpMySQLDumpLaesstAltenDumpBeiFehlschlagUnangetastet(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{roots: []string{dir}}
	dest := filepath.Join(dir, "dump.sql")
	if err := os.WriteFile(dest, []byte("alter-stand"), 0o600); err != nil {
		t.Fatal(err)
	}

	writeFakeMysqldump(t, "printf 'unwichtig'\nexit 1\n")

	raw, err := json.Marshal(MySQLDumpParams{Database: "kunde_shop", Path: dest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.opMySQLDump(context.Background(), raw); err == nil {
		t.Fatal("opMySQLDump lieferte keinen Fehler, obwohl mysqldump scheiterte")
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alter-stand" {
		t.Fatalf("dest wurde trotz Fehlschlag verändert: %q", got)
	}
	noLeftoverTempFiles(t, dir)
}

// TestOpMySQLDumpUebernimmtDenNeuenDumpBeiErfolg hält den Erfolgsfall
// dagegen: der neue Inhalt landet an dest, und keine Temp-Datei bleibt liegen.
func TestOpMySQLDumpUebernimmtDenNeuenDumpBeiErfolg(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{roots: []string{dir}}
	dest := filepath.Join(dir, "dump.sql")

	writeFakeMysqldump(t, "printf 'neuer-dump'\n")

	raw, err := json.Marshal(MySQLDumpParams{Database: "kunde_shop", Path: dest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.opMySQLDump(context.Background(), raw); err != nil {
		t.Fatalf("opMySQLDump: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "neuer-dump" {
		t.Fatalf("dest = %q, erwartet %q", got, "neuer-dump")
	}
	noLeftoverTempFiles(t, dir)
}
