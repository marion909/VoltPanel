package core

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// CertService holt und erneuert Zertifikate über ACME.
//
// HTTP-01 läuft über das ACME-Webroot, das jeder generierte Vhost ausliefert.
// DNS-01 geht über die Cloudflare-API und ist der einzige Weg zu einem
// Wildcard-Zertifikat.
type CertService struct {
	cfg     *config.Config
	store   *store.Store
	agent   *agent.Client
	secrets *authn.SecretBox
	log     *slog.Logger
}

func NewCertService(cfg *config.Config, st *store.Store, ag *agent.Client, secrets *authn.SecretBox, log *slog.Logger) *CertService {
	if log == nil {
		log = slog.Default()
	}
	return &CertService{cfg: cfg, store: st, agent: ag, secrets: secrets, log: log}
}

// CloudflareToken holt den entschlüsselten Token eines Mandanten.
//
// Ein leerer Rückgabewert heißt "keiner hinterlegt" und ist kein Fehler —
// dann läuft die Prüfung eben über HTTP-01.
func (s *CertService) CloudflareToken(ctx context.Context, sc store.Scope, tenantID int64) (string, error) {
	if s.secrets == nil {
		return "", nil
	}
	tenant, err := s.store.GetTenant(ctx, sc, tenantID)
	if err != nil {
		return "", err
	}
	if tenant.CloudflareToken == "" {
		return "", nil
	}
	return s.secrets.Decrypt(tenant.CloudflareToken)
}

// SetCloudflareToken speichert den Token verschlüsselt. Ein leerer Wert
// entfernt ihn.
func (s *CertService) SetCloudflareToken(ctx context.Context, sc store.Scope, tenantID int64, token string) error {
	if s.secrets == nil {
		return errors.New("kein schlüssel für verschlüsselte werte geladen")
	}
	tenant, err := s.store.GetTenant(ctx, sc, tenantID)
	if err != nil {
		return err
	}

	if strings.TrimSpace(token) == "" {
		tenant.CloudflareToken = ""
	} else {
		if tenant.CloudflareToken, err = s.secrets.Encrypt(token); err != nil {
			return err
		}
	}
	return s.store.UpdateTenant(ctx, sc, tenant)
}

