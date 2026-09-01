package core

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// DatabaseService verwaltet MariaDB-Datenbanken und ihre Benutzer.
type DatabaseService struct {
	store   *store.Store
	agent   *agent.Client
	cfg     *config.Config
	secrets *authn.SecretBox
	quota   *QuotaService
}

func NewDatabaseService(st *store.Store, ag *agent.Client, cfg *config.Config, secrets *authn.SecretBox) *DatabaseService {
	return &DatabaseService{
		store: st, agent: ag, cfg: cfg, secrets: secrets,
		quota: NewQuotaService(st, ag, cfg, nil),
	}
}

type CreateDatabaseInput struct {
	Name      string
	SiteID    *int64
	Charset   string
	Collation string
	TenantID  int64

	// WithUser legt gleich einen passenden Benutzer an — der Normalfall, weil
	// eine Datenbank ohne Zugang niemandem nützt.
	WithUser bool
	Username string
	Password string
}

type CreateDatabaseResult struct {
	Database *store.Database `json:"database"`
	User     *store.DBUser   `json:"user,omitempty"`
	// Password wird genau einmal zurückgegeben — danach steht es nur noch
	// verschlüsselt in der Datenbank.
	Password string `json:"password,omitempty"`
}

// CreateDatabase legt Datenbank und optional den zugehörigen Benutzer an.
func (s *DatabaseService) CreateDatabase(ctx context.Context, sc store.Scope, in CreateDatabaseInput) (*CreateDatabaseResult, error) {
	if in.TenantID == 0 {
		in.TenantID = sc.TenantID
	}
	tenantScope, err := sc.ForTenant(in.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.quota.CheckCount(ctx, sc, in.TenantID, ResourceDatabases); err != nil {
		return nil, err
	}

	// Erst die Eingabe prüfen, dann normalisieren: "mein-shop" wird zu
	// "mein_shop", aber "x; DROP DATABASE mysql" wird abgelehnt statt
	// stillschweigend in einen harmlosen Namen verwandelt.
	if !store.ValidNameInput(in.Name) {
		return nil, fmt.Errorf("name %q: erlaubt sind buchstaben, ziffern, leerzeichen, "+
			"bindestrich und unterstrich, beginnend mit einem buchstaben", in.Name)
	}

	// Der Tenant-Präfix verhindert, dass sich zwei Kunden um den Namen
	// "wordpress" streiten — MySQL kennt keine Mandanten.
	prefix, err := s.tenantPrefix(ctx, tenantScope, in.TenantID)
	if err != nil {
		return nil, err
	}
	name := prefixedName(prefix, in.Name, 48)
	if !store.ValidDBName(name) {
		return nil, fmt.Errorf("aus %q ergibt sich kein gültiger datenbankname (%q)", in.Name, name)
	}

	db := &store.Database{
		TenantID: in.TenantID, SiteID: in.SiteID, Name: name,
		Charset: in.Charset, Collation: in.Collation,
	}
	if err := s.store.CreateDatabase(ctx, tenantScope, db); err != nil {
		return nil, err
	}

	if err := s.agent.CreateDatabase(ctx, db.Name, db.Charset, db.Collation); err != nil {
		// Die Zeile wieder entfernen, sonst zeigt das Panel eine Datenbank an,
		// die es auf dem Server nicht gibt.
		_ = s.store.DeleteDatabase(ctx, tenantScope, db.ID)
		return nil, fmt.Errorf("datenbank anlegen: %w", err)
	}

	res := &CreateDatabaseResult{Database: db}
	if !in.WithUser {
		return res, nil
	}

	user, password, err := s.createUser(ctx, tenantScope, db, in.Username, in.Password)
	if err != nil {
		// Die Datenbank bleibt bestehen: sie ist gültig, es fehlt nur der
		// Zugang. Ein Rückbau würde hier mehr zerstören als reparieren.
		return res, fmt.Errorf("datenbank %s angelegt, aber benutzer fehlgeschlagen: %w", db.Name, err)
	}
	res.User, res.Password = user, password
	return res, nil
}

// CreateUser legt einen weiteren Benutzer für eine bestehende Datenbank an.
func (s *DatabaseService) CreateUser(ctx context.Context, sc store.Scope, databaseID int64, username, password, grants string) (*store.DBUser, string, error) {
	db, err := s.store.GetDatabase(ctx, sc, databaseID)
	if err != nil {
		return nil, "", err
	}
	tenantScope, err := sc.ForTenant(db.TenantID)
	if err != nil {
		return nil, "", err
	}

	user, plain, err := s.createUser(ctx, tenantScope, db, username, password)
	if err != nil {
		return nil, "", err
	}
	if grants != "" && strings.ToUpper(grants) != user.Grants {
		user.Grants = strings.ToUpper(grants)
		if err := s.SetGrants(ctx, tenantScope, user.ID, user.Grants); err != nil {
			return user, plain, err
		}
	}
	return user, plain, nil
}

func (s *DatabaseService) createUser(ctx context.Context, sc store.Scope, db *store.Database, username, password string) (*store.DBUser, string, error) {
	if username == "" {
		username = db.Name
	}
	if !store.ValidNameInput(username) {
		return nil, "", fmt.Errorf("benutzername %q: erlaubt sind buchstaben, ziffern, "+
			"leerzeichen, bindestrich und unterstrich, beginnend mit einem buchstaben", username)
	}
	prefix, err := s.tenantPrefix(ctx, sc, db.TenantID)
	if err != nil {
		return nil, "", err
	}
	username = prefixedName(prefix, username, 31)
	if !store.ValidDBUser(username) {
		return nil, "", fmt.Errorf("aus %q ergibt sich kein gültiger benutzername", username)
	}

	if password == "" {
		if password, err = authn.GeneratePassword(24); err != nil {
			return nil, "", err
		}
	}
	encrypted, err := s.secrets.Encrypt(password)
	if err != nil {
		return nil, "", err
	}

	user := &store.DBUser{
		TenantID: db.TenantID, DatabaseID: db.ID, Username: username,
		HostPattern: "localhost", Grants: "ALL", PasswordEnc: encrypted,
	}
	if err := s.store.CreateDBUser(ctx, sc, user); err != nil {
		return nil, "", err
	}

	if err := s.agent.CreateDBUser(ctx, agent.MySQLUserParams{
		Username: user.Username, HostPattern: user.HostPattern,
		Database: db.Name, Grants: user.Grants, Password: password,
	}); err != nil {
		_ = s.store.DeleteDBUser(ctx, sc, user.ID)
		return nil, "", err
	}
	return user, password, nil
}

// SetGrants ändert die Berechtigungsstufe eines Benutzers.
func (s *DatabaseService) SetGrants(ctx context.Context, sc store.Scope, userID int64, grants string) error {
	user, err := s.store.GetDBUser(ctx, sc, userID)
	if err != nil {
		return err
	}
	db, err := s.store.GetDatabase(ctx, sc, user.DatabaseID)
	if err != nil {
		return err
	}
	if !store.ValidGrantSet(grants) {
		return fmt.Errorf("berechtigung %q ist unbekannt (ALL, READWRITE, READONLY)", grants)
	}

	user.Grants = strings.ToUpper(grants)
	if err := s.store.UpdateDBUser(ctx, sc, user); err != nil {
		return err
	}

	hosts, err := s.remoteHostsOf(ctx, sc, user.ID)
	if err != nil {
		return err
	}
	var failed []string
	for _, host := range append([]string{user.HostPattern}, hosts...) {
		if err := s.agent.GrantDBUser(ctx, agent.MySQLUserParams{
			Username: user.Username, HostPattern: host,
			Database: db.Name, Grants: user.Grants,
		}); err != nil {
			failed = append(failed, host+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("die rechte gelten nicht für alle herkünfte: %s",
			strings.Join(failed, "; "))
	}
	return nil
}

// SetPassword setzt das Passwort eines Datenbankbenutzers neu.
func (s *DatabaseService) SetPassword(ctx context.Context, sc store.Scope, userID int64, password string) (string, error) {
	user, err := s.store.GetDBUser(ctx, sc, userID)
	if err != nil {
		return "", err
	}
	if password == "" {
		if password, err = authn.GeneratePassword(24); err != nil {
			return "", err
		}
	}

	// Das Konto auf localhost zuerst und für sich: scheitert es, hat sich
	// nichts geändert, und das gespeicherte Passwort bleibt das gültige.
	if err := s.agent.SetDBUserPassword(ctx, user.Username, user.HostPattern, password); err != nil {
		return "", err
	}
	// Erst nach dem erfolgreichen Wechsel speichern — sonst zeigt das Panel
	// ein Passwort an, das gar nicht gilt.
	if user.PasswordEnc, err = s.secrets.Encrypt(password); err != nil {
		return "", err
	}
	if err := s.store.UpdateDBUser(ctx, sc, user); err != nil {
		return "", err
	}

	// Die Konten der Herkünfte danach. Ein Fehler hier ist keiner, der das
	// Gespeicherte falsch macht — er bedeutet, dass ein Zugang von außen noch
	// auf dem alten Passwort steht. Das muss gesagt werden, und weil das neue
	// Passwort schon in der Datenbank steht, hilft ein zweiter Versuch.
	hosts, err := s.remoteHostsOf(ctx, sc, user.ID)
	if err != nil {
		return "", err
	}
	var failed []string
	for _, host := range hosts {
		if err := s.agent.SetDBUserPassword(ctx, user.Username, host, password); err != nil {
			failed = append(failed, host+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return "", fmt.Errorf("das passwort gilt für %s, aber nicht für alle herkünfte: %s. "+
			"das neue passwort ist gespeichert und lässt sich anzeigen; ein zweiter versuch "+
			"holt die fehlenden nach", user.HostPattern, strings.Join(failed, "; "))
	}
	return password, nil
}

// RevealPassword entschlüsselt das gespeicherte Passwort für die Anzeige.
func (s *DatabaseService) RevealPassword(ctx context.Context, sc store.Scope, userID int64) (string, error) {
	user, err := s.store.GetDBUser(ctx, sc, userID)
	if err != nil {
		return "", err
	}
	if user.PasswordEnc == "" {
		return "", errors.New("für diesen benutzer ist kein passwort hinterlegt")
	}
	return s.secrets.Decrypt(user.PasswordEnc)
}

func (s *DatabaseService) DeleteUser(ctx context.Context, sc store.Scope, userID int64) error {
	user, err := s.store.GetDBUser(ctx, sc, userID)
	if err != nil {
		return err
	}
	// Die Herkünfte zuerst: sie sind die Konten, über die eine Verbindung von
	// außen möglich ist. Bliebe eines stehen, wäre der Zugang weiter offen,
	// während das Panel den Benutzer als gelöscht führt.
	hosts, err := s.remoteHostsOf(ctx, sc, userID)
	if err != nil {
		return err
	}
	for _, host := range append(hosts, user.HostPattern) {
		if err := s.agent.DropDBUser(ctx, user.Username, host); err != nil {
			return fmt.Errorf("konto %s@%s entfernen: %w", user.Username, host, err)
		}
	}
	// db_remote_hosts hängt per ON DELETE CASCADE am Benutzer.
	return s.store.DeleteDBUser(ctx, sc, userID)
}

// DeleteDatabase entfernt die Datenbank samt aller zugehörigen Benutzer.
func (s *DatabaseService) DeleteDatabase(ctx context.Context, sc store.Scope, databaseID int64) error {
	db, err := s.store.GetDatabase(ctx, sc, databaseID)
	if err != nil {
		return err
	}

	users, err := s.store.ListDBUsers(ctx, sc, databaseID)
	if err != nil {
		return err
	}
	var problems []string
	for _, user := range users {
		hosts, err := s.remoteHostsOf(ctx, sc, user.ID)
		if err != nil {
			return err
		}
		for _, host := range append(hosts, user.HostPattern) {
			if err := s.agent.DropDBUser(ctx, user.Username, host); err != nil {
				problems = append(problems, user.Username+"@"+host+": "+err.Error())
			}
		}
	}

	if err := s.agent.DropDatabase(ctx, db.Name); err != nil {
		return fmt.Errorf("datenbank %s entfernen: %w", db.Name, err)
	}
	// Die db_users hängen per ON DELETE CASCADE an der Datenbank.
	if err := s.store.DeleteDatabase(ctx, sc, databaseID); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("datenbank %s entfernt, aber benutzer blieben zurück: %s",
			db.Name, strings.Join(problems, "; "))
	}
	return nil
}

// SyncSizes holt die tatsächliche Belegung vom Server und schreibt sie zurück.
func (s *DatabaseService) SyncSizes(ctx context.Context) error {
	sizes, err := s.agent.DatabaseSizes(ctx)
	if err != nil {
		return err
	}
	for name, size := range sizes {
		if err := s.store.UpdateDatabaseSize(ctx, name, size); err != nil {
			return err
		}
	}
	return nil
}

// maxImportBytes deckelt die eingespielte SQL-Datei nach dem Auspacken.
//
// Die Grenze gilt für die entpackte Größe, nicht für den Upload: sonst wäre ein
// kleines gzip mit riesigem Inhalt ein Weg, die Platte vollzuschreiben.
const maxImportBytes = 1 << 30 // 1 GiB

// dumpPath baut den Ablageort für einen Dump. Der Name kommt aus der Datenbank
// und dem Zeitstempel, nie aus der Anfrage.
func (s *DatabaseService) dumpPath(name string) (string, string, error) {
	dir := filepath.Join(s.cfg.BackupDir, "dumps")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", fmt.Errorf("dump-verzeichnis: %w", err)
	}
	file := DumpName(name)
	return filepath.Join(dir, file), file, nil
}

// Dump schreibt einen SQL-Dump ins Backup-Verzeichnis und gibt den Pfad zurück.
func (s *DatabaseService) Dump(ctx context.Context, sc store.Scope, databaseID int64) (string, int64, error) {
	db, err := s.store.GetDatabase(ctx, sc, databaseID)
	if err != nil {
		return "", 0, err
	}
	path, _, err := s.dumpPath(db.Name)
	if err != nil {
		return "", 0, err
	}
	size, err := s.agent.DumpDatabase(ctx, db.Name, path)
	return path, size, err
}

// DumpTo erzeugt einen Dump und streamt ihn direkt weiter, ohne ihn liegen zu
// lassen. Der Umweg über die Datei ist nötig, weil mysqldump auf ein Handle
// schreibt und der Agent das Ergebnis nur blockweise zurückgeben kann.
func (s *DatabaseService) DumpTo(ctx context.Context, sc store.Scope, databaseID int64, w io.Writer) (int64, error) {
	db, err := s.store.GetDatabase(ctx, sc, databaseID)
	if err != nil {
		return 0, err
	}
	path, _, err := s.dumpPath(db.Name)
	if err != nil {
		return 0, err
	}

	if _, err := s.agent.DumpDatabase(ctx, db.Name, path); err != nil {
		return 0, err
	}
	// Die Datei gehört root und mit 0600 niemandem sonst — sie muss über den
	// Agent wieder abgeräumt werden, nicht mit os.Remove.
	defer func() {
		if err := s.agent.RemovePath(context.WithoutCancel(ctx), path, false); err != nil {
			slog.Warn("dump nicht aufgeräumt", "pfad", path, "err", err)
		}
	}()

	return streamFromAgent(ctx, s.agent, path, w)
}

// DumpName ist der Dateiname, unter dem ein Dump beim Aufrufer ankommt.
func DumpName(database string) string {
	return fmt.Sprintf("%s-%s.sql", database, time.Now().UTC().Format("20060102-150405"))
}

// Import spielt eine hochgeladene SQL-Datei in eine Datenbank ein.
//
// Der Strom wird erst auf die Platte geschrieben und dann eingelesen. Ihn
// direkt in den mysql-Client zu leiten ginge nicht: zwischen Web-Prozess und
// Agent liegt das Socket-Protokoll, das einzelne Nachrichten deckelt.
func (s *DatabaseService) Import(ctx context.Context, sc store.Scope, databaseID int64, src io.Reader) (int64, error) {
	db, err := s.store.GetDatabase(ctx, sc, databaseID)
	if err != nil {
		return 0, err
	}

	dir := filepath.Join(s.cfg.BackupDir, "imports")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, fmt.Errorf("import-verzeichnis: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "import-*.sql")
	if err != nil {
		return 0, fmt.Errorf("zwischendatei: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	written, err := copyUnpacked(tmp, src, maxImportBytes)
	if err != nil {
		return written, err
	}
	if written == 0 {
		return 0, fmt.Errorf("die datei ist leer")
	}
	if err := tmp.Close(); err != nil {
		return written, fmt.Errorf("zwischendatei: %w", err)
	}

	return written, s.agent.ImportDatabase(ctx, db.Name, tmp.Name())
}

// copyUnpacked schreibt den Strom weiter und packt ihn dabei aus, falls er
// gzip-komprimiert ankommt. Erkannt wird das an den ersten beiden Bytes, nicht
// an der Dateiendung — die kommt vom Browser und sagt nichts.
func copyUnpacked(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	buf := bufio.NewReader(src)
	head, err := buf.Peek(2)
	if err != nil || head[0] != 0x1f || head[1] != 0x8b {
		return copyLimited(dst, buf, limit)
	}

	written, err := unpackGzip(dst, buf, limit)
	// Die Größengrenze erklärt sich selbst; alles andere ist ein Fehler im
	// Archiv und braucht einen Satz davor. "flate: corrupt input before
	// offset 9" allein sagt niemandem, was zu tun ist.
	if err != nil && !errors.Is(err, errTooLarge) {
		return written, fmt.Errorf(
			"die datei ist gzip-gepackt, lässt sich aber nicht auspacken: %w", err)
	}
	return written, err
}

func unpackGzip(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	return copyLimited(dst, gz, limit)
}

// errTooLarge trennt die Größengrenze von einem kaputten Archiv. Nur die
// Grenze hat schon eine verständliche Meldung.
var errTooLarge = errors.New("größengrenze erreicht")

func copyLimited(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	n, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return limit, fmt.Errorf("die datei ist entpackt größer als %d bytes: %w", limit, errTooLarge)
	}
	return n, nil
}

// tenantPrefix liefert den Namensraum eines Mandanten in MySQL.
func (s *DatabaseService) tenantPrefix(ctx context.Context, sc store.Scope, tenantID int64) (string, error) {
	tenant, err := s.store.GetTenant(ctx, sc, tenantID)
	if err != nil {
		return "", err
	}
	slug := reNonAlnum.ReplaceAllString(strings.ToLower(tenant.Slug), "_")
	return strings.Trim(slug, "_"), nil
}

// prefixedName setzt den Tenant-Präfix davor und hält die Längengrenze ein.
//
// Ist der Name bereits richtig präfigiert, bleibt er unverändert — sonst würde
// jede Bearbeitung den Präfix ein weiteres Mal anhängen.
func prefixedName(prefix, name string, maxLen int) string {
	name = strings.Trim(reNonAlnum.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if prefix == "" {
		return truncateName(name, maxLen)
	}
	if strings.HasPrefix(name, prefix+"_") {
		return truncateName(name, maxLen)
	}
	return truncateName(prefix+"_"+name, maxLen)
}

func truncateName(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return strings.TrimRight(s[:maxLen], "_")
}
