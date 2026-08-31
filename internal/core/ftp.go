package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// FTPService verwaltet virtuelle Pure-FTPd-Zugänge.
//
// Ein Zugang hängt immer an einer Site und arbeitet unter deren
// Systembenutzer. Es gibt keinen freistehenden FTP-Zugang: ohne Site gäbe es
// weder ein Verzeichnis, in das er gehört, noch ein Konto, unter dem er läuft.
type FTPService struct {
	store   *store.Store
	agent   *agent.Client
	cfg     *config.Config
	secrets *authn.SecretBox
	quota   *QuotaService
}

func NewFTPService(st *store.Store, ag *agent.Client, cfg *config.Config, secrets *authn.SecretBox) *FTPService {
	return &FTPService{
		store: st, agent: ag, cfg: cfg, secrets: secrets,
		quota: NewQuotaService(st, ag, cfg, nil),
	}
}

// Setup richtet den Dienst ein. Das geschieht nicht bei der Installation:
// Pure-FTPd gehört nicht auf jeden Server, und ein Dienst, der nur deshalb
// läuft, weil er mitinstalliert wurde, ist offene Angriffsfläche ohne Nutzen.
func (s *FTPService) Setup(ctx context.Context) (*agent.FTPSetupResult, error) {
	return s.agent.FTPSetup(ctx)
}

func (s *FTPService) Status(ctx context.Context) (*agent.FTPSetupResult, error) {
	return s.agent.FTPStatus(ctx)
}

// CreateInput beschreibt einen neuen Zugang.
type CreateFTPInput struct {
	SiteID int64
	// Username ohne Mandantenpräfix; er wird davorgesetzt, damit sich zwei
	// Kunden nicht denselben Namen greifen können. Die PureDB kennt nur einen
	// Namensraum für den ganzen Server.
	Username string
	Password string
	// Subdir grenzt den Zugang auf ein Unterverzeichnis der Site ein. Leer
	// heißt: das Wurzelverzeichnis der Site.
	Subdir  string
	QuotaMB int64
}

// Create legt einen Zugang an und trägt ihn beim Dienst ein.
func (s *FTPService) Create(ctx context.Context, sc store.Scope, in CreateFTPInput) (*store.FTPAccount, string, error) {
	site, err := s.store.GetSite(ctx, sc, in.SiteID)
	if err != nil {
		return nil, "", err
	}
	if site.SystemUser == "" {
		return nil, "", errors.New("diese site hat keinen systembenutzer — bitte neu erzeugen lassen")
	}
	tenantScope, err := sc.ForTenant(site.TenantID)
	if err != nil {
		return nil, "", err
	}
	if err := s.quota.CheckCount(ctx, tenantScope, site.TenantID, ResourceFTP); err != nil {
		return nil, "", err
	}

	username, err := s.buildName(ctx, tenantScope, site, in.Username)
	if err != nil {
		return nil, "", err
	}
	home, err := joinInside(site.RootPath, in.Subdir)
	if err != nil {
		return nil, "", err
	}

	password := in.Password
	if password == "" {
		if password, err = authn.GeneratePassword(20); err != nil {
			return nil, "", err
		}
	}
	encrypted, err := s.secrets.Encrypt(password)
	if err != nil {
		return nil, "", err
	}

	account := &store.FTPAccount{
		TenantID: site.TenantID, SiteID: &site.ID, Username: username,
		PasswordEnc: encrypted, HomeDir: home, QuotaMB: in.QuotaMB, Status: "active",
		// Vorläufige Werte: die echten kommen vom Agent, der den
		// Systembenutzer nachschlägt. Sie stehen hier nur, weil die Zeile
		// nicht ohne sie in die Datenbank darf.
		UID: 1000, GID: 1000,
	}
	if err := s.store.CreateFTPAccount(ctx, tenantScope, account); err != nil {
		return nil, "", err
	}

	if err := s.apply(ctx, tenantScope, account, site.SystemUser, password); err != nil {
		// Kein halber Zustand: gibt es den Zugang beim Dienst nicht, gibt es
		// ihn auch im Panel nicht.
		_ = s.store.DeleteFTPAccount(ctx, tenantScope, account.ID)
		return nil, "", err
	}
	return account, password, nil
}

// SetPassword setzt ein neues Passwort und gibt es einmal zurück.
func (s *FTPService) SetPassword(ctx context.Context, sc store.Scope, id int64, password string) (string, error) {
	account, site, err := s.resolve(ctx, sc, id)
	if err != nil {
		return "", err
	}
	if password == "" {
		if password, err = authn.GeneratePassword(20); err != nil {
			return "", err
		}
	}
	encrypted, err := s.secrets.Encrypt(password)
	if err != nil {
		return "", err
	}

	previous := account.PasswordEnc
	account.PasswordEnc = encrypted
	if err := s.store.UpdateFTPAccount(ctx, sc, account); err != nil {
		return "", err
	}
	if err := s.apply(ctx, sc, account, site.SystemUser, password); err != nil {
		// Zurück auf den alten Stand: sonst zeigte das Panel ein Passwort an,
		// mit dem sich niemand anmelden kann.
		account.PasswordEnc = previous
		_ = s.store.UpdateFTPAccount(ctx, sc, account)
		return "", err
	}
	return password, nil
}

