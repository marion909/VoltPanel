package core

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/version"
)

// Einen Mandanten mitnehmen.
//
// Das Backup ist serverweit — für eine Sicherung richtig, für einen Umzug zu
// grob. Hier entsteht ein Bündel, das genau einen Mandanten enthält: seine
// Zeilen, seine Dateien, seine Datenbanken.
//
// Der heikle Teil sind die Geheimnisse. In der Datenbank stehen sie
// verschlüsselt, aber mit dem Schlüssel *dieses* Servers — auf einem anderen
// wären sie unlesbar. Sie im Klartext mitzugeben hieße, eine Datei zu erzeugen,
// in der die Passwörter aller Kunden dieses Mandanten stehen. Also werden sie
// umgeschlüsselt: auf einen Schlüssel, den der Betreiber kennt und der Server
// nirgends aufbewahrt.

const (
	// bundleName ist die Datei mit allem außer den Dateien.
	bundleName = "bundle.json"
	// exportSitesDir und exportDBDir sind die Verzeichnisse im Archiv.
	exportSitesDir = "sites"
	exportDBDir    = "databases"

	// minPassphrase ist die Untergrenze. Kürzer wäre der Schlüssel des
	// Bündels schwächer als jedes Passwort darin.
	minPassphrase = 12
)

// argon2-Parameter. Das Bündel wird selten erzeugt und selten gelesen; ein
// paar hundert Millisekunden fallen dabei nicht ins Gewicht, gegen einen
// Angriff auf die Passphrase zählen sie.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
)

// ExportService packt einen Mandanten in ein Archiv und wieder aus.
type ExportService struct {
	cfg     *config.Config
	store   *store.Store
	agent   *agent.Client
	secrets *authn.SecretBox
	log     *slog.Logger
}

func NewExportService(cfg *config.Config, st *store.Store, ag *agent.Client,
	secrets *authn.SecretBox, log *slog.Logger) *ExportService {

	if log == nil {
		log = slog.Default()
	}
	return &ExportService{cfg: cfg, store: st, agent: ag, secrets: secrets, log: log}
}

// ExportResult beschreibt ein fertiges Bündel.
type ExportResult struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Checksum  string `json:"checksum"`
	Sites     int    `json:"sites"`
	Databases int    `json:"databases"`
	// Warnings sind Teile, die nicht mitkonnten. Ein Export, der still
	// weniger mitnimmt als erwartet, ist schlimmer als einer, der es sagt.
	Warnings []string `json:"warnings,omitempty"`
}

// ExportTenant schreibt ein Bündel und gibt zurück, was darin steht.
//
// Die Passphrase schützt die Geheimnisse im Bündel — nicht das Bündel selbst.
// Die Dateien der Sites und die Datenbankauszüge liegen darin wie in jedem
// Backup: lesbar für den, der die Datei hat. Das steht auch so in der Meldung,
// damit niemand die Passphrase für mehr hält, als sie ist.
func (s *ExportService) ExportTenant(ctx context.Context, sc store.Scope,
	tenantID int64, passphrase string) (*ExportResult, error) {

	if len([]rune(passphrase)) < minPassphrase {
		return nil, fmt.Errorf("die passphrase muss mindestens %d zeichen haben — "+
			"sie schützt die passwörter aller zugänge dieses mandanten", minPassphrase)
	}

	bundle, err := CollectTenant(ctx, s.store, sc, tenantID)
	if err != nil {
		return nil, err
	}
	bundle.Schema = version.SchemaVersion
	bundle.Version = version.Version
	bundle.ExportedAt = time.Now().Unix()
	bundle.Source = s.cfg.PanelDomain

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("salz erzeugen: %w", err)
	}
	box, err := passphraseBox(passphrase, salt)
	if err != nil {
		return nil, err
	}
	res := &ExportResult{}
	if err := s.rekeySecrets(bundle, box, salt, res); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(s.cfg.BackupDir, 0o750); err != nil {
		return nil, fmt.Errorf("backup-verzeichnis: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	res.Path = filepath.Join(s.cfg.BackupDir,
		fmt.Sprintf("mandant-%s-%s.tar.gz", bundle.Tenant.Slug, stamp))

	// 0600: darin stehen Datenbankauszüge und die Dateien der Kunden.
	f, err := os.OpenFile(res.Path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	hasher := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, hasher))
	tw := tar.NewWriter(gz)

	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := addBytes(tw, bundleName, raw); err != nil {
		return nil, err
	}

	for _, site := range bundle.Sites {
		if site.RootPath == "" {
			continue
		}
		if err := addTree(ctx, tw, site.RootPath,
			filepath.Join(exportSitesDir, site.Domain)); err != nil {

			// Eine Site ohne Verzeichnis ist kein Grund, den ganzen Export
			// abzubrechen — aber sie gehört benannt.
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s: dateien nicht gesichert (%v)", site.Domain, err))
			continue
		}
		res.Sites++
	}

	s.addDatabases(ctx, tw, bundle, res)

	// Reihenfolge zählt: tar schließen, dann gzip.
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	res.SizeBytes = info.Size()
	res.Checksum = hex.EncodeToString(hasher.Sum(nil))
	return res, nil
}

