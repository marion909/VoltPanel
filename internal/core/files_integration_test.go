package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// testEnv ist ein vollständiger Aufbau: echter Agent auf einem Unix-Socket,
// echte SQLite-Datenbank, echte Verzeichnisse.
type testEnv struct {
	store      *store.Store
	agent      *agent.Client
	cfg        *config.Config
	files      *FileService
	sitesDir   string
	systemUser string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// Unix-Socket-Pfade sind auf ~104 Zeichen begrenzt — t.TempDir() liegt
	// auf manchen Systemen zu tief verschachtelt.
	dir, err := os.MkdirTemp("/tmp", "volt-core-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sitesDir := filepath.Join(dir, "www")
	if err := os.MkdirAll(sitesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := agent.NewServer(agent.ServerOptions{
		SocketPath: filepath.Join(dir, "a.sock"),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		SitesDir:   sitesDir,
		NginxDir:   filepath.Join(dir, "nginx"),
		PHPDir:     filepath.Join(dir, "php"),
		CertDir:    filepath.Join(dir, "certs"),
		LogDir:     filepath.Join(dir, "log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(ctx)
	}()

	st, err := store.Open(filepath.Join(dir, "volt.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	client := agent.NewClient(filepath.Join(dir, "a.sock"))
	t.Cleanup(func() {
		client.Close()
		st.Close()
		cancel()
		wg.Wait()
	})

	cfg := config.Default()
	cfg.SitesDir = sitesDir
	cfg.DataDir = dir

	return &testEnv{
		store: st, agent: client, cfg: cfg, sitesDir: sitesDir,
		systemUser: testSystemUser(t),
		files:      NewFileService(st, client, cfg),
	}
}

// testSystemUser ist der Linux-Benutzer, dem die Testdateien gehören.
//
// Der Agent lehnt ein chown auf einen nicht existierenden Benutzer ab — zu
// Recht, denn sonst gehörten die Dateien am Ende root und der PHP-Prozess der
// Site könnte sie nicht mehr schreiben. Für den Test heißt das: es muss ein
// Konto sein, das es auf dieser Maschine wirklich gibt.
func testSystemUser(t *testing.T) string {
	t.Helper()

	current, err := user.Current()
	if err != nil {
		t.Skipf("aktueller Benutzer nicht ermittelbar: %v", err)
	}
	name := current.Username
	// Auf manchen Systemen steht hier "DOMAIN\name" oder Großbuchstaben —
	// beides würde die Namensprüfung des Agents nicht passieren.
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToLower(name)
	if name == "" || name == "root" {
		t.Skip("dieser Test braucht ein gewöhnliches Benutzerkonto")
	}
	return name
}

// seedSite legt Tenant, Benutzer und Site an und erzeugt das Verzeichnis.
func (e *testEnv) seedSite(t *testing.T, slug string) (*store.Tenant, *store.User, *store.Site) {
	t.Helper()
	ctx := context.Background()
	sys := store.SystemScope()

	tenant := &store.Tenant{Name: slug, Slug: slug}
	if err := e.store.CreateTenant(ctx, sys, tenant); err != nil {
		t.Fatal(err)
	}
	user := &store.User{
		TenantID: tenant.ID, Email: slug + "@example.at",
		PasswordHash: "x", Role: store.RoleCustomer,
	}
	if err := e.store.CreateUser(ctx, sys, user); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(e.sitesDir, slug+".example.at")
	site := &store.Site{
		TenantID: tenant.ID, Domain: slug + ".example.at", Type: store.SiteStatic,
		SystemUser: e.systemUser, RootPath: root, DocumentRoot: "public",
	}
	if err := e.store.CreateSite(ctx, sys, site); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	return tenant, user, site
}

// TestFileServiceTenantIsolation ist der eigentliche Punkt dieses Layers:
// Der Agent erlaubt alles unter /var/www — erst hier werden die Kunden getrennt.
func TestFileServiceTenantIsolation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	_, _, aliceSite := env.seedSite(t, "alice")
	bobTenant, bobUser, bobSite := env.seedSite(t, "bob")
	bobScope := store.UserScope(bobUser.ID, bobTenant.ID, store.RoleCustomer)
	sys := store.SystemScope()

	if err := env.files.Write(ctx, sys, aliceSite.ID, "public/config.php",
		"<?php $db_pass = 'geheim';"); err != nil {
		t.Fatal(err)
	}

	t.Run("fremde site_id", func(t *testing.T) {
		if _, err := env.files.List(ctx, bobScope, aliceSite.ID, ""); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Bob konnte Alices Verzeichnis auflisten: %v", err)
		}
		if _, err := env.files.Read(ctx, bobScope, aliceSite.ID, "public/config.php"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Bob konnte Alices Datei lesen: %v", err)
		}
		if err := env.files.Write(ctx, bobScope, aliceSite.ID, "public/x.php", "pwned"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Bob konnte in Alices Site schreiben: %v", err)
		}
		if err := env.files.Remove(ctx, bobScope, aliceSite.ID, "public/config.php", false); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Bob konnte Alices Datei löschen: %v", err)
		}
	})

	t.Run("ausbruch über den relativen pfad", func(t *testing.T) {
		// Bob benutzt seine eigene, gültige site_id und versucht, über den
		// Pfad zu Alice zu kommen.
		escapes := []string{
			"../alice.example.at/public/config.php",
			"public/../../alice.example.at/public/config.php",
			"/../alice.example.at/public/config.php",
		}
		for _, rel := range escapes {
			if content, err := env.files.Read(ctx, bobScope, bobSite.ID, rel); err == nil {
				t.Fatalf("Bob las über %q fremden Inhalt: %q", rel, content)
			}
			if err := env.files.Write(ctx, bobScope, bobSite.ID, rel, "pwned"); err == nil {
				t.Fatalf("Bob schrieb über %q in ein fremdes Verzeichnis", rel)
			}
		}

		got, err := env.files.Read(ctx, sys, aliceSite.ID, "public/config.php")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "geheim") {
			t.Fatalf("Alices Datei wurde verändert: %q", got)
		}
	})

	t.Run("verschieben aus der site heraus", func(t *testing.T) {
		if err := env.files.Write(ctx, bobScope, bobSite.ID, "public/eigen.txt", "meins"); err != nil {
			t.Fatal(err)
		}
		if err := env.files.Move(ctx, bobScope, bobSite.ID, "public/eigen.txt",
			"../alice.example.at/public/untergeschoben.txt"); err == nil {
			t.Fatal("Bob konnte eine Datei in Alices Site verschieben")
		}
	})
}

// TestFileServiceRoundTrip prüft die üblichen Vorgänge über die ganze Kette:
// Service → Socket → Agent → Dateisystem.
func TestFileServiceRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sys := store.SystemScope()
	_, _, site := env.seedSite(t, "alice")

	t.Run("schreiben und lesen", func(t *testing.T) {
		if err := env.files.Write(ctx, sys, site.ID, "public/index.html", "<h1>hallo</h1>"); err != nil {
			t.Fatal(err)
		}
		got, err := env.files.Read(ctx, sys, site.ID, "public/index.html")
		if err != nil {
			t.Fatal(err)
		}
		if got != "<h1>hallo</h1>" {
			t.Fatalf("Read = %q", got)
		}
	})

	t.Run("auflisten liefert relative pfade", func(t *testing.T) {
		entries, err := env.files.List(ctx, sys, site.ID, "public")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("List lieferte %d Einträge", len(entries))
		}
		// Ein absoluter Serverpfad im Frontend wäre eine Einladung, ihn zu
		// manipulieren — es dürfen nur site-relative Pfade herauskommen.
		if entries[0].Path != "public/index.html" {
			t.Fatalf("Pfad = %q, erwartet %q", entries[0].Path, "public/index.html")
		}
	})

	t.Run("verzeichnis anlegen", func(t *testing.T) {
		if err := env.files.Mkdir(ctx, sys, site.ID, "public/assets"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(site.RootPath, "public", "assets")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("upload und download", func(t *testing.T) {
		// Deutlich über der Blockgrenze, damit der Weg wirklich geteilt wird.
		payload := bytes.Repeat([]byte("VoltPanel"), 600_000)

		written, err := env.files.Upload(ctx, sys, site.ID, "public/gross.bin",
			bytes.NewReader(payload), 0)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if written != int64(len(payload)) {
			t.Fatalf("Upload schrieb %d von %d bytes", written, len(payload))
		}

		var buf bytes.Buffer
		read, err := env.files.Download(ctx, sys, site.ID, "public/gross.bin", &buf)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		if read != int64(len(payload)) || !bytes.Equal(buf.Bytes(), payload) {
			t.Fatalf("Download lieferte %d von %d bytes zurück", read, len(payload))
		}
	})

	t.Run("upload respektiert das limit", func(t *testing.T) {
		payload := bytes.Repeat([]byte("x"), 1<<20)
		if _, err := env.files.Upload(ctx, sys, site.ID, "public/zugross.bin",
			bytes.NewReader(payload), 1024); err == nil {
			t.Fatal("Upload überschritt das Limit ohne Fehler")
		}
		// Eine halbe Datei darf nicht zurückbleiben.
		if _, err := os.Stat(filepath.Join(site.RootPath, "public", "zugross.bin")); err == nil {
			t.Fatal("die abgebrochene Datei ist liegen geblieben")
		}
	})

	t.Run("leere datei", func(t *testing.T) {
		if _, err := env.files.Upload(ctx, sys, site.ID, "public/leer.txt",
			strings.NewReader(""), 0); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(site.RootPath, "public", "leer.txt"))
		if err != nil {
			t.Fatalf("leere Datei wurde nicht angelegt: %v", err)
		}
		if info.Size() != 0 {
			t.Fatalf("leere Datei ist %d bytes groß", info.Size())
		}
	})

	t.Run("archivieren und entpacken", func(t *testing.T) {
		if _, err := env.files.Archive(ctx, sys, site.ID,
			[]string{"public/assets"}, "sicherung.tar.gz"); err != nil {
			t.Fatalf("Archive: %v", err)
		}
		if _, err := env.files.Extract(ctx, sys, site.ID, "sicherung.tar.gz", "wieder"); err != nil {
			t.Fatalf("Extract: %v", err)
		}
		if _, err := os.Stat(filepath.Join(site.RootPath, "wieder", "assets")); err != nil {
			t.Fatalf("entpacktes Verzeichnis fehlt: %v", err)
		}
	})

	t.Run("umbenennen und löschen", func(t *testing.T) {
		if err := env.files.Move(ctx, sys, site.ID, "public/index.html", "public/start.html"); err != nil {
			t.Fatal(err)
		}
		if _, err := env.files.Read(ctx, sys, site.ID, "public/start.html"); err != nil {
			t.Fatal(err)
		}
		if err := env.files.Remove(ctx, sys, site.ID, "public/start.html", false); err != nil {
			t.Fatal(err)
		}
		if _, err := env.files.Read(ctx, sys, site.ID, "public/start.html"); err == nil {
			t.Fatal("die gelöschte Datei ist noch lesbar")
		}
	})

	t.Run("wurzel der site ist geschützt", func(t *testing.T) {
		if err := env.files.Remove(ctx, sys, site.ID, "", true); err == nil {
			t.Fatal("das Wurzelverzeichnis der Site ließ sich löschen")
		}
	})
}
