package api

import (
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.CertDir = t.TempDir()
	cfg.PanelDomain = "panel.example.at"
	return cfg
}

func TestSelfSignedIsCreatedOnce(t *testing.T) {
	cfg := testConfig(t)
	log := slog.New(slog.DiscardHandler)

	if err := ensureSelfSigned(cfg, log); err != nil {
		t.Fatalf("erzeugen: %v", err)
	}

	pair := cfg.SelfSignedPanelCert()
	first, err := os.ReadFile(pair.Cert)
	if err != nil {
		t.Fatalf("zertifikat fehlt: %v", err)
	}

	info, err := os.Stat(pair.Key)
	if err != nil {
		t.Fatalf("schlüssel fehlt: %v", err)
	}
	// Der Schlüssel darf niemandem sonst offenstehen.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("schlüsselrechte %o, erwartet 600", perm)
	}

	// Ein zweiter Aufruf darf das Zertifikat nicht ersetzen: sonst müsste man
	// die Ausnahme im Browser nach jedem Neustart neu bestätigen.
	if err := ensureSelfSigned(cfg, log); err != nil {
		t.Fatalf("zweiter durchlauf: %v", err)
	}
	second, _ := os.ReadFile(pair.Cert)
	if string(first) != string(second) {
		t.Error("zertifikat wurde beim zweiten start ersetzt")
	}
}

func TestSelfSignedCarriesPanelDomain(t *testing.T) {
	cfg := testConfig(t)
	if err := ensureSelfSigned(cfg, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}

	pair := cfg.SelfSignedPanelCert()
	kp, err := tls.LoadX509KeyPair(pair.Cert, pair.Key)
	if err != nil {
		t.Fatalf("paar nicht ladbar: %v", err)
	}
	leaf, err := x509Leaf(kp)
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname(cfg.PanelDomain); err != nil {
		t.Errorf("panel-domain fehlt im zertifikat: %v", err)
	}
	if time.Until(leaf.NotAfter) < 24*time.Hour {
		t.Error("notzertifikat ist bereits abgelaufen")
	}
}

// TestReloaderPrefersRealCert deckt den Fall ab, für den es den Reloader gibt:
// nach `volt cert issue` muss das echte Zertifikat ohne Neustart greifen.
func TestReloaderPrefersRealCert(t *testing.T) {
	cfg := testConfig(t)
	log := slog.New(slog.DiscardHandler)
	if err := ensureSelfSigned(cfg, log); err != nil {
		t.Fatal(err)
	}

	r := &certReloader{chain: cfg.PanelTLSChain, label: "panel", log: log}
	first, err := r.load()
	if err != nil {
		t.Fatalf("erstes laden: %v", err)
	}
	if leaf, _ := x509Leaf(*first); leaf.Issuer.CommonName != cfg.PanelDomain {
		t.Fatalf("erwartet wurde zunächst das selbstsignierte, bekam %q", leaf.Issuer.CommonName)
	}

	// Ein "echtes" Zertifikat unter dem Domainnamen ablegen. Für den Test
	// genügt ein zweites selbstsigniertes an der richtigen Stelle.
	realDir := filepath.Join(cfg.CertDir, cfg.PanelDomain)
	if err := os.MkdirAll(realDir, 0o750); err != nil {
		t.Fatal(err)
	}
	other := config.Default()
	other.CertDir = t.TempDir()
	other.PanelDomain = "echt.example.at"
	if err := ensureSelfSigned(other, log); err != nil {
		t.Fatal(err)
	}
	src := other.SelfSignedPanelCert()
	copyTo(t, src.Cert, filepath.Join(realDir, "fullchain.pem"))
	copyTo(t, src.Key, filepath.Join(realDir, "privkey.pem"))

	second, err := r.load()
	if err != nil {
		t.Fatalf("zweites laden: %v", err)
	}
	leaf, _ := x509Leaf(*second)
	if leaf.Subject.CommonName != "echt.example.at" {
		t.Errorf("reloader blieb beim alten zertifikat (%q)", leaf.Subject.CommonName)
	}
}

// TestReloaderKeepsCertWhenFilesVanish: ein misslungenes Erneuern darf nicht
// dazu führen, dass niemand mehr ins Panel kommt.
func TestReloaderKeepsCertWhenFilesVanish(t *testing.T) {
	cfg := testConfig(t)
	log := slog.New(slog.DiscardHandler)
	if err := ensureSelfSigned(cfg, log); err != nil {
		t.Fatal(err)
	}

	r := &certReloader{chain: cfg.PanelTLSChain, label: "panel", log: log}
	if _, err := r.load(); err != nil {
		t.Fatal(err)
	}

	pair := cfg.SelfSignedPanelCert()
	os.Remove(pair.Cert)
	os.Remove(pair.Key)

	if _, err := r.load(); err != nil {
		t.Errorf("panel wurde unerreichbar, obwohl ein zertifikat im speicher lag: %v", err)
	}
}

func copyTo(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// --- Auslieferung unter einem Pfadpräfix ------------------------------------

func TestFrontendBase(t *testing.T) {
	cases := []struct{ access, want string }{
		{"", "/"},
		{"68f5131f", "/68f5131f/"},
		{"/68f5131f/", "/68f5131f/"},
	}
	for _, tc := range cases {
		cfg := config.Default()
		cfg.AccessPath = tc.access
		s := &Server{cfg: cfg}
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
		if got := s.frontendBase(c); got != tc.want {
			t.Errorf("access_path %q → %q, erwartet %q", tc.access, got, tc.want)
		}
	}
}

// TestBasePfadBleibtAufDerKundendomainVerborgen: der Zugriffspfad ist das
// Geheimnis des Betreibers. Stünde er im <base href>, hinge er an jeder
// Adresse, die die App bildet — und der Kunde kennte den Weg zum Panel des
// Betreibers, obwohl er ihn nie brauchte.
func TestBasePfadBleibtAufDerKundendomainVerborgen(t *testing.T) {
	cfg := config.Default()
	cfg.AccessPath = "68f5131f"
	s := &Server{cfg: cfg}

	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	c.Set(loginTenantKey, &store.Tenant{ID: 2, Name: "bob"})

	if got := s.frontendBase(c); got != "/" {
		t.Errorf("auf der Kundendomain wurde %q ausgeliefert", got)
	}
}
