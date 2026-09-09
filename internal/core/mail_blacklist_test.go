package core

import (
	"context"
	"net"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

// setzeBlacklistDNS ersetzt die DNSBL-Auskunft für die Dauer eines Tests —
// dasselbe Muster wie setzeDNS in mail_test.go, nur für den eigenen Var.
func setzeBlacklistDNS(t *testing.T, lookup func(context.Context, string) ([]string, error)) {
	t.Helper()
	alt := dnsBlacklistLookup
	t.Cleanup(func() { dnsBlacklistLookup = alt })
	dnsBlacklistLookup = lookup
}

func TestCheckBlacklistErkenntGelisteteDomain(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	sc := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domID := ersteDomain(t, env, alice)

	setzeBlacklistDNS(t, func(context.Context, string) ([]string, error) {
		return []string{"127.0.1.2"}, nil // eine DNSBL antwortet mit einem Treffer, nicht mit NXDOMAIN
	})

	d, err := svc.CheckBlacklist(ctx, sc, domID)
	if err != nil {
		t.Fatal(err)
	}
	if d.BlacklistStatus != BlacklistListed {
		t.Errorf("status = %q, erwartet %q", d.BlacklistStatus, BlacklistListed)
	}
	if d.BlacklistCheckedAt == 0 {
		t.Error("blacklist_checked_at wurde nicht gesetzt")
	}

	// Und es steht auch wirklich im Store, nicht nur im Rückgabewert.
	gespeichert, err := env.store.GetMailDomain(ctx, sc, domID)
	if err != nil {
		t.Fatal(err)
	}
	if gespeichert.BlacklistStatus != BlacklistListed {
		t.Errorf("gespeicherter status = %q, erwartet %q", gespeichert.BlacklistStatus, BlacklistListed)
	}
}

func TestCheckBlacklistErkenntSaubereDomain(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	sc := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domID := ersteDomain(t, env, alice)

	setzeBlacklistDNS(t, func(context.Context, string) ([]string, error) {
		// NXDOMAIN ist die normale Antwort einer DNSBL für eine unauffällige Domäne.
		return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
	})

	d, err := svc.CheckBlacklist(ctx, sc, domID)
	if err != nil {
		t.Fatal(err)
	}
	if d.BlacklistStatus != BlacklistClean {
		t.Errorf("status = %q, erwartet %q", d.BlacklistStatus, BlacklistClean)
	}
}

// Ein Resolver-Ausfall darf nicht als "sauber" im Store landen — das wäre
// eine falsche Auskunft, die bis zur nächsten Prüfung stehen bliebe.
func TestCheckBlacklistSpeichertNichtsBeiEchtemFehler(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	svc := mailService(env)

	alice := seedMailTenant(t, env, "alice")
	sc := store.Scope{TenantID: alice, Role: store.RoleOwner}
	domID := ersteDomain(t, env, alice)

	setzeBlacklistDNS(t, func(context.Context, string) ([]string, error) {
		return nil, &net.DNSError{Err: "timeout", IsTimeout: true}
	})

	if _, err := svc.CheckBlacklist(ctx, sc, domID); err == nil {
		t.Fatal("ein resolver-timeout wurde stillschweigend als ergebnis übernommen")
	}

	d, err := env.store.GetMailDomain(ctx, sc, domID)
	if err != nil {
		t.Fatal(err)
	}
	if d.BlacklistStatus != "" {
		t.Errorf("status = %q, erwartet leer (nie geprüft)", d.BlacklistStatus)
	}
}
