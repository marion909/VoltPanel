package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) userCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Panel-Benutzer verwalten"}
	cmd.AddCommand(a.userListCmd(), a.userAddCmd(), a.userPasswdCmd(), a.user2FAResetCmd())
	return cmd
}

func (a *app) userListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Listet alle Benutzer",
		RunE: a.withApp(false, func(cmd *cobra.Command, _ []string) error {
			users, err := a.store.ListUsers(cmd.Context(), store.SystemScope())
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tE-MAIL\tROLLE\tTENANT\t2FA\tSTATUS\tLETZTER LOGIN")
			for _, u := range users {
				last := "nie"
				if u.LastLoginAt != nil {
					last = time.Unix(*u.LastLoginAt, 0).Format("2006-01-02 15:04")
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\t%s\n", u.ID, u.Email, u.Role,
					u.TenantID, onOff(u.TOTPEnabled, "an", "aus"), u.Status, last)
			}
			return w.Flush()
		}),
	}
}

func (a *app) userAddCmd() *cobra.Command {
	var (
		role     string
		tenantID int64
		password string
	)

	cmd := &cobra.Command{
		Use:   "add <email>",
		Short: "Legt einen Benutzer an",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			r := store.Role(role)
			if !r.Valid() {
				return fmt.Errorf("rolle %q ist unbekannt (owner, admin, reseller, customer)", role)
			}

			generated := false
			var err error
			if password == "" {
				if password, err = authn.GeneratePassword(20); err != nil {
					return err
				}
				generated = true
			}
			if err := authn.DefaultPolicy().Check(password); err != nil {
				return err
			}
			hash, err := authn.HashPassword(password)
			if err != nil {
				return err
			}

			user := &store.User{
				TenantID: tenantID, Email: args[0], PasswordHash: hash,
				Role: r, MustChangePW: generated,
			}
			if err := a.store.CreateUser(cmd.Context(), store.SystemScope(), user); err != nil {
				return err
			}

			fmt.Printf("Benutzer %s angelegt (id %d, rolle %s, tenant %d)\n",
				user.Email, user.ID, user.Role, user.TenantID)
			if generated {
				fmt.Printf("Passwort: %s\n", password)
			}
			return nil
		}),
	}

	cmd.Flags().StringVar(&role, "role", "customer", "owner | admin | reseller | customer")
	cmd.Flags().Int64Var(&tenantID, "tenant", 1, "Tenant-ID")
	cmd.Flags().StringVar(&password, "password", "", "Passwort (leer = wird erzeugt)")
	return cmd
}

func (a *app) userPasswdCmd() *cobra.Command {
	var password string

	cmd := &cobra.Command{
		Use:   "passwd <email>",
		Short: "Setzt das Passwort eines Benutzers neu",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			user, err := a.store.UserByEmail(ctx, args[0])
			if err != nil {
				return err
			}

			generated := false
			if password == "" {
				if password, err = authn.GeneratePassword(20); err != nil {
					return err
				}
				generated = true
			}
			if err := authn.DefaultPolicy().Check(password); err != nil {
				return err
			}
			if user.PasswordHash, err = authn.HashPassword(password); err != nil {
				return err
			}
			user.MustChangePW = generated

			if err := a.store.UpdateUser(ctx, store.SystemScope(), user); err != nil {
				return err
			}
			// Ein zurückgesetztes Passwort muss bestehende Sitzungen beenden,
			// sonst bleibt ein Angreifer trotz Reset eingeloggt.
			if err := a.store.DeleteUserSessions(ctx, user.ID); err != nil {
				return err
			}

			fmt.Printf("Passwort für %s gesetzt, alle Sitzungen beendet.\n", user.Email)
			if generated {
				fmt.Printf("Passwort: %s\n", password)
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&password, "password", "", "Neues Passwort (leer = wird erzeugt)")
	return cmd
}

// user2FAResetCmd ist der Notausgang, wenn jemand sein Authenticator-Gerät
// verloren hat. Nur lokal auf dem Server auslösbar.
func (a *app) user2FAResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "2fa-reset <email>",
		Short: "Schaltet die Zwei-Faktor-Anmeldung eines Benutzers ab",
		Args:  cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			user, err := a.store.UserByEmail(ctx, args[0])
			if err != nil {
				return err
			}
			if !user.TOTPEnabled {
				fmt.Printf("2FA war für %s nicht aktiv.\n", user.Email)
				return nil
			}
			if !confirm(fmt.Sprintf("2FA für %s wirklich abschalten?", user.Email)) {
				fmt.Println("Abgebrochen.")
				return nil
			}

			user.TOTPEnabled, user.TOTPSecret = false, ""
			if err := a.store.UpdateUser(ctx, store.SystemScope(), user); err != nil {
				return err
			}
			if err := a.store.DeleteUserSessions(ctx, user.ID); err != nil {
				return err
			}

			_ = a.store.Log(ctx, &store.AuditEntry{
				TenantID: &user.TenantID, UserID: &user.ID, Actor: "cli",
				Action: "auth.2fa_reset", TargetType: "user", TargetID: user.Email,
			})
			fmt.Printf("2FA für %s abgeschaltet, alle Sitzungen beendet.\n", user.Email)
			return nil
		}),
	}
}
