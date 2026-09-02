package release

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"testing"
)

// mitSchluessel setzt für die Dauer des Tests einen erzeugten Schlüssel ein und
// gibt die Signierfunktion dazu zurück.
//
// Der echte Schlüssel steht im Quelltext leer — er entsteht beim Einrichten der
// Veröffentlichung. Der Test darf davon nicht abhängen, sonst prüfte er auf
// jedem Rechner etwas anderes.
func mitSchluessel(t *testing.T) (*Verifier, func([]byte) string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	v := NewVerifier(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	return v, func(payload []byte) string {
		digest := sha256.Sum256(payload)
		sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(sig)
	}
}

// TestOhneSchluesselWirdNichtDurchgewunken ist die wichtigste Zeile.
//
// Eine Signaturprüfung, die ohne Schlüssel stillschweigend durchwinkt, ist
// keine — sie sieht nur so aus, und niemand merkt den Unterschied, bis es
// darauf ankommt.
func TestOhneSchluesselWirdNichtDurchgewunken(t *testing.T) {
	ohne := NewVerifier(nil)

	if ohne.HasKey() {
		t.Error("ohne Schlüssel meldet HasKey true")
	}
	err := ohne.Verify([]byte(`{"version":"1.0.0"}`), "")
	if !errors.Is(err, ErrNoKey) {
		t.Errorf("Verify ohne Schlüssel: %v, erwartet ErrNoKey", err)
	}

	// Auch mit einer syntaktisch gültigen Signatur nicht.
	if err := ohne.Verify([]byte("x"),
		base64.StdEncoding.EncodeToString([]byte("irgendwas"))); err == nil {
		t.Error("ohne Schlüssel wurde eine Signatur angenommen")
	}

	// Und Leerzeichen sind kein Schlüssel.
	if NewVerifier([]byte("\n  \n")).HasKey() {
		t.Error("Leerzeichen gelten als Schlüssel")
	}

	// Der eingebettete Schlüssel dieses Builds: leer im Quelltext, gefüllt beim
	// Einrichten der Veröffentlichung. Beides ist in Ordnung — nur darf ein
	// leerer nicht als "geprüft" durchgehen, und genau das prüft der Rest
	// dieser Datei.
	if !Default().HasKey() {
		t.Log("dieses Binary trägt keinen Release-Schlüssel — siehe docs/release.md")
	}
}

// TestEchteSignaturGehtDurch: sonst prüfte der Test oben nur, dass gar nichts
// durchkommt.
func TestEchteSignaturGehtDurch(t *testing.T) {
	v, signiere := mitSchluessel(t)
	payload := []byte(`{"version":"1.4.0","assets":{}}`)

	if !v.HasKey() {
		t.Fatal("HasKey meldet false, obwohl ein Schlüssel gesetzt ist")
	}
	if err := v.Verify(payload, signiere(payload)); err != nil {
		t.Errorf("die eigene Signatur wurde abgelehnt: %v", err)
	}
	// Mit Zeilenumbruch drumherum — so kommt sie aus einer Datei.
	if err := v.Verify(payload, "\n"+signiere(payload)+"\n"); err != nil {
		t.Errorf("mit Zeilenumbruch abgelehnt: %v", err)
	}
}

// TestSignaturDecktDenGanzenRumpf: latest.json enthält die Prüfsummen aller
// Bestandteile. Wer die Datei signiert hat, hat damit auch die Binaries
// signiert — aber nur, solange ein geändertes Byte auffällt.
func TestSignaturDecktDenGanzenRumpf(t *testing.T) {
	v, signiere := mitSchluessel(t)
	echt := []byte(`{"version":"1.4.0","assets":{"linux_amd64":{"sha256":"aa"}}}`)
	sig := signiere(echt)

	gefaelscht := [][]byte{
		[]byte(`{"version":"1.4.0","assets":{"linux_amd64":{"sha256":"bb"}}}`),
		[]byte(`{"version":"9.9.9","assets":{"linux_amd64":{"sha256":"aa"}}}`),
		append(append([]byte{}, echt...), ' '),
		echt[:len(echt)-1],
		{},
	}
	for i, body := range gefaelscht {
		if err := v.Verify(body, sig); err == nil {
			t.Errorf("Fassung %d ging mit fremder Signatur durch: %s", i, body)
		}
	}
}

// TestKaputteSignaturWirdAbgelehnt: was kein base64 oder keine gültige
// Signatur ist, darf nicht als "keine Signatur nötig" durchgehen.
func TestKaputteSignaturWirdAbgelehnt(t *testing.T) {
	v, signiere := mitSchluessel(t)
	payload := []byte(`{"version":"1.4.0"}`)
	echt := signiere(payload)

	for name, sig := range map[string]string{
		"leer":            "",
		"kein base64":     "!!! keine signatur !!!",
		"leeres base64":   base64.StdEncoding.EncodeToString(nil),
		"kein DER":        base64.StdEncoding.EncodeToString([]byte("nur text")),
		"abgeschnitten":   echt[:len(echt)-8],
		"fremde Signatur": base64.StdEncoding.EncodeToString(make([]byte, 70)),
	} {
		if err := v.Verify(payload, sig); err == nil {
			t.Errorf("%s wurde angenommen", name)
		}
	}
}
