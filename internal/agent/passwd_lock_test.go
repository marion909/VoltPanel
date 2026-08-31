package agent

import (
	"os"
	"strings"
	"testing"
)

// TestStaleLockHintNamesTheFiles: die Meldung muss den Pfad nennen und sagen,
// was zu tun ist. "try again later" hat genau das nicht getan.
func TestStaleLockHintNamesTheFiles(t *testing.T) {
	dir := t.TempDir()

	orig := passwdLockFiles
	t.Cleanup(func() { passwdLockFiles = orig })
	passwdLockFiles = []string{dir + "/passwd.lock", dir + "/group.lock"}

	if hint := staleLockHint(); hint != "" {
		t.Fatalf("ohne Sperrdateien sollte nichts gemeldet werden, kam: %q", hint)
	}

	if err := os.WriteFile(dir+"/passwd.lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	hint := staleLockHint()
	switch {
	case hint == "":
		t.Fatal("eine liegengebliebene Sperrdatei wurde nicht gemeldet")
	case !strings.Contains(hint, "passwd.lock"):
		t.Errorf("die Meldung nennt die Datei nicht: %q", hint)
	case !strings.Contains(hint, "rm -f"):
		t.Errorf("die Meldung sagt nicht, wie man sie loswird: %q", hint)
	}
}

// TestDiagnosisPrefersTheWriteProblem: ist /etc schreibgeschützt, ist das der
// Grund — eine Sperrdatei könnte dort gar nicht erst entstehen. Die Reihenfolge
// der Prüfungen entscheidet, ob der Hinweis in die richtige Richtung zeigt.
func TestDiagnosisIsAlwaysSpecific(t *testing.T) {
	d := diagnoseUserLock()
	if d == "" {
		t.Fatal("die Diagnose darf nie leer sein — sonst bleibt nur 'try again later'")
	}
	if !strings.Contains(d, "/etc") && !strings.Contains(d, "Sperrdatei") && !strings.Contains(d, "pwd.lock") {
		t.Errorf("die Diagnose nennt keine der möglichen Ursachen: %q", d)
	}
}
