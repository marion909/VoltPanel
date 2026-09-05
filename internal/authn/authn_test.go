package authn

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("Korrekt-Pferd-Batterie-9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unerwartetes Hash-Format: %s", hash)
	}
	if err := VerifyPassword("Korrekt-Pferd-Batterie-9", hash); err != nil {
		t.Fatalf("richtiges Passwort abgelehnt: %v", err)
	}
	if err := VerifyPassword("falsch", hash); err != ErrPasswordMismatch {
		t.Fatalf("falsches Passwort: erwartet ErrPasswordMismatch, bekam %v", err)
	}
}

// TestPasswordHashIsSalted: gleiche Eingabe, verschiedene Hashes.
func TestPasswordHashIsSalted(t *testing.T) {
	a, _ := HashPassword("gleiches-passwort")
	b, _ := HashPassword("gleiches-passwort")
	if a == b {
		t.Fatal("zwei Hashes desselben Passworts sind identisch — das Salt fehlt")
	}
}

func TestVerifyPasswordRejectsBrokenHash(t *testing.T) {
	for _, h := range []string{"", "plaintext", "$argon2id$broken", "$bcrypt$v=19$m=1,t=1,p=1$AA$AA",
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$AA"} {
		if err := VerifyPassword("x", h); err == nil {
			t.Errorf("VerifyPassword akzeptierte Hash %q", h)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	p := DefaultPolicy()
	if err := p.Check("Sicheres-Passwort-2026"); err != nil {
		t.Fatalf("gültiges Passwort abgelehnt: %v", err)
	}
	for _, bad := range []string{"", "kurz", "alleskleingeschrieben", "ALLESGROSSGESCHRIEBEN", "KeineZifferHier"} {
		if err := p.Check(bad); err == nil {
			t.Errorf("Policy akzeptierte %q", bad)
		}
	}
}

func TestGeneratePasswordPassesPolicy(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := GeneratePassword(24)
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 24 {
			t.Fatalf("Länge %d, erwartet 24", len(pw))
		}
		if seen[pw] {
			t.Fatal("GeneratePassword hat sich wiederholt")
		}
		seen[pw] = true
	}
	// Die Zeichenauswahl lässt verwechselbare Zeichen bewusst weg.
	for i := 0; i < 20; i++ {
		pw, _ := GeneratePassword(40)
		if strings.ContainsAny(pw, "lI1O0") {
			t.Fatalf("Passwort %q enthält verwechselbare Zeichen", pw)
		}
	}
}

// TestPickCharVerwirftVerzerrendeBytes deckt den Fund ab, dass
// alphabet[int(b)%len(alphabet)] ohne Rejection-Sampling die ersten
// 256%len(alphabet) Zeichen des Alphabets mit einer höheren Wahrscheinlichkeit
// gezogen hätte (256 ist kein Vielfaches der Alphabetlänge). pickChar muss
// jedes Byte ab dieser Schwelle ablehnen und jedes darunter unverzerrt aufs
// Alphabet abbilden.
func TestPickCharVerwirftVerzerrendeBytes(t *testing.T) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#%+="
	limit := 256 - 256%len(alphabet)

	for b := limit; b <= 255; b++ {
		if _, ok := pickChar(byte(b), alphabet); ok {
			t.Errorf("byte %d wurde akzeptiert, müsste die Verzerrung vermeiden", b)
		}
	}
	for b := 0; b < limit; b++ {
		ch, ok := pickChar(byte(b), alphabet)
		if !ok {
			t.Fatalf("byte %d wurde abgelehnt, sollte akzeptiert werden", b)
		}
		if want := alphabet[b%len(alphabet)]; ch != want {
			t.Errorf("byte %d -> %q, erwartet %q", b, ch, want)
		}
	}
}

func TestSessionTokenHashing(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == hash {
		t.Fatal("Token und gespeicherter Hash sind identisch")
	}
	if HashToken(token) != hash {
		t.Fatal("HashToken liefert nicht denselben Wert wie NewSessionToken")
	}

	other, _, _ := NewSessionToken()
	if HashToken(other) == hash {
		t.Fatal("zwei Tokens ergeben denselben Hash")
	}
}

func TestSecretBoxRoundTrip(t *testing.T) {
	box, err := LoadSecretBox(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}

	const plain = "cf_api_token_geheim"
	enc, err := box.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, plain) {
		t.Fatal("Klartext steht im Chiffrat")
	}
	got, err := box.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("Decrypt = %q, erwartet %q", got, plain)
	}

	// Gleicher Klartext, unterschiedliche Nonce.
	enc2, _ := box.Encrypt(plain)
	if enc == enc2 {
		t.Fatal("zweimal derselbe Chiffretext — die Nonce wird nicht neu gezogen")
	}
}

// TestSecretBoxDetectsTampering: GCM muss jede Manipulation bemerken.
func TestSecretBoxDetectsTampering(t *testing.T) {
	box, err := LoadSecretBox(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := box.Encrypt("wert")

	tampered := []byte(enc)
	tampered[len(tampered)-1] ^= 'A'
	if _, err := box.Decrypt(string(tampered)); err != ErrDecrypt {
		t.Fatalf("verändertes Chiffrat: erwartet ErrDecrypt, bekam %v", err)
	}

	// Ein anderer Schlüssel darf nicht entschlüsseln können.
	other, err := LoadSecretBox(filepath.Join(t.TempDir(), "andere.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Decrypt(enc); err != ErrDecrypt {
		t.Fatalf("fremder Schlüssel: erwartet ErrDecrypt, bekam %v", err)
	}
}

func TestSecretBoxKeyFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	if _, err := LoadSecretBox(path); err != nil {
		t.Fatal(err)
	}
	info, err := statFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("Schlüsseldatei hat Rechte %o, erwartet 600", perm)
	}
}

func TestTOTPRoundTrip(t *testing.T) {
	secret, qr, err := NewTOTPSecret("VoltPanel", "admin@example.at")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		t.Fatal("leeres TOTP-Secret")
	}
	if !strings.HasPrefix(qr, "data:image/png;base64,") {
		t.Fatalf("QR-Code ist kein data-URI: %.40s", qr)
	}
	ok1, _ := VerifyTOTP(secret, "000000")
	ok2, _ := VerifyTOTP(secret, "123456")
	if ok1 && ok2 {
		t.Fatal("VerifyTOTP akzeptiert beliebige Codes")
	}

	code, err := currentCode(secret)
	if err != nil {
		t.Fatal(err)
	}
	ok, step := VerifyTOTP(secret, code)
	if !ok {
		t.Fatal("aktuell gültiger Code wurde abgelehnt")
	}
	if want := time.Now().UTC().Unix() / totpPeriod; step != want {
		t.Fatalf("VerifyTOTP lieferte Schritt %d, erwartet %d", step, want)
	}

	// Replay: derselbe Schritt darf kein zweites Mal als "neu" erkennbar sein
	// — die eigentliche Sperre liegt bei den Aufrufern (die den zuletzt
	// akzeptierten Schritt vergleichen), aber VerifyTOTP muss ihnen dafür
	// zuverlässig denselben Schritt für denselben Code liefern.
	ok, step2 := VerifyTOTP(secret, code)
	if !ok || step2 != step {
		t.Fatalf("VerifyTOTP lieferte beim zweiten Aufruf mit demselben Code einen anderen Schritt: %d vs. %d", step2, step)
	}
}
