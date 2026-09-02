package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/release"
	"github.com/marion909/voltpanel/internal/store"
)

// updateEnv stellt zwei "Binaries" und einen Server bereit, der ihre
// Nachfolger ausliefert.
type updateEnv struct {
	updater   *Updater
	release   *Release
	voltPath  string
	agentPath string

	mux     *http.ServeMux
	baseURL string
	extra   []string
}

// serveText liefert beliebigen Inhalt über den Testserver aus und gibt das
// passende Asset zurück — für Unit-Dateien im Fahrplan.
func (e *updateEnv) serveText(t *testing.T, content string) ReleaseAsset {
	t.Helper()
	path := "/unit" + strconv.Itoa(len(e.extra))
	e.extra = append(e.extra, content)
	body := content
	e.mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	})
	return ReleaseAsset{URL: e.baseURL + path, SHA256: sum([]byte(content)), Size: int64(len(content))}
}

func newUpdateEnv(t *testing.T, serveAgent bool) *updateEnv {
	t.Helper()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "etc"), 0o750); err != nil {
		t.Fatal(err)
	}

	voltPath := filepath.Join(binDir, "volt")
	agentPath := filepath.Join(binDir, "volt-agent")
	write(t, voltPath, "alt-volt")
	write(t, agentPath, "alt-agent")

	newVolt, newAgent := []byte("neu-volt"), []byte("neu-agent")
	mux := http.NewServeMux()
	mux.HandleFunc("/volt", func(w http.ResponseWriter, _ *http.Request) { w.Write(newVolt) })
	if serveAgent {
		mux.HandleFunc("/agent", func(w http.ResponseWriter, _ *http.Request) { w.Write(newAgent) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, err := store.Open(filepath.Join(dir, "volt.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config.Default()
	cfg.DataDir, cfg.ConfigDir = dir, filepath.Join(dir, "etc")
	cfg.BackupDir, cfg.DBPath = filepath.Join(dir, "backups"), filepath.Join(dir, "volt.db")

	u := NewUpdater(cfg, st, slog.New(slog.DiscardHandler))
	u.self = voltPath

	rel := &Release{
		Version: "9.9.9",
		Assets: map[string]ReleaseAsset{
			Platform(): {
				URL: srv.URL + "/volt", SHA256: sum(newVolt), Size: int64(len(newVolt)),
				Agent: &ReleaseAsset{
					URL: srv.URL + "/agent", SHA256: sum(newAgent), Size: int64(len(newAgent)),
				},
			},
		},
	}

	return &updateEnv{
		updater: u, release: rel, voltPath: voltPath, agentPath: agentPath,
		mux: mux, baseURL: srv.URL,
	}
}

// TestApplySwapsBothBinaries hält fest, worum es geht: ein neues Panel mit
// altem Agent sprechen irgendwann verschiedene Protokolle.
func TestApplySwapsBothBinaries(t *testing.T) {
	env := newUpdateEnv(t, true)
	ctx := context.Background()

	snap, err := env.updater.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.AgentPath == "" {
		t.Fatal("der agent wurde nicht mitgesichert — ein rollback könnte ihn nicht zurückholen")
	}

	if err := env.updater.Apply(ctx, env.release, snap); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if got := read(t, env.voltPath); got != "neu-volt" {
		t.Errorf("volt ist %q, erwartet neu-volt", got)
	}
	if got := read(t, env.agentPath); got != "neu-agent" {
		t.Errorf("agent ist %q, erwartet neu-agent", got)
	}
}

// TestApplyRollsBackWhenAgentIsUnavailable deckt Prinzip 4 der Roadmap ab:
// ein halbes Update ist schlimmer als gar keines.
func TestApplyRollsBackWhenAgentIsUnavailable(t *testing.T) {
	env := newUpdateEnv(t, false) // der Server liefert /agent nicht aus
	ctx := context.Background()

	snap, err := env.updater.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if err := env.updater.Apply(ctx, env.release, snap); err == nil {
		t.Fatal("apply meldete erfolg, obwohl der agent nicht ladbar war")
	}

	if got := read(t, env.voltPath); got != "alt-volt" {
		t.Errorf("volt blieb auf %q — der rollback hat das binary nicht zurückgeholt", got)
	}
	if got := read(t, env.agentPath); got != "alt-agent" {
		t.Errorf("agent ist %q, erwartet alt-agent", got)
	}
}

// TestApplyRejectsManipulatedBinary: die Prüfsumme ist der eigentliche Schutz.
func TestApplyRejectsManipulatedBinary(t *testing.T) {
	env := newUpdateEnv(t, true)
	ctx := context.Background()

	asset := env.release.Assets[Platform()]
	asset.SHA256 = sum([]byte("etwas ganz anderes"))
	env.release.Assets[Platform()] = asset

	snap, err := env.updater.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.updater.Apply(ctx, env.release, snap); err == nil {
		t.Fatal("ein binary mit falscher prüfsumme wurde installiert")
	}
	if got := read(t, env.voltPath); got != "alt-volt" {
		t.Errorf("volt wurde trotz falscher prüfsumme ersetzt (%q)", got)
	}
}

func TestAgentPathNextTo(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "volt")
	write(t, self, "x")

	if got := agentPathNextTo(self); got != "" {
		t.Errorf("ohne agent-datei sollte %q leer sein", got)
	}
	write(t, filepath.Join(dir, "volt-agent"), "y")
	if got := agentPathNextTo(self); got != filepath.Join(dir, "volt-agent") {
		t.Errorf("agent nicht gefunden: %q", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestSameVersionIgnoresTagPrefix(t *testing.T) {
	same := [][2]string{
		{"0.1.4", "v0.1.4"},
		{"v0.1.4", "0.1.4"},
		{" 0.1.4 ", "0.1.4"},
		{"0.1.4", "0.1.4"},
	}
	for _, c := range same {
		if !sameVersion(c[0], c[1]) {
			t.Errorf("%q und %q sollten dieselbe Version sein", c[0], c[1])
		}
	}

	differ := [][2]string{
		{"0.1.4", "0.1.5"},
		{"0.1.4", "0.1.4-beta.1"},
		{"", "0.1.4"},
	}
	for _, c := range differ {
		if sameVersion(c[0], c[1]) {
			t.Errorf("%q und %q sind verschiedene Versionen", c[0], c[1])
		}
	}
}

// signierterKanal baut einen Update-Kanal mit latest.json und Signatur.
func signierterKanal(t *testing.T, u *Updater, manifest string) (sig func(string), stop func()) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	u.verifier = release.NewVerifier(
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	signatur := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/stable/latest.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(manifest))
	})
	mux.HandleFunc("/stable/latest.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		if signatur == "" {
			http.NotFound(w, nil)
			return
		}
		w.Write([]byte(signatur))
	})
	srv := httptest.NewServer(mux)
	u.cfg.UpdateBaseURL, u.cfg.UpdateChannel = srv.URL, "stable"

	return func(body string) {
			d := sha256.Sum256([]byte(body))
			raw, err := ecdsa.SignASN1(crand.Reader, key, d[:])
			if err != nil {
				t.Fatal(err)
			}
			signatur = base64.StdEncoding.EncodeToString(raw)
		}, func() {
			srv.Close()
		}
}

// TestUpdateOhneSignaturWirdAbgelehnt ist der Kern.
//
// Ein Update schreibt das Panel und den Root-Daemon neu. Wer die Angaben dazu
// unterschieben kann, hat den Server. Der Prüfsummenvergleich beim
// Herunterladen hilft dagegen nicht: die Summe steht in derselben Datei, die
// von derselben Adresse kommt.
func TestUpdateOhneSignaturWirdAbgelehnt(t *testing.T) {
	env := newUpdateEnv(t, true)
	manifest := `{"version":"9.9.9","assets":{}}`
	sig, stop := signierterKanal(t, env.updater, manifest)
	defer stop()

	// Ohne Signaturdatei am Kanal: Abbruch, nicht "dann eben ohne".
	if _, err := env.updater.LatestRelease(context.Background()); err == nil {
		t.Error("ein Kanal ohne Signatur wurde angenommen")
	}

	// Mit falscher Signatur: ebenfalls Abbruch.
	sig(`{"version":"1.0.0","assets":{}}`)
	if _, err := env.updater.LatestRelease(context.Background()); err == nil {
		t.Error("eine Signatur über einen anderen Inhalt wurde angenommen")
	}

	// Mit der richtigen: geht durch.
	sig(manifest)
	rel, err := env.updater.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("die eigene Signatur wurde abgelehnt: %v", err)
	}
	if rel.Version != "9.9.9" {
		t.Errorf("Version %q", rel.Version)
	}
}

