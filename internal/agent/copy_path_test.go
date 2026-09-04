package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyPathUeberspringtSymlinks deckt die Lücke ab, die copyPath vorher
// hatte: ein Symlink wurde 1:1 nachgebaut (os.Readlink → os.Symlink
// unverändert), ohne das Linkziel zu prüfen — anders als die Archiv-
// Extraktion in derselben Datei, die Symlinks konsequent überspringt statt
// sie zu übernehmen. Ein bereits im Jail liegender, nach außen zeigender
// Symlink hätte sich damit an einen vom Aufrufer gewählten Ort duplizieren
// lassen.
func TestCopyPathUeberspringtSymlinks(t *testing.T) {
	t.Run("einzelner symlink als quelle", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "link")
		if err := os.Symlink("/etc/passwd", link); err != nil {
			t.Fatal(err)
		}
		to := filepath.Join(dir, "kopie")

		if err := copyPath(link, to); err != nil {
			t.Fatalf("copyPath: %v", err)
		}
		if _, err := os.Lstat(to); err == nil {
			t.Fatal("copyPath hat den Symlink an das Ziel dupliziert")
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	})

	t.Run("verzeichnis mit symlink darin", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "quelle")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "echt.txt"), []byte("inhalt"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc/shadow", filepath.Join(src, "boese.link")); err != nil {
			t.Fatal(err)
		}

		to := filepath.Join(t.TempDir(), "ziel")
		if err := copyPath(src, to); err != nil {
			t.Fatalf("copyPath: %v", err)
		}

		if got, err := os.ReadFile(filepath.Join(to, "echt.txt")); err != nil || string(got) != "inhalt" {
			t.Fatalf("reguläre Datei wurde nicht mitkopiert: %v, %q", err, got)
		}
		if _, err := os.Lstat(filepath.Join(to, "boese.link")); err == nil {
			t.Fatal("copyPath hat den Symlink im Baum an das Ziel dupliziert")
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	})
}
