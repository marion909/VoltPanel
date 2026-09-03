package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/marion909/voltpanel/internal/templates"
)

// Webmail (Roundcube) als server-weite Installation. Kein App-Store-Eintrag
// (internal/core/appstore.go) — es gehört keinem Mandanten, jedes Postfach
// jedes Mandanten soll sich hier anmelden. Der Kern legt vorher an, was auch
// eine Site bräuchte (Systembenutzer, PHP-FPM-Pool, Datenbank samt Benutzer)
// — aber ohne eine Site- oder Datenbank-Zeile, denn beide Tabellen haben eine
// harte tenant_id. Was hier passiert, ist nur der Teil, den keine vorhandene
// Operation abdeckt: Roundcube holen, prüfen, auspacken, konfigurieren, sein
// eigenes Datenbankschema einspielen.
//
// Derselbe sichere Weg wie bei WordPress: Archiv laden, gegen eine
// Prüfsumme halten, mit archive/tar selbst auspacken (fetchAndExtract aus
// ops_node.go). Ein Unterschied: WordPress veröffentlicht eine
// Prüfsummen-Datei unter einer festen Adresse, dagegen wird jedes Mal
// geprüft. Roundcube tut das nicht — die Summe steht deshalb hier fest,
// zusammen mit der Version. Ein Versionswechsel ist ein Codeänderung, keine
// Handarbeit auf dem Server: roundcubeURL und roundcubeSHA256 zusammen
// ändern, nie nur eines von beiden.

var (
	roundcubeURL    = "https://github.com/roundcube/roundcubemail/releases/download/1.7.3/roundcubemail-1.7.3-complete.tar.gz"
	roundcubeSHA256 = "443cde2ea03b840ce4701fe23c273f01e68702f176d282e60248236bbb5f5f85"
)

const (
	// Das echte Archiv ist rund 6.4 MB; die Decke ist großzügig gegen eine
	// Gegenstelle, die ins Unermessliche antwortet, nicht als Erwartung.
	roundcubeDownloadMax = 64 << 20
	roundcubeTimeout     = 5 * time.Minute
)

// WebmailInstallParams beschreibt, wohin Roundcube kommt und womit es sich
// verbindet. Datenbank und Systembenutzer bestehen schon — angelegt vom Kern
// über dieselben Bausteine wie bei jeder anderen Datenbank oder Site.
type WebmailInstallParams struct {
	WebRoot    string `json:"web_root"`
	SystemUser string `json:"system_user"`
	WebGroup   string `json:"web_group"`

	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	DBPassword string `json:"db_password"`

	IMAPPort int `json:"imap_port"`
	SMTPPort int `json:"smtp_port"`
}

// opWebmailInstall holt den Roundcube-Kern, konfiguriert ihn und spielt sein
// Datenbankschema ein.
func (s *Server) opWebmailInstall(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[WebmailInstallParams](raw, OpWebmailInstall)
	if err != nil {
		return nil, err
	}
	// unprivilegierteIDs, nicht siteUserIDs: der Systembenutzer gehört keiner
	// Site, sondern ist ein eigenes Dienstkonto, das der Agent selbst angelegt
	// hat — dieselbe Kategorie wie vmail und opendkim.
	if _, _, err := unprivilegierteIDs(OpWebmailInstall, p.SystemUser); err != nil {
		return nil, err
	}
	if err := checkMySQLName("datenbankname", p.DBName, reMyDBName); err != nil {
		return nil, err
	}
	dest, err := jail(p.WebRoot, s.roots)
	if err != nil {
		return nil, err
	}

	if err := installRoundcubeFiles(ctx, dest); err != nil {
		return nil, opErr(OpWebmailInstall, "%v", err)
	}

	desKey, err := templates.NewRoundcubeDESKey()
	if err != nil {
		return nil, opErr(OpWebmailInstall, "des-schlüssel: %v", err)
	}
	cfg, err := templates.RenderRoundcubeConfig(templates.RoundcubeConfigData{
		DBUser: p.DBUser, DBPassword: p.DBPassword, DBName: p.DBName,
		IMAPPort: p.IMAPPort, SMTPPort: p.SMTPPort, DESKey: desKey,
	})
	if err != nil {
		return nil, opErr(OpWebmailInstall, "konfiguration: %v", err)
	}
	if err := writeFileAtomic(filepath.Join(dest, "config", "config.inc.php"), []byte(cfg), 0o640); err != nil {
		return nil, opErr(OpWebmailInstall, "konfiguration schreiben: %v", err)
	}

	// Roundcubes eigener Installationsassistent beweist nur einmal etwas: bei
	// der Einrichtung. Danach ist er eine offene Tür — jeder unauthentifizierte
	// Besucher sähe PHP- und Datenbankversion und könnte den Assistenten
	// erneut anstoßen.
	if err := os.RemoveAll(filepath.Join(dest, "installer")); err != nil {
		return nil, opErr(OpWebmailInstall, "installer entfernen: %v", err)
	}

	// Derselbe grund wie beim auspacken oben: ein zweiter versuch nach einem
	// fehlschlag weiter unten (zertifikat, vhost) trifft hier auf die
	// tabellen des ersten. Roundcubes mysql.initial.sql kennt kein "CREATE
	// TABLE IF NOT EXISTS" — ein zweiter import schlüge mit "table already
	// exists" fehl, geschehen auf einem echten server. Die datenbank gehört
	// ausschließlich dieser einen installation (angelegt von
	// WebmailService.Install eigens dafür), ein leeren vor dem einspielen
	// ist also kein Verlust. Privilegien überleben ein DROP DATABASE — MySQL
	// bindet sie an den namen, nicht an die existenz des schemas —,
	// CreateDBUser weiter oben in WebmailService.Install muss deshalb nicht
	// erneut laufen.
	if err := resetWebmailDatabase(ctx, p.DBName); err != nil {
		return nil, opErr(OpWebmailInstall, "%v", err)
	}

	sqlPath := filepath.Join(dest, "SQL", "mysql.initial.sql")
	f, err := os.Open(sqlPath)
	if err != nil {
		return nil, opErr(OpWebmailInstall, "schema-datei: %v", err)
	}
	defer f.Close()
	if err := s.importSQLFile(ctx, p.DBName, f); err != nil {
		return nil, opErr(OpWebmailInstall, "datenbankschema: %v", err)
	}

	if err := applyOwner(dest, p.SystemUser, p.WebGroup, true); err != nil {
		return nil, opErr(OpWebmailInstall, "rechte setzen: %v", err)
	}

	return TextResult{Text: "webmail ausgepackt und eingerichtet"}, nil
}

