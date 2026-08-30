package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) siteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "site", Short: "Websites verwalten"}
	cmd.AddCommand(a.siteListCmd(), a.siteAddCmd(), a.siteRemoveCmd(), a.siteRebuildCmd())
	return cmd
}

func (a *app) siteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Listet alle Sites",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			sites, err := a.store.ListSites(cmd.Context(), store.SystemScope())
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDOMAIN\tTYP\tPHP\tTENANT\tSSL\tSTATUS")
			for _, s := range sites {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%s\t%s\n", s.ID, s.Domain, s.Type,
					orDash(s.PHPVersion), s.TenantID, onOff(s.SSLEnabled, "ja", "nein"), s.Status)
			}
			return w.Flush()
		}),
	}
}

func (a *app) siteAddCmd() *cobra.Command {
	var (
		siteType string
		phpVer   string
		proxyTo  string
		docRoot  string
		tenantID int64
		aliases  []string
	)

	cmd := &cobra.Command{
		Use:   "add <domain>",
		Short: "Legt eine Site an (Verzeichnisse, Systembenutzer, FPM-Pool, Vhost)",
		Args:  cobra.ExactArgs(1),
		Example: "  volt site add example.at --php 8.3 --tenant 4\n" +
			"  volt site add app.example.at --type proxy --proxy-to http://127.0.0.1:3000",
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			// PHP-Version angegeben, Typ nicht: der Wunsch ist eindeutig.
			if phpVer != "" && !cmd.Flags().Changed("type") {
				siteType = string(store.SitePHP)
			}
			if proxyTo != "" && !cmd.Flags().Changed("type") {
				siteType = string(store.SiteProxy)
			}

			svc := core.NewSiteService(a.store, a.agent, a.cfg)
			site, err := svc.CreateSite(cmd.Context(), store.SystemScope(), core.CreateSiteInput{
				Domain: args[0], Aliases: aliases, Type: store.SiteType(siteType),
				PHPVersion: phpVer, ProxyTarget: proxyTo, DocumentRoot: docRoot, TenantID: tenantID,
			})
			if err != nil {
				return err
			}

			fmt.Printf("Site %s angelegt.\n", site.Domain)
			fmt.Printf("  Verzeichnis:   %s\n", site.WebRoot())
			fmt.Printf("  Systembenutzer: %s\n", site.SystemUser)
			if site.Type == store.SitePHP {
				fmt.Printf("  PHP:           %s (Pool %s)\n", site.PHPVersion, core.PoolName(site.Domain))
			}
			fmt.Printf("\nZertifikat holen mit: volt cert issue %s\n", site.Domain)
			return nil
		}),
	}

	cmd.Flags().StringVar(&siteType, "type", "static", "static | php | proxy")
	cmd.Flags().StringVar(&phpVer, "php", "", "PHP-Version, z.B. 8.3 (setzt --type php)")
	cmd.Flags().StringVar(&proxyTo, "proxy-to", "", "Proxy-Ziel, z.B. http://127.0.0.1:3000 (setzt --type proxy)")
	cmd.Flags().StringVar(&docRoot, "document-root", "public", "Unterverzeichnis, das ausgeliefert wird")
	cmd.Flags().Int64Var(&tenantID, "tenant", 1, "Tenant-ID")
	cmd.Flags().StringSliceVar(&aliases, "alias", nil, "Weitere Domains (mehrfach angebbar)")
	return cmd
}

func (a *app) siteRemoveCmd() *cobra.Command {
	var (
		purge bool
		yes   bool
	)

	cmd := &cobra.Command{
		Use:   "remove <domain>",
		Short: "Entfernt eine Site",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()
			site, err := a.store.SiteByDomain(ctx, sys, args[0])
			if err != nil {
				return err
			}

			question := fmt.Sprintf("Site %s entfernen?", site.Domain)
			if purge {
				question = fmt.Sprintf("Site %s MIT ALLEN DATEIEN unter %s löschen?", site.Domain, site.RootPath)
			}
			if !yes && !confirm(question) {
				fmt.Println("Abgebrochen.")
				return nil
			}

			svc := core.NewSiteService(a.store, a.agent, a.cfg)
			if err := svc.DeleteSite(ctx, sys, site.ID, !purge); err != nil {
				return err
			}

			fmt.Printf("Site %s entfernt.\n", site.Domain)
			if !purge {
				fmt.Printf("Die Dateien unter %s sind noch da.\n", site.RootPath)
			}
			return nil
		}),
	}

	cmd.Flags().BoolVar(&purge, "purge", false, "Auch das Datenverzeichnis löschen")
	cmd.Flags().BoolVar(&yes, "yes", false, "Nicht nachfragen")
	return cmd
}

// siteRebuildCmd erzeugt Vhost und Pool aus der Datenbank neu — die Reparatur,
// wenn eine Config von Hand verbogen wurde.
func (a *app) siteRebuildCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "rebuild [domain]",
		Short: "Erzeugt Vhost und FPM-Pool aus dem Datenbankstand neu",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()
			svc := core.NewSiteService(a.store, a.agent, a.cfg)

			var sites []*store.Site
			switch {
			case all:
				var err error
				if sites, err = a.store.ListSites(ctx, sys); err != nil {
					return err
				}
			case len(args) == 1:
				site, err := a.store.SiteByDomain(ctx, sys, args[0])
				if err != nil {
					return err
				}
				sites = []*store.Site{site}
			default:
				return fmt.Errorf("entweder eine domain angeben oder --all")
			}

			var failed int
			for _, site := range sites {
				if err := svc.Rebuild(ctx, sys, site.ID); err != nil {
					fmt.Printf("  %-40s FEHLER: %v\n", site.Domain, err)
					failed++
					continue
				}
				fmt.Printf("  %-40s neu erzeugt\n", site.Domain)
			}
			if failed > 0 {
				return fmt.Errorf("%d von %d sites konnten nicht neu erzeugt werden", failed, len(sites))
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "Alle Sites neu erzeugen")
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
