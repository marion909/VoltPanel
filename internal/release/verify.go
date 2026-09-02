// Package release prüft die Signatur der Release-Angaben.
//
// Bis hierher verglich `volt update` die Prüfsumme aus latest.json mit dem
// heruntergeladenen Binary. Der Kommentar daneben behauptete, nur wer den
// Release signiert habe, kenne diese Summe — das stimmte nicht: beide kommen
// von derselben Adresse. Wer den Server oder die Leitung dorthin beherrscht,
// liefert ein anderes Binary *und* die dazu passende Summe.
//
// Bei einem Update ist das der Totalschaden: das Panel schreibt sich selbst und
// den Root-Daemon neu. Ein signiertes latest.json schließt das, denn die Datei
// enthält die Prüfsummen aller Bestandteile — wer sie signiert hat, hat damit
// auch die Binaries signiert.
package release

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// publicKeyPEM ist der öffentliche Schlüssel, gegen den geprüft wird.
//
// Eingebettet, nicht heruntergeladen: ein Schlüssel, den man sich beim selben
// Server holt wie die Datei, die er beglaubigen soll, beglaubigt gar nichts.
//
// Die Datei ist im Quelltext leer. Sie zu füllen ist ein Schritt beim
// Einrichten der Veröffentlichung — siehe docs/release.md. Solange sie leer
// ist, lehnt Verify jede Prüfung ab, statt sie zu überspringen: eine
// Signaturprüfung, die ohne Schlüssel stillschweigend durchwinkt, ist keine.
//
//go:embed release.pub
var publicKeyPEM []byte

var (
	// ErrNoKey heißt: dieses Binary wurde ohne Schlüssel gebaut.
	ErrNoKey = errors.New("in diesem build steckt kein release-schlüssel")
	// ErrBadSignature heißt: die Signatur passt nicht zu den Angaben.
	ErrBadSignature = errors.New("die signatur der release-angaben stimmt nicht")
)

// Verifier prüft gegen einen bestimmten Schlüssel.
//
// Als Typ und nicht als Paketfunktion, damit ein Test seinen eigenen Schlüssel
// mitbringen kann. Die Alternative wäre eine exportierte Funktion, die den
// eingebetteten Schlüssel überschreibt — und die stünde dann auch dem
// laufenden Programm zur Verfügung, wo sie nichts verloren hat.
type Verifier struct {
	pem []byte
}

// Default prüft gegen den eingebetteten Schlüssel. Das ist der Normalfall.
func Default() *Verifier { return &Verifier{pem: publicKeyPEM} }

// NewVerifier prüft gegen einen übergebenen Schlüssel im PEM-Format.
func NewVerifier(publicKeyPEM []byte) *Verifier { return &Verifier{pem: publicKeyPEM} }

// HasKey sagt, ob dieser Verifier überhaupt prüfen kann.
//
// Für `volt doctor`: ein Server, dessen Panel Updates nicht prüfen kann, soll
// das erfahren, bevor das erste Update ansteht.
func (v *Verifier) HasKey() bool { return len(strings.TrimSpace(string(v.pem))) > 0 }

// Verify prüft die Signatur über den Rumpf.
//
// Erwartet wird das Format von `cosign sign-blob --key`: eine
// base64-kodierte ECDSA-Signatur im DER-Format über den SHA-256 des Rumpfs.
func (v *Verifier) Verify(payload []byte, signature string) error {
	pub, err := v.publicKey()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("%w: die signatur ist nicht base64", ErrBadSignature)
	}

	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(pub, digest[:], sig) {
		return ErrBadSignature
	}
	return nil
}

// publicKey liest den Schlüssel dieses Verifiers.
func (v *Verifier) publicKey() (*ecdsa.PublicKey, error) {
	raw := []byte(strings.TrimSpace(string(v.pem)))
	if len(raw) == 0 {
		return nil, ErrNoKey
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("der eingebettete release-schlüssel ist kein pem")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("der eingebettete release-schlüssel ist unlesbar: %w", err)
	}
	ec, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("der eingebettete release-schlüssel ist kein ecdsa-schlüssel (%T)", key)
	}
	return ec, nil
}
