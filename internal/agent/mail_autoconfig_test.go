package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// autoconfigTestServer liefert einen Server, dessen autoconfigDir bereits
// existiert — jail() löst seine Wurzeln über EvalSymlinks auf und lehnt jeden
// Pfad ab, solange die Wurzel selbst noch fehlt, genau wie bei sites in
// testServer.
func autoconfigTestServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := testServer(t)
	if err := os.MkdirAll(srv.autoconfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return srv
}

// opMailAutoconfig muss beide Dateien schreiben und ihre Pfade zurückgeben —
// dieselben Pfade, die core.MailService gleich darauf in den Vhost einsetzt.
// Stimmen sie nicht, liefert der Vhost auf eine Datei, die es nicht gibt.
func TestOpMailAutoconfigSchreibtBeideDateien(t *testing.T) {
	srv := autoconfigTestServer(t)

	raw := mustJSON(t, AutoconfigParams{
		Domain: "kunde.example.at", Host: "mail.example.at",
		IMAPPort: 993, SMTPPort: 587,
	})
	res, err := srv.opMailAutoconfig(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	paths, ok := res.(map[string]string)
	if !ok {
		t.Fatalf("unerwarteter rückgabetyp %T", res)
	}

	mozilla, microsoft := paths["mozilla_path"], paths["microsoft_path"]
	if mozilla == "" || microsoft == "" {
		t.Fatalf("pfade fehlen: %+v", paths)
	}
	if !strings.HasPrefix(mozilla, srv.autoconfigDir) || !strings.HasPrefix(microsoft, srv.autoconfigDir) {
		t.Errorf("pfade liegen nicht unter autoconfigDir: %+v", paths)
	}

	mInhalt, err := os.ReadFile(mozilla)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mInhalt), "mail.example.at") || !strings.Contains(string(mInhalt), "993") {
		t.Errorf("mozilla-datei enthält nicht die erwarteten werte:\n%s", mInhalt)
	}

	dInhalt, err := os.ReadFile(microsoft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dInhalt), "mail.example.at") || !strings.Contains(string(dInhalt), "587") {
		t.Errorf("microsoft-datei enthält nicht die erwarteten werte:\n%s", dInhalt)
	}
}

// Ein zweiter Aufruf für dieselbe Domäne überschreibt, statt eine zweite
// Fassung danebenzulegen — Prinzip 2 der Roadmap, jede Aktion idempotent.
func TestOpMailAutoconfigIstIdempotent(t *testing.T) {
	srv := autoconfigTestServer(t)
	params := AutoconfigParams{Domain: "kunde.example.at", Host: "alt.example.at", IMAPPort: 993, SMTPPort: 587}

	res1, err := srv.opMailAutoconfig(t.Context(), mustJSON(t, params))
	if err != nil {
		t.Fatal(err)
	}
	params.Host = "neu.example.at"
	res2, err := srv.opMailAutoconfig(t.Context(), mustJSON(t, params))
	if err != nil {
		t.Fatal(err)
	}

	p1 := res1.(map[string]string)
	p2 := res2.(map[string]string)
	if p1["mozilla_path"] != p2["mozilla_path"] {
		t.Errorf("zweiter aufruf ergab einen anderen pfad: %q vs %q", p1["mozilla_path"], p2["mozilla_path"])
	}
	inhalt, err := os.ReadFile(p2["mozilla_path"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(inhalt), "neu.example.at") {
		t.Error("die datei zeigt noch den alten host")
	}
	if strings.Contains(string(inhalt), "alt.example.at") {
		t.Error("die alte fassung steht noch mit drin")
	}
}

// checkDomain muss feuern, bevor irgendetwas geschrieben wird — sonst ließe
// sich über den Domainnamen ein Verzeichnis außerhalb von autoconfigDir
// ansprechen. Gegenprobe: ohne die Prüfung entstünde hier ein Verzeichnis;
// mit ihr bleibt autoconfigDir leer.
func TestOpMailAutoconfigLehntUngueltigeDomaeneAb(t *testing.T) {
	srv, _ := testServer(t)
	raw := mustJSON(t, AutoconfigParams{
		Domain: "../../etc/passwd", Host: "mail.example.at", IMAPPort: 993, SMTPPort: 587,
	})
	if _, err := srv.opMailAutoconfig(t.Context(), raw); err == nil {
		t.Fatal("eine domäne mit '..' wurde angenommen")
	}
	eintraege, _ := os.ReadDir(srv.autoconfigDir)
	if len(eintraege) != 0 {
		t.Errorf("trotz abgelehnter domäne ist etwas entstanden: %+v", eintraege)
	}
}

func TestOpMailAutoconfigLehntUngueltigenHostAb(t *testing.T) {
	srv, _ := testServer(t)
	raw := mustJSON(t, AutoconfigParams{
		Domain: "kunde.example.at", Host: "mail with spaces", IMAPPort: 993, SMTPPort: 587,
	})
	if _, err := srv.opMailAutoconfig(t.Context(), raw); err == nil {
		t.Fatal("ein host mit leerzeichen wurde angenommen")
	}
}

// jail() sorgt dafür, dass eine Domäne nicht über einen aufgelösten Symlink
// aus autoconfigDir hinausführt — dieselbe Prüfung wie beim Zertifikat.
func TestOpMailAutoconfigDateienBleibenImVerzeichnis(t *testing.T) {
	srv := autoconfigTestServer(t)
	raw := mustJSON(t, AutoconfigParams{
		Domain: "kunde.example.at", Host: "mail.example.at", IMAPPort: 993, SMTPPort: 587,
	})
	res, err := srv.opMailAutoconfig(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	paths := res.(map[string]string)
	want := filepath.Join(srv.autoconfigDir, "kunde.example.at", "config-v1.1.xml")
	if paths["mozilla_path"] != want {
		t.Errorf("mozilla_path = %q, erwartet %q", paths["mozilla_path"], want)
	}
}
