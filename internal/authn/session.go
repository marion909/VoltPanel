package authn

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// NewSessionToken erzeugt ein Session-Token und dessen Hash.
//
// Der Klartext geht als Cookie an den Browser, gespeichert wird nur der Hash.
// Wer die Datenbank liest, kann damit keine Sitzung übernehmen — dasselbe
// Prinzip wie bei Passwörtern.
func NewSessionToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("session-token erzeugen: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashToken(token), nil
}

// HashToken bildet den Speicherwert zu einem Token.
//
// SHA-256 ohne Salt ist hier richtig: das Token hat 256 Bit Entropie aus einem
// CSPRNG, gegen Rateangriffe braucht es also keine Verlangsamung — und der
// Lookup muss bei jeder Anfrage schnell sein.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
