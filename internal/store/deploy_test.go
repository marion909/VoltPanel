package store

import (
	"context"
	"errors"
	"testing"
)

// TestDeployBleibtImMandanten deckt ab, dass GetDeploy/DeployForSite jetzt
// scope.where() nutzen (wie repo_site.go, repo_cert.go, …), statt die Zeile
// erst zu laden und danach per sc.owns() zu prüfen — funktional identisch,
// aber konsistent mit dem sonst im Paket verwendeten Muster, das den
// Tenant-Filter direkt ins SQL bäckt.
func TestDeployBleibtImMandanten(t *testing.T) {
	st := newTestStore(t)
	ctx, sys := context.Background(), SystemScope()

	site := seedAppSite(t, st, "eins")
	hookID, err := NewHookID()
	if err != nil {
		t.Fatal(err)
	}
	d := &Deploy{
		TenantID: site.TenantID, SiteID: site.ID,
		RepoURL: "https://example.at/eins.git", Ref: "main",
		HookID: hookID, HookSecretEnc: "geheim",
	}
	if err := st.CreateDeploy(ctx, sys, d); err != nil {
		t.Fatal(err)
	}
	fremd := Scope{TenantID: site.TenantID + 999, Role: RoleOwner}

	if _, err := st.GetDeploy(ctx, fremd, d.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDeploy aus fremdem Mandanten: %v", err)
	}
	if _, err := st.DeployForSite(ctx, fremd, site.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeployForSite aus fremdem Mandanten: %v", err)
	}

	gelesen, err := st.GetDeploy(ctx, sys, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gelesen.RepoURL != d.RepoURL {
		t.Errorf("RepoURL = %q, erwartet %q", gelesen.RepoURL, d.RepoURL)
	}

	überSite, err := st.DeployForSite(ctx, sys, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if überSite.ID != d.ID {
		t.Errorf("DeployForSite liefert ID %d, erwartet %d", überSite.ID, d.ID)
	}
}
