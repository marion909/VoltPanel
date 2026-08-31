package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

var reNonAlnumCLI = regexp.MustCompile(`[^a-z0-9]+`)

func lower(s string) string      { return strings.ToLower(strings.TrimSpace(s)) }
func trimDashes(s string) string { return strings.Trim(s, "-") }

func (a *app) planCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Short: "Hosting-Pakete verwalten"}
	cmd.AddCommand(a.planListCmd(), a.planAddCmd(), a.planRemoveCmd())
	return cmd
}

func (a *app) planListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Listet die Hosting-Pakete",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			plans, err := a.store.ListPlans(cmd.Context(), store.SystemScope())
			if err != nil {
				return err
			}
			if len(plans) == 0 {
				fmt.Println("Noch kein Paket angelegt — es gelten für alle Mandanten keine Grenzen.")
				fmt.Println("Beispiel: volt plan add Klein --sites 5 --databases 5 --disk 5000")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tSITES\tDBS\tFTP\tCRON\tPLATZ\tTRAFFIC\tSTANDARD")
			for _, p := range plans {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					p.ID, p.Name,
					limitText(int64(p.MaxSites), false), limitText(int64(p.MaxDatabases), false),
					limitText(int64(p.MaxFTP), false), limitText(int64(p.MaxCronjobs), false),
					limitText(p.DiskQuotaMB*1024*1024, true), limitText(p.TrafficQuotaMB*1024*1024, true),
					onOff(p.IsDefault, "ja", ""))
			}
			return w.Flush()
		}),
	}
}

// limitText zeigt 0 als "∞" — 0 bedeutet im Datenmodell "keine Grenze".
func limitText(v int64, bytes bool) string {
	if v <= 0 {
		return "∞"
	}
	if bytes {
		return humanBytes(v)
	}
	return fmt.Sprint(v)
}

func (a *app) planAddCmd() *cobra.Command {
	var (
		description string
		sites       int
		databases   int
		ftp         int
		mailboxes   int
		cronjobs    int
		diskMB      int64
		trafficMB   int64
		isDefault   bool
	)

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Legt ein Hosting-Paket an",
		Long: "Jeder Wert bedeutet 0 = unbegrenzt. Ein Paket ohne gepflegte Werte ist\n" +
			"also kein gesperrtes Paket, sondern eines ohne Grenzen.",
		Args:    cobra.ExactArgs(1),
		Example: "  volt plan add Klein --sites 5 --databases 5 --disk 5000 --traffic 100000",
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			plan := &store.Plan{
				Name: args[0], Description: description,
				MaxSites: sites, MaxDatabases: databases, MaxFTP: ftp,
				MaxMailboxes: mailboxes, MaxCronjobs: cronjobs,
				DiskQuotaMB: diskMB, TrafficQuotaMB: trafficMB, IsDefault: isDefault,
			}
			if err := a.store.CreatePlan(cmd.Context(), store.SystemScope(), plan); err != nil {
				return err
			}

			fmt.Printf("Paket %s angelegt (id %d).\n", plan.Name, plan.ID)
			fmt.Printf("\nZuordnen mit: volt tenant set-plan <tenant-id> %d\n", plan.ID)
			return nil
		}),
	}

	cmd.Flags().StringVar(&description, "description", "", "Beschreibung")
	cmd.Flags().IntVar(&sites, "sites", 0, "Maximale Anzahl Websites (0 = unbegrenzt)")
	cmd.Flags().IntVar(&databases, "databases", 0, "Maximale Anzahl Datenbanken")
	cmd.Flags().IntVar(&ftp, "ftp", 0, "Maximale Anzahl FTP-Zugänge")
	cmd.Flags().IntVar(&mailboxes, "mailboxes", 0, "Maximale Anzahl Postfächer")
	cmd.Flags().IntVar(&cronjobs, "cronjobs", 0, "Maximale Anzahl Cronjobs")
	cmd.Flags().Int64Var(&diskMB, "disk", 0, "Speicherplatz in MB")
	cmd.Flags().Int64Var(&trafficMB, "traffic", 0, "Traffic in MB pro Monat")
	cmd.Flags().BoolVar(&isDefault, "default", false, "Neuen Mandanten automatisch zuordnen")
	return cmd
}

func (a *app) planRemoveCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "remove <id>",
		Short: "Entfernt ein Hosting-Paket",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			plan, err := a.store.GetPlan(ctx, sys, id)
			if err != nil {
				return err
			}

			// Das ist eine stille Lockerung, keine Verschärfung — darauf muss
			// hingewiesen werden.
			if !yes && !confirm(fmt.Sprintf(
				"Paket %q entfernen? Zugeordnete Mandanten haben danach keine Grenzen mehr.", plan.Name)) {
				fmt.Println("Abgebrochen.")
				return nil
			}

			if err := a.store.DeletePlan(ctx, sys, id); err != nil {
				return err
			}
			fmt.Printf("Paket %s entfernt.\n", plan.Name)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Nicht nachfragen")
	return cmd
}