// addDatabases hängt die Auszüge an. Ohne Agent geht das nicht — dann steht
// eine Warnung im Ergebnis, statt dass ein halber Umzug entsteht.
func (s *ExportService) addDatabases(ctx context.Context, tw *tar.Writer,
	bundle *TenantBundle, res *ExportResult) {

	if len(bundle.Databases) == 0 {
		return
	}
	if s.agent == nil {
		res.Warnings = append(res.Warnings,
			"datenbanken nicht gesichert: kein agent verfügbar")
		return
	}

	for _, db := range bundle.Databases {
		tmp, err := os.CreateTemp(s.cfg.BackupDir, ".volt-dump-*.sql")
		if err != nil {
			res.Warnings = append(res.Warnings, db.Name+": "+err.Error())
			continue
		}
		path := tmp.Name()
		tmp.Close()

		if _, err := s.agent.DumpDatabase(ctx, db.Name, path); err != nil {
			os.Remove(path)
			res.Warnings = append(res.Warnings, db.Name+": "+err.Error())
			continue
		}
		if err := addFile(tw, path, filepath.Join(exportDBDir, db.Name+".sql")); err != nil {
			os.Remove(path)
			res.Warnings = append(res.Warnings, db.Name+": "+err.Error())
			continue
		}
		os.Remove(path)
		res.Databases++
	}
}

// rekeySecrets holt jedes Geheimnis aus der Verschlüsselung dieses Servers und
// legt es unter der Passphrase wieder ab.
//
// Die Zeilen selbst behalten dabei ihre alten Werte nicht: sie werden geleert.
// Sonst stünde in bundle.json beides, und der Import müsste raten, welches
// gilt — und auf dem Zielserver wäre das alte ohnehin unlesbar.
//
// Zwei Schichten decken das ab, und beide sind nötig. Alle Geheimnisfelder
// tragen `json:"-"`, kommen also gar nicht erst in die Datei; und hier werden
// sie zusätzlich geleert. Nachgemessen: nimmt man einer dieser Schichten weg,
// hält die andere — nimmt man beide weg, steht der Geheimtext dieses Servers
// im Bündel.
func (s *ExportService) rekeySecrets(b *TenantBundle, box *authn.SecretBox,
	salt []byte, res *ExportResult) error {

	b.Secrets["salt"] = hex.EncodeToString(salt)

	// Jedes Feld einzeln, mit einem Schlüssel, der sagt, wohin es gehört.
	umschluesseln := func(key string, feld *string) {
		if *feld == "" {
			return
		}
		klartext, err := s.secrets.Decrypt(*feld)
		if err != nil {
			// Ein Geheimnis, das dieser Server selbst nicht mehr lesen kann,
			// bleibt weg. Es mitzunehmen brächte nichts — und stillschweigend
			// wegzulassen wäre schlimmer, als es zu sagen.
			res.Warnings = append(res.Warnings, key+": nicht entschlüsselbar, bleibt leer")
			*feld = ""
			return
		}
		neu, err := box.Encrypt(klartext)
		if err != nil {
			res.Warnings = append(res.Warnings, key+": "+err.Error())
			*feld = ""
			return
		}
		b.Secrets[key] = neu
		*feld = ""
	}

	umschluesseln("tenant.cloudflare_token", &b.Tenant.CloudflareToken)
	for _, u := range b.Users {
		umschluesseln(fmt.Sprintf("user.%d.totp", u.ID), &u.TOTPSecret)
	}
	for _, list := range b.DBUsers {
		for _, u := range list {
			umschluesseln(fmt.Sprintf("dbuser.%d.password", u.ID), &u.PasswordEnc)
		}
	}
	for _, list := range b.FTP {
		for _, a := range list {
			umschluesseln(fmt.Sprintf("ftp.%d.password", a.ID), &a.PasswordEnc)
		}
	}
	for _, t := range b.Targets {
		umschluesseln(fmt.Sprintf("target.%d.secret", t.ID), &t.SecretEnc)
	}
	for _, a := range b.Apps {
		umschluesseln(fmt.Sprintf("app.%d.env", a.ID), &a.EnvEnc)
	}
	for _, d := range b.Deploys {
		umschluesseln(fmt.Sprintf("deploy.%d.hook", d.ID), &d.HookSecretEnc)
	}
	return nil
}

