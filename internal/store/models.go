package store

import (
	"encoding/json"
	"time"
)

type Tenant struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	PlanID *int64 `json:"plan_id"`
	Status string `json:"status"`

	// CloudflareToken liegt verschlüsselt vor und wird nie serialisiert.
	// Ob einer hinterlegt ist, sagt HasCloudflareToken.
	CloudflareToken string `json:"-"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// HasCloudflareToken sagt der Oberfläche, ob DNS-01 möglich ist — ohne den
// Token selbst herauszugeben.
func (t *Tenant) HasCloudflareToken() bool { return t.CloudflareToken != "" }

// MarshalJSON ergänzt das abgeleitete Feld, ohne das Geheimnis mitzuschicken.
func (t Tenant) MarshalJSON() ([]byte, error) {
	type alias Tenant // verhindert die Endlosschleife über MarshalJSON
	return json.Marshal(struct {
		alias
		HasCloudflareToken bool `json:"has_cloudflare_token"`
	}{alias(t), t.CloudflareToken != ""})
}

type Plan struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	MaxSites       int    `json:"max_sites"`
	MaxDatabases   int    `json:"max_databases"`
	MaxFTP         int    `json:"max_ftp"`
	MaxMailboxes   int    `json:"max_mailboxes"`
	MaxCronjobs    int    `json:"max_cronjobs"`
	DiskQuotaMB    int64  `json:"disk_quota_mb"`
	TrafficQuotaMB int64  `json:"traffic_quota_mb"`
	Description    string `json:"description"`
	IsDefault      bool   `json:"is_default"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// Unlimited sagt, ob ein Grenzwert unbegrenzt bedeutet. 0 heißt bewusst
// "kein Limit" und nicht "nichts erlaubt" — sonst wäre ein Paket ohne
// gepflegte Werte eine Sperre.
func Unlimited(limit int64) bool { return limit <= 0 }

