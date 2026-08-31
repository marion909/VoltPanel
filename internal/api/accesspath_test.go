package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/metrics"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/webui"
)

// requireFrontend überspringt, wenn kein gebautes Frontend eingebettet ist.
// `go test ./...` soll auch ohne Node durchlaufen; in CI baut der Testlauf
// das Frontend vorher, damit hier nichts stillschweigend übersprungen wird.
func requireFrontend(t *testing.T) {
	t.Helper()
	if !webui.Built() {
		t.Skip("kein gebautes Frontend eingebettet (make web)")
	}
}

const testAccessPath = "68f5131fbe68d76d9c61588f"

// serverWithAccessPath baut ein Panel, das unter einem Pfadpräfix liegt —
// der Normalfall nach einer Installation, denn install.sh würfelt einen aus.
func serverWithAccessPath(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()

	st, err := store.Open(filepath.Join(dir, "volt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	secrets, err := authn.LoadSecretBox(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir, cfg.SitesDir, cfg.LogDir = dir, filepath.Join(dir, "www"), dir
	cfg.SocketPath = filepath.Join(dir, "nicht-vorhanden.sock")
	cfg.AccessPath = testAccessPath

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(Options{
		Config: cfg, Store: st, Agent: agent.NewClient(cfg.SocketPath),
		Metrics: metrics.New(time.Second, quiet), Secrets: secrets, Logger: quiet,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestBareAccessPathRedirects deckt genau die URL ab, die install.sh am Ende
// ausgibt. Sie endet ohne Schrägstrich und traf damit keine Route: die Gruppe
// registriert "/<präfix>/*", und das verlangt den Schrägstrich.
func TestBareAccessPathRedirects(t *testing.T) {
	srv := serverWithAccessPath(t)

	rec := get(t, srv, "/"+testAccessPath)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status %d, erwartet 301 — die URL aus der Installationsausgabe muss ankommen", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/"+testAccessPath+"/" {
		t.Errorf("Location %q, erwartet %q", loc, "/"+testAccessPath+"/")
	}
}

// TestIndexCarriesBaseTag hält fest, wie der Pfadpräfix im Browser ankommt.
// Ohne das Tag laden Assets und API-Aufrufe an ihm vorbei — die App bliebe
// weiß, obwohl der Server einwandfrei antwortet.
func TestIndexCarriesBaseTag(t *testing.T) {
	requireFrontend(t)
	srv := serverWithAccessPath(t)

	rec := get(t, srv, "/"+testAccessPath+"/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, erwartet 200", rec.Code)
	}

	want := `<base href="/` + testAccessPath + `/">`
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("index.html trägt %s nicht:\n%s", want, truncateBody(rec.Body.String()))
	}
}

// TestDeepRouteAlsoCarriesBase: eine Unterseite liegt tiefer im Pfad. Ohne
// <base> lösten relative Adressen von dort gegen das falsche Verzeichnis auf.
func TestDeepRouteAlsoCarriesBase(t *testing.T) {
	requireFrontend(t)
	srv := serverWithAccessPath(t)

	rec := get(t, srv, "/"+testAccessPath+"/sites/5")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, erwartet 200 (SPA-Fallback)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<base href="/`+testAccessPath+`/">`) {
		t.Error("Unterseite ohne <base> — Assets zeigten von dort ins Leere")
	}
}

// TestRootStaysClosed: der zufällige Pfad soll das Panel aus den Trefferlisten
// von Portscannern heraushalten. Auf der Wurzel darf deshalb nichts stehen.
func TestRootStaysClosed(t *testing.T) {
	srv := serverWithAccessPath(t)

	if rec := get(t, srv, "/"); rec.Code == http.StatusOK {
		t.Error("die Wurzel antwortet mit 200 — der Pfadpräfix verbirgt dann nichts")
	}
}

func truncateBody(s string) string {
	if len(s) > 400 {
		return s[:400] + " …"
	}
	return s
}
