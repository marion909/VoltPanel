package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) backupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "backup", Short: "Backups erstellen und zurückspielen"}
	cmd.AddCommand(a.backupCreateCmd(), a.backupListCmd(), a.backupRestoreCmd())
	return cmd
}

func (a *app) backupCreateCmd() *cobra.Command {
	var (
		sites     []string
		allSites  bool
		noConfigs bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Erstellt ein Backup aus Datenbank, Konfiguration und optional Site-Dateien",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			if allSites {
				list, err := a.store.ListSites(ctx, store.SystemScope())
				if err != nil {
					return err
				}
				for _, s := range list {
					sites = append(sites, s.Domain)
				}
			}

			svc := core.NewBackupService(a.cfg, a.store, a.log)
			res, err := svc.Create(ctx, core.CreateOptions{
				IncludeConfig: !noConfigs, SiteDomains: sites, TenantID: 1,
			})
			if err != nil {
				return err
			}

			fmt.Printf("Backup erstellt: %s\n", res.Path)
			fmt.Printf("  Größe:     %s\n", humanBytes(res.SizeBytes))
			fmt.Printf("  Prüfsumme: %s\n", res.Checksum)
			fmt.Printf("  Dauer:     %s\n", res.Duration.Round(1e6))
			return nil
		}),
	}

	cmd.Flags().StringSliceVar(&sites, "site", nil, "Dateien dieser Site mitsichern (mehrfach angebbar)")
	cmd.Flags().BoolVar(&allSites, "all-sites", false, "Dateien aller Sites mitsichern")
	cmd.Flags().BoolVar(&noConfigs, "no-config", false, "Konfigurationsverzeichnis auslassen")
	return cmd
}

func (a *app) backupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Listet die lokal vorhandenen Backups",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			archives, err := core.NewBackupService(a.cfg, a.store, a.log).ListArchives()
			if err != nil {
				return err
			}
			if len(archives) == 0 {
				fmt.Printf("Keine Backups in %s.\n", a.cfg.BackupDir)
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "DATEI\tGRÖSSE\tERSTELLT")
			for _, f := range archives {
				fmt.Fprintf(w, "%s\t%s\t%s\n", f.Name(), humanBytes(f.Size()),
					f.ModTime().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		}),
	}
}

func (a *app) backupRestoreCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "restore <archiv>",
		Short: "Spielt die Datenbank aus einem Backup zurück",
		Long: "Überschreibt die aktuelle Panel-Datenbank. Vorher wird automatisch eine\n" +
			"Sicherheitskopie unter <db>.vor-restore angelegt.\n\n" +
			"Site-Dateien werden bewusst nicht automatisch überschrieben — sie liegen\n" +
			"im Archiv und lassen sich gezielt herausholen.",
		Args: cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			if !yes && !confirm(fmt.Sprintf("Datenbank %s wirklich aus %s überschreiben?", a.cfg.DBPath, args[0])) {
				fmt.Println("Abgebrochen.")
				return nil
			}

			svc := core.NewBackupService(a.cfg, a.store, a.log)
			if err := svc.Restore(cmd.Context(), args[0]); err != nil {
				return err
			}

			fmt.Println("Datenbank zurückgespielt.")
			fmt.Println("Jetzt: systemctl restart volt-agent volt-web && volt site rebuild --all")
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Nicht nachfragen")
	return cmd
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