// TestUpdateOhneSchluesselImBinary: ein Binary ohne Schlüssel kann nicht
// prüfen. Es soll das sagen, nicht stillschweigend aktualisieren.
func TestUpdateOhneSchluesselImBinary(t *testing.T) {
	env := newUpdateEnv(t, true)
	manifest := `{"version":"9.9.9","assets":{}}`
	sig, stop := signierterKanal(t, env.updater, manifest)
	defer stop()
	sig(manifest)

	env.updater.verifier = release.NewVerifier(nil)
	_, err := env.updater.LatestRelease(context.Background())
	if err == nil {
		t.Fatal("ohne Schlüssel wurde das Update angenommen")
	}
	if !strings.Contains(err.Error(), "release-schlüssel") {
		t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
	}
	// Und die Meldung sagt, was zu tun wäre.
	if !strings.Contains(err.Error(), "update_allow_unsigned") {
		t.Errorf("die Meldung nennt den Ausweg nicht: %v", err)
	}
}

// TestUnsigniertNurAufAnsage: die Möglichkeit steht da, weil ein Betreiber mit
// eigenem Kanal vielleicht keinen Schlüssel führt. Sie muss aber ausdrücklich
// gesetzt werden — eine Voreinstellung, die Signaturen überspringt, wäre keine
// Prüfung.
func TestUnsigniertNurAufAnsage(t *testing.T) {
	env := newUpdateEnv(t, true)
	manifest := `{"version":"9.9.9","assets":{}}`
	_, stop := signierterKanal(t, env.updater, manifest)
	defer stop()
	env.updater.verifier = release.NewVerifier(nil)

	if env.updater.cfg.UpdateAllowUnsigned {
		t.Fatal("update_allow_unsigned ist voreingestellt an")
	}
	env.updater.cfg.UpdateAllowUnsigned = true
	if _, err := env.updater.LatestRelease(context.Background()); err != nil {
		t.Errorf("mit update_allow_unsigned schlug es fehl: %v", err)
	}
}
