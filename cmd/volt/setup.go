package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/spf13/cobra"
)

// setupCmd legt den ersten Tenant und den ersten Owner an. install.sh ruft das
// mit --print-password auf und zeigt das Ergebnis am Ende der Installation.
func (a *app) setupCmd() *cobra.Command {
	var (
		email      string
		password   string
		tenantName string
		printPW    bool
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Richtet den ersten Tenant und Owner ein",
		RunE: a.withApp(true, func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			count, err := a.store.CountUsers(ctx)
			if err != nil {
				return err
			}
			if count > 0 {
				return errors.New("es existiert bereits ein benutzer — `volt user add` legt weitere an")
			}

			if email == "" {
				if email, err = prompt("E-Mail für den Owner: "); err != nil {
					return err
				}
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

			sys := store.SystemScope()
			tenant := &store.Tenant{Name: tenantName, Slug: "system"}
			if err := a.store.CreateTenant(ctx, sys, tenant); err != nil {
				return err
			}

			hash, err := authn.HashPassword(password)
			if err != nil {
				return err
			}
			user := &store.User{
				TenantID: tenant.ID, Email: email, DisplayName: "Owner",
				PasswordHash: hash, Role: store.RoleOwner,
				// Ein generiertes Passwort steht im Terminal-Verlauf und im
				// Installationsprotokoll — es muss beim ersten Login weg.
				MustChangePW: generated,
			}
			if err := a.store.CreateUser(ctx, sys, user); err != nil {
				return err
			}

			_ = a.store.Log(ctx, &store.AuditEntry{
				TenantID: &tenant.ID, UserID: &user.ID, Actor: "cli",
				Action: "setup.complete", TargetType: "user", TargetID: email,
			})

			fmt.Printf("\nOwner angelegt: %s\n", email)
			if printPW || generated {
				fmt.Printf("Passwort:       %s\n", password)
				fmt.Println("\nBitte sofort nach dem ersten Login ändern.")
			}
			return nil
		}),
	}

	cmd.Flags().StringVar(&email, "email", "", "E-Mail des Owners")
	cmd.Flags().StringVar(&password, "password", "", "Passwort (leer = wird erzeugt)")
	cmd.Flags().StringVar(&tenantName, "tenant", "System", "Name des ersten Tenants")
	cmd.Flags().BoolVar(&printPW, "print-password", false, "Passwort im Klartext ausgeben")
	return cmd
}

func prompt(label string) (string, error) {
	fmt.Print(label)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", errors.New("eingabe darf nicht leer sein")
	}
	return value, nil
}

// confirm fragt vor unumkehrbaren Aktionen nach.
func confirm(question string) bool {
	fmt.Printf("%s [j/N]: ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "j", "ja", "y", "yes":
		return true
	}
	return false
}