// acmeUser ist das Konto beim ACME-Server. Der Schlüssel liegt auf Platte und
// wird wiederverwendet — ein neues Konto bei jedem Aufruf würde die
// Rate-Limits von Let's Encrypt sehr schnell reißen.
type acmeUser struct {
	email        string
	registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// IssueOptions steuert, wie ein Zertifikat beschafft wird.
type IssueOptions struct {
	Domains []string
	// CloudflareToken aktiviert DNS-01. Für Wildcards zwingend, weil
	// HTTP-01 keine *.example.at beweisen kann.
	CloudflareToken string
	SiteID          *int64
	TenantID        int64
}

// Issue beschafft ein Zertifikat und installiert es über den Agent.
func (s *CertService) Issue(ctx context.Context, sc store.Scope, opts IssueOptions) (*store.Cert, error) {
	if len(opts.Domains) == 0 {
		return nil, errors.New("keine domain angegeben")
	}
	for _, d := range opts.Domains {
		if !store.ValidDomain(d) {
			return nil, fmt.Errorf("%q ist kein gültiger domainname", d)
		}
	}

	// Die Site muss demselben Mandanten gehören wie opts.TenantID — sonst
	// könnte ein Zertifikat mit der tenant_id eines Mandanten, aber der
	// site_id einer fremden Site angelegt werden. Aktuell sind alle bekannten
	// Aufrufer diszipliniert (laden die Site vorher selbst bzw. übergeben
	// keine SiteID), aber die Zuordnung wurde bisher erst später in enableSSL
	// implizit geprüft, wenn der Cert-Datensatz bereits angelegt war — analog
	// zum selben, aber end-to-end exploitierbaren Muster bei
	// DatabaseService.CreateDatabase.
	if opts.SiteID != nil {
		site, err := s.store.GetSite(ctx, sc, *opts.SiteID)
		if err != nil {
			return nil, err
		}
		if site.TenantID != opts.TenantID {
			return nil, store.ErrNotFound
		}
	}

	// Ohne ausdrücklich übergebenen Token den des Mandanten verwenden — sonst
	// müsste ihn jeder Aufrufer selbst heraussuchen und entschlüsseln.
	if opts.CloudflareToken == "" && opts.TenantID > 0 {
		token, err := s.CloudflareToken(ctx, sc, opts.TenantID)
		if err != nil {
			return nil, err
		}
		opts.CloudflareToken = token
	}

	wildcard := false
	for _, d := range opts.Domains {
		if strings.HasPrefix(d, "*.") {
			wildcard = true
		}
	}
	if wildcard && opts.CloudflareToken == "" {
		return nil, errors.New("wildcard-zertifikate brauchen dns-01 — bitte einen cloudflare-api-token hinterlegen")
	}
	if s.cfg.ACMEEmail == "" {
		return nil, errors.New("acme_email ist nicht konfiguriert")
	}

	challenge := "http-01"
	if opts.CloudflareToken != "" {
		challenge = "dns-01"
	}

	client, err := s.newClient(opts.CloudflareToken)
	if err != nil {
		return nil, err
	}

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: opts.Domains,
		Bundle:  true, // Kette mitliefern, sonst meckern mobile Clients
	})
	if err != nil {
		return nil, fmt.Errorf("zertifikat für %s: %w", strings.Join(opts.Domains, ", "), err)
	}

	certPath, keyPath, err := s.agent.InstallCert(ctx, opts.Domains[0],
		string(res.Certificate), string(res.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("zertifikat installieren: %w", err)
	}

	notBefore, notAfter, err := parseValidity(res.Certificate)
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	cert := &store.Cert{
		TenantID: opts.TenantID, SiteID: opts.SiteID, Domains: opts.Domains,
		Issuer: "letsencrypt", Challenge: challenge,
		CertPath: certPath, KeyPath: keyPath,
		NotBefore: &notBefore, NotAfter: &notAfter, LastRenewalAt: &now,
		AutoRenew: true, Status: "active",
	}

	// Ein bestehendes Zertifikat derselben Site wird ersetzt, nicht dupliziert.
	if opts.SiteID != nil {
		existing, err := s.store.CertBySite(ctx, sc, *opts.SiteID)
		switch {
		case err == nil:
			cert.ID = existing.ID
			if err := s.store.UpdateCert(ctx, sc, cert); err != nil {
				return nil, err
			}
			return cert, s.enableSSL(ctx, sc, *opts.SiteID)
		case !errors.Is(err, store.ErrNotFound):
			// Ein echter (transienter) Store-Fehler ist kein "es gibt noch
			// keins" — sonst würde CreateCert gleich danach versucht, einen
			// zweiten Datensatz für dieselbe Site anzulegen, oder ein
			// eigentlicher Fehler bliebe unbemerkt verschluckt.
			return nil, err
		}
	}
	if err := s.store.CreateCert(ctx, sc, cert); err != nil {
		return nil, err
	}
	if opts.SiteID != nil {
		return cert, s.enableSSL(ctx, sc, *opts.SiteID)
	}
	return cert, nil
}

// enableSSL schaltet die Site auf HTTPS und schreibt den Vhost neu.
func (s *CertService) enableSSL(ctx context.Context, sc store.Scope, siteID int64) error {
	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return err
	}
	if site.SSLEnabled {
		// Der Vhost verweist bereits auf die Dateien; der Agent hat nach dem
		// Schreiben ohnehin neu geladen.
		return nil
	}

	site.SSLEnabled = true
	if err := s.store.UpdateSite(ctx, sc, site); err != nil {
		return err
	}
	return NewSiteService(s.store, s.agent, s.cfg).Rebuild(ctx, sc, siteID)
}

