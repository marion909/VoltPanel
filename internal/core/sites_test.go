package core

import (
	"context"
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

func TestCreateSiteSchreibtSharedVorVhost(t *testing.T) {
	st, cfg := newSiteServiceTestStore(t)
	fake := &siteAgentFake{}
	svc := &SiteService{
		store: st,
		agent: fake,
		cfg:   cfg,
		quota: NewQuotaService(st, nil, cfg, nil),
	}

	tenant := &store.Tenant{Name: "Alice", Slug: "alice"}
	if err := st.CreateTenant(context.Background(), store.SystemScope(), tenant); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateSite(context.Background(), store.SystemScope(), CreateSiteInput{
		Domain:      "proxy.example.at",
		Type:        store.SiteProxy,
		ProxyTarget: "http://127.0.0.1:3000",
		TenantID:    tenant.ID,
	}); err != nil {
		t.Fatalf("CreateSite: %v", err)
	}

	got := strings.Join(fake.calls, ",")
	if !strings.HasPrefix(got, "shared,user.create,") {
		t.Fatalf("Aufrufe = %s, erwartet shared vor dem Vhost-Provisioning", got)
	}
	if !strings.Contains(fake.shared, "log_format volt") {
		t.Fatalf("gemeinsame Config enthält kein log_format volt:\n%s", fake.shared)
	}
	if !strings.Contains(fake.vhost, "access_log ") || !strings.Contains(fake.vhost, " volt;") {
		t.Fatalf("Vhost nutzt das volt-Logformat nicht:\n%s", fake.vhost)
	}
}

func newSiteServiceTestStore(t *testing.T) (*store.Store, *config.Config) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(dir + "/volt.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dir + "/data"
	cfg.SitesDir = dir + "/www"
	cfg.LogDir = dir + "/log"
	cfg.NginxDir = dir + "/nginx"
	return st, cfg
}

type siteAgentFake struct {
	calls  []string
	shared string
	vhost  string
}

func (f *siteAgentFake) record(call string) {
	f.calls = append(f.calls, call)
}

func (f *siteAgentFake) WriteShared(_ context.Context, content string) error {
	f.record("shared")
	f.shared = content
	return nil
}

func (f *siteAgentFake) CreateSystemUser(context.Context, string, string) error {
	f.record("user.create")
	return nil
}

func (f *siteAgentFake) DeleteSystemUser(context.Context, string, bool) error {
	f.record("user.delete")
	return nil
}

func (f *siteAgentFake) Mkdir(context.Context, string, uint32, string) error {
	f.record("mkdir")
	return nil
}

func (f *siteAgentFake) MkdirGroup(context.Context, string, uint32, string, string) error {
	f.record("mkdir.group")
	return nil
}

func (f *siteAgentFake) WriteFileGroup(context.Context, string, string, uint32, string, string) error {
	f.record("file.write")
	return nil
}

func (f *siteAgentFake) RemovePath(context.Context, string, bool) error {
	f.record("path.remove")
	return nil
}

func (f *siteAgentFake) WritePHPPool(context.Context, string, string, string) error {
	f.record("pool.write")
	return nil
}

func (f *siteAgentFake) RemovePHPPool(context.Context, string, string) error {
	f.record("pool.remove")
	return nil
}

func (f *siteAgentFake) WriteVhost(_ context.Context, _ string, content string) error {
	f.record("vhost")
	f.vhost = content
	return nil
}

func (f *siteAgentFake) RemoveVhost(context.Context, string) error {
	f.record("vhost.remove")
	return nil
}

func (f *siteAgentFake) WriteHtpasswd(context.Context, string, []string) (string, error) {
	f.record("htpasswd.write")
	return "", nil
}

func (f *siteAgentFake) RemoveHtpasswd(context.Context, string) error {
	f.record("htpasswd.remove")
	return nil
}