// passphraseBox leitet den Schlüssel des Bündels aus der Passphrase ab.
//
// argon2id, nicht ein einfaches Hashen: eine Passphrase ist kein Schlüssel, und
// der Unterschied ist, wie viele Versuche pro Sekunde jemand machen kann, der
// das Bündel in die Hand bekommt.
func passphraseBox(passphrase string, salt []byte) (*authn.SecretBox, error) {
	if len(salt) < 16 {
		return nil, errors.New("das salz des bündels ist zu kurz")
	}
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, 32)
	return authn.NewSecretBox(key)
}

// addBytes schreibt einen Puffer als Datei ins Archiv.
func addBytes(tw *tar.Writer, name string, data []byte) error {
	h := &tar.Header{
		Name: name, Mode: 0o600, Size: int64(len(data)),
		ModTime: time.Now(), Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// bundleSecret holt ein Geheimnis aus dem Bündel zurück in den Klartext.
func bundleSecret(b *TenantBundle, box *authn.SecretBox, key string) (string, bool) {
	enc, ok := b.Secrets[key]
	if !ok || enc == "" {
		return "", false
	}
	klartext, err := box.Decrypt(enc)
	if err != nil {
		return "", false
	}
	return klartext, true
}

// OpenBundle liest bundle.json aus einem Archiv und entschlüsselt die
// Geheimnisse.
//
// Getrennt vom Einspielen, weil man ein Bündel ansehen können soll, bevor man
// es einspielt: was steckt darin, von welchem Server, gegen welches Schema.
func OpenBundle(path, passphrase string) (*TenantBundle, *authn.SecretBox, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, fmt.Errorf("archiv lesen: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("archiv lesen: %w", err)
		}
		if strings.TrimPrefix(h.Name, "./") != bundleName {
			continue
		}

		raw, err := io.ReadAll(io.LimitReader(tr, 64<<20))
		if err != nil {
			return nil, nil, err
		}
		var b TenantBundle
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, nil, fmt.Errorf("%s unlesbar: %w", bundleName, err)
		}

		salt, err := hex.DecodeString(b.Secrets["salt"])
		if err != nil || len(salt) < 16 {
			return nil, nil, errors.New("dem bündel fehlt das salz — es lässt sich nicht öffnen")
		}
		box, err := passphraseBox(passphrase, salt)
		if err != nil {
			return nil, nil, err
		}

		// Eine Probe: passt die Passphrase nicht, soll das jetzt auffallen und
		// nicht mitten im Einspielen.
		if err := probeBundle(&b, box); err != nil {
			return nil, nil, err
		}
		return &b, box, nil
	}
	return nil, nil, fmt.Errorf("in %s steckt kein %s", filepath.Base(path), bundleName)
}

// probeBundle prüft die Passphrase an einem beliebigen Geheimnis.
func probeBundle(b *TenantBundle, box *authn.SecretBox) error {
	for key, enc := range b.Secrets {
		if key == "salt" || enc == "" {
			continue
		}
		if _, err := box.Decrypt(enc); err != nil {
			return errors.New("die passphrase passt nicht zu diesem bündel")
		}
		return nil
	}
	// Ein Bündel ohne Geheimnisse: dann gibt es nichts zu prüfen, und das ist
	// in Ordnung — ein Mandant ohne FTP-Zugang, ohne Datenbankbenutzer und
	// ohne Token hat schlicht keine.
	return nil
}
