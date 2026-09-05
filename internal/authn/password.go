// Package authn deckt Passwörter, Sessions, 2FA und die Verschlüsselung von
// Secrets ab, die in der Panel-Datenbank liegen.
package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// Argon2id-Parameter. Die Werte orientieren sich an der OWASP-Empfehlung
// (19 MiB, 2 Durchläufe, 1 Thread) — genug Aufwand gegen Offline-Angriffe,
// ohne den Login auf einem kleinen VPS spürbar zu bremsen.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

var (
	ErrPasswordMismatch = errors.New("passwort stimmt nicht")
	ErrHashFormat       = errors.New("passwort-hash hat ein unbekanntes format")
)

// HashPassword erzeugt einen Argon2id-Hash im PHC-String-Format. Die Parameter
// stehen im Hash selbst, damit sie später erhöht werden können, ohne alte
// Hashes ungültig zu machen.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt erzeugen: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword prüft ein Passwort gegen einen Hash in konstanter Zeit.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrHashFormat
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return ErrHashFormat
	}

	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return ErrHashFormat
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrHashFormat
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrHashFormat
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// PasswordPolicy sind die Mindestanforderungen an ein Panel-Passwort.
type PasswordPolicy struct {
	MinLength      int
	RequireUpper   bool
	RequireLower   bool
	RequireDigit   bool
	RequireSpecial bool
}

func DefaultPolicy() PasswordPolicy {
	return PasswordPolicy{MinLength: 12, RequireUpper: true, RequireLower: true, RequireDigit: true}
}

// Check meldet alle Verstöße auf einmal, damit der Nutzer nicht nach jeder
// Korrektur den nächsten Einzelfehler serviert bekommt.
func (p PasswordPolicy) Check(password string) error {
	var upper, lower, digit, special bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			special = true
		}
	}

	var missing []string
	if len([]rune(password)) < p.MinLength {
		missing = append(missing, fmt.Sprintf("mindestens %d zeichen", p.MinLength))
	}
	if p.RequireUpper && !upper {
		missing = append(missing, "einen großbuchstaben")
	}
	if p.RequireLower && !lower {
		missing = append(missing, "einen kleinbuchstaben")
	}
	if p.RequireDigit && !digit {
		missing = append(missing, "eine ziffer")
	}
	if p.RequireSpecial && !special {
		missing = append(missing, "ein sonderzeichen")
	}
	if len(missing) > 0 {
		return fmt.Errorf("passwort braucht %s", strings.Join(missing, ", "))
	}
	return nil
}

// GeneratePassword erzeugt das Initialpasswort, das install.sh ausgibt.
func GeneratePassword(length int) (string, error) {
	// Ohne l/I/1/O/0 — das Passwort wird aus einem Terminal abgetippt.
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#%+="
	if length < 12 {
		length = 20
	}

	out := make([]byte, length)
	buf := make([]byte, length)
	for i := 0; i < length; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			ch, ok := pickChar(b, alphabet)
			if !ok {
				continue
			}
			out[i] = ch
			i++
			if i == length {
				break
			}
		}
	}
	return string(out), nil
}

// pickChar bildet b per Rejection-Sampling auf ein Zeichen des Alphabets ab.
//
// 256 ist kein Vielfaches von len(alphabet): ein einfaches
// alphabet[int(b)%len(alphabet)] würde die ersten 256%len(alphabet) Zeichen
// mit einer Extra-Restklasse und damit leicht höherer Wahrscheinlichkeit
// ziehen. Bytes ab der Schwelle werden deshalb abgelehnt (Aufrufer zieht neu),
// statt sie per Modulo zu verzerren.
func pickChar(b byte, alphabet string) (byte, bool) {
	limit := byte(256 - 256%len(alphabet))
	if b >= limit {
		return 0, false
	}
	return alphabet[int(b)%len(alphabet)], true
}