type User struct {
	ID           int64  `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	PasswordHash string `json:"-"`
	Role         Role   `json:"role"`
	TOTPSecret   string `json:"-"`
	TOTPEnabled  bool   `json:"totp_enabled"`
	MustChangePW bool   `json:"must_change_pw"`
	Locale       string `json:"locale"`
	LastLoginAt  *int64 `json:"last_login_at"`
	FailedLogins int    `json:"-"`
	LockedUntil  *int64 `json:"-"`
	Status       string `json:"status"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Locked sagt, ob der Login gerade wegen zu vieler Fehlversuche gesperrt ist.
func (u *User) Locked() bool {
	return u.LockedUntil != nil && *u.LockedUntil > time.Now().Unix()
}

func (u *User) Active() bool { return u.Status == "active" }

type Session struct {
	ID        string `json:"id"`
	UserID    int64  `json:"user_id"`
	TenantID  int64  `json:"tenant_id"`
	UserAgent string `json:"user_agent"`
	IP        string `json:"ip"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedAt int64  `json:"created_at"`
}

type SiteType string

const (
	SiteStatic SiteType = "static"
	SitePHP    SiteType = "php"
	SiteProxy  SiteType = "proxy"
)

func (t SiteType) Valid() bool {
	switch t {
	case SiteStatic, SitePHP, SiteProxy:
		return true
	}
	return false
}

type Site struct {
	ID           int64    `json:"id"`
	TenantID     int64    `json:"tenant_id"`
	Domain       string   `json:"domain"`
	Aliases      []string `json:"aliases"`
	Type         SiteType `json:"type"`
	SystemUser   string   `json:"system_user"`
	RootPath     string   `json:"root_path"`
	DocumentRoot string   `json:"document_root"`
	PHPVersion   string   `json:"php_version"`
	ProxyTarget  string   `json:"proxy_target"`
	SSLEnabled   bool     `json:"ssl_enabled"`
	ForceHTTPS   bool     `json:"force_https"`
	HSTS         bool     `json:"hsts"`
	Status       string   `json:"status"`

	// Gemessener Verbrauch. Wird vom Quota-Job aktualisiert, nicht bei
	// jeder Anfrage neu berechnet.
	DiskBytes      int64  `json:"disk_bytes"`
	DiskFiles      int64  `json:"disk_files"`
	DiskMeasuredAt *int64 `json:"disk_measured_at"`
	TrafficBytes   int64  `json:"traffic_bytes"`
	TrafficPeriod  string `json:"traffic_period"`

	// Settings sind die Vhost-Zusätze: Weiterleitungen, IP-Regeln,
	// Passwortschutz, eigene Direktiven.
	Settings SiteSettings `json:"settings"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// WebRoot ist das Verzeichnis, das Nginx ausliefert.
func (s *Site) WebRoot() string {
	if s.DocumentRoot == "" {
		return s.RootPath
	}
	return s.RootPath + "/" + s.DocumentRoot
}

// ServerNames sind alle Namen, auf die der Vhost hört.
func (s *Site) ServerNames() []string {
	return append([]string{s.Domain}, s.Aliases...)
}

type PHPPool struct {
	ID                int64  `json:"id"`
	TenantID          int64  `json:"tenant_id"`
	SiteID            int64  `json:"site_id"`
	PHPVersion        string `json:"php_version"`
	PoolName          string `json:"pool_name"`
	SocketPath        string `json:"socket_path"`
	PM                string `json:"pm"`
	MaxChildren       int    `json:"max_children"`
	MemoryLimit       string `json:"memory_limit"`
	MaxExecutionTime  int    `json:"max_execution_time"`
	UploadMaxFilesize string `json:"upload_max_filesize"`
	OpenBasedir       string `json:"open_basedir"`
	DisableFunctions  string `json:"disable_functions"`
	ExtraINI          string `json:"extra_ini"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

type Database struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	SiteID    *int64 `json:"site_id"`
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type DBUser struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	DatabaseID  int64  `json:"database_id"`
	Username    string `json:"username"`
	HostPattern string `json:"host_pattern"`
	Grants      string `json:"grants"`
	PasswordEnc string `json:"-"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type Cert struct {
	ID            int64    `json:"id"`
	TenantID      int64    `json:"tenant_id"`
	SiteID        *int64   `json:"site_id"`
	Domains       []string `json:"domains"`
	Issuer        string   `json:"issuer"`
	Challenge     string   `json:"challenge"`
	CertPath      string   `json:"cert_path"`
	KeyPath       string   `json:"key_path"`
	NotBefore     *int64   `json:"not_before"`
	NotAfter      *int64   `json:"not_after"`
	LastRenewalAt *int64   `json:"last_renewal_at"`
	LastError     string   `json:"last_error"`
	AutoRenew     bool     `json:"auto_renew"`
	Status        string   `json:"status"`
	CreatedAt     int64    `json:"created_at"`
	UpdatedAt     int64    `json:"updated_at"`
}

// DaysLeft ist die Restlaufzeit; ohne Ablaufdatum kommt 0 zurück.
func (c *Cert) DaysLeft() int {
	if c.NotAfter == nil {
		return 0
	}
	return int(time.Until(time.Unix(*c.NotAfter, 0)).Hours() / 24)
}

// NeedsRenewal folgt der Let's-Encrypt-Empfehlung: erneuern ab 30 Tagen Restlaufzeit.
func (c *Cert) NeedsRenewal() bool { return c.AutoRenew && c.DaysLeft() <= 30 }

// FTPAccount ist ein virtueller Pure-FTPd-Zugang. Virtuell heißt: es entsteht
// kein zusätzlicher Linux-Benutzer. Der Zugang meldet sich gegen die PureDB an
// und arbeitet unter der UID des Systembenutzers seiner Site — genau der, unter
// dem auch PHP und die Cronjobs dieser Site laufen.
type FTPAccount struct {
	ID       int64  `json:"id"`
	TenantID int64  `json:"tenant_id"`
	SiteID   *int64 `json:"site_id"`
	Username string `json:"username"`
	// PasswordEnc ist verschlüsselt und geht nie über die Liste hinaus —
	// nur über das ausdrückliche Anzeigen.
	PasswordEnc string `json:"-"`
	HomeDir     string `json:"home_dir"`
	UID         int    `json:"uid"`
	GID         int    `json:"gid"`
	QuotaMB     int64  `json:"quota_mb"`
	Status      string `json:"status"`
	LastError   string `json:"last_error"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type Cronjob struct {
	ID           int64  `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	SiteID       *int64 `json:"site_id"`
	Name         string `json:"name"`
	Schedule     string `json:"schedule"`
	Command      string `json:"command"`
	RunAs        string `json:"run_as"`
	Enabled      bool   `json:"enabled"`
	LastRunAt    *int64 `json:"last_run_at"`
	LastExitCode *int   `json:"last_exit_code"`
	LastOutput   string `json:"last_output"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type Backup struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	SiteID      *int64 `json:"site_id"`
	DatabaseID  *int64 `json:"database_id"`
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	Checksum    string `json:"checksum"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	StartedAt   *int64 `json:"started_at"`
	FinishedAt  *int64 `json:"finished_at"`
	CreatedAt   int64  `json:"created_at"`
}

type AuditEntry struct {
	ID         int64  `json:"id"`
	TenantID   *int64 `json:"tenant_id"`
	UserID     *int64 `json:"user_id"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Detail     string `json:"detail"`
	IP         string `json:"ip"`
	Result     string `json:"result"`
	CreatedAt  int64  `json:"created_at"`
}
