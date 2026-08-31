package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestCheckExtensionRejectsAnythingButAModuleName ist die eigentliche
// Schranke dieser Operation: der Agent installiert über apt, und ohne sie
// wäre das ein Weg, ein beliebiges Paket auf den Server zu holen.
func TestCheckExtensionRejectsAnythingButAModuleName(t *testing.T) {
	bad := []string{
		"",                 // leer
		"openssh-server",   // Bindestrich: würde php8.3-openssh-server ergeben
		"redis;rm -rf /",   // Trennzeichen
		"../../etc/passwd", // Pfad
		"Redis",            // Großbuchstaben
		"8redis",           // Ziffer vorn
		"reallylongextensionname12345",
	}
	for _, name := range bad {
		if err := checkExtension(name); err == nil {
			t.Errorf("%q wurde zugelassen", name)
		}
	}

	for _, name := range []string{"redis", "imagick", "bcmath", "gd", "sqlite3"} {
		if err := checkExtension(name); err != nil {
			t.Errorf("%q wurde abgelehnt: %v", name, err)
		}
	}
}

// TestIniNamesStripsTheLoadOrder: Debian benennt die Dateien "20-redis.ini".
// Die Ziffern sind die Ladereihenfolge und gehören nicht zum Modulnamen.
func TestIniNamesStripsTheLoadOrder(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"20-redis.ini", "10-opcache.ini", "imagick.ini", "liesmich.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := iniNames(dir)
	want := []string{"imagick", "opcache", "redis"}
	if len(got) != len(want) {
		t.Fatalf("gefunden %v, erwartet %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("an Position %d steht %q, erwartet %q", i, got[i], want[i])
		}
	}
}

// TestInputErrorsTravelAsInput hält fest, dass die Unterscheidung den Socket
// übersteht. Ohne sie kommt eine abgelehnte Eingabe als Gateway-Fehler beim
// Benutzer an — mit einem Text, der so klingt, als sei der Agent kaputt.
func TestInputErrorsTravelAsInput(t *testing.T) {
	srv := &Server{registry: map[Op]Handler{}}

	srv.registry["test.input"] = func(context.Context, json.RawMessage) (any, error) {
		return nil, opInputErr("test.input", "so nicht")
	}
	srv.registry["test.legacy"] = func(context.Context, json.RawMessage) (any, error) {
		// Die älteren Prüfungen melden errBadInput statt eines OpError.
		return nil, fmt.Errorf("%w: domain %q", errBadInput, "kaputt")
	}
	srv.registry["test.broken"] = func(context.Context, json.RawMessage) (any, error) {
		return nil, opErr("test.broken", "systemd antwortet nicht")
	}
	srv.log = slog.New(slog.NewTextHandler(io.Discard, nil))

	cases := map[Op]bool{
		"test.input":  true,
		"test.legacy": true,
		"test.broken": false,
	}
	for op, wantInput := range cases {
		resp := srv.dispatch(context.Background(), &Request{ID: "1", Op: op})
		if resp.OK {
			t.Fatalf("%s: unerwartet erfolgreich", op)
		}
		if resp.Input != wantInput {
			t.Errorf("%s: Input=%v, erwartet %v", op, resp.Input, wantInput)
		}
	}
}
