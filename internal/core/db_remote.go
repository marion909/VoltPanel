package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

// Datenbankzugriff von außen.
//
// Ein MySQL-Konto ist ein Paar aus Benutzer und Herkunft. Das Panel führt für
// einen Datenbankbenutzer deshalb mehrere Konten: das ursprüngliche auf
// localhost und je eines pro Eintrag in seiner Herkunftsliste. Alle tragen
// denselben Namen, dasselbe Passwort und dieselben Rechte — von außen sieht es
// aus wie ein Zugang, der von mehreren Orten aus funktioniert, und genau so
// erwartet es jemand, der ihn benutzt.
//
// Das hat einen Preis: jede Änderung am Benutzer muss alle seine Konten
// treffen. Passwort, Rechte, Löschen. Bleibt eines zurück, ist es ein Zugang,
// von dem das Panel nichts mehr weiß — genau die Art Rest, die dieses Projekt
// an anderer Stelle als Waise aufspürt.

// maxRemoteHosts begrenzt die Liste je Benutzer.
//
// Nicht wegen der Last — zehn Konten sind für MariaDB nichts —, sondern weil
// eine Whitelist, die unbegrenzt wächst, keine mehr ist. Wer mehr als zehn
// Standorte hat, hat ein Netz und trägt es als Netz ein.
const maxRemoteHosts = 10

// ListRemoteHosts liefert die Herkunftsliste eines Benutzers.
func (s *DatabaseService) ListRemoteHosts(ctx context.Context, sc store.Scope,
	userID int64) ([]*store.DBRemoteHost, error) {

	// Der Benutzer wird im Zugriffsbereich aufgelöst, bevor irgendetwas
	// gelistet wird: sonst wäre die Liste eines fremden Benutzers über eine
	// geratene ID zu haben.
	if _, err := s.store.GetDBUser(ctx, sc, userID); err != nil {
		return nil, err
	}
	return s.store.ListRemoteHosts(ctx, sc, userID)
}

// AddRemoteHost nimmt eine Herkunft in die Liste auf und legt das zugehörige
// MySQL-Konto an.
func (s *DatabaseService) AddRemoteHost(ctx context.Context, sc store.Scope,
	userID int64, host, note string) (*store.DBRemoteHost, error) {

	user, err := s.store.GetDBUser(ctx, sc, userID)
	if err != nil {
		return nil, err
	}
	db, err := s.store.GetDatabase(ctx, sc, user.DatabaseID)
	if err != nil {
		return nil, err
	}

	// Die Eingabe zuerst. Das Repository prüft sie beim Anlegen ohnehin noch
	// einmal — aber erst nach allem hier, und dann bekäme jemand, der "%"
	// eintippt, die Meldung über ein fehlendes Passwort. Eine Fehlermeldung,
	// die auf das Falsche zeigt, kostet mehr Zeit als gar keine.
	host, err = store.NormalizeRemoteHost(host)
	if err != nil {
		return nil, err
	}

	existing, err := s.store.ListRemoteHosts(ctx, sc, userID)
	if err != nil {
		return nil, err
	}
	if len(existing) >= maxRemoteHosts {
		return nil, fmt.Errorf("mehr als %d herkünfte je benutzer sind nicht vorgesehen — "+
			"für viele adressen aus demselben netz reicht ein eintrag der form 10.0.0.0/24",
			maxRemoteHosts)
	}

	// Ohne hinterlegtes Passwort ließe sich das neue Konto nicht auf dasselbe
	// setzen wie das bestehende. Zwei Konten mit verschiedenen Passwörtern
	// unter einem Namen sind eine Falle, kein Feature.
	if user.PasswordEnc == "" {
		return nil, errors.New("für diesen benutzer ist kein passwort hinterlegt — " +
			"bitte zuerst ein neues setzen, dann steht es für alle herkünfte zur verfügung")
	}
	password, err := s.secrets.Decrypt(user.PasswordEnc)
	if err != nil {
		return nil, err
	}

	entry := &store.DBRemoteHost{
		TenantID: user.TenantID, DBUserID: user.ID,
		Host: host, Note: strings.TrimSpace(note),
	}
	if err := s.store.CreateRemoteHost(ctx, sc, entry); err != nil {
		return nil, err
	}

	if err := s.agent.CreateDBUser(ctx, agent.MySQLUserParams{
		Username: user.Username, HostPattern: entry.Host,
		Database: db.Name, Grants: user.Grants, Password: password,
	}); err != nil {
		// Dieselbe Reihenfolge wie beim FTP-Zugang: erst die Zeile, dann der
		// Server, und bei einem Fehler die Zeile wieder weg. Eine Herkunft, die
		// im Panel steht und in MariaDB nicht, wäre ein Zugang, der scheinbar
		// existiert.
		_ = s.store.DeleteRemoteHost(ctx, sc, entry.ID)
		return nil, err
	}
	return entry, nil
}

// RemoveRemoteHost entfernt eine Herkunft samt ihrem MySQL-Konto.
func (s *DatabaseService) RemoveRemoteHost(ctx context.Context, sc store.Scope, id int64) error {
	entry, err := s.store.GetRemoteHost(ctx, sc, id)
	if err != nil {
		return err
	}
	user, err := s.store.GetDBUser(ctx, sc, entry.DBUserID)
	if err != nil {
		return err
	}

	// Der Server zuerst. Andersherum bliebe bei einem Fehler ein Konto übrig,
	// das niemand mehr sieht — und der Zugang von außen bestünde weiter.
	if err := s.agent.DropDBUser(ctx, user.Username, entry.Host); err != nil {
		return err
	}
	return s.store.DeleteRemoteHost(ctx, sc, id)
}

// remoteHostsOf liefert die Herkünfte eines Benutzers für die Operationen, die
// alle seine Konten gleichzeitig treffen müssen.
func (s *DatabaseService) remoteHostsOf(ctx context.Context, sc store.Scope,
	userID int64) ([]string, error) {

	entries, err := s.store.ListRemoteHosts(ctx, sc, userID)
	if err != nil {
		return nil, err
	}
	hosts := make([]string, 0, len(entries))
	for _, e := range entries {
		hosts = append(hosts, e.Host)
	}
	return hosts, nil
}

// RemoteStatus sagt, ob MariaDB überhaupt Verbindungen von außen annimmt.
//
// Ohne diese Auskunft stünde in der Oberfläche eine Liste, die nichts bewirkt:
// Debian bindet MariaDB ab Werk an 127.0.0.1.
func (s *DatabaseService) RemoteStatus(ctx context.Context) (*agent.MySQLRemoteResult, error) {
	return s.agent.MySQLRemoteStatus(ctx)
}

// SetRemoteAccess stellt den Datenbankserver ins Netz — oder wieder zurück.
//
// Serverweit und deshalb dem Administrator vorbehalten. Die Prüfung darauf
// steht in der API-Schicht, wo auch die Rolle bekannt ist.
func (s *DatabaseService) SetRemoteAccess(ctx context.Context, enabled bool) (*agent.MySQLRemoteResult, error) {
	return s.agent.SetMySQLRemote(ctx, enabled)
}
