package agent

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 ist das Format, in dem WordPress seine Prüfsumme veröffentlicht — nicht unsere Wahl, sondern die Gegenstelle. Siehe wordpressChecksum.
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WordPress als App-Store-Eintrag.
//
// Anders als ein Plugin (internal/core/plugins.go) erweitert das hier nicht
// den Server, sondern erzeugt eine ganz gewöhnliche PHP-Site: die Zeilen dafür
// legt der Kern schon an (CreateSite, CreateDatabase), bevor diese Operation
// überhaupt läuft. Was hier passiert, ist nur der eine Teil, den keine
// vorhandene Operation abdeckt — den WordPress-Kern holen, prüfen, auspacken,
// dem Systembenutzer der Site übereignen.
//
// Derselbe sichere Weg wie bei einer Node-Fassung: Archiv laden, gegen eine
// veröffentlichte Prüfsumme halten, mit archive/tar selbst auspacken statt mit
// dem Programm tar — siehe fetchAndExtract in ops_node.go, das hier
// wiederverwendet wird.

// Als Variablen, nicht als Konstanten: der Test ersetzt beide durch eine
// lokale Gegenstelle, statt bei jedem Lauf wirklich wordpress.org zu laden.
// Verändert werden sie sonst nirgends — im laufenden Agent steht dort immer
// dieselbe feste Adresse, nie eine aus einer Anfrage.
var (
	wordpressURL         = "https://wordpress.org/latest.tar.gz"
	wordpressChecksumURL = wordpressURL + ".sha1"
)

const (
	// WordPress gepackt liegt bei rund 25 MB; die Decke ist großzügig gegen
	// einen Server, der ins Unermessliche antwortet, nicht als Erwartung.
	wordpressDownloadMax = 128 << 20
	wordpressTimeout     = 5 * time.Minute
)

type WordPressInstallParams struct {
	// WebRoot ist das Verzeichnis, das Nginx für die Site ausliefert
	// (site.WebRoot() im Kern) — nicht das Wurzelverzeichnis der Site selbst.
	WebRoot string `json:"web_root"`
	// SystemUser muss der Benutzer *dieser* Site sein. Dieselbe Regel wie bei
	// einem FTP-Zugang: siteUserIDs prüft das Präfix, nicht nur den Namen.
	SystemUser string `json:"system_user"`
	// WebGroup ist die Gruppe des Webservers (config.yaml: web_group) — kommt
	// vom Aufrufer, weil sie konfigurierbar ist und der Agent sie nicht selbst
	// kennt.
	WebGroup string `json:"web_group"`
}

// opAppStoreWordPress holt den WordPress-Kern in eine bereits angelegte Site.
//
// Was hier *nicht* passiert: irgendetwas ausführen. WordPress selbst führt
// seinen Fünf-Minuten-Installer im Browser aus, sobald die Domain aufgerufen
// wird — das ist WordPress' eigener, etablierter Weg und kein Grund, hier ein
// `php wp-cli.phar` mit erzeugten Argumenten nachzubauen.
func (s *Server) opAppStoreWordPress(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[WordPressInstallParams](raw, OpAppStoreWordPress)
	if err != nil {
		return nil, err
	}
	dest, err := jail(p.WebRoot, s.roots)
	if err != nil {
		return nil, err
	}
	// Dieselbe Schranke wie bei einem FTP-Zugang oder einem Deploy: nur der
	// Systembenutzer *einer Site*, nie ein Systemkonto. Ein WordPress, das
	// jemandem sonst gehört, wäre PHP-Code, der unter fremder Kennung liest
	// und schreibt.
	if _, _, err := siteUserIDs(OpAppStoreWordPress, p.SystemUser); err != nil {
		return nil, err
	}

	got, err := installWordPressFiles(ctx, dest)
	if err != nil {
		return nil, opErr(OpAppStoreWordPress, "%v", err)
	}

	if err := applyOwner(dest, p.SystemUser, p.WebGroup, true); err != nil {
		return nil, opErr(OpAppStoreWordPress, "rechte setzen: %v", err)
	}

	return TextResult{Text: "wordpress-kern ausgepackt, prüfsumme " + got}, nil
}

