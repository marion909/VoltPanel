// Command volt-agent ist der privilegierte Teil von VoltPanel.
//
// Er läuft als root und führt ausschließlich die typisierten Operationen aus,
// die internal/agent kennt. Er nimmt keine HTTP-Anfragen entgegen, spricht nur
// über einen Unix-Socket und prüft dabei, welcher Systembenutzer verbunden ist.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/version"
)

func main() {
	var (
		configPath  = flag.String("config", "", "Pfad zur Konfiguration (Standard: /etc/volt/config.yaml)")
		peerUser    = flag.String("peer-user", "volt", "Systembenutzer, der sich verbinden darf")
		logLevel    = flag.String("log-level", "info", "debug | info | warn | error")
		showVersion = flag.Bool("version", false, "Version ausgeben und beenden")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Full())
		return
	}

	log := newLogger(*logLevel)
	if err := run(*configPath, *peerUser, log); err != nil {
		log.Error("agent beendet", "err", err)
		os.Exit(1)
	}
}

func run(configPath, peerUser string, log *slog.Logger) error {
	// Ohne root kann der Agent seine Aufgabe nicht erfüllen — das soll beim
	// Start auffallen und nicht erst bei der ersten Operation.
	if os.Geteuid() != 0 {
		return fmt.Errorf("volt-agent muss als root laufen (aktuelle uid %d)", os.Geteuid())
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	srv, err := agent.NewServer(agent.ServerOptions{
		SocketPath:  cfg.SocketPath,
		PeerUser:    peerUser,
		Logger:      log,
		NginxDir:    cfg.NginxDir,
		PHPDir:      cfg.PHPFPMDir,
		CertDir:     cfg.CertDir,
		SitesDir:    cfg.SitesDir,
		LogDir:      cfg.LogDir,
		PanelDomain: cfg.PanelDomain,
	})
	if err != nil {
		return err
	}
	if err := srv.Listen(); err != nil {
		return err
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("volt-agent gestartet", "version", version.Version, "socket", cfg.SocketPath, "peer", peerUser)
	return srv.Serve(ctx)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	// Strukturierte Logs nach stderr; systemd nimmt sie ins Journal auf.
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