// Reveal gibt das hinterlegte Passwort heraus. Der Kunde muss es in seinen
// FTP-Client eintragen können; ein Hash wäre dafür nutzlos.
func (s *FTPService) Reveal(ctx context.Context, sc store.Scope, id int64) (string, error) {
	account, err := s.store.GetFTPAccount(ctx, sc, id)
	if err != nil {
		return "", err
	}
	return s.secrets.Decrypt(account.PasswordEnc)
}

// SetStatus schaltet einen Zugang ab oder wieder frei.
//
// Abgeschaltet heißt: aus der PureDB entfernt. Pure-FTPd kennt keinen
// gesperrten Zustand, und ein Eintrag, der bloß im Panel als inaktiv markiert
// wäre, könnte sich weiter anmelden.
func (s *FTPService) SetStatus(ctx context.Context, sc store.Scope, id int64, status string) error {
	account, site, err := s.resolve(ctx, sc, id)
	if err != nil {
		return err
	}
	if status != "active" && status != "disabled" {
		return fmt.Errorf("unbekannter status %q", status)
	}
	if account.Status == status {
		return nil
	}

	if status == "disabled" {
		if err := s.agent.DeleteFTPUser(ctx, account.Username); err != nil {
			return err
		}
		account.Status = status
		return s.store.UpdateFTPAccount(ctx, sc, account)
	}

	password, err := s.secrets.Decrypt(account.PasswordEnc)
	if err != nil {
		return fmt.Errorf("das hinterlegte passwort ist nicht lesbar — bitte ein neues setzen: %w", err)
	}
	account.Status = status
	if err := s.apply(ctx, sc, account, site.SystemUser, password); err != nil {
		return err
	}
	return s.store.UpdateFTPAccount(ctx, sc, account)
}

// Delete entfernt den Zugang. Die Dateien bleiben: sie gehören der Site.
func (s *FTPService) Delete(ctx context.Context, sc store.Scope, id int64) error {
	account, err := s.store.GetFTPAccount(ctx, sc, id)
	if err != nil {
		return err
	}
	if err := s.agent.DeleteFTPUser(ctx, account.Username); err != nil {
		return err
	}
	return s.store.DeleteFTPAccount(ctx, sc, id)
}

// Orphans nennt die Zugänge, die der Dienst kennt und das Panel nicht.
//
// So etwas entsteht, wenn ein Aufruf mitten im Anlegen abbricht. Sichtbar ist
// das sonst nirgends — und ein Zugang, von dem das Panel nichts weiß, lässt
// sich auch nicht abschalten.
func (s *FTPService) Orphans(ctx context.Context, sc store.Scope) ([]string, error) {
	known, err := s.store.ListFTPAccounts(ctx, sc.Elevate(), 0)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]bool, len(known))
	for _, a := range known {
		byName[a.Username] = true
	}

	live, err := s.agent.FTPUsers(ctx)
	if err != nil {
		return nil, err
	}
	orphans := []string{}
	for _, name := range live {
		if !byName[name] {
			orphans = append(orphans, name)
		}
	}
	return orphans, nil
}

// apply trägt den Zugang beim Dienst ein.
func (s *FTPService) apply(ctx context.Context, sc store.Scope, a *store.FTPAccount, systemUser, password string) error {
	res, err := s.agent.SetFTPUser(ctx, agent.FTPUserParams{
		Username: a.Username, Password: password, SystemUser: systemUser,
		HomeDir: a.HomeDir, QuotaMB: a.QuotaMB,
	})
	if err != nil {
		return err
	}

	// UID und GID kommen vom Agent, der sie nachgeschlagen hat. Das Panel
	// merkt sie sich nur, um sie anzuzeigen — verlassen tut sich darauf
	// niemand.
	if res.UID > 0 && (a.UID != res.UID || a.GID != res.GID) {
		a.UID, a.GID = res.UID, res.GID
		return s.store.UpdateFTPAccount(ctx, sc, a)
	}
	return nil
}

// resolve holt Zugang und zugehörige Site im Zugriffsbereich des Aufrufers.
func (s *FTPService) resolve(ctx context.Context, sc store.Scope, id int64) (*store.FTPAccount, *store.Site, error) {
	account, err := s.store.GetFTPAccount(ctx, sc, id)
	if err != nil {
		return nil, nil, err
	}
	if account.SiteID == nil {
		return nil, nil, errors.New("dieser zugang hängt an keiner site")
	}
	site, err := s.store.GetSite(ctx, sc, *account.SiteID)
	if err != nil {
		return nil, nil, err
	}
	return account, site, nil
}

// buildName setzt den Mandantenpräfix davor und hält die Längengrenze ein.
func (s *FTPService) buildName(ctx context.Context, sc store.Scope, site *store.Site, wish string) (string, error) {
	if wish == "" {
		// Ohne Wunsch der Domainname ohne Punkte — er sagt einem Kunden mehr
		// als eine laufende Nummer.
		wish = strings.ReplaceAll(site.Domain, ".", "_")
	}
	if !store.ValidNameInput(wish) {
		return "", fmt.Errorf("benutzername %q: erlaubt sind buchstaben, ziffern, leerzeichen, "+
			"bindestrich und unterstrich, beginnend mit einem buchstaben", wish)
	}

	tenant, err := s.store.GetTenant(ctx, sc, site.TenantID)
	if err != nil {
		return "", err
	}
	prefix := strings.Trim(reNonAlnum.ReplaceAllString(strings.ToLower(tenant.Slug), "_"), "_")
	return prefixedName(prefix, wish, 31), nil
}
