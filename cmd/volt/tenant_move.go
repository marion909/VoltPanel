package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/marion909/voltpanel/internal/core"
	"github.com/marion909/voltpanel/internal/store"
)

// Einen Mandanten umziehen.
//
// Auf der Kommandozeile und nicht im Panel: ein Umzug betrifft zwei Server, und
// auf mindestens einem davon sitzt ohnehin jemand auf einer Shell. Der Export
// ist zusätzlich über das Panel abrufbar; das Einspielen nicht — eine Datei mit
// den Passwörtern eines ganzen Mandanten durch einen Browser zu schicken, um
// sie danach auf dem Server zu suchen, macht den Weg länger und nicht sicherer.

func (a *app) tenantExportCmd() *cobra.Command {
	var passphrase string

	cmd := &cobra.Command{
		Use:   "export <id>",
		Short: "Packt einen Mandanten in ein Bündel zum Umziehen",
		Long: "Schreibt Zeilen, Dateien und Datenbankauszüge eines Mandanten in ein " +
			"Archiv.\n\nDie Geheimnisse darin — FTP- und Datenbankpasswörter, " +
			"TOTP-Secrets, Tokens — werden auf die Passphrase umgeschlüsselt. Ohne " +
			"sie lässt sich das Bündel zwar auspacken, aber kein Zugang daraus " +
			"wiederherstellen.",
		Args: cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			pass, err := readPassphrase(passphrase, true)
			if err != nil {
				return err
			}

			svc := core.NewExportService(a.cfg, a.store, a.agent, a.secrets, a.log)
			res, err := svc.ExportTenant(cmd.Context(), store.SystemScope(), id, pass)
			if err != nil {
				return err
			}

			fmt.Printf("Bündel:      %s\n", res.Path)
			fmt.Printf("Größe:       %.1f MB\n", float64(res.SizeBytes)/(1<<20))
			fmt.Printf("Prüfsumme:   %s\n", res.Checksum)
			fmt.Printf("Sites:       %d\n", res.Sites)
			fmt.Printf("Datenbanken: %d\n", res.Databases)
			for _, w := range res.Warnings {
				fmt.Fprintf(os.Stderr, "Hinweis: %s\n", w)
			}
			fmt.Println()
			fmt.Println("Das Bündel enthält die Dateien und Datenbankauszüge im Klartext.")
			fmt.Println("Die Passphrase schützt die Zugangsdaten darin, nicht die Daten selbst —")
			fmt.Println("das Archiv gehört so behandelt wie ein Backup.")
			return nil
		}),
	}
	cmd.Flags().StringVar(&passphrase, "passphrase", "",
		"Passphrase für die Geheimnisse (ohne Angabe wird nachgefragt)")
	return cmd
}

func (a *app) tenantImportCmd() *cobra.Command {
	var passphrase string
	var nurAnsehen bool

	cmd := &cobra.Command{
		Use:   "import <bündel.tar.gz>",
		Short: "Spielt einen Mandanten aus einem Bündel ein",
		Long: "Legt den Mandanten auf diesem Server neu an: Zeilen, Dateien, " +
			"Datenbanken — und danach, was der Server selbst führt: Linux-Benutzer, " +
			"Vhosts, FPM-Pools, Cron-Dateien, FTP-Zugänge und Units.\n\nEin " +
			"vorhandener Mandant mit demselben Slug wird nicht überschrieben — ein " +
			"halb überschriebener wäre schlimmer als gar keiner.\n\nWas dabei nicht " +
			"gelingt, steht als Hinweis am Ende und bricht den Import nicht ab: die " +
			"Daten liegen dann schon, und ein Abbruch ließe einen halben Mandanten " +
			"zurück.",
		Args: cobra.ExactArgs(1),
		RunE: a.withApp(false, func(cmd *cobra.Command, args []string) error {
			pass, err := readPassphrase(passphrase, false)
			if err != nil {
				return err
			}

			svc := core.NewExportService(a.cfg, a.store, a.agent, a.secrets, a.log)

			if nurAnsehen {
				bundle, _, err := core.OpenBundle(args[0], pass)
				if err != nil {
					return err
				}
				fmt.Printf("Mandant:     %s (%s)\n", bundle.Tenant.Name, bundle.Tenant.Slug)
				fmt.Printf("Von:         %s\n", orElse(bundle.Source, "unbekannt"))
				fmt.Printf("VoltPanel:   %s, Schema %d\n", bundle.Version, bundle.Schema)
				fmt.Printf("Sites:       %d\n", len(bundle.Sites))
				fmt.Printf("Benutzer:    %d\n", len(bundle.Users))
				fmt.Printf("Datenbanken: %d\n", len(bundle.Databases))
				return nil
			}

			res, err := svc.ImportTenant(cmd.Context(), args[0], pass)
			if err != nil {
				return err
			}
			fmt.Printf("Mandant %s angelegt (id %d)\n", res.Slug, res.TenantID)
			fmt.Printf("Sites %d, Benutzer %d, Datenbanken %d, Cronjobs %d, Apps %d\n",
				res.Sites, res.Users, res.Databases, res.Cronjobs, res.Apps)
			fmt.Printf("Auf diesem Server hergestellt: %d von %d Sites\n",
				res.Rebuilt, res.Sites)
			for _, w := range res.Warnings {
				fmt.Fprintf(os.Stderr, "Hinweis: %s\n", w)
			}
			fmt.Println()
			if res.Rebuilt < res.Sites {
				fmt.Println("Was oben als Hinweis steht, wiederholt `volt site rebuild <domain>`.")
			}
			fmt.Println("Zertifikate holt `volt cert issue <domain>`, sobald der DNS-Eintrag")
			fmt.Println("hierher zeigt — vorher hat ACME nichts, woran es die Domain erkennt.")
			return nil
		}),
	}
	cmd.Flags().StringVar(&passphrase, "passphrase", "",
		"Passphrase des Bündels (ohne Angabe wird nachgefragt)")
	cmd.Flags().BoolVar(&nurAnsehen, "show", false,
		"Nur ansehen, was im Bündel steckt — nichts einspielen")
	return cmd
}

// readPassphrase fragt die Passphrase ab, wenn keine übergeben wurde.
//
// Ohne Echo und über das Terminal, nicht über ein Flag: ein Flag stünde in der
// Prozessliste und in der Shell-Historie. Angeben lässt es sich trotzdem — für
// ein Skript, das die Passphrase aus einem Tresor holt.
func readPassphrase(vorgabe string, zweimal bool) (string, error) {
	if vorgabe != "" {
		return vorgabe, nil
	}
	if !term.IsTerminal(int(syscall.Stdin)) {
		return "", errors.New("keine passphrase angegeben und kein terminal zum nachfragen")
	}

	fmt.Fprint(os.Stderr, "Passphrase: ")
	erste, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if !zweimal {
		return string(erste), nil
	}

	fmt.Fprint(os.Stderr, "Wiederholen: ")
	zweite, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(erste) != string(zweite) {
		return "", errors.New("die beiden eingaben stimmen nicht überein")
	}
	return string(erste), nil
}

func orElse(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
