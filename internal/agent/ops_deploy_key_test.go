package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureDeployDirGibtSiteUidExecuteRecht deckt die Lücke ab, die
// opDeployKey vorher hatte: das Schlüsselverzeichnis entstand mit 0750 —
// gehört root:root, aber der ssh/git-Aufruf läuft per runAsUser unter der
// Site-UID. Ohne das Execute-Bit für "other" scheiterte jeder SSH-basierte
// Git-Deploy mit "Permission denied", obwohl die Schlüsseldatei selbst
// korrekt 0600 der Site-UID gehörte. Anders als setgid/setuid (siehe
// TestMkdirKeepsSetgidAndDropsSetuid) sind einfache rwx-Bits plattform-
// unabhängiges POSIX-Verhalten — kein Linux-Skip nötig.
func TestEnsureDeployDirGibtSiteUidExecuteRecht(t *testing.T) {
	t.Run("frisch angelegt", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "deploy")
		if err := ensureDeployDir(dir); err != nil {
			t.Fatal(err)
		}
		assertMode(t, dir, 0o751)
	})

	t.Run("bereits vorhanden mit dem alten 0750", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := ensureDeployDir(dir); err != nil {
			t.Fatal(err)
		}
		// MkdirAll allein hätte ein bereits bestehendes Verzeichnis bei 0750
		// belassen — genau das Szenario nach einem Upgrade dieses Fixes.
		assertMode(t, dir, 0o751)
	})
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s hat Modus %o, erwartet %o", path, got, want)
	}
}
