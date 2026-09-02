package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNodeVersionIstKeinPfad: die Version wird Teil einer URL *und* Teil eines
// Pfades. Ein ".." darin wäre beides zugleich — ein anderer Ort zum
// Herunterladen und ein anderer zum Auspacken.
func TestNodeVersionIstKeinPfad(t *testing.T) {
	gut := []string{"22.12.0", "20.11.1", "18.0.0", "8.9.4"}
	for _, v := range gut {
		if !reNodeVersion.MatchString(v) {
			t.Errorf("%q wurde abgelehnt", v)
		}
	}
	schlecht := []string{
		"", "22", "22.12", "v22.12.0", "../../etc", "22.12.0/../..",
		"22.12.0 && id", "22.12.0\nrm", "latest", "22.12.0-rc1", "1234.0.0",
	}
	for _, v := range schlecht {
		if reNodeVersion.MatchString(v) {
			t.Errorf("%q ging durch", v)
		}
	}
}

// tarGz baut ein Archiv wie das von Node: alles unter einem Wurzelverzeichnis.
func tarGz(t *testing.T, eintraege []*tar.Header, inhalt map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range eintraege {
		data := inhalt[h.Name]
		h.Size = int64(len(data))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// entpacke ist nodeExtract ohne den Download.
func entpacke(t *testing.T, archiv []byte, dest string) error {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archiv))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil
		}
		if err := extractOne(tr, h, dest); err != nil {
			return err
		}
	}
}

// TestArchivKommtNichtAusSeinemVerzeichnis ist der Kern des Auspackens.
//
// Ein Archiv ist Eingabe. Wer sie an ein Werkzeug weitergibt, das sie anders
// auslegt als man selbst, hat die Prüfung verschenkt — deshalb wird hier jeder
// Eintrag angesehen, statt tar aufzurufen.
func TestArchivKommtNichtAusSeinemVerzeichnis(t *testing.T) {
	// Das Ziel liegt in einem eigenen Unterverzeichnis, und daneben ist Platz.
	// Sonst prüfte der Test nur, dass der Prozess nicht nach /etc schreiben
	// darf — grün, ohne die Prüfung je anzufassen. Genau darauf bin ich beim
	// ersten Anlauf hereingefallen.
	cases := map[string]string{
		"Pfadwechsel nach oben": "node-v22/../entkommen.txt",
		"zwei Ebenen hoch":      "node-v22/bin/../../entkommen.txt",
		"absoluter Pfad":        "node-v22//etc/passwd",
	}
	for name, pfad := range cases {
		eltern := t.TempDir()
		dest := filepath.Join(eltern, "ziel")
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}

		h := &tar.Header{Name: pfad, Mode: 0o644, Typeflag: tar.TypeReg}
		archiv := tarGz(t, []*tar.Header{h}, map[string]string{pfad: "boese"})
		err := entpacke(t, archiv, dest)

		// Beides muss stimmen: die Ablehnung *und* dass nichts danebenliegt.
		if err == nil {
			t.Errorf("%s wurde ausgepackt", name)
		}
		if _, statErr := os.Stat(filepath.Join(eltern, "entkommen.txt")); statErr == nil {
			t.Errorf("%s: eine Datei liegt neben dem Zielverzeichnis", name)
		}
	}
}

// TestSymlinkBleibtImArchiv: ein Link auf /etc/shadow wäre nach dem Auspacken
// eine Datei im Node-Verzeichnis, die dorthin zeigt — und alles, was danach
// unter diesem Namen gelesen wird, läse /etc/shadow.
func TestSymlinkBleibtImArchiv(t *testing.T) {
	for name, link := range map[string]string{
		"absoluter Link": "/etc/shadow",
		"Link nach oben": "../../../etc/shadow",
	} {
		dest := t.TempDir()
		h := &tar.Header{
			Name: "node-v22/bin/npm", Typeflag: tar.TypeSymlink, Linkname: link, Mode: 0o777,
		}
		archiv := tarGz(t, []*tar.Header{h}, nil)
		if err := entpacke(t, archiv, dest); err == nil {
			t.Errorf("%s wurde angelegt", name)
		}
	}

	// Ein Link innerhalb des Archivs ist der Normalfall und muss durchgehen:
	// npm ist genau so einer.
	dest := t.TempDir()
	h := &tar.Header{
		Name: "node-v22/bin/npm", Typeflag: tar.TypeSymlink,
		Linkname: "../lib/node_modules/npm/bin/npm-cli.js", Mode: 0o777,
	}
	if err := entpacke(t, tarGz(t, []*tar.Header{h}, nil), dest); err != nil {
		t.Errorf("ein gewöhnlicher Link wurde abgelehnt: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "bin", "npm")); err != nil {
		t.Errorf("der Link fehlt: %v", err)
	}
}

// TestKeinSetuidAusDemArchiv: ein setuid-Bit aus einem heruntergeladenen Archiv
// wäre ein Programm, das jeder Benutzer des Servers mit fremden Rechten starten
// könnte.
func TestKeinSetuidAusDemArchiv(t *testing.T) {
	dest := t.TempDir()
	eintraege := []*tar.Header{
		{Name: "node-v22/bin/node", Typeflag: tar.TypeReg, Mode: 0o4755},
		{Name: "node-v22/LICENSE", Typeflag: tar.TypeReg, Mode: 0o666},
	}
	archiv := tarGz(t, eintraege, map[string]string{
		"node-v22/bin/node": "binary", "node-v22/LICENSE": "text",
	})
	if err := entpacke(t, archiv, dest); err != nil {
		t.Fatal(err)
	}

	bin, err := os.Stat(filepath.Join(dest, "bin", "node"))
	if err != nil {
		t.Fatal(err)
	}
	if bin.Mode()&os.ModeSetuid != 0 {
		t.Errorf("setuid steht auf der Datei: %v", bin.Mode())
	}
	if bin.Mode().Perm() != 0o755 {
		t.Errorf("Rechte sind %v, erwartet 0755", bin.Mode().Perm())
	}
	// Und was nicht ausführbar war, wird es auch nicht.
	lic, err := os.Stat(filepath.Join(dest, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	if lic.Mode().Perm() != 0o644 {
		t.Errorf("Rechte der LICENSE sind %v, erwartet 0644", lic.Mode().Perm())
	}
}

// TestNodePfadKommtAusEinerZahl: käme er aus der Anfrage, wäre "eine
// Node-Fassung entfernen" ein Weg, jedes Verzeichnis des Servers zu löschen.
func TestNodePfadKommtAusEinerZahl(t *testing.T) {
	srv, _ := testServer(t)

	dir := srv.nodeDir(22)
	if !strings.HasPrefix(dir, srv.nodeRoot) || !strings.HasSuffix(dir, "node22") {
		t.Errorf("nodeDir(22) = %q, erwartet unter %q", dir, srv.nodeRoot)
	}
	// In NodeInstallParams gibt es kein Feld für einen Pfad, und in der
	// Entfern-Operation nur eine Zahl.
	for _, major := range []int{0, -1, -22, 1000, 999999} {
		raw := []byte(`{"major":` + itoa(major) + `}`)
		if _, err := srv.opNodeRemove(t.Context(), raw); err == nil {
			t.Errorf("die Hauptversion %d wurde angenommen", major)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
