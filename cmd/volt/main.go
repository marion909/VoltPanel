// Command volt ist die CLI und zugleich der Webserver des Panels.
//
// Es läuft unprivilegiert als Benutzer "volt"; alles, was root braucht, geht
// über den Socket an volt-agent.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/version"
	"github.com/spf13/cobra"
)

// app bündelt die Abhängigkeiten der Unterbefehle. Store und Agent werden erst
// beim ersten Zugriff geöffnet — `volt --version` soll keine Datenbank brauchen.
type app struct {
	configPath string
	logLevel   string

	cfg     *config.Config
	store   *store.Store
	agent   *agent.Client
	secrets *authn.SecretBox
	log     *slog.Logger
}

func main() {
	a := &app{}
	root := &cobra.Command{
		Use:   "volt",
		Short: "VoltPanel — selbst gehostetes Linux Hosting Control Panel",
		Long: "VoltPanel verwaltet Websites, PHP, Datenbanken und Zertifikate auf\n" +
			"einem Linux-Server. Ein Binary, ein Befehl zum Installieren, einer zum Updaten.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Full(),
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().StringVar(&a.configPath, "config", "", "Pfad zur Konfiguration")
	root.PersistentFlags().StringVar(&a.logLevel, "log-level", "info", "debug | info | warn | error")

	root.AddCommand(
		a.serveCmd(),
		a.statusCmd(),
		a.doctorCmd(),
		a.updateCmd(),
		a.restartCmd(),
		a.setupCmd(),
		a.userCmd(),
		a.siteCmd(),
		a.certCmd(),
		a.backupCmd(),
		a.dbCmd(),
		a.cronCmd(),
		a.tenantCmd(),
		a.planCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "fehler:", err)
		os.Exit(1)
	}
}

// init öffnet Konfiguration, Datenbank, Schlüssel und Agent-Verbindung.
func (a *app) init(migrate bool) error {
	if a.cfg != nil {
		return nil
	}

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(a.logLevel)); err != nil {
		lvl = slog.LevelInfo
	}
	a.log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	cfg, err := config.Load(a.configPath)
	if err != nil {
		return err
	}
	a.cfg = cfg

	if a.store, err = store.Open(cfg.DBPath); err != nil {
		return err
	}
	if migrate {
		from, to, err := a.store.Migrate(context.Background())
		if err != nil {
			return err
		}
		if from != to {
			a.log.Info("schema migriert", "von", from, "auf", to)
		}
	} else if err := a.requireCurrentSchema(); err != nil {
		return err
	}

	if a.secrets, err = authn.LoadSecretBox(cfg.SecretKeyPath); err != nil {
		return err
	}
	a.agent = agent.NewClient(cfg.SocketPath)

	// Ein Aufruf als root darf der Datenbank nicht die Rechte verstellen.
	if err := alignDBOwnership(cfg.DBPath); err != nil {
		a.log.Warn("eigentuemer der datenbank nicht angeglichen", "fehler", err)
	}
	return nil
}

// checkSchema meldet ein veraltetes Schema mit einem brauchbaren Hinweis.
//
// Lesende Befehle migrieren bewusst nicht — eine Migration soll nur dort
// laufen, wo sie erwartet wird. Ohne diese Prüfung bekäme der Benutzer nach
// einem Update aber einen rohen SQL-Fehler über eine fehlende Spalte.
func (a *app) requireCurrentSchema() error {
	current, err := a.store.SchemaVersion(context.Background())
	if err != nil {
		return err
	}
	switch {
	case current == version.SchemaVersion:
		return nil
	case current < version.SchemaVersion:
		return fmt.Errorf(
			"die datenbank steht auf schema v%d, dieses binary erwartet v%d — "+
				"`volt serve` oder `volt update` bringt sie auf den stand",
			current, version.SchemaVersion)
	default:
		return fmt.Errorf(
			"die datenbank steht auf schema v%d, dieses binary kennt nur v%d — "+
				"bitte volt aktualisieren statt es herunterzustufen",
			current, version.SchemaVersion)
	}
}

func (a *app) close() {
	if a.agent != nil {
		_ = a.agent.Close()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
	// Noch einmal nach dem Schließen: die WAL-Dateien entstehen erst beim
	// ersten Schreibzugriff, also nach der Prüfung in init.
	if a.cfg != nil {
		_ = alignDBOwnership(a.cfg.DBPath)
	}
}

// withApp verdrahtet Initialisierung und Aufräumen um einen Unterbefehl.
func (a *app) withApp(migrate bool, fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := a.init(migrate); err != nil {
			return err
		}
		defer a.close()
		return fn(cmd, args)
	}
}

// parseID liest eine numerische ID aus einem CLI-Argument.
func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%q ist keine gültige id", s)
	}
	return id, nil
}