// installWordPressFiles holt, prüft und packt den Kern in dest aus.
//
// Vom Setzen der Rechte getrennt, damit sich das Auspacken selbst — der Teil
// mit einem Netzwerkaufruf, den ein Test durch eine lokale Gegenstelle
// ersetzen kann — unabhängig von einem echten Systembenutzer prüfen lässt.
// applyOwner danach braucht ein Konto, das es auf einem Entwicklungsrechner
// nicht gibt.
func installWordPressFiles(ctx context.Context, dest string) (string, error) {
	want, err := wordpressChecksum(ctx)
	if err != nil {
		return "", err
	}

	// In einem Unterverzeichnis von dest auspacken, nicht direkt hinein: erst
	// nach der Prüfsumme wird sichtbar gemacht, was ausgepackt wurde. Ein
	// Abbruch mittendrin — abgebrochener Download, falsche Prüfsumme — lässt
	// die Site so stehen, wie sie vor diesem Aufruf war.
	tmp, err := os.MkdirTemp(dest, ".volt-wp-*")
	if err != nil {
		return "", fmt.Errorf("arbeitsverzeichnis: %w", err)
	}
	defer os.RemoveAll(tmp)

	got, err := fetchAndExtract(ctx, wordpressURL, tmp, wordpressTimeout, wordpressDownloadMax, sha1.New()) //nolint:gosec
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("die prüfsumme des wordpress-kerns stimmt nicht: erwartet %s, bekommen %s",
			want, got)
	}

	// Die Platzhalterseite von CreateSite weg — sonst liefert Nginx sie
	// weiterhin aus: die site.conf.tmpl versucht index.html vor index.php.
	_ = os.Remove(filepath.Join(dest, "index.html"))

	entries, err := os.ReadDir(tmp)
	if err != nil {
		return "", fmt.Errorf("ausgepacktes verzeichnis lesen: %w", err)
	}
	for _, e := range entries {
		von := filepath.Join(tmp, e.Name())
		nach := filepath.Join(dest, e.Name())
		if err := os.Rename(von, nach); err != nil {
			return "", fmt.Errorf("%s einsetzen: %w", e.Name(), err)
		}
	}
	return got, nil
}

// wordpressChecksum holt die veröffentlichte SHA-1-Summe des aktuellen Kerns.
//
// Was das leistet: es fängt einen abgebrochenen oder verstümmelten Download,
// und es fängt den Fall, dass zwischen Prüfsummen-Datei und Archiv etwas
// anderes ausgeliefert wird.
//
// Was es nicht leistet: Schutz vor einem übernommenen wordpress.org. Beide
// Dateien kommen von dort, und wer die eine austauschen kann, kann auch die
// andere. Der Anker ist das TLS-Zertifikat von wordpress.org, nicht diese
// Summe — dieselbe Grenze wie bei den Node-Fassungen, siehe nodeChecksum.
//
// SHA-1 und nicht SHA-256: das ist das Format, in dem WordPress seine Summe
// veröffentlicht, seit die Datei existiert. Es hier durch etwas Stärkeres zu
// ersetzen gäbe es nicht her — verglichen wird gegen das, was die Gegenstelle
// tatsächlich anbietet.
func wordpressChecksum(ctx context.Context) (string, error) {
	body, err := httpGetSigned(ctx, wordpressChecksumURL, wordpressTimeout)
	if err != nil {
		return "", err
	}
	defer body.Close()

	// Ein einzelner Read() gibt keine Zusage, den ganzen Rumpf zu liefern,
	// auch wenn er in der Praxis bei 40 Byte fast immer reicht — "fast immer"
	// ist bei einer Prüfsumme, gegen die ein Download beurteilt wird, die
	// falsche Grundlage.
	raw, err := io.ReadAll(io.LimitReader(body, 1<<10))
	if err != nil {
		return "", fmt.Errorf("prüfsumme lesen: %w", err)
	}
	return parseWordPressChecksum(raw)
}

// parseWordPressChecksum liest den Rumpf von wordpressChecksumURL: eine
// einzelne Zeile mit der SHA-1-Summe in Kleinbuchstaben, ohne Dateinamen
// daneben — anders als bei Node steht hier nichts weiter in der Datei.
func parseWordPressChecksum(raw []byte) (string, error) {
	sum := strings.ToLower(strings.TrimSpace(string(raw)))
	if len(sum) != 40 {
		return "", fmt.Errorf("die prüfsumme unter %s ist unbrauchbar (%d zeichen statt 40)",
			wordpressChecksumURL, len(sum))
	}
	for _, c := range sum {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("die prüfsumme unter %s ist unbrauchbar (%q)",
				wordpressChecksumURL, sum)
		}
	}
	return sum, nil
}
