package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/store"
)

// DNSService verwaltet DNS-Zonen und -Einträge über die beim Mandanten
// hinterlegten Provider-Tokens. Der Cloudflare-Token ist derselbe, den
// CertService schon für DNS-01 benutzt — hier nur mitbenutzt, nicht
// verdoppelt.
type DNSService struct {
	store   *store.Store
	secrets *authn.SecretBox
	certs   *CertService
}

func NewDNSService(st *store.Store, secrets *authn.SecretBox, certs *CertService) *DNSService {
	return &DNSService{store: st, secrets: secrets, certs: certs}
}

func (s *DNSService) CloudflareToken(ctx context.Context, sc store.Scope, tenantID int64) (string, error) {
	return s.certs.CloudflareToken(ctx, sc, tenantID)
}

func (s *DNSService) SetCloudflareToken(ctx context.Context, sc store.Scope, tenantID int64, token string) error {
	return s.certs.SetCloudflareToken(ctx, sc, tenantID, token)
}

// HetznerToken holt den entschlüsselten Token eines Mandanten. Ein leerer
// Rückgabewert heißt "keiner hinterlegt" und ist kein Fehler.
func (s *DNSService) HetznerToken(ctx context.Context, sc store.Scope, tenantID int64) (string, error) {
	if s.secrets == nil {
		return "", nil
	}
	tenant, err := s.store.GetTenant(ctx, sc, tenantID)
	if err != nil {
		return "", err
	}
	if tenant.HetznerToken == "" {
		return "", nil
	}
	return s.secrets.Decrypt(tenant.HetznerToken)
}

// SetHetznerToken speichert den Token verschlüsselt. Ein leerer Wert entfernt ihn.
func (s *DNSService) SetHetznerToken(ctx context.Context, sc store.Scope, tenantID int64, token string) error {
	if s.secrets == nil {
		return errors.New("kein schlüssel für verschlüsselte werte geladen")
	}
	tenant, err := s.store.GetTenant(ctx, sc, tenantID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		tenant.HetznerToken = ""
	} else {
		if tenant.HetznerToken, err = s.secrets.Encrypt(token); err != nil {
			return err
		}
	}
	return s.store.UpdateTenant(ctx, sc, tenant)
}

// providersFor liefert die konfigurierten Provider-Clients des Mandanten aus
// sc, benannt nach ihrem Schlüssel ("cloudflare"/"hetzner"). Ein Mandant ohne
// Token bei einem Provider taucht dort einfach nicht auf.
func (s *DNSService) providersFor(ctx context.Context, sc store.Scope) (map[string]dnsProvider, error) {
	out := map[string]dnsProvider{}
	if token, err := s.CloudflareToken(ctx, sc, sc.TenantID); err != nil {
		return nil, err
	} else if token != "" {
		out["cloudflare"] = newCloudflareClient(token)
	}
	if token, err := s.HetznerToken(ctx, sc, sc.TenantID); err != nil {
		return nil, err
	} else if token != "" {
		out["hetzner"] = newHetznerDNSClient(token)
	}
	return out, nil
}

func (s *DNSService) provider(ctx context.Context, sc store.Scope, name string) (dnsProvider, error) {
	providers, err := s.providersFor(ctx, sc)
	if err != nil {
		return nil, err
	}
	p, ok := providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q ist für diesen mandanten nicht eingerichtet", name)
	}
	return p, nil
}

// ListZones fragt alle konfigurierten Provider ab und markiert jede Zone mit
// ihrem Provider, damit die Oberfläche weiß, wohin nachfolgende
// Record-Aufrufe gehen müssen.
func (s *DNSService) ListZones(ctx context.Context, sc store.Scope) ([]DNSZone, error) {
	providers, err := s.providersFor(ctx, sc)
	if err != nil {
		return nil, err
	}
	out := []DNSZone{}
	for name, p := range providers {
		zonen, err := p.ListZones(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, z := range zonen {
			z.Provider = name
			out = append(out, z)
		}
	}
	return out, nil
}

func (s *DNSService) ListRecords(ctx context.Context, sc store.Scope, provider, zoneID string) ([]DNSRecord, error) {
	p, err := s.provider(ctx, sc, provider)
	if err != nil {
		return nil, err
	}
	return p.ListRecords(ctx, zoneID)
}

func (s *DNSService) CreateRecord(ctx context.Context, sc store.Scope, provider, zoneID string, r DNSRecord) error {
	p, err := s.provider(ctx, sc, provider)
	if err != nil {
		return err
	}
	return p.CreateRecord(ctx, zoneID, r)
}

func (s *DNSService) UpdateRecord(ctx context.Context, sc store.Scope, provider, zoneID, oldValue string, r DNSRecord) error {
	p, err := s.provider(ctx, sc, provider)
	if err != nil {
		return err
	}
	return p.UpdateRecord(ctx, zoneID, oldValue, r)
}

func (s *DNSService) DeleteRecord(ctx context.Context, sc store.Scope, provider, zoneID string, r DNSRecord) error {
	p, err := s.provider(ctx, sc, provider)
	if err != nil {
		return err
	}
	return p.DeleteRecord(ctx, zoneID, r)
}
