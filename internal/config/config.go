// Package config lädt die Panel-Konfiguration aus /etc/volt/config.yaml
// (bzw. VOLT_CONFIG) und liefert für alles einen brauchbaren Default.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultConfigPath = "/etc/volt/config.yaml"
	DefaultSocketPath = "/run/volt/agent.sock"
)

type Config struct {
	// Web
	ListenAddr string
	Port       int
	AccessPath string // "geheimer" Pfad-Präfix fürs Panel, leer = /
	TrustProxy bool

	// Pfade
	DataDir    string // /var/lib/volt
	ConfigDir  string // /etc/volt
	LogDir     string // /var/log/volt
	SitesDir   string // /var/www
	BackupDir  string // /var/backups/volt
	DBPath     string // /var/lib/volt/volt.db
	SocketPath string // /run/volt/agent.sock
	NginxDir   string // /etc/nginx
	PHPFPMDir  string // /etc/php
	CertDir    string // /var/lib/volt/certs

	// Sicherheit
	SecretKeyPath string // Datei mit dem 32-Byte-Schlüssel für Secrets-at-rest
	SessionTTLMin int
	IPWhitelist   []string

	// ACME
	ACMEEmail     string
	ACMEDirectory string

	// Update
	UpdateChannel string
	UpdateBaseURL string
}

func Default() *Config {
	return &Config{
		ListenAddr:    "0.0.0.0",
		Port:          8443,
		AccessPath:    "",
		TrustProxy:    false,
		DataDir:       "/var/lib/volt",
		ConfigDir:     "/etc/volt",
		LogDir:        "/var/log/volt",
		SitesDir:      "/var/www",
		BackupDir:     "/var/backups/volt",
		DBPath:        "/var/lib/volt/volt.db",
		SocketPath:    DefaultSocketPath,
		NginxDir:      "/etc/nginx",
		PHPFPMDir:     "/etc/php",
		CertDir:       "/var/lib/volt/certs",
		SecretKeyPath: "/etc/volt/secret.key",
		SessionTTLMin: 720,
		ACMEDirectory: "https://acme-v02.api.letsencrypt.org/directory",
		UpdateChannel: "stable",
		UpdateBaseURL: "https://get.voltpanel.dev",
	}
}

// Load liest die Config-Datei, falls vorhanden, und legt Umgebungsvariablen
// (VOLT_*) obendrauf. Fehlt die Datei, gilt der Default — das hält den ersten
// Start und die Tests frei von Vorbedingungen.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = os.Getenv("VOLT_CONFIG")
	}
	if path == "" {
		path = DefaultConfigPath
	}

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := cfg.applyYAML(string(raw)); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// kein Fehler: Defaults gelten
	default:
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	cfg.applyEnv()
	return cfg, cfg.Validate()
}

// applyYAML versteht bewusst nur flaches "key: value". Die Config hat keine
// verschachtelten Strukturen, und so bleibt der Binary frei von einer
// YAML-Abhängigkeit.
func (c *Config) applyYAML(s string) error {
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return fmt.Errorf("zeile %d: erwartet 'key: value'", i+1)
		}
		if err := c.set(strings.TrimSpace(key), strings.Trim(strings.TrimSpace(val), `"'`)); err != nil {
			return fmt.Errorf("zeile %d: %w", i+1, err)
		}
	}
	return nil
}

func (c *Config) applyEnv() {
	for key := range fieldSetters {
		if v, ok := os.LookupEnv("VOLT_" + strings.ToUpper(key)); ok {
			_ = c.set(key, v)
		}
	}
}

var fieldSetters = map[string]func(*Config, string) error{
	"listen_addr":     func(c *Config, v string) error { c.ListenAddr = v; return nil },
	"port":            func(c *Config, v string) error { return setInt(&c.Port, v) },
	"access_path":     func(c *Config, v string) error { c.AccessPath = strings.Trim(v, "/"); return nil },
	"trust_proxy":     func(c *Config, v string) error { c.TrustProxy = isTrue(v); return nil },
	"data_dir":        func(c *Config, v string) error { c.DataDir = v; return nil },
	"config_dir":      func(c *Config, v string) error { c.ConfigDir = v; return nil },
	"log_dir":         func(c *Config, v string) error { c.LogDir = v; return nil },
	"sites_dir":       func(c *Config, v string) error { c.SitesDir = v; return nil },
	"backup_dir":      func(c *Config, v string) error { c.BackupDir = v; return nil },
	"db_path":         func(c *Config, v string) error { c.DBPath = v; return nil },
	"socket_path":     func(c *Config, v string) error { c.SocketPath = v; return nil },
	"nginx_dir":       func(c *Config, v string) error { c.NginxDir = v; return nil },
	"php_fpm_dir":     func(c *Config, v string) error { c.PHPFPMDir = v; return nil },
	"cert_dir":        func(c *Config, v string) error { c.CertDir = v; return nil },
	"secret_key_path": func(c *Config, v string) error { c.SecretKeyPath = v; return nil },
	"session_ttl_min": func(c *Config, v string) error { return setInt(&c.SessionTTLMin, v) },
	"ip_whitelist":    func(c *Config, v string) error { c.IPWhitelist = splitList(v); return nil },
	"acme_email":      func(c *Config, v string) error { c.ACMEEmail = v; return nil },
	"acme_directory":  func(c *Config, v string) error { c.ACMEDirectory = v; return nil },
	"update_channel":  func(c *Config, v string) error { c.UpdateChannel = v; return nil },
	"update_base_url": func(c *Config, v string) error { c.UpdateBaseURL = v; return nil },
}

func (c *Config) set(key, val string) error {
	fn, ok := fieldSetters[key]
	if !ok {
		return fmt.Errorf("unbekannter schlüssel %q", key)
	}
	return fn(c, val)
}

func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port %d liegt außerhalb 1–65535", c.Port)
	}
	if c.SessionTTLMin < 1 {
		return errors.New("session_ttl_min muss mindestens 1 sein")
	}
	switch c.UpdateChannel {
	case "stable", "beta":
	default:
		return fmt.Errorf("update_channel %q ist weder stable noch beta", c.UpdateChannel)
	}
	for _, p := range []string{c.DataDir, c.ConfigDir, c.SitesDir} {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("pfad %q muss absolut sein", p)
		}
	}
	return nil
}

// SiteRoot ist das Wurzelverzeichnis einer Site. Alle Datei-Operationen werden
// gegen diesen Pfad eingesperrt.
func (c *Config) SiteRoot(domain string) string { return filepath.Join(c.SitesDir, domain) }

func setInt(dst *int, v string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("%q ist keine zahl", v)
	}
	*dst = n
	return nil
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(strings.Trim(v, "[]"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
