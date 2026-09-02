package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/release"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/version"
	"github.com/spf13/cobra"
)

// check ist ein einzelner Diagnosepunkt.
type check struct {
	name   string
	status string // ok | warn | fail
	detail string
}

// doctorCmd prüft, ob das System in einem Zustand ist, in dem das Panel
// arbeiten kann. Gedacht als erster Griff, wenn etwas nicht funktioniert.
func (a *app) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Selbstdiagnose: Dienste, Ports, Rechte, Schema, Zertifikate",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			fmt.Println(version.Full())
			fmt.Println()

			checks := make([]check, 0, 16)
			checks = append(checks, a.checkSchema(ctx))
			checks = append(checks, a.checkPaths()...)
			checks = append(checks, a.checkAgent(ctx))
			checks = append(checks, a.checkServices(ctx)...)
			checks = append(checks, a.checkPort())
			checks = append(checks, a.checkSharedConfig())
			checks = append(checks, a.checkUserLocks())
			checks = append(checks, a.checkUpdateSignature())
			checks = append(checks, a.checkCerts(ctx)...)

			var failed, warned int
			for _, c := range checks {
				symbol := "  ok  "
				switch c.status {
				case "warn":
					symbol, warned = " warn ", warned+1
				case "fail":
					symbol, failed = " FAIL ", failed+1
				}
				fmt.Printf("[%s] %-32s %s\n", symbol, c.name, c.detail)
			}

			fmt.Printf("\n%d geprüft, %d Warnungen, %d Fehler\n", len(checks), warned, failed)
			if failed > 0 {
				return fmt.Errorf("%d prüfung(en) fehlgeschlagen", failed)
			}
			return nil
		}),
	}
}

func (a *app) checkSchema(ctx context.Context) check {
	got, err := a.store.SchemaVersion(ctx)
	if err != nil {
		return check{"Datenbank-Schema", "fail", err.Error()}
	}
	switch {
	case got == version.SchemaVersion:
		return check{"Datenbank-Schema", "ok", fmt.Sprintf("v%d", got)}
	case got < version.SchemaVersion:
		return check{"Datenbank-Schema", "warn",
			fmt.Sprintf("v%d, Binary erwartet v%d — `volt serve` migriert beim Start", got, version.SchemaVersion)}
	default:
		return check{"Datenbank-Schema", "fail",
			fmt.Sprintf("v%d ist neuer als das Binary (v%d) — bitte volt aktualisieren", got, version.SchemaVersion)}
	}
}

func (a *app) checkPaths() []check {
	out := []check{}
	// Der Schlüssel entsperrt alle gespeicherten Secrets — zu weite Rechte
	// sind hier ein echtes Problem, keine Kosmetik.
	if info, err := os.Stat(a.cfg.SecretKeyPath); err != nil {
		out = append(out, check{"Secret-Schlüssel", "fail", err.Error()})
	} else if perm := info.Mode().Perm(); perm&0o077 != 0 {
		out = append(out, check{"Secret-Schlüssel", "fail",
			fmt.Sprintf("%s hat Rechte %o, erwartet 600", a.cfg.SecretKeyPath, perm)})
	} else {
		out = append(out, check{"Secret-Schlüssel", "ok", a.cfg.SecretKeyPath})
	}

	for name, dir := range map[string]string{
		"Datenverzeichnis":   a.cfg.DataDir,
		"Site-Verzeichnis":   a.cfg.SitesDir,
		"Backup-Verzeichnis": a.cfg.BackupDir,
		"Log-Verzeichnis":    a.cfg.LogDir,
	} {
		if _, err := os.Stat(dir); err != nil {
			out = append(out, check{name, "warn", dir + " fehlt"})
			continue
		}
		// Schreibbarkeit tatsächlich testen statt Rechte zu interpretieren.
		probe := filepath.Join(dir, ".volt-doctor-probe")
		if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
			out = append(out, check{name, "fail", "nicht beschreibbar: " + err.Error()})
			continue
		}
		os.Remove(probe)
		out = append(out, check{name, "ok", dir})
	}
	return out
}

func (a *app) checkAgent(ctx context.Context) check {
	if err := a.agent.Healthy(ctx); err != nil {
		return check{"Agent-Verbindung", "fail", err.Error()}
	}
	return check{"Agent-Verbindung", "ok", a.cfg.SocketPath}
}

func (a *app) checkServices(ctx context.Context) []check {
	services, err := a.agent.Services(ctx)
	if err != nil {
		return []check{{"Dienste", "warn", "nicht abfragbar: " + err.Error()}}
	}

	out := make([]check, 0, len(services))
	for _, svc := range services {
		if svc.Active {
			out = append(out, check{"Dienst " + svc.Name, "ok", svc.SubState})
			continue
		}
		// Nginx ist für ein Hosting-Panel nicht optional; alles andere kann
		// legitim gestoppt sein.
		status := "warn"
		if svc.Name == "nginx" {
			status = "fail"
		}
		out = append(out, check{"Dienst " + svc.Name, status, "gestoppt"})
	}
	return out
}

