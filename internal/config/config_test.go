package config

import "testing"

// TestValidatePruefteNichtAlleAbsolutenPfade deckt den Fund ab, dass
// Validate() bisher nur DataDir, ConfigDir und SitesDir auf einen absoluten
// Pfad prüfte, obwohl laut den Feldkommentaren auch BackupDir, DBPath,
// SocketPath, NginxDir, PHPFPMDir, CertDir, SecretKeyPath und LogDir feste
// absolute Pfade sein sollen. Ein versehentlich relativer Wert in einem
// dieser Felder hätte sonst erst zur Laufzeit — abhängig vom Arbeitsverzeichnis
// des Daemons — an unerwarteter Stelle Dateien angelegt, statt beim Start
// klar zu scheitern.
func TestValidatePruefteNichtAlleAbsolutenPfade(t *testing.T) {
	setters := map[string]func(*Config, string){
		"DataDir":       func(c *Config, v string) { c.DataDir = v },
		"ConfigDir":     func(c *Config, v string) { c.ConfigDir = v },
		"SitesDir":      func(c *Config, v string) { c.SitesDir = v },
		"BackupDir":     func(c *Config, v string) { c.BackupDir = v },
		"DBPath":        func(c *Config, v string) { c.DBPath = v },
		"SocketPath":    func(c *Config, v string) { c.SocketPath = v },
		"NginxDir":      func(c *Config, v string) { c.NginxDir = v },
		"PHPFPMDir":     func(c *Config, v string) { c.PHPFPMDir = v },
		"CertDir":       func(c *Config, v string) { c.CertDir = v },
		"SecretKeyPath": func(c *Config, v string) { c.SecretKeyPath = v },
		"LogDir":        func(c *Config, v string) { c.LogDir = v },
	}

	for name, set := range setters {
		cfg := Default()
		set(cfg, "relativ/pfad")
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s als relativer Pfad wurde von Validate() akzeptiert", name)
		}
	}

	if err := Default().Validate(); err != nil {
		t.Errorf("Default() besteht Validate() nicht: %v", err)
	}
}
