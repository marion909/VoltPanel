package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) certCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cert", Short: "TLS-Zertifikate verwalten"}
	cmd.AddCommand(a.certListCmd(), a.certIssueCmd(), a.certRenewCmd())
	return cmd
}

func (a *app) certListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Listet alle Zertifikate mit Restlaufzeit",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			certs, err := a.store.ListCerts(cmd.Context(), store.SystemScope())
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDOMAINS\tVERFAHREN\tLÄUFT AB\tRESTTAGE\tSTATUS")
			for _, c := range certs {
				expiry := "-"
				if c.NotAfter != nil {
					expiry = time.Unix(*c.NotAfter, 0).Format("2006-01-02")
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%s\n", c.ID, strings.Join(c.Domains, ", "),
					c.Challenge, expiry, c.DaysLeft(), c.Status)
			}
			return w.Flush()
		}),
	}
}

func (a *app) certIssueCmd() *cobra.Command {
	var (
		cfToken  string
		extra    []string
		tenantID int64
	)

	cmd := &cobra.Command{
		Use:   "issue <domain>",
		Short: "Holt ein Zertifikat über Let's Encrypt",
		Long: "Ohne --cloudflare-token läuft die Prüfung über HTTP-01 und der Vhost muss\n" +
			"bereits auf Port 80 erreichbar sein. Wildcards brauchen zwingend DNS-01,\n" +
			"also einen Cloudflare-Token.",
		Args: cobra.ExactArgs(1),
		Example: "  volt cert issue example.at\n" +
			"  volt cert issue '*.example.at' --alt example.at --cloudflare-token $CF_TOKEN",
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()
			domains := append([]string{args[0]}, extra...)

			opts := core.IssueOptions{
				Domains: domains, CloudflareToken: cfToken, TenantID: tenantID,
			}
			// Gehört die Domain zu einer bekannten Site, wird das Zertifikat
			// daran gehängt und die Site direkt auf HTTPS geschaltet.
			if site, err := a.store.SiteByDomain(ctx, sys, args[0]); err == nil {
				opts.SiteID, opts.TenantID = &site.ID, site.TenantID
			}

			fmt.Printf("Fordere Zertifikat für %s an …\n", strings.Join(domains, ", "))
			svc := core.NewCertService(a.cfg, a.store, a.agent, a.log)
			cert, err := svc.Issue(ctx, sys, opts)
			if err != nil {
				return err
			}

			fmt.Printf("Zertifikat ausgestellt, gültig bis %s (%d Tage).\n",
				time.Unix(*cert.NotAfter, 0).Format("2006-01-02"), cert.DaysLeft())
			fmt.Printf("  %s\n  %s\n", cert.CertPath, cert.KeyPath)
			return nil
		}),
	}

	cmd.Flags().StringVar(&cfToken, "cloudflare-token", "", "Cloudflare-API-Token für DNS-01")
	cmd.Flags().StringSliceVar(&extra, "alt", nil, "Weitere Domains im selben Zertifikat")
	cmd.Flags().Int64Var(&tenantID, "tenant", 1, "Tenant-ID, falls keine Site zugeordnet ist")
	return cmd
}

func (a *app) certRenewCmd() *cobra.Command {
	var (
		all     bool
		cfToken string
	)

	cmd := &cobra.Command{
		Use:   "renew",
		Short: "Erneuert Zertifikate mit weniger als 30 Tagen Restlaufzeit",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			if !all {
				return fmt.Errorf("bitte --all angeben, um alle fälligen zertifikate zu erneuern")
			}

			svc := core.NewCertService(a.cfg, a.store, a.agent, a.log)
			renewed, errs := svc.RenewDue(cmd.Context(), func(*store.Cert) string { return cfToken })

			fmt.Printf("%d zertifikat(e) erneuert.\n", renewed)
			for _, err := range errs {
				fmt.Fprintf(os.Stderr, "  fehler: %v\n", err)
			}
			if len(errs) > 0 {
				return fmt.Errorf("%d erneuerung(en) fehlgeschlagen", len(errs))
			}
			return nil
		}),
	}

	cmd.Flags().BoolVar(&all, "all", false, "Alle fälligen Zertifikate erneuern")
	cmd.Flags().StringVar(&cfToken, "cloudflare-token", "", "Cloudflare-API-Token für DNS-01-Zertifikate")
	return cmd
}
