package authn

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// NewTOTPSecret erzeugt ein 2FA-Geheimnis samt QR-Code als data:-URI, den das
// Frontend direkt in ein <img> setzen kann.
func NewTOTPSecret(issuer, account string) (secret, qrDataURI string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1, // was Google Authenticator & Co. erwarten
	})
	if err != nil {
		return "", "", fmt.Errorf("totp-secret erzeugen: %w", err)
	}

	img, err := key.Image(240, 240)
	if err != nil {
		return "", "", fmt.Errorf("qr-code erzeugen: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", fmt.Errorf("qr-code kodieren: %w", err)
	}
	return key.Secret(), "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// VerifyTOTP prüft einen Code. Skew=1 lässt den vorherigen und nächsten
// 30-Sekunden-Schritt gelten — sonst scheitert jeder Login an einer leicht
// falsch gehenden Handy-Uhr.
func VerifyTOTP(secret, code string) bool {
	ok, err := totp.ValidateCustom(code, secret, time.Now().UTC(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}
