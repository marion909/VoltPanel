package authn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SecretBox verschlüsselt Werte, die in der Panel-Datenbank liegen müssen, aber
// dort nicht im Klartext stehen dürfen: TOTP-Secrets, Cloudflare-API-Tokens,
// DB-Passwörter für den Export.
//
// AES-256-GCM mit zufälliger Nonce pro Wert. Der Schlüssel liegt als Datei mit
// 0600 neben der Config — ein DB-Backup allein reicht damit nicht, um an die
// Secrets zu kommen.
type SecretBox struct {
	aead cipher.AEAD
}

var ErrDecrypt = errors.New("wert konnte nicht entschlüsselt werden")

// LoadSecretBox liest den Schlüssel aus keyPath und legt ihn an, falls er fehlt.
func LoadSecretBox(keyPath string) (*SecretBox, error) {
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		if key, err = generateKeyFile(keyPath); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("schlüsseldatei %s: %w", keyPath, err)
	}

	if len(key) != 32 {
		return nil, fmt.Errorf("schlüsseldatei %s enthält %d bytes, erwartet werden 32", keyPath, len(key))
	}
	return NewSecretBox(key)
}

func NewSecretBox(key []byte) (*SecretBox, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes-schlüssel: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &SecretBox{aead: aead}, nil
}

func generateKeyFile(keyPath string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("schlüssel erzeugen: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
		return nil, err
	}
	// 0600: nur der Besitzer. Die Datei ist der Generalschlüssel für alle
	// gespeicherten Secrets.
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return nil, fmt.Errorf("schlüsseldatei %s schreiben: %w", keyPath, err)
	}
	return key, nil
}

// Encrypt liefert nonce||ciphertext als base64.
func (s *SecretBox) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce erzeugen: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *SecretBox) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrDecrypt
	}
	if len(raw) < s.aead.NonceSize() {
		return "", ErrDecrypt
	}

	nonce, ciphertext := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// GCM erkennt jede Veränderung — der Fehler wird bewusst nicht
		// weitergereicht, damit er keine Details über den Inhalt verrät.
		return "", ErrDecrypt
	}
	return string(plain), nil
}
