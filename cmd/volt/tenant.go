package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) tenantCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tenant", Short: "Mandanten verwalten"}
	cmd.AddCommand(a.tenantListCmd(), a.tenantAddCmd(), a.tenantSetPlanCmd(),
		a.tenantSuspendCmd(), a.tenantUsageCmd(),
		a.tenantExportCmd(), a.tenantImportCmd())
	return cmd
}

func (a *app) tenantListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Listet alle Mandanten mit Paket und Verbrauch",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			tenants, err := a.store.ListTenants(ctx, sys)
			if err != nil {
				return err
			}
			quota := core.NewQuotaService(a.store, a.agent, a.cfg, a.log)

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tSLUG\tPAKET\tSITES\tDBS\tPLATZ\tSTATUS")
			for _, t := range tenants {
				status, err := quota.Status(ctx, sys, t.ID)
				if err != nil {
					fmt.Fprintf(w, "%d\t%s\t%s\t?\t?\t?\t?\t%s\n", t.ID, t.Name, t.Slug, t.Status)
					continue
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					t.ID, t.Name, t.Slug, status.PlanName,
					countOf(status, core.ResourceSites),
					countOf(status, core.ResourceDatabases),
					diskOf(status), t.Status)
			}
			return w.Flush()
		}),
	}
}

// countOf formatiert "benutzt/grenze", oder nur die Zahl ohne Grenze.
func countOf(status *core.QuotaStatus, res core.Resource) string {
	for _, e := range status.Entries {
		if e.Resource != res {
			continue
		}
		if e.Limit <= 0 {
			return fmt.Sprintf("%d", e.Used)
		}
		return fmt.Sprintf("%d/%d", e.Used, e.Limit)
	}
	return "-"
}

func diskOf(status *core.QuotaStatus) string {
	for _, e := range status.Entries {
		if e.Resource != core.ResourceDisk {
			continue
		}
		if e.Limit <= 0 {
			return humanBytes(e.Used)
		}
		return fmt.Sprintf("%s/%s", humanBytes(e.Used), humanBytes(e.Limit))
	}
	return "-"
}

func (a *app) tenantAddCmd() *cobra.Command {
	var (
		slug   string
		planID int64
	)

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Legt einen Mandanten an",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			if slug == "" {
				slug = slugify(args[0])
			}
			tenant := &store.Tenant{Name: args[0], Slug: slug}

			// Ohne ausdrückliches Paket das Standardpaket zuordnen, falls es
			// eines gibt — sonst startet jeder neue Kunde ohne jede Grenze.
			if planID > 0 {
				tenant.PlanID = &planID
			} else if plan, err := a.store.DefaultPlan(ctx); err == nil {
				tenant.PlanID = &plan.ID
			}

			if err := a.store.CreateTenant(ctx, sys, tenant); err != nil {
				return err
			}

			fmt.Printf("Mandant %s angelegt (id %d, slug %s).\n", tenant.Name, tenant.ID, tenant.Slug)
			if tenant.PlanID != nil {
				if plan, err := a.store.GetPlan(ctx, sys, *tenant.PlanID); err == nil {
					fmt.Printf("  Paket: %s\n", plan.Name)
				}
			} else {
				fmt.Println("  Kein Paket zugeordnet — es gelten keine Grenzen.")
			}
			fmt.Printf("\nBenutzer anlegen: volt user add kunde@example.at --role customer --tenant %d\n", tenant.ID)
			return nil
		}),
	}

	cmd.Flags().StringVar(&slug, "slug", "", "Kurzname (Standard: aus dem Namen abgeleitet)")
	cmd.Flags().Int64Var(&planID, "plan", 0, "Paket-ID (Standard: das als Standard markierte Paket)")
	return cmd
}

func (a *app) tenantSetPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-plan <tenant-id> <plan-id>",
		Short: "Ordnet einem Mandanten ein Paket zu (0 = keines)",
		Args:  cobra.ExactArgs(2),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			tenantID, err := parseID(args[0])
			if err != nil {
				return err
			}
			tenant, err := a.store.GetTenant(ctx, sys, tenantID)
			if err != nil {
				return err
			}

			if args[1] == "0" {
				tenant.PlanID = nil
			} else {
				planID, err := parseID(args[1])
				if err != nil {
					return err
				}
				if _, err := a.store.GetPlan(ctx, sys, planID); err != nil {
					return fmt.Errorf("paket %d: %w", planID, err)
				}
				tenant.PlanID = &planID
			}

			if err := a.store.UpdateTenant(ctx, sys, tenant); err != nil {
				return err
			}
			if tenant.PlanID == nil {
				fmt.Printf("Mandant %s hat jetzt kein Paket — es gelten keine Grenzen.\n", tenant.Name)
			} else {
				fmt.Printf("Mandant %s hat jetzt Paket %d.\n", tenant.Name, *tenant.PlanID)
			}
			return nil
		}),
	}
}

func (a *app) tenantSuspendCmd() *cobra.Command {
	var resume bool

	cmd := &cobra.Command{
		Use:   "suspend <tenant-id>",
		Short: "Sperrt einen Mandanten (oder hebt die Sperre mit --resume auf)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			tenant, err := a.store.GetTenant(ctx, sys, id)
			if err != nil {
				return err
			}

			tenant.Status = "suspended"
			if resume {
				tenant.Status = "active"
			}
			if err := a.store.UpdateTenant(ctx, sys, tenant); err != nil {
				return err
			}

			fmt.Printf("Mandant %s ist jetzt %s.\n", tenant.Name, tenant.Status)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&resume, "resume", false, "Sperre aufheben")
	return cmd
}

func (a *app) tenantUsageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usage [tenant-id]",
		Short: "Zeigt Verbrauch und Grenzen; ohne Argument für alle Mandanten",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()
			quota := core.NewQuotaService(a.store, a.agent, a.cfg, a.log)

			var tenants []*store.Tenant
			if len(args) == 1 {
				id, err := parseID(args[0])
				if err != nil {
					return err
				}
				tenant, err := a.store.GetTenant(ctx, sys, id)
				if err != nil {
					return err
				}
				tenants = []*store.Tenant{tenant}
			} else {
				var err error
				if tenants, err = a.store.ListTenants(ctx, sys); err != nil {
					return err
				}
			}

			for _, tenant := range tenants {
				status, err := quota.Status(ctx, sys, tenant.ID)
				if err != nil {
					return err
				}

				fmt.Printf("\n%s (%s) — Paket: %s\n", tenant.Name, tenant.Slug, status.PlanName)
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				for _, e := range status.Entries {
					used, limit := fmt.Sprint(e.Used), "unbegrenzt"
					if e.Bytes {
						used = humanBytes(e.Used)
					}
					if e.Limit > 0 {
						limit = fmt.Sprint(e.Limit)
						if e.Bytes {
							limit = humanBytes(e.Limit)
						}
						limit = fmt.Sprintf("%s (%.0f%%)", limit, e.Percent)
					}
					fmt.Fprintf(w, "  %s\t%s\t%s\n", e.Resource, used, limit)
				}
				if err := w.Flush(); err != nil {
					return err
				}
			}
			return nil
		}),
	}
}

// slugify bildet aus einem Namen einen Kurznamen.
func slugify(name string) string {
	slug := reNonAlnumCLI.ReplaceAllString(lower(name), "-")
	return trimDashes(slug)
}
