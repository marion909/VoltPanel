package main

import (
	"fmt"

	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/version"
	"github.com/spf13/cobra"
)

func (a *app) updateCmd() *cobra.Command {
	var (
		check  bool
		dryRun bool
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Aktualisiert VoltPanel auf die neueste Version des Kanals",
		Long: "Vor dem Tausch wird ein Snapshot aus Binary, Datenbank und Konfiguration\n" +
			"angelegt. Scheitert die Migration, wird dieser Stand automatisch zurückgespielt.",
		// Ohne Migration öffnen: die Migration gehört zum Update selbst und
		// darf erst nach dem Snapshot laufen.
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			updater := core.NewUpdater(a.cfg, a.store, a.log)

			rel, err := updater.LatestRelease(ctx)
			if err != nil {
				return err
			}

			fmt.Printf("Installiert: %s\nVerfügbar:   %s (Kanal %s)\n",
				version.Version, rel.Version, a.cfg.UpdateChannel)
			if rel.Version == version.Version {
				fmt.Println("\nBereits aktuell.")
				return nil
			}
			if rel.Notes != "" {
				fmt.Printf("\n%s\n", rel.Notes)
			}
			if check {
				return nil
			}
			if dryRun {
				fmt.Printf("\n--dry-run: es würde %s für %s geladen und installiert.\n",
					rel.Version, core.Platform())
				return nil
			}
			if !yes && !confirm(fmt.Sprintf("\nAuf %s aktualisieren?", rel.Version)) {
				fmt.Println("Abgebrochen.")
				return nil
			}

			snap, err := updater.Snapshot(ctx)
			if err != nil {
				return fmt.Errorf("snapshot: %w", err)
			}
			fmt.Printf("Snapshot: %s\n", snap.Dir)

			if err := updater.Apply(ctx, rel, snap); err != nil {
				return err
			}

			fmt.Printf("\nAuf %s aktualisiert.\n", rel.Version)
			fmt.Println("Dienste neu starten: systemctl restart volt-agent volt-web")
			return nil
		}),
	}

	cmd.Flags().BoolVar(&check, "check", false, "Nur prüfen, ob eine neue Version vorliegt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Zeigen, was passieren würde, ohne es zu tun")
	cmd.Flags().BoolVar(&yes, "yes", false, "Nicht nachfragen")
	return cmd
}
