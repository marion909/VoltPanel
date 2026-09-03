package templates

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/marion909/voltpanel/internal/store"
)

// wp-config.php für eine WordPress-Installation aus dem App-Store
// (internal/core/appstore.go).
//
// Derselbe Grund wie bei jeder anderen Vorlage in diesem Paket: text/template
// escaped nichts von selbst, und wp-config.php ist ausführbarer PHP-Code —
// wer hier eine Zeichenkette ungeprüft einsetzt, schreibt PHP, das ein anderer
// bestimmt. phpstr (unten) ist die eine Stelle, an der jeder eingesetzte Wert
// vorbeikommt.

// phpstr escaped einen Wert für eine einfach gequotete PHP-Zeichenkette.
//
// Innerhalb von '…' kennt PHP genau zwei Sonderzeichen: \ und '. Beide werden
// verdoppelt maskiert — nicht mehr und nicht weniger, denn alles andere
// (Zeilenumbrüche etwa) hat innerhalb einfacher Anführungszeichen ohnehin
// keine besondere Bedeutung.
//
// Gebraucht wird das hier zweimal: einmal als Bauplan (die Datenbank-Angaben
// sind schon durch store.ValidDBName/-User geprüft und könnten so ohnehin
// nicht ausbrechen), einmal als echte Schranke — Absicherung, die nicht davon
// abhängt, dass eine andere Stelle im Programm ihre Prüfung nie verliert.
func phpstr(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// WordPressConfigData ist das Eingabemodell von wp-config.php.
type WordPressConfigData struct {
	DBName      string
	DBUser      string
	DBPassword  string
	TablePrefix string

	AuthKey, SecureAuthKey, LoggedInKey, NonceKey     string
	AuthSalt, SecureAuthSalt, LoggedInSalt, NonceSalt string
}

// NewWordPressSecrets erzeugt die acht Schlüssel/Salze, die WordPress für
// Cookies und Nonces benutzt.
//
// WordPress selbst schlägt vor, sie von einer eigenen API
// (api.wordpress.org/secret-key) zu holen. Hier entstehen sie stattdessen
// lokal, aus demselben CSPRNG wie jedes andere Geheimnis in diesem Programm
// (siehe authn.NewSessionToken) — ein zweiter Netzwerkaufruf ins Spiel zu
// bringen, nur um Zufallsbytes zu bekommen, die dieser Server selbst genauso
// gut erzeugt, wäre eine Abhängigkeit ohne Gegenwert.
func NewWordPressSecrets() (WordPressConfigData, error) {
	werte := make([]string, 8)
	for i := range werte {
		buf := make([]byte, 48)
		if _, err := rand.Read(buf); err != nil {
			return WordPressConfigData{}, fmt.Errorf("schlüssel erzeugen: %w", err)
		}
		werte[i] = base64.RawURLEncoding.EncodeToString(buf)
	}
	return WordPressConfigData{
		AuthKey: werte[0], SecureAuthKey: werte[1], LoggedInKey: werte[2], NonceKey: werte[3],
		AuthSalt: werte[4], SecureAuthSalt: werte[5], LoggedInSalt: werte[6], NonceSalt: werte[7],
	}, nil
}

// checkWordPressConfig prüft, was in eine ausführbare PHP-Datei geht.
//
// DBName und DBUser noch einmal gegen dieselben Muster wie im Store — eine
// Vorlage, die sich auf eine fremde Prüfung verlässt, ist die Stelle, an der
// beide auseinanderlaufen können, sobald eine Seite sich ändert.
func checkWordPressConfig(d WordPressConfigData) error {
	switch {
	case !store.ValidDBName(d.DBName):
		return fmt.Errorf("%q ist kein gültiger datenbankname", d.DBName)
	case !store.ValidDBUser(d.DBUser):
		return fmt.Errorf("%q ist kein gültiger datenbankbenutzer", d.DBUser)
	case d.DBPassword == "":
		return fmt.Errorf("kein datenbankpasswort übergeben")
	case d.AuthKey == "" || d.SecureAuthKey == "" || d.LoggedInKey == "" || d.NonceKey == "" ||
		d.AuthSalt == "" || d.SecureAuthSalt == "" || d.LoggedInSalt == "" || d.NonceSalt == "":
		return fmt.Errorf("nicht alle acht schlüssel/salze sind gesetzt — NewWordPressSecrets vergessen?")
	}
	return nil
}

// RenderWordPressConfig erzeugt wp-config.php.
func RenderWordPressConfig(d WordPressConfigData) (string, error) {
	if d.TablePrefix == "" {
		d.TablePrefix = "wp_"
	}
	if err := checkWordPressConfig(d); err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "wordpress-config.php.tmpl", d); err != nil {
		return "", err
	}
	return b.String(), nil
}
