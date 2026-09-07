package core

import (
	"context"
	"errors"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// fakeSiteAgent protokolliert die Reihenfolge der Aufrufe und kann eine
// einzelne Methode gezielt scheitern lassen — genug, um UpdatePHP zu testen,
// ohne einen echten, root-fähigen Agent zu brauchen.
type fakeSiteAgent struct {
	calls  []string
	failOn string
}

func (f *fakeSiteAgent) call(name string) error {
	f.calls = append(f.calls, name)
	if name == f.failOn {
		return errors.New("gewollter testfehler bei " + name)
	}
	return nil
}

func (f *fakeSiteAgent) WriteShared(ctx context.Context, content string) error {
	return f.call("WriteShared")
}
func (f *fakeSiteAgent) CreateSystemUser(ctx context.Context, username, homeDir string) error {
	return f.call("CreateSystemUser")
}
func (f *fakeSiteAgent) DeleteSystemUser(ctx context.Context, username string, removeHome bool) error {
	return f.call("DeleteSystemUser")
}
func (f *fakeSiteAgent) Mkdir(ctx context.Context, path string, mode uint32, owner string) error {
	return f.call("Mkdir")
}
func (f *fakeSiteAgent) MkdirGroup(ctx context.Context, path string, mode uint32, owner, group string) error {
	return f.call("MkdirGroup")
}
func (f *fakeSiteAgent) WriteFileGroup(ctx context.Context, path, content string, mode uint32, owner, group string) error {
	return f.call("WriteFileGroup")
}
func (f *fakeSiteAgent) RemovePath(ctx context.Context, path string, recursive bool) error {
	return f.call("RemovePath")
}
func (f *fakeSiteAgent) WritePHPPool(ctx context.Context, phpVersion, poolName, content string) error {
	return f.call("WritePHPPool")
}
func (f *fakeSiteAgent) RemovePHPPool(ctx context.Context, phpVersion, poolName string) error {
	return f.call("RemovePHPPool")
}
func (f *fakeSiteAgent) WriteVhost(ctx context.Context, domain, content string) error {
	return f.call("WriteVhost")
}
func (f *fakeSiteAgent) RemoveVhost(ctx context.Context, domain string) error {
	return f.call("RemoveVhost")
}
func (f *fakeSiteAgent) WriteHtpasswd(ctx context.Context, domain string, entries []string) (string, error) {
	return "", f.call("WriteHtpasswd")
}
func (f *fakeSiteAgent) RemoveHtpasswd(ctx context.Context, domain string) error {
	return f.call("RemoveHtpasswd")
}

// seedPHPSite legt eine PHP-Site samt Pool an, ohne den Agent zu brauchen.
func seedPHPSite(t *testing.T, env *testEnv, slug, phpVersion string) (*store.Site, *store.PHPPool) {
	t.Helper()
	ctx := context.Background()
	sys := store.SystemScope()

	tenant := &store.Tenant{Name: slug, Slug: slug}
	if err := env.store.CreateTenant(ctx, sys, tenant); err != nil {
		t.Fatal(err)
	}
	site := &store.Site{
		TenantID: tenant.ID, Domain: slug + ".example.at", Type: store.SitePHP,
		SystemUser: "site_" + slug, RootPath: "/var/www/" + slug, DocumentRoot: "public",
		PHPVersion: phpVersion,
	}
	if err := env.store.CreateSite(ctx, sys, site); err != nil {
		t.Fatal(err)
	}
	pool := &store.PHPPool{
		TenantID: tenant.ID, SiteID: site.ID, PHPVersion: phpVersion,
		PoolName: PoolName(site.Domain), SocketPath: "/run/php/" + PoolName(site.Domain) + ".sock",
		PM: "ondemand", MaxChildren: 10, MemoryLimit: "256M",
		MaxExecutionTime: 30, UploadMaxFilesize: "64M",
	}
	if err := env.store.CreatePHPPool(ctx, sys, pool); err != nil {
		t.Fatal(err)
	}
	return site, pool
}

// TestUpdatePHPEntferntAltenPoolErstNachDemNeuen deckt den Fund ab, dass
// UpdatePHP beim Versionswechsel den alten FPM-Pool löschte, *bevor*
// UpdateSite/UpdatePHPPool/Rebuild überhaupt gelaufen waren. Schlug einer der
// drei danach fehl, blieb die Site ganz ohne FPM-Pool-Konfiguration zurück —
// ein dauerhaftes 502, bis zum nächsten erfolgreichen Versuch.
func TestUpdatePHPEntferntAltenPoolErstNachDemNeuen(t *testing.T) {
	env := newTestEnv(t)
	sys := store.SystemScope()
	site, _ := seedPHPSite(t, env, "alice", "8.2")

	fake := &fakeSiteAgent{}
	svc := &SiteService{store: env.store, agent: fake, cfg: env.cfg, quota: NewQuotaService(env.store, nil, env.cfg, nil)}

	newVersion := "8.3"
	if _, err := svc.UpdatePHP(context.Background(), sys, site.ID, UpdatePHPInput{
		PHPVersion: &newVersion,
	}); err != nil {
		t.Fatalf("UpdatePHP: %v", err)
	}

	removeIdx, writeIdx := -1, -1
	for i, c := range fake.calls {
		switch c {
		case "RemovePHPPool":
			removeIdx = i
		case "WritePHPPool":
			writeIdx = i
		}
	}
	if writeIdx == -1 {
		t.Fatal("WritePHPPool wurde nie aufgerufen")
	}
	if removeIdx == -1 {
		t.Fatal("RemovePHPPool wurde nie aufgerufen")
	}
	if removeIdx < writeIdx {
		t.Fatalf("RemovePHPPool (Index %d) lief vor WritePHPPool (Index %d): %v",
			removeIdx, writeIdx, fake.calls)
	}
}

// TestUpdatePHPBehaeltAltenPoolBeiFehlschlag: scheitert Rebuild (hier beim
// Schreiben des Vhosts, der letzte Schritt), darf der alte Pool nicht
// verschwunden sein — sonst liefe die Site unter keiner Version mehr.
func TestUpdatePHPBehaeltAltenPoolBeiFehlschlag(t *testing.T) {
	env := newTestEnv(t)
	sys := store.SystemScope()
	site, _ := seedPHPSite(t, env, "alice", "8.2")

	fake := &fakeSiteAgent{failOn: "WriteVhost"}
	svc := &SiteService{store: env.store, agent: fake, cfg: env.cfg, quota: NewQuotaService(env.store, nil, env.cfg, nil)}

	newVersion := "8.3"
	if _, err := svc.UpdatePHP(context.Background(), sys, site.ID, UpdatePHPInput{
		PHPVersion: &newVersion,
	}); err == nil {
		t.Fatal("UpdatePHP hat den erzwungenen Fehlschlag nicht gemeldet")
	}

	for _, c := range fake.calls {
		if c == "RemovePHPPool" {
			t.Fatalf("RemovePHPPool wurde trotz gescheitertem Rebuild aufgerufen: %v", fake.calls)
		}
	}
}