// resetWebmailDatabase leert die Datenbank vor dem Einspielen des Schemas —
// idempotent im selben Sinn wie installRoundcubeFiles: ein zweiter Versuch
// ersetzt, statt an Resten zu scheitern.
func resetWebmailDatabase(ctx context.Context, name string) error {
	db, err := mysqlConn()
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(name)); err != nil {
		return fmt.Errorf("datenbank leeren: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		"CREATE DATABASE "+quoteIdent(name)+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		return fmt.Errorf("datenbank neu anlegen: %w", err)
	}
	return nil
}

// installRoundcubeFiles holt und prüft den Roundcube-Kern und packt ihn in
// dest aus.
//
// Vom Konfigurieren und von den Rechten getrennt, damit der Teil mit dem
// Netzwerkaufruf sich unabhängig von einer echten Datenbank oder einem
// echten Systembenutzer prüfen lässt — derselbe Schnitt wie bei
// installWordPressFiles.
func installRoundcubeFiles(ctx context.Context, dest string) error {
	tmp, err := os.MkdirTemp(dest, ".volt-webmail-*")
	if err != nil {
		return fmt.Errorf("arbeitsverzeichnis: %w", err)
	}
	defer os.RemoveAll(tmp)

	got, err := fetchAndExtract(ctx, roundcubeURL, tmp, roundcubeTimeout, roundcubeDownloadMax, sha256.New())
	if err != nil {
		return err
	}
	if got != roundcubeSHA256 {
		return fmt.Errorf("die prüfsumme des roundcube-kerns stimmt nicht: erwartet %s, bekommen %s",
			roundcubeSHA256, got)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		return fmt.Errorf("ausgepacktes verzeichnis lesen: %w", err)
	}
	for _, e := range entries {
		von := filepath.Join(tmp, e.Name())
		nach := filepath.Join(dest, e.Name())
		// Ein zweiter Versuch nach einem Fehlschlag weiter unten (Datenbank,
		// Zertifikat, Vhost) trifft hier auf die Reste des ersten — Install
		// schreibt seine Zeile in die webmail-Tabelle erst ganz am Ende, also
		// gibt es beim Wiederholen noch keine, die einen Neuversuch verböte.
		// os.Rename schlüge sonst an genau der Stelle fehl, an der der erste
		// Versuch stehengeblieben ist. Idempotent heißt hier: die frisch
		// geprüften Dateien ersetzen die Reste, statt an ihnen zu scheitern.
		if err := os.RemoveAll(nach); err != nil {
			return fmt.Errorf("%s vor dem einsetzen entfernen: %w", e.Name(), err)
		}
		if err := os.Rename(von, nach); err != nil {
			return fmt.Errorf("%s einsetzen: %w", e.Name(), err)
		}
	}
	return nil
}
