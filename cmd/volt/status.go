package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/version"
	"github.com/spf13/cobra"
)

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Zeigt Version, Dienste und Kennzahlen",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			fmt.Println(version.Full())

			schema, err := a.store.SchemaVersion(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("Schema:    v%d (Binary kennt v%d)\n", schema, version.SchemaVersion)
			fmt.Printf("Datenbank: %s\n", a.store.Path())
			fmt.Printf("Socket:    %s\n", a.cfg.SocketPath)

			sys := store.SystemScope()
			sites, _ := a.store.ListSites(ctx, sys)
			tenants, _ := a.store.ListTenants(ctx, sys)
			users, _ := a.store.CountUsers(ctx)
			fmt.Printf("Bestand:   %d Sites, %d Tenants, %d Benutzer\n\n", len(sites), len(tenants), users)

			if err := a.agent.Healthy(ctx); err != nil {
				fmt.Printf("Agent:     NICHT ERREICHBAR — %v\n", err)
				return nil
			}
			fmt.Println("Agent:     erreichbar")

			services, err := a.agent.Services(ctx)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "\nDIENST\tSTATUS\tAUTOSTART")
			for _, svc := range services {
				fmt.Fprintf(w, "%s\t%s\t%s\n", svc.Name, onOff(svc.Active, "läuft", "gestoppt"),
					onOff(svc.Enabled, "ja", "nein"))
			}
			return w.Flush()
		}),
	}
}

func (a *app) restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Startet die Panel-Dienste neu",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()

			// Nur volt-web: der Agent führt den Befehl aus und würde sich
			// sonst mitten in der eigenen Antwort beenden.
			if _, err := a.agent.ServiceAction(ctx, "restart", "volt-web"); err != nil {
				return fmt.Errorf("volt-web neu starten: %w", err)
			}
			fmt.Println("volt-web neu gestartet.")
			fmt.Println("Für den Agent: systemctl restart volt-agent")
			return nil
		}),
	}
}

func onOff(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}
