package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
)

// TestLeererInhaltHashStimmt prüft eine Konstante gegen die Rechnung.
//
// e3b0c442… ist der SHA-256 der leeren Zeichenkette, und er steht hier als
// getippter Text. Ein vertauschtes Zeichen darin fiele sonst erst auf, wenn ein
// echter Speicher mit 403 antwortet — ohne zu sagen, warum.
func TestLeererInhaltHashStimmt(t *testing.T) {
	sum := sha256.Sum256(nil)
	if got := hex.EncodeToString(sum[:]); got != emptyPayloadHash {
		t.Errorf("emptyPayloadHash = %q, gerechnet %q", emptyPayloadHash, got)
	}
}

// TestKanonischeAnfrageHatDieVorgeschriebeneForm.
//
// Die Form ist von AWS festgelegt und unversöhnlich: Methode, Pfad, Query,
// Header (klein, sortiert, je eine Zeile), Leerzeile, Liste der Header,
// Inhaltshash. Weicht ein Zeichen ab, antwortet der Speicher mit
// SignatureDoesNotMatch und nennt die Stelle nicht.
func TestKanonischeAnfrageHatDieVorgeschriebeneForm(t *testing.T) {
	req, err := http.NewRequest(http.MethodPut,
		"https://sicherung.s3.eu-central-1.amazonaws.com/2026/01/volt-20260101.tar.gz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "sicherung.s3.eu-central-1.amazonaws.com"
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("x-amz-content-sha256", emptyPayloadHash)
	req.Header.Set("x-amz-date", "20260101T000000Z")
	// Ein Header, der nicht signiert werden darf: er ändert sich unterwegs
	// durch Proxies, und dann stimmt die Signatur nicht mehr.
	req.Header.Set("User-Agent", "volt")

	canonical, signed := canonicalRequest(req, emptyPayloadHash)

	want := strings.Join([]string{
		"PUT",
		"/2026/01/volt-20260101.tar.gz",
		"",
		"content-type:application/gzip",
		"host:sicherung.s3.eu-central-1.amazonaws.com",
		"x-amz-content-sha256:" + emptyPayloadHash,
		"x-amz-date:20260101T000000Z",
		"",
		"content-type;host;x-amz-content-sha256;x-amz-date",
		emptyPayloadHash,
	}, "\n")

	if canonical != want {
		t.Errorf("kanonische Anfrage falsch:\n--- erhalten ---\n%s\n--- erwartet ---\n%s",
			canonical, want)
	}
	if signed != "content-type;host;x-amz-content-sha256;x-amz-date" {
		t.Errorf("SignedHeaders = %q", signed)
	}
	if strings.Contains(canonical, "user-agent") {
		t.Error("der User-Agent wurde mitsigniert — ein Proxy ändert ihn und die Signatur bricht")
	}
}

// TestPfadKodierungFolgtRFC3986: AWS kodiert anders als url.QueryEscape. Ein
// Leerzeichen wird %20 und nicht +, und der Schrägstrich zwischen Segmenten
// bleibt stehen.
func TestPfadKodierungFolgtRFC3986(t *testing.T) {
	cases := map[string]string{
		"/2026/01/volt.tar.gz": "/2026/01/volt.tar.gz",
		"/mit leerzeichen.gz":  "/mit%20leerzeichen.gz",
		"/tilde~punkt.-_":      "/tilde~punkt.-_",
		"/plus+und&kaufmann":   "/plus%2Bund%26kaufmann",
		"/klammer(auf)":        "/klammer%28auf%29",
		"/stern*":              "/stern%2A",
	}
	for input, want := range cases {
		if got := encodePath(input); got != want {
			t.Errorf("encodePath(%q) = %q, erwartet %q", input, got, want)
		}
	}

	// Innerhalb eines Segments muss der Schrägstrich kodiert werden — sonst
	// wäre aus einem Dateinamen plötzlich ein Verzeichniswechsel.
	if got := encodeSegment("a/b"); got != "a%2Fb" {
		t.Errorf("encodeSegment(\"a/b\") = %q, erwartet \"a%%2Fb\"", got)
	}
}

// TestSchluesselWirdOhneDoppelteTrennerGebaut.
func TestSchluesselWirdOhneDoppelteTrennerGebaut(t *testing.T) {
	cases := []struct{ prefix, name, want string }{
		{"", "a.gz", "a.gz"},
		{"volt", "a.gz", "volt/a.gz"},
		{"/volt/", "a.gz", "volt/a.gz"},
		{"volt/kunde", "2026/01/a.gz", "volt/kunde/2026/01/a.gz"},
	}
	for _, tc := range cases {
		if got := joinKey(tc.prefix, tc.name); got != tc.want {
			t.Errorf("joinKey(%q, %q) = %q, erwartet %q", tc.prefix, tc.name, got, tc.want)
		}
	}
}

// TestEndpunktWirdGeprueft: die häufigste Falscheingabe ist eine ganze URL im
// Feld für den Host. Ohne Prüfung entstünde daraus ein unsinniger Hostname und
// eine Fehlermeldung, die auf die Zugangsdaten zeigt statt auf das Feld.
func TestEndpunktWirdGeprueft(t *testing.T) {
	basis := S3Config{
		Endpoint: "s3.eu-central-1.amazonaws.com", Region: "eu-central-1",
		Bucket: "sicherung", AccessKey: "AKIA", Secret: "geheim",
	}
	if err := validateS3(basis); err != nil {
		t.Fatalf("eine gültige Konfiguration wurde abgelehnt: %v", err)
	}

	schlecht := map[string]S3Config{
		"ganze url":       {Endpoint: "https://s3.amazonaws.com"},
		"mit pfad":        {Endpoint: "s3.amazonaws.com/bucket"},
		"mit port":        {Endpoint: "s3.amazonaws.com:443"},
		"ohne endpunkt":   {Region: "eu", Bucket: "b", AccessKey: "a", Secret: "s"},
		"ohne region":     {Endpoint: "s3.example.at", Bucket: "b", AccessKey: "a", Secret: "s"},
		"ohne bucket":     {Endpoint: "s3.example.at", Region: "eu", AccessKey: "a", Secret: "s"},
		"ohne schluessel": {Endpoint: "s3.example.at", Region: "eu", Bucket: "b"},
	}
	for name, cfg := range schlecht {
		if err := validateS3(cfg); err == nil {
			t.Errorf("%s wurde angenommen", name)
		}
	}

	// Ein Zeilenumbruch in einem Wert wäre eine zweite Kopfzeile in der
	// HTTP-Anfrage.
	umbruch := basis
	umbruch.Prefix = "volt\r\nX-Amz-Foo: bar"
	if err := validateS3(umbruch); err == nil {
		t.Error("ein Zeilenumbruch im Präfix wurde angenommen")
	}
}
