package release

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Was das Release-Skript erzeugt, muss dieses Paket annehmen.
//
// Beide Seiten sind für sich geprüft — hier wird geprüft, dass sie
// zusammenpassen. Das Skript kann seit dem openssl-Weg ohne cosign signieren;
// ob das Ergebnis dasselbe Format hat, war bis hierher eine Behauptung, und
// eine falsche Behauptung an dieser Stelle heißt: der Kanal ist signiert,
// jedes Panel lehnt ihn ab, und niemand kann mehr aktualisieren.
func TestOpenSSLSignaturWirdAngenommen(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("ohne openssl lässt sich das nicht prüfen")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "key.pem")
	pub := filepath.Join(dir, "pub.pem")
	body := filepath.Join(dir, "latest.json")
	der := filepath.Join(dir, "sig.der")

	// Genau die Aufrufe aus scripts/release-assets.sh und docs/release.md.
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("openssl", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("openssl %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("ecparam", "-genkey", "-name", "prime256v1", "-noout", "-out", key)
	run("ec", "-in", key, "-pubout", "-out", pub)

	inhalt := []byte("{\"version\":\"0.4.0\",\"assets\":{}}\n")
	if err := os.WriteFile(body, inhalt, 0o600); err != nil {
		t.Fatal(err)
	}
	run("dgst", "-sha256", "-sign", key, "-out", der, body)

	rohSig, err := os.ReadFile(der)
	if err != nil {
		t.Fatal(err)
	}
	// base64 | tr -d '\n' — dasselbe wie im Skript.
	sig := base64.StdEncoding.EncodeToString(rohSig)

	schluessel, err := os.ReadFile(pub)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(schluessel)
	if !v.HasKey() {
		t.Fatal("der von openssl erzeugte schlüssel gilt als leer")
	}
	if err := v.Verify(inhalt, sig); err != nil {
		t.Fatalf("die signatur des release-skripts wird abgelehnt: %v", err)
	}

	// Und eine veränderte Datei darf nicht durchgehen — sonst prüfte der Test
	// nur, dass Verify irgendetwas zurückgibt.
	if err := v.Verify([]byte("{\"version\":\"9.9.9\"}"), sig); err == nil {
		t.Error("eine veränderte datei wurde angenommen")
	}
}
