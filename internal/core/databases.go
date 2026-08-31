package core

import (
	"context"
	"errors"
	"fmt"
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
	return s.agent.GrantDBUser(ctx, agent.MySQLUserParams{
		Username: user.Username, HostPattern: user.HostPattern,
		Database: db.Name, Grants: user.Grants,
	})
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
	if err := s.agent.DropDBUser(ctx, user.Username, user.HostPattern); err != nil {
		return err
	}
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
		if err := s.agent.DropDBUser(ctx, user.Username, user.HostPattern); err != nil {
			problems = append(problems, user.Username+": "+err.Error())
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

// Dump schreibt einen SQL-Dump ins Backup-Verzeichnis und gibt den Pfad zurück.
func (s *DatabaseService) Dump(ctx context.Context, sc store.Scope, databaseID int64) (string, int64, error) {
	db, err := s.store.GetDatabase(ctx, sc, databaseID)
	if err != nil {
		return "", 0, err
	}

	stamp := time.Now().UTC().Format("20060102-150405")
	path := filepath.Join(s.cfg.BackupDir, "dumps", fmt.Sprintf("%s-%s.sql", db.Name, stamp))
	size, err := s.agent.DumpDatabase(ctx, db.Name, path)
	return path, size, err
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