func (a *app) checkPort() check {
	addr := net.JoinHostPort(a.cfg.ListenAddr, fmt.Sprint(a.cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return check{"Panel-Port", "warn", addr + " antwortet nicht (läuft volt-web?)"}
	}
	conn.Close()
	return check{"Panel-Port", "ok", addr}
}

// checkUpdateSignature sagt, ob dieses Panel ein Update überhaupt prüfen kann.
//
// Ein Update schreibt das Panel und den Root-Daemon neu. Wer die Angaben dazu
// unterschieben kann, hat den Server — deshalb soll ein Betreiber das erfahren,
// bevor das erste Update ansteht, und nicht erst, wenn es fehlschlägt.
func (a *app) checkUpdateSignature() check {
	switch {
	case a.cfg.UpdateAllowUnsigned:
		return check{"Update-Signatur", "warn",
			"update_allow_unsigned steht in der config.yaml — Updates werden " +
				"ohne Signaturprüfung eingespielt"}
	case !release.Default().HasKey():
		return check{"Update-Signatur", "warn",
			"dieses Binary trägt keinen Release-Schlüssel; `volt update` lehnt " +
				"deshalb jeden Kanal ab (siehe docs/release.md)"}
	}
	return check{"Update-Signatur", "ok", "latest.json wird gegen den eingebetteten Schlüssel geprüft"}
}

// checkUserLocks meldet liegengebliebene Sperrdateien von useradd.
//
// Sie blockieren jedes Anlegen einer Site — und zwar dauerhaft, obwohl die
// Meldung "try again later" lautet. Hier gefunden zu werden ist deutlich
// billiger, als es beim ersten Kunden zu merken.
func (a *app) checkUserLocks() check {
	locks := []string{
		"/etc/passwd.lock", "/etc/group.lock",
		"/etc/shadow.lock", "/etc/gshadow.lock",
	}

	var found []string
	for _, path := range locks {
		if _, err := os.Lstat(path); err == nil {
			found = append(found, path)
		}
	}
	if len(found) == 0 {
		// Die Sperre der Benutzerverwaltung ist nicht die Existenz einer
		// Datei, sondern ein fcntl-Lock auf /etc/.pwd.lock. Ein haengendes
		// useradd haelt ihn und blockiert alles Weitere.
		if holder := agent.PwdLockHolder(); holder != "" {
			return check{"Benutzerverwaltung", "warn",
				holder + " hält /etc/.pwd.lock — solange scheitert jedes Anlegen einer Site"}
		}
		return check{"Benutzerverwaltung", "ok", "keine liegengebliebenen Sperrdateien"}
	}
	return check{"Benutzerverwaltung", "fail",
		strings.Join(found, ", ") + " blockieren useradd — nach dem Prüfen entfernen"}
}

// checkSharedConfig prüft die vhost-übergreifende nginx-Config.
//
// Fehlt sie, beantwortet die Standardseite der Distribution die
// ACME-Prüfungen mit 404 — und ein `volt cert issue` scheitert mit einer
// Meldung von Let's Encrypt, die den Grund nicht nennt.
func (a *app) checkSharedConfig() check {
	path := filepath.Join(a.cfg.NginxDir, "conf.d", "volt-shared.conf")
	if _, err := os.Stat(path); err != nil {
		return check{"Nginx-Grundconfig", "fail",
			path + " fehlt — ACME-Prüfungen laufen ins Leere. `volt site rebuild --all` schreibt sie"}
	}

	webroot := filepath.Join(a.cfg.DataDir, "acme")
	if _, err := os.Stat(webroot); err != nil {
		return check{"Nginx-Grundconfig", "warn", "ACME-Webroot " + webroot + " fehlt"}
	}
	return check{"Nginx-Grundconfig", "ok", path}
}

func (a *app) checkCerts(ctx context.Context) []check {
	certs, err := a.store.ListCerts(ctx, store.SystemScope())
	if err != nil {
		return []check{{"Zertifikate", "warn", err.Error()}}
	}
	if len(certs) == 0 {
		return []check{{"Zertifikate", "ok", "keine hinterlegt"}}
	}

	out := make([]check, 0, len(certs))
	for _, c := range certs {
		days := c.DaysLeft()
		name := "Zertifikat " + c.Domains[0]
		switch {
		case days <= 0:
			out = append(out, check{name, "fail", "abgelaufen"})
		case days <= 14:
			out = append(out, check{name, "warn", fmt.Sprintf("läuft in %d Tagen ab", days)})
		default:
			out = append(out, check{name, "ok", fmt.Sprintf("noch %d Tage", days)})
		}
	}
	return out
}
