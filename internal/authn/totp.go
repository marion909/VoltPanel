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

// totpPeriod ist die Fensterbreite in Sekunden, über die ein Code gilt.
const totpPeriod = 30

// VerifyTOTP prüft einen Code. Skew=1 lässt den vorherigen und nächsten
// 30-Sekunden-Schritt gelten — sonst scheitert jeder Login an einer leicht
// falsch gehenden Handy-Uhr.
//
// Anders als totp.ValidateCustom (das nur ok/Fehler liefert) gibt VerifyTOTP
// zusätzlich den getroffenen Zeitschritt zurück. Ohne den kann ein Aufrufer
// nicht nachhalten, welcher Schritt bereits verbraucht wurde — ein
// abgefangener/mitgeloggter gültiger Code bliebe sonst bis zu ~90 Sekunden
// lang (vorheriges/aktuelles/nächstes Fenster) beliebig oft gültig.
func VerifyTOTP(secret, code string) (ok bool, step int64) {
	opts := totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	}
	now := time.Now().UTC().Unix() / totpPeriod

	// Dieselbe Reihenfolge wie totp.ValidateCustom intern verwendet (aktueller
	// Schritt, dann +1/-1 usw.) — nur mit GenerateCodeCustom je Kandidat statt
	// mit ValidateCustom insgesamt, damit der getroffene Schritt sichtbar wird.
	candidates := []int64{now}
	for i := int64(1); i <= int64(opts.Skew); i++ {
		candidates = append(candidates, now+i, now-i)
	}
	for _, c := range candidates {
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(c*totpPeriod, 0).UTC(), opts)
		if err != nil {
			continue
		}
		if expected == code {
			return true, c
		}
	}
	return false, 0
}
