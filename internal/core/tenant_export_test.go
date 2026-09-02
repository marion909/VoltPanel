package core

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// seedExportTenant baut einen Mandanten mit Site, FTP-Zugang und Datenbank.
func seedExportTenant(t *testing.T, env *testEnv, slug string) *store.Tenant {
	t.Helper()
	ctx, sys := t.Context(), store.SystemScope()

	tenant, _, site := env.seedSite(t, slug)

	// Ein FTP-Zugang mit Passwort — das ist der Wert, um den es beim
	// Umschlüsseln geht.
	enc, err := env.secrets.Encrypt("ftp-passwort-" + slug)
	if err != nil {
		t.Fatal(err)
	}
	siteID := site.ID
	if err := env.store.CreateFTPAccount(ctx, sys, &store.FTPAccount{
		TenantID: tenant.ID, SiteID: &siteID, Username: slug + "_ftp",
		PasswordEnc: enc, HomeDir: site.RootPath, UID: 1001, GID: 1001,
	}); err != nil {
		t.Fatal(err)
	}

	// Und eine Datei in der Site, damit im Archiv etwas liegt.
	if err := os.WriteFile(filepath.Join(site.RootPath, "public", "index.html"),
		[]byte("<h1>"+slug+"</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tenant
}

func exportService(env *testEnv) *ExportService {
	// Ohne Agent: Datenbankauszüge bleiben dann weg, und das steht als Warnung
	// im Ergebnis. Geprüft wird hier alles andere.
	return NewExportService(env.cfg, env.store, nil, env.secrets, nil)
}

// TestExportNimmtNurEinenMandantenMit ist der Grund für das Ganze.
//
// Das Backup war serverweit — eine Kopie der ganzen Datenbank. Für einen Umzug
// heißt das: der Mandant nimmt alle anderen mit.
func TestExportNimmtNurEinenMandantenMit(t *testing.T) {
	env := newTestEnv(t)
	alice := seedExportTenant(t, env, "alice")
	seedExportTenant(t, env, "bob")

	svc := exportService(env)
	res, err := svc.ExportTenant(t.Context(), store.SystemScope(), alice.ID,
		"eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}

	inhalt := archivInhalt(t, res.Path)
	for name := range inhalt {
		if strings.Contains(name, "bob") {
			t.Errorf("im Bündel von alice steckt %q", name)
		}
	}
	if _, ok := inhalt["sites/alice.example.at/public/index.html"]; !ok {
		t.Errorf("die Datei der Site fehlt: %v", schluessel(inhalt))
	}

	bundle, _, err := OpenBundle(res.Path, "eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Tenant.Slug != "alice" {
		t.Errorf("Mandant im Bündel: %q", bundle.Tenant.Slug)
	}
	if len(bundle.Sites) != 1 {
		t.Errorf("%d Sites im Bündel", len(bundle.Sites))
	}
	for _, u := range bundle.Users {
		if u.TenantID != alice.ID {
			t.Errorf("ein fremder Benutzer steckt im Bündel: %+v", u)
		}
	}
}

// TestGeheimnisseWerdenUmgeschluesselt: in der Datenbank stehen sie
// verschlüsselt, aber mit dem Schlüssel *dieses* Servers. Auf einem anderen
// wären sie unlesbar — mitzunehmen wären sie dann so gut wie gar nicht.
//
// Und im Klartext dürfen sie nicht im Bündel stehen: das wäre eine Datei, in
// der die Passwörter aller Zugänge dieses Mandanten stehen.
func TestGeheimnisseWerdenUmgeschluesselt(t *testing.T) {
	env := newTestEnv(t)
	alice := seedExportTenant(t, env, "alice")

	svc := exportService(env)
	res, err := svc.ExportTenant(t.Context(), store.SystemScope(), alice.ID,
		"eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}

	roh := archivInhalt(t, res.Path)[bundleName]
	if strings.Contains(roh, "ftp-passwort-alice") {
		t.Errorf("das Passwort steht im Klartext im Bündel:\n%s", roh)
	}
	// Auch nicht in der Verschlüsselung dieses Servers: die wäre auf dem
	// Zielserver nicht zu lesen, und der Umzug wäre halb.
	//
	// Verglichen wird gegen den Geheimtext, der wirklich in der Datenbank
	// steht. Ihn hier neu zu erzeugen ginge nicht: AES-GCM nimmt je
	// Verschlüsselung eine neue Nonce, der Vergleich könnte also nie zutreffen
	// und wäre grün, ohne etwas zu prüfen. Genau das war er zuerst.
	sites, err := env.store.ListSites(t.Context(),
		store.Scope{TenantID: alice.ID, Role: store.RoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := env.store.ListFTPAccounts(t.Context(), store.SystemScope(), sites[0].ID)
	if err != nil || len(accounts) == 0 {
		t.Fatalf("kein FTP-Zugang zum Vergleichen: %v", err)
	}
	if strings.Contains(roh, accounts[0].PasswordEnc) {
		t.Error("das Geheimnis steckt unverändert im Bündel")
	}

	// Mit der richtigen Passphrase kommt es zurück.
	bundle, box, err := OpenBundle(res.Path, "eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	gefunden := false
	for key := range bundle.Secrets {
		if !strings.HasPrefix(key, "ftp.") {
			continue
		}
		klar, ok := bundleSecret(bundle, box, key)
		if !ok {
			t.Fatalf("%s ließ sich nicht öffnen", key)
		}
		if klar != "ftp-passwort-alice" {
			t.Errorf("%s enthielt %q", key, klar)
		}
		gefunden = true
	}
	if !gefunden {
		t.Errorf("kein FTP-Geheimnis im Bündel: %v", schluessel(bundle.Secrets))
	}
}

// TestFalschePassphraseOeffnetNichts: sonst wäre die Passphrase Zierde.
func TestFalschePassphraseOeffnetNichts(t *testing.T) {
	env := newTestEnv(t)
	alice := seedExportTenant(t, env, "alice")

	svc := exportService(env)
	res, err := svc.ExportTenant(t.Context(), store.SystemScope(), alice.ID,
		"eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}

	for _, falsch := range []string{
		"eine-lange-passphras", "eine-lange-passphrase!", "", "andere-passphrase",
	} {
		if _, _, err := OpenBundle(res.Path, falsch); err == nil {
			t.Errorf("die Passphrase %q hat das Bündel geöffnet", falsch)
		}
	}
}

// TestKurzePassphraseWirdAbgelehnt: sie schützt die Passwörter aller Zugänge
// dieses Mandanten. Kürzer wäre der Schlüssel des Bündels schwächer als jedes
// Passwort darin.
func TestKurzePassphraseWirdAbgelehnt(t *testing.T) {
	env := newTestEnv(t)
	alice := seedExportTenant(t, env, "alice")
	svc := exportService(env)

	for _, kurz := range []string{"", "kurz", "elfzeichen"} {
		if _, err := svc.ExportTenant(t.Context(), store.SystemScope(), alice.ID, kurz); err == nil {
			t.Errorf("die Passphrase %q wurde angenommen", kurz)
		}
	}
}

// TestImportLegtDenMandantenNeuAn: die Nummern aus dem Bündel gelten auf dem
// Zielserver nicht. Was aufeinander zeigt, muss umgehängt werden — sonst hängt
// es nach dem Import an einer fremden Zeile.
func TestImportLegtDenMandantenNeuAn(t *testing.T) {
	quelle := newTestEnv(t)
	alice := seedExportTenant(t, quelle, "alice")
	res, err := exportService(quelle).ExportTenant(t.Context(), store.SystemScope(),
		alice.ID, "eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}

	// Ein zweiter Server. Damit die Nummern sich unterscheiden, bekommt er
	// zuerst einen anderen Mandanten.
	ziel := newTestEnv(t)
	seedExportTenant(t, ziel, "fremd")

	imported, err := exportService(ziel).ImportTenant(t.Context(), res.Path,
		"eine-lange-passphrase")
	if err != nil {
		t.Fatalf("import: %v (%v)", err, imported)
	}
	if imported.Slug != "alice" {
		t.Errorf("Slug %q", imported.Slug)
	}
	if imported.Sites != 1 {
		t.Errorf("%d Sites eingespielt, erwartet 1 — %v", imported.Sites, imported.Warnings)
	}

	ctx, sys := t.Context(), store.SystemScope()
	sites, err := ziel.store.ListSites(ctx, store.Scope{TenantID: imported.TenantID, Role: store.RoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("%d Sites beim Mandanten", len(sites))
	}
	// Der Pfad entsteht neu aus dem Verzeichnis dieses Servers.
	if !strings.HasPrefix(sites[0].RootPath, ziel.cfg.SitesDir) {
		t.Errorf("RootPath ist %q, erwartet unter %q", sites[0].RootPath, ziel.cfg.SitesDir)
	}
	// Und die Datei liegt dort.
	if _, err := os.Stat(filepath.Join(sites[0].RootPath, "public", "index.html")); err != nil {
		t.Errorf("die Datei der Site fehlt: %v", err)
	}

	// Der FTP-Zugang hängt an der *neuen* Site, und sein Passwort ist wieder
	// mit dem Schlüssel dieses Servers verschlüsselt.
	accounts, err := ziel.store.ListFTPAccounts(ctx, sys, sites[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("%d FTP-Zugänge, erwartet 1 — %v", len(accounts), imported.Warnings)
	}
	klar, err := ziel.secrets.Decrypt(accounts[0].PasswordEnc)
	if err != nil {
		t.Fatalf("das Passwort ist auf dem Zielserver nicht lesbar: %v", err)
	}
	if klar != "ftp-passwort-alice" {
		t.Errorf("Passwort ist %q", klar)
	}
}

// TestImportUeberschreibtNichts: ein halb überschriebener Mandant wäre
// schlimmer als gar keiner, und welche Hälfte gälte, wüsste danach niemand.
func TestImportUeberschreibtNichts(t *testing.T) {
	env := newTestEnv(t)
	alice := seedExportTenant(t, env, "alice")
	res, err := exportService(env).ExportTenant(t.Context(), store.SystemScope(),
		alice.ID, "eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}

	// Auf denselben Server zurückspielen: den Mandanten gibt es schon.
	_, err = exportService(env).ImportTenant(t.Context(), res.Path, "eine-lange-passphrase")
	if err == nil {
		t.Fatal("der vorhandene Mandant wurde überschrieben")
	}
	if !strings.Contains(err.Error(), "gibt es auf diesem server schon") {
		t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
	}
}

// TestBuendelIstEingabe: es stammt vom eigenen Server, aber wer es in die Hand
// bekommt, kann darin stehen lassen, was er will. Dieselbe Sorgfalt wie beim
// Node-Archiv.
func TestBuendelIstEingabe(t *testing.T) {
	eltern := t.TempDir()
	root := filepath.Join(eltern, "site")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := map[string]*tar.Header{
		"Pfadwechsel nach oben": {Name: "x", Typeflag: tar.TypeReg, Mode: 0o644},
		"absoluter Pfad":        {Name: "y", Typeflag: tar.TypeReg, Mode: 0o644},
	}
	subs := map[string]string{
		"Pfadwechsel nach oben": "../entkommen.txt",
		"absoluter Pfad":        "/etc/passwd",
	}
	for name, h := range cases {
		err := writeUnder(root, subs[name], h, strings.NewReader("boese"))
		if err == nil {
			t.Errorf("%s wurde geschrieben", name)
		}
		if _, statErr := os.Stat(filepath.Join(eltern, "entkommen.txt")); statErr == nil {
			t.Errorf("%s: eine Datei liegt neben dem Zielverzeichnis", name)
		}
	}

	// Ein Symlink aus dem Verzeichnis heraus ebenfalls nicht.
	link := &tar.Header{Name: "z", Typeflag: tar.TypeSymlink, Linkname: "../../../etc/shadow"}
	if err := writeUnder(root, "bin/x", link, nil); err == nil {
		t.Error("ein Symlink aus dem Verzeichnis heraus wurde angelegt")
	}
	// Einer innerhalb schon.
	ok := &tar.Header{Name: "z", Typeflag: tar.TypeSymlink, Linkname: "../public/index.html"}
	if err := writeUnder(root, "bin/x", ok, nil); err != nil {
		t.Errorf("ein gewöhnlicher Link wurde abgelehnt: %v", err)
	}
}

// archivInhalt liest ein tar.gz vollständig in eine Map.
func archivInhalt(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	out := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimPrefix(h.Name, "./")] = string(data)
	}
}

func schluessel(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Nach dem Import steht der Mandant auch auf dem Server, nicht nur in der
// Datenbank.
//
// Vorher endete der Import mit einem Hinweis, man möge `volt site rebuild
// --all` laufen lassen. Ein Hinweis ist kein Zustand: wer ihn übersieht, hat
// einen Mandanten, der in der Oberfläche vollständig aussieht und dessen
// Websites 502 liefern — und der Zusammenhang zum Import ist dann längst
// nicht mehr sichtbar.
//
// Geprüft wird deshalb, dass der Import es überhaupt versucht, und zwar für
// jede Site. Ob es gelingt, hängt am Server: `useradd` und `nginx -t` gibt es
// auf einem Entwicklungsrechner nicht. Der Test verlangt daher entweder den
// Erfolg oder eine Warnung, die die Domain nennt — was er nicht durchgehen
// lässt, ist stillschweigend gar nichts zu tun.
func TestImportStelltDieSiteAufDemServerHer(t *testing.T) {
	quelle := newTestEnv(t)
	alice := seedExportTenant(t, quelle, "alice")
	res, err := exportService(quelle).ExportTenant(t.Context(), store.SystemScope(),
		alice.ID, "eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}

	// Mit echtem Agent, nicht mit dem nil aus exportService(): geprüft wird
	// hier gerade der Weg zum Server.
	ziel := newTestEnv(t)
	svc := NewExportService(ziel.cfg, ziel.store, ziel.agent, ziel.secrets, nil)
	imported, err := svc.ImportTenant(t.Context(), res.Path, "eine-lange-passphrase")
	if err != nil {
		t.Fatal(err)
	}

	sites, err := ziel.store.ListSites(t.Context(),
		store.Scope{TenantID: imported.TenantID, Role: store.RoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) == 0 {
		t.Fatal("keine Site eingespielt")
	}

	for _, site := range sites {
		if imported.Rebuilt == len(sites) {
			continue // hergestellt, nichts weiter zu prüfen
		}
		genannt := false
		for _, w := range imported.Warnings {
			if strings.Contains(w, site.Domain) {
				genannt = true
				break
			}
		}
		if !genannt {
			t.Errorf("%s wurde weder hergestellt noch als Warnung genannt — "+
				"der Import hat es gar nicht erst versucht. Warnungen: %v",
				site.Domain, imported.Warnings)
		}
	}
}
