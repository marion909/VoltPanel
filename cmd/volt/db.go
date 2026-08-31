package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) dbCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "db", Short: "Datenbanken verwalten"}
	cmd.AddCommand(a.dbListCmd(), a.dbAddCmd(), a.dbRemoveCmd(), a.dbDumpCmd(), a.dbPasswdCmd())
	return cmd
}

func (a *app) dbService() *core.DatabaseService {
	return core.NewDatabaseService(a.store, a.agent, a.cfg, a.secrets)
}

func (a *app) dbListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Listet alle Datenbanken mit Größe und Benutzern",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			// Die Größen kommen live vom Server; ein nicht laufendes MariaDB
			// soll die Liste aber nicht verhindern.
			if err := a.dbService().SyncSizes(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "hinweis: größen nicht abrufbar (%v)\n", err)
			}

			dbs, err := a.store.ListDatabases(ctx, sys)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tGRÖSSE\tTENANT\tBENUTZER")
			for _, db := range dbs {
				users, _ := a.store.ListDBUsers(ctx, sys, db.ID)
				names := make([]string, 0, len(users))
				for _, u := range users {
					names = append(names, u.Username+"("+u.Grants+")")
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\n", db.ID, db.Name,
					humanBytes(db.SizeBytes), db.TenantID, orDash(joinComma(names)))
			}
			return w.Flush()
		}),
	}
}

func (a *app) dbAddCmd() *cobra.Command {
	var (
		tenantID int64
		user     string
		password string
		noUser   bool
	)

	cmd := &cobra.Command{
		Use:     "add <name>",
		Short:   "Legt eine Datenbank samt Benutzer an",
		Args:    cobra.ExactArgs(1),
		Example: "  volt db add wordpress --tenant 4",
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			res, err := a.dbService().CreateDatabase(cmd.Context(), store.SystemScope(),
				core.CreateDatabaseInput{
					Name: args[0], TenantID: tenantID, WithUser: !noUser,
					Username: user, Password: password,
				})
			if err != nil {
				return err
			}

			fmt.Printf("Datenbank angelegt: %s\n", res.Database.Name)
			if res.User != nil {
				fmt.Printf("  Benutzer: %s@%s\n", res.User.Username, res.User.HostPattern)
				fmt.Printf("  Passwort: %s\n", res.Password)
				fmt.Println("\nDas Passwort steht danach nur noch verschlüsselt in der Panel-Datenbank.")
			}
			return nil
		}),
	}

	cmd.Flags().Int64Var(&tenantID, "tenant", 1, "Tenant-ID")
	cmd.Flags().StringVar(&user, "user", "", "Benutzername (Standard: wie die Datenbank)")
	cmd.Flags().StringVar(&password, "password", "", "Passwort (leer = wird erzeugt)")
	cmd.Flags().BoolVar(&noUser, "no-user", false, "Nur die Datenbank anlegen, ohne Benutzer")
	return cmd
}

func (a *app) dbRemoveCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Entfernt eine Datenbank samt zugehöriger Benutzer",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			db, err := a.findDatabase(ctx, args[0])
			if err != nil {
				return err
			}
			if !yes && !confirm(fmt.Sprintf(
				"Datenbank %s (%s) mit allen Daten unwiderruflich löschen?",
				db.Name, humanBytes(db.SizeBytes))) {
				fmt.Println("Abgebrochen.")
				return nil
			}

			if err := a.dbService().DeleteDatabase(ctx, sys, db.ID); err != nil {
				return err
			}
			fmt.Printf("Datenbank %s entfernt.\n", db.Name)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Nicht nachfragen")
	return cmd
}

func (a *app) dbDumpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dump <name>",
		Short: "Schreibt einen SQL-Dump ins Backup-Verzeichnis",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, err := a.findDatabase(ctx, args[0])
			if err != nil {
				return err
			}

			path, size, err := a.dbService().Dump(ctx, store.SystemScope(), db.ID)
			if err != nil {
				return err
			}
			fmt.Printf("Dump geschrieben: %s (%s)\n", path, humanBytes(size))
			return nil
		}),
	}
}

func (a *app) dbPasswdCmd() *cobra.Command {
	var password string

	cmd := &cobra.Command{
		Use:   "passwd <benutzername>",
		Short: "Setzt das Passwort eines Datenbankbenutzers neu",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx, sys := cmd.Context(), store.SystemScope()

			users, err := a.store.ListDBUsers(ctx, sys, 0)
			if err != nil {
				return err
			}
			for _, u := range users {
				if u.Username != args[0] {
					continue
				}
				plain, err := a.dbService().SetPassword(ctx, sys, u.ID, password)
				if err != nil {
					return err
				}
				fmt.Printf("Passwort für %s@%s gesetzt: %s\n", u.Username, u.HostPattern, plain)
				return nil
			}
			return fmt.Errorf("datenbankbenutzer %q nicht gefunden", args[0])
		}),
	}
	cmd.Flags().StringVar(&password, "password", "", "Neues Passwort (leer = wird erzeugt)")
	return cmd
}

// findDatabase sucht anhand des Namens, damit die CLI ohne IDs auskommt.
func (a *app) findDatabase(ctx context.Context, name string) (*store.Database, error) {
	dbs, err := a.store.ListDatabases(ctx, store.SystemScope())
	if err != nil {
		return nil, err
	}
	for _, db := range dbs {
		if db.Name == name {
			return db, nil
		}
	}
	return nil, fmt.Errorf("datenbank %q nicht gefunden", name)
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
