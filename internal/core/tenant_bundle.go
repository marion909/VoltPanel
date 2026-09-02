package core

import (
	"context"
	"fmt"

	"github.com/marion909/voltpanel/internal/store"
)

// Alles, was zu einem Mandanten gehört, in einer Struktur.
//
// Das Backup war bisher serverweit: eine Kopie der ganzen Datenbank plus
// ausgewählte Site-Verzeichnisse. Für eine Sicherung reicht das, für einen
// Umzug nicht — dort soll ein Mandant den Server wechseln, ohne die anderen
// mitzunehmen.
//
// Eingesammelt wird über den gewöhnlichen Scope. Das ist kein Zufall: dieselbe
// Regel, die im Panel dafür sorgt, dass ein Kunde nur seine eigenen Zeilen
// sieht, sorgt hier dafür, dass nur seine eigenen Zeilen im Bündel landen. Ein
// zweiter Weg, der "alles zu diesem Mandanten" anders beantwortet, wäre die
// Stelle, an der die beiden Antworten auseinandergehen.

// TenantBundle ist der Inhalt eines Exports, ohne die Dateien.
type TenantBundle struct {
	// Schema und Version sagen, wogegen das Bündel geschrieben wurde. Ein
	// Import auf einen älteren Server soll daran scheitern und nicht an einer
	// fehlenden Spalte mitten im Einspielen.
	Schema     int    `json:"schema"`
	Version    string `json:"version"`
	ExportedAt int64  `json:"exported_at"`
	// Source ist der Servername, von dem es stammt — nur zur Orientierung.
	Source string `json:"source,omitempty"`

	Tenant *store.Tenant `json:"tenant"`
	Plan   *store.Plan   `json:"plan,omitempty"`

	Users     []*store.User                   `json:"users"`
	Sites     []*store.Site                   `json:"sites"`
	Pools     map[int64]*store.PHPPool        `json:"pools"`
	Databases []*store.Database               `json:"databases"`
	DBUsers   map[int64][]*store.DBUser       `json:"db_users"`
	Remotes   map[int64][]*store.DBRemoteHost `json:"remote_hosts"`
	FTP       map[int64][]*store.FTPAccount   `json:"ftp"`
	Cronjobs  []*store.Cronjob                `json:"cronjobs"`
	Apps      []*store.App                    `json:"apps"`
	Deploys   []*store.Deploy                 `json:"deploys"`
	Targets   []*store.BackupTarget           `json:"backup_targets"`
	Certs     []*store.Cert                   `json:"certs"`

	// Secrets sind die verschlüsselten Werte, umgeschlüsselt auf die
	// Passphrase des Exports. Sie stehen hier und nicht in den Zeilen oben,
	// weil dort die Verschlüsselung *dieses* Servers steht — auf einem anderen
	// wäre sie nicht zu lesen.
	Secrets map[string]string `json:"secrets"`
}

// CollectTenant sammelt alles ein, was zu einem Mandanten gehört.
//
// sc muss den Mandanten umfassen. Übergeben wird er, statt ihn hier zu bilden:
// so entscheidet der Aufrufer über die Berechtigung, und diese Funktion muss
// nicht wissen, wer fragt.
func CollectTenant(ctx context.Context, st *store.Store, sc store.Scope,
	tenantID int64) (*TenantBundle, error) {

	tenant, err := st.GetTenant(ctx, sc, tenantID)
	if err != nil {
		return nil, err
	}
	// Ab hier ausschließlich im Scope dieses Mandanten. Auch wenn der Aufrufer
	// mehr dürfte: was eingesammelt wird, soll genau das sein, was zu diesem
	// einen gehört.
	nur := store.Scope{TenantID: tenantID, Role: store.RoleOwner}

	b := &TenantBundle{
		Tenant:  tenant,
		Pools:   map[int64]*store.PHPPool{},
		DBUsers: map[int64][]*store.DBUser{},
		Remotes: map[int64][]*store.DBRemoteHost{},
		FTP:     map[int64][]*store.FTPAccount{},
		Secrets: map[string]string{},
	}

	if plan, err := st.PlanForTenant(ctx, nur, tenantID); err == nil {
		b.Plan = plan
	}
	if b.Users, err = st.ListUsers(ctx, nur); err != nil {
		return nil, fmt.Errorf("benutzer: %w", err)
	}
	if b.Sites, err = st.ListSites(ctx, nur); err != nil {
		return nil, fmt.Errorf("sites: %w", err)
	}
	if b.Databases, err = st.ListDatabases(ctx, nur); err != nil {
		return nil, fmt.Errorf("datenbanken: %w", err)
	}
	if b.Cronjobs, err = st.ListCronjobs(ctx, nur); err != nil {
		return nil, fmt.Errorf("cronjobs: %w", err)
	}
	if b.Apps, err = st.ListApps(ctx, nur); err != nil {
		return nil, fmt.Errorf("apps: %w", err)
	}
	if b.Deploys, err = st.ListDeploys(ctx, nur); err != nil {
		return nil, fmt.Errorf("deploys: %w", err)
	}
	if b.Targets, err = st.ListBackupTargets(ctx, nur); err != nil {
		return nil, fmt.Errorf("backup-ziele: %w", err)
	}
	if b.Certs, err = st.ListCerts(ctx, nur); err != nil {
		return nil, fmt.Errorf("zertifikate: %w", err)
	}

	// Was an einer Site oder einer Datenbank hängt, einzeln nachladen.
	for _, site := range b.Sites {
		if pool, err := st.PHPPoolBySite(ctx, nur, site.ID); err == nil {
			b.Pools[site.ID] = pool
		}
		accounts, err := st.ListFTPAccounts(ctx, nur, site.ID)
		if err != nil {
			return nil, fmt.Errorf("ftp-zugänge von %s: %w", site.Domain, err)
		}
		if len(accounts) > 0 {
			b.FTP[site.ID] = accounts
		}
	}
	for _, db := range b.Databases {
		users, err := st.ListDBUsers(ctx, nur, db.ID)
		if err != nil {
			return nil, fmt.Errorf("datenbankbenutzer von %s: %w", db.Name, err)
		}
		if len(users) > 0 {
			b.DBUsers[db.ID] = users
		}
		for _, u := range users {
			hosts, err := st.ListRemoteHosts(ctx, nur, u.ID)
			if err != nil {
				return nil, fmt.Errorf("herkunftsliste von %s: %w", u.Username, err)
			}
			if len(hosts) > 0 {
				b.Remotes[u.ID] = hosts
			}
		}
	}
	return b, nil
}

// Domains sind die Domains aller Sites des Bündels.
func (b *TenantBundle) Domains() []string {
	out := make([]string, 0, len(b.Sites))
	for _, s := range b.Sites {
		out = append(out, s.Domain)
	}
	return out
}
