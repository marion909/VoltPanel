package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) cronCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cron", Short: "Cronjobs verwalten"}
	cmd.AddCommand(a.cronListCmd(), a.cronAddCmd(), a.cronRemoveCmd(),
		a.cronLogCmd(), a.cronSyncCmd())
	return cmd
}

func (a *app) cronService() *core.CronService {
	return core.NewCronService(a.store, a.agent, a.cfg)
}

func (a *app) cronListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Listet alle Cronjobs",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			jobs, err := a.store.ListCronjobs(cmd.Context(), store.SystemScope())
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tZEITPLAN\tBENUTZER\tAKTIV\tLETZTER LAUF\tCODE")
			for _, job := range jobs {
				last, code := "nie", "-"
				if job.LastRunAt != nil {
					last = time.Unix(*job.LastRunAt, 0).Format("2006-01-02 15:04")
				}
				if job.LastExitCode != nil {
					code = fmt.Sprint(*job.LastExitCode)
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", job.ID, job.Name, job.Schedule,
					job.RunAs, onOff(job.Enabled, "ja", "nein"), last, code)
			}
			return w.Flush()
		}),
	}
}

func (a *app) cronAddCmd() *cobra.Command {
	var (
		schedule string
		command  string
		site     string
		disabled bool
	)

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Legt einen Cronjob an",
		Long: "Der Job läuft unter dem Systembenutzer der angegebenen Site — nicht als root\n" +
			"und nicht als Panel-Benutzer. Er kann damit genau das, was die Site auch kann.",
		Args: cobra.ExactArgs(1),
		Example: `  volt cron add laravel_schedule \
    --site example.at \
    --schedule "* * * * *" \
    --command "/usr/bin/php8.3 /var/www/example.at/artisan schedule:run"`,
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			target, err := a.store.SiteByDomain(ctx, sys, site)
			if err != nil {
				return fmt.Errorf("site %q: %w", site, err)
			}

			job, err := a.cronService().CreateCronjob(ctx, sys, core.CreateCronjobInput{
				Name: args[0], Schedule: schedule, Command: command,
				SiteID: &target.ID, Enabled: !disabled, TenantID: target.TenantID,
			})
			if err != nil {
				return err
			}

			fmt.Printf("Cronjob %s angelegt (id %d).\n", job.Name, job.ID)
			fmt.Printf("  Zeitplan: %s\n", job.Schedule)
			fmt.Printf("  Benutzer: %s\n", job.RunAs)
			fmt.Printf("  Datei:    /etc/cron.d/%s\n", core.CronFileName(job.ID))
			if disabled {
				fmt.Println("\nDer Job ist noch abgeschaltet.")
			}
			return nil
		}),
	}

	cmd.Flags().StringVar(&schedule, "schedule", "", "Zeitplan im 5-Feld-Format, z.B. \"*/5 * * * *\"")
	cmd.Flags().StringVar(&command, "command", "", "Auszuführendes Kommando")
	cmd.Flags().StringVar(&site, "site", "", "Domain der Site, unter deren Benutzer der Job läuft")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "Job angelegt, aber noch nicht aktiv")
	_ = cmd.MarkFlagRequired("schedule")
	_ = cmd.MarkFlagRequired("command")
	_ = cmd.MarkFlagRequired("site")
	return cmd
}

func (a *app) cronRemoveCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "remove <id>",
		Short: "Entfernt einen Cronjob",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			job, err := a.store.GetCronjob(ctx, sys, id)
			if err != nil {
				return err
			}
			if !yes && !confirm(fmt.Sprintf("Cronjob %q entfernen?", job.Name)) {
				fmt.Println("Abgebrochen.")
				return nil
			}

			if err := a.cronService().DeleteCronjob(ctx, sys, id); err != nil {
				return err
			}
			fmt.Printf("Cronjob %s entfernt.\n", job.Name)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Nicht nachfragen")
	return cmd
}

func (a *app) cronLogCmd() *cobra.Command {
	var lines int

	cmd := &cobra.Command{
		Use:   "log <id>",
		Short: "Zeigt die Ausgabe der letzten Läufe",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			text, err := a.cronService().Log(cmd.Context(), store.SystemScope(), id, lines)
			if err != nil {
				return err
			}
			if text == "" {
				fmt.Println("Noch keine Ausgabe — der Job ist vermutlich noch nicht gelaufen.")
				return nil
			}
			fmt.Println(text)
			return nil
		}),
	}
	cmd.Flags().IntVar(&lines, "lines", 100, "Anzahl der Zeilen")
	return cmd
}

// cronSyncCmd schreibt alle Jobs neu — das Gegenstück zu `volt site rebuild`.
func (a *app) cronSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Schreibt alle Cronjobs aus dem Datenbankstand neu nach /etc/cron.d",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			applied, errs := a.cronService().SyncAll(cmd.Context())
			fmt.Printf("%d cronjob(s) geschrieben.\n", applied)
			for _, err := range errs {
				fmt.Fprintf(os.Stderr, "  fehler: %v\n", err)
			}
			if len(errs) > 0 {
				return fmt.Errorf("%d cronjob(s) fehlgeschlagen", len(errs))
			}
			return nil
		}),
	}
}
