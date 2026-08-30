package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"github.com/marion909/voltpanel/internal/api"
	"github.com/marion909/voltpanel/internal/metrics"
	"github.com/spf13/cobra"
)

func (a *app) serveCmd() *cobra.Command {
	var devOrigin string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Startet den Panel-Webserver (wird normalerweise von systemd aufgerufen)",
		RunE: a.withApp(true, func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			collector := metrics.New(2*time.Second, a.log)
			go collector.Run(ctx)
			go a.purgeSessions(ctx)

			srv, err := api.New(api.Options{
				Config: a.cfg, Store: a.store, Agent: a.agent,
				Metrics: collector, Secrets: a.secrets,
				Logger: a.log, DevOrigin: devOrigin,
			})
			if err != nil {
				return err
			}

			// Der Agent muss nicht schon laufen — das Panel soll auch dann
			// starten, damit man im Browser überhaupt sieht, was fehlt.
			if err := a.agent.Healthy(ctx); err != nil {
				a.log.Warn("agent nicht erreichbar, systemaktionen sind vorerst nicht möglich", "err", err)
			}
			return srv.Start(ctx)
		}),
	}
	cmd.Flags().StringVar(&devOrigin, "dev-origin", "",
		"erlaubt zusätzlich diesen Origin (für den Vite-Dev-Server, z.B. http://localhost:5173)")
	return cmd
}

// purgeSessions räumt abgelaufene Sessions weg, damit die Tabelle nicht wächst.
func (a *app) purgeSessions(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := a.store.PurgeExpiredSessions(ctx)
			if err != nil {
				a.log.Warn("sessions aufräumen fehlgeschlagen", "err", err)
				continue
			}
			if n > 0 {
				a.log.Debug("abgelaufene sessions entfernt", "anzahl", n)
			}
		}
	}
}
