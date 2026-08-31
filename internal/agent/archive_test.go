package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSafeJoinBlocksZipSlip: ein Archiv bestimmt die Pfade seiner Einträge
// selbst. Ohne diese Prüfung schreibt ein hochgeladenes Archiv mit
// "../../etc/cron.d/x" außerhalb des Zielverzeichnisses — als root.
func TestSafeJoinBlocksZipSlip(t *testing.T) {
	const dest = "/var/www/example.at/uploads"

	// Diese Namen dürfen niemals außerhalb des Ziels landen — ob sie dabei
	// abgelehnt oder ins Ziel normalisiert werden, ist beides sicher.
	dangerous := []string{
		"../../../etc/cron.d/evil",
		"..\\..\\..\\etc\\passwd",
		"/etc/shadow",
		"a/../../../../etc/hosts",
		"datei\x00.txt",
		strings.Repeat("../", 40) + "etc/passwd",
	}
	for _, name := range dangerous {
		got, err := safeJoin(dest, name)
		if err != nil {
			continue // erwartet
		}
		if got != dest && !strings.HasPrefix(got, dest+string(filepath.Separator)) {
			t.Errorf("safeJoin(%q, %q) = %q liegt außerhalb des Ziels", dest, name, got)
		}
	}

	allowed := map[string]string{
		"index.html":        dest + "/index.html",
		"a/b/c.txt":         dest + "/a/b/c.txt",
		"./index.html":      dest + "/index.html",
		"a/../b.txt":        dest + "/b.txt",
		"ordner/":           dest + "/ordner",
		"datei mit raum.md": dest + "/datei mit raum.md",
	}
	for name, want := range allowed {
		got, err := safeJoin(dest, name)
		if err != nil {
			t.Errorf("safeJoin(%q, %q) verweigert: %v", dest, name, err)
			continue
		}
		if got != want {
			t.Errorf("safeJoin(%q, %q) = %q, erwartet %q", dest, name, got, want)
		}
	}
}

// TestExtractTarGzRejectsEscape baut ein bösartiges Archiv und prüft, dass beim
// Entpacken nichts außerhalb landet.
func TestExtractTarGzRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.tar.gz")
	dest := filepath.Join(dir, "ziel")
	outside := filepath.Join(dir, "uebernommen.txt")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{
		Name: "../uebernommen.txt", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	// safeJoin normalisiert "../x" auf "x" innerhalb des Ziels, statt zu
	// scheitern — genau wie tar es tut. Entscheidend ist nicht, dass ein
	// Fehler kommt, sondern dass nichts außerhalb landet.
	if _, err := extractTarGz(t.Context(), archive, dest); err != nil {
		t.Logf("extractTarGz meldete: %v", err)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("das Archiv hat eine Datei außerhalb des Zielverzeichnisses geschrieben")
	}
	// Der Eintrag muss im Ziel gelandet sein, nicht verschwunden.
	if _, err := os.Stat(filepath.Join(dest, "uebernommen.txt")); err != nil {
		t.Fatalf("der Eintrag wurde weder im Ziel abgelegt noch abgelehnt: %v", err)
	}
}

func TestExtractZipRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.zip")
	dest := filepath.Join(dir, "ziel")
	outside := filepath.Join(dir, "uebernommen.txt")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../uebernommen.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("pwned")); err != nil {
		t.Fatal(err)
	}
	zw.Close()

	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := extractZip(t.Context(), archive, dest); err != nil {
		t.Logf("extractZip meldete: %v", err)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("das Archiv hat eine Datei außerhalb des Zielverzeichnisses geschrieben")
	}
	if _, err := os.Stat(filepath.Join(dest, "uebernommen.txt")); err != nil {
		t.Fatalf("der Eintrag wurde weder im Ziel abgelegt noch abgelehnt: %v", err)
	}
}