// RenewDue erneuert alle fälligen Zertifikate und meldet, wie viele es waren.
//
// tokenFor darf nil sein; dann wird der Cloudflare-Token des jeweiligen
// Mandanten benutzt. Der Parameter existiert für die CLI, wo sich ein Token
// einmalig übergeben lässt, ohne ihn zu hinterlegen.
func (s *CertService) RenewDue(ctx context.Context, tokenFor func(*store.Cert) string) (renewed int, errs []error) {
	certs, err := s.store.CertsDueForRenewal(ctx, 30)
	if err != nil {
		return 0, []error{err}
	}

	for _, cert := range certs {
		sc := store.SystemScope()
		token := ""
		if tokenFor != nil {
			token = tokenFor(cert)
		}
		if token == "" {
			if fromTenant, err := s.CloudflareToken(ctx, sc, cert.TenantID); err == nil {
				token = fromTenant
			}
		}
		if cert.Challenge == "dns-01" && token == "" {
			errs = append(errs, fmt.Errorf("%s: dns-01 ohne cloudflare-token nicht erneuerbar",
				strings.Join(cert.Domains, ", ")))
			continue
		}

		_, err := s.Issue(ctx, sc, IssueOptions{
			Domains: cert.Domains, CloudflareToken: token,
			SiteID: cert.SiteID, TenantID: cert.TenantID,
		})
		if err != nil {
			// Fehler festhalten, damit das Panel den Grund anzeigen kann.
			cert.LastError, cert.Status = err.Error(), "failed"
			_ = s.store.UpdateCert(ctx, sc, cert)
			errs = append(errs, fmt.Errorf("%s: %w", strings.Join(cert.Domains, ", "), err))
			continue
		}
		renewed++
		s.log.Info("zertifikat erneuert", "domains", cert.Domains)
	}
	return renewed, errs
}

func (s *CertService) newClient(cloudflareToken string) (*lego.Client, error) {
	user, err := s.loadOrCreateAccount()
	if err != nil {
		return nil, err
	}

	legoCfg := lego.NewConfig(user)
	legoCfg.CADirURL = s.cfg.ACMEDirectory
	legoCfg.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(legoCfg)
	if err != nil {
		return nil, fmt.Errorf("acme-client: %w", err)
	}

	if cloudflareToken != "" {
		provider, err := cloudflare.NewDNSProviderConfig(&cloudflare.Config{
			AuthToken: cloudflareToken,
			// Großzügig, weil DNS-Änderungen bei Cloudflare zwar schnell sind,
			// die Propagation aber nicht garantiert ist.
			PropagationTimeout: 3 * time.Minute,
			PollingInterval:    5 * time.Second,
			TTL:                120,
		})
		if err != nil {
			return nil, fmt.Errorf("cloudflare-provider: %w", err)
		}
		if err := client.Challenge.SetDNS01Provider(provider); err != nil {
			return nil, fmt.Errorf("dns-01 einrichten: %w", err)
		}
	} else {
		// Der Webroot ist derselbe, den jeder generierte Vhost unter
		// /.well-known/acme-challenge/ ausliefert.
		webroot := filepath.Join(s.cfg.DataDir, "acme")
		if err := os.MkdirAll(filepath.Join(webroot, ".well-known", "acme-challenge"), 0o755); err != nil {
			return nil, fmt.Errorf("acme-webroot: %w", err)
		}
		if err := client.Challenge.SetHTTP01Provider(webrootProvider{dir: webroot}); err != nil {
			return nil, fmt.Errorf("http-01 einrichten: %w", err)
		}
	}

	if user.registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, fmt.Errorf("acme-konto registrieren: %w", err)
		}
		user.registration = reg
	}
	return client, nil
}

// loadOrCreateAccount hält den ACME-Kontoschlüssel dauerhaft vor.
func (s *CertService) loadOrCreateAccount() (*acmeUser, error) {
	keyPath := filepath.Join(s.cfg.DataDir, "acme", "account.key")
	user := &acmeUser{email: s.cfg.ACMEEmail}

	raw, err := os.ReadFile(keyPath)
	if err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, fmt.Errorf("kontoschlüssel %s ist kein pem", keyPath)
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("kontoschlüssel %s: %w", keyPath, err)
		}
		user.key = key
		return user, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, fmt.Errorf("kontoschlüssel schreiben: %w", err)
	}

	user.key = key
	return user, nil
}

// parseValidity liest Gültigkeitszeitraum aus dem ausgestellten Zertifikat.
func parseValidity(certPEM []byte) (notBefore, notAfter int64, err error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return 0, 0, errors.New("zertifikat ist kein pem")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return 0, 0, fmt.Errorf("zertifikat lesen: %w", err)
	}
	return cert.NotBefore.Unix(), cert.NotAfter.Unix(), nil
}