// TestArchiveRoundTrip: packen und entpacken muss den Inhalt erhalten.
func TestArchiveRoundTrip(t *testing.T) {
	client, sitesDir := startTestAgent(t)
	ctx := t.Context()

	src := filepath.Join(sitesDir, "example.at", "public")
	files := map[string]string{
		"index.html":     "<h1>start</h1>",
		"css/app.css":    "body{margin:0}",
		"js/app.js":      "console.log(1)",
		"leer.txt":       "",
		"tief/er/x.json": `{"a":1}`,
	}
	for name, content := range files {
		if err := client.WriteFile(ctx, filepath.Join(src, name), content, 0o644, ""); err != nil {
			t.Fatal(err)
		}
	}

	for _, ext := range []string{".tar.gz", ".zip"} {
		t.Run(ext, func(t *testing.T) {
			archive := filepath.Join(sitesDir, "sicherung"+ext)
			if _, err := client.Archive(ctx, []string{src}, archive, ""); err != nil {
				t.Fatalf("Archive: %v", err)
			}

			dest := filepath.Join(sitesDir, "wieder"+strings.ReplaceAll(ext, ".", "_"))
			if _, err := client.Extract(ctx, archive, dest, ""); err != nil {
				t.Fatalf("Extract: %v", err)
			}

			for name, want := range files {
				got, err := client.ReadFile(ctx, filepath.Join(dest, "public", name))
				if err != nil {
					t.Errorf("%s fehlt nach dem Entpacken: %v", name, err)
					continue
				}
				if got != want {
					t.Errorf("%s = %q, erwartet %q", name, got, want)
				}
			}
		})
	}
}

// TestChunkedTransfer prüft das blockweise Lesen und Schreiben — die Grundlage
// für Up- und Download großer Dateien.
func TestChunkedTransfer(t *testing.T) {
	client, sitesDir := startTestAgent(t)
	ctx := t.Context()
	path := filepath.Join(sitesDir, "gross.bin")

	// Etwas über zwei Blöcke, damit der letzte Block angebrochen ist.
	payload := make([]byte, ChunkSize*2+1234)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	var offset int64
	for offset < int64(len(payload)) {
		end := min(offset+ChunkSize, int64(len(payload)))
		if err := client.WriteChunk(ctx, path, offset, payload[offset:end], "", offset == 0); err != nil {
			t.Fatalf("WriteChunk bei %d: %v", offset, err)
		}
		offset = end
	}

	var got []byte
	offset = 0
	for {
		chunk, err := client.ReadChunk(ctx, path, offset, ChunkSize)
		if err != nil {
			t.Fatalf("ReadChunk bei %d: %v", offset, err)
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, data...)
		offset += int64(len(data))
		if chunk.EOF || len(data) == 0 {
			break
		}
	}

	if !bytes.Equal(got, payload) {
		t.Fatalf("zurückgelesene Daten weichen ab: %d von %d bytes", len(got), len(payload))
	}
}

// TestChunkedTransferRespectsJail: auch die Blockoperationen dürfen nicht aus
// den erlaubten Verzeichnissen herausführen.
func TestChunkedTransferRespectsJail(t *testing.T) {
	client, _ := startTestAgent(t)
	ctx := t.Context()

	for _, path := range []string{"/etc/passwd", "/tmp/../etc/shadow", "relativ.txt"} {
		if _, err := client.ReadChunk(ctx, path, 0, 1024); err == nil {
			t.Errorf("ReadChunk hat %q erlaubt", path)
		}
		if err := client.WriteChunk(ctx, path, 0, []byte("x"), "", true); err == nil {
			t.Errorf("WriteChunk hat %q erlaubt", path)
		}
	}
}

func TestMoveAndCopyStayInJail(t *testing.T) {
	client, sitesDir := startTestAgent(t)
	ctx := t.Context()

	src := filepath.Join(sitesDir, "quelle.txt")
	if err := client.WriteFile(ctx, src, "inhalt", 0o644, ""); err != nil {
		t.Fatal(err)
	}

	// Innerhalb erlaubt.
	dst := filepath.Join(sitesDir, "unterordner", "ziel.txt")
	if err := client.CopyPath(ctx, src, dst, false); err != nil {
		t.Fatalf("CopyPath innerhalb: %v", err)
	}
	if got, err := client.ReadFile(ctx, dst); err != nil || got != "inhalt" {
		t.Fatalf("kopierte Datei = %q, %v", got, err)
	}

	// Nach draußen nicht — weder als Quelle noch als Ziel.
	for _, outside := range []string{"/tmp/entkommen.txt", "/etc/entkommen"} {
		if err := client.MovePath(ctx, src, outside, true); err == nil {
			t.Errorf("MovePath nach %q wurde erlaubt", outside)
		}
		if err := client.CopyPath(ctx, src, outside, true); err == nil {
			t.Errorf("CopyPath nach %q wurde erlaubt", outside)
		}
		if err := client.MovePath(ctx, outside, dst, true); err == nil {
			t.Errorf("MovePath von %q wurde erlaubt", outside)
		}
	}
}
