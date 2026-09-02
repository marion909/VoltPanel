package templates

import (
	"strings"
	"testing"
)

func gueltigeMailData() MailData {
	return MailData{
		GeneratedAt: "2026-09-02T12:00:00+02:00",
		MailRoot:    "/var/vmail",
		VMailUID:    5000,
		VMailGID:    5000,
		Domains:     []string{"example.at"},
		Mailboxes: []MailboxEntry{{
			Address: "post@example.at",
			Hash:    "{SSHA512}abcDEF123+/=",
			QuotaMB: 1024,
			Maildir: "example.at/post",
		}},
		Aliases: []AliasEntry{{Source: "info@example.at", Destination: "post@example.at"}},
	}
}

// Eine Map ist zeilenweise aufgebaut. Was einen Zeilenumbruch in einen Wert
// bekommt, schreibt die nächste Zuordnung selbst — und die kann fremde Post
// abfangen.
func TestMailMapNimmtKeineZweiteZeile(t *testing.T) {
	faelle := map[string]func(*MailData){
		"umbruch in der adresse": func(d *MailData) {
			d.Mailboxes[0].Address = "post@example.at\nroot@fremde.at"
		},
		"leerzeichen in der adresse": func(d *MailData) {
			d.Mailboxes[0].Address = "post@example.at root@fremde.at"
		},
		"umbruch in der domäne": func(d *MailData) {
			d.Domains = []string{"example.at\nfremde.at"}
		},
		"umbruch im alias": func(d *MailData) {
			d.Aliases[0].Source = "info@example.at\nroot@fremde.at"
		},
		"umbruch im ziel": func(d *MailData) {
			d.Aliases[0].Destination = "post@example.at\n@fremde.at"
		},
		"doppelpunkt im hash": func(d *MailData) {
			// Der Doppelpunkt trennt in der Dovecot-Datei die Felder.
			d.Mailboxes[0].Hash = "{SSHA512}abc:def:0:0::/etc::"
		},
		"leerer hash": func(d *MailData) {
			d.Mailboxes[0].Hash = ""
		},
		"maildir mit ..": func(d *MailData) {
			d.Mailboxes[0].Maildir = "../../etc"
		},
		"maildir absolut": func(d *MailData) {
			d.Mailboxes[0].Maildir = "/etc/passwd"
		},
	}

	for name, kaputt := range faelle {
		t.Run(name, func(t *testing.T) {
			d := gueltigeMailData()
			kaputt(&d)
			for _, render := range []func(MailData) (string, error){
				RenderPostfixDomains, RenderPostfixMailboxes,
				RenderPostfixAliases, RenderDovecotUsers,
			} {
				if _, err := render(d); err == nil {
					t.Error("wurde in eine map geschrieben")
				}
			}
		})
	}
}

// Der gültige Fall muss durchgehen, sonst prüft der Test oben nur, dass alles
// abgelehnt wird.
func TestMailMapSchreibtDenGueltigenFall(t *testing.T) {
	d := gueltigeMailData()

	dom, err := RenderPostfixDomains(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dom, "example.at\tOK") {
		t.Errorf("die domäne fehlt:\n%s", dom)
	}

	boxen, err := RenderPostfixMailboxes(d)
	if err != nil {
		t.Fatal(err)
	}
	// Der abschließende Schrägstrich sagt Postfix "Maildir". Ohne ihn landet
	// alles in einer einzigen Datei.
	if !strings.Contains(boxen, "post@example.at\texample.at/post/") {
		t.Errorf("das maildir steht falsch da:\n%s", boxen)
	}

	users, err := RenderDovecotUsers(d)
	if err != nil {
		t.Fatal(err)
	}
	will := "post@example.at:{SSHA512}abcDEF123+/=:5000:5000::/var/vmail/example.at/post::" +
		"userdb_quota_rule=*:storage=1024M"
	if !strings.Contains(users, will) {
		t.Errorf("die zeile für dovecot stimmt nicht:\n%s\nerwartet:\n%s", users, will)
	}

	// 0 heißt im Panel "unbegrenzt". Eine Regel mit 0M wäre in Dovecot das
	// Gegenteil: gar kein Platz.
	d.Mailboxes[0].QuotaMB = 0
	users, err = RenderDovecotUsers(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(users, "storage=0M") {
		t.Errorf("aus unbegrenzt wurde kein platz:\n%s", users)
	}
	if strings.Contains(users, "quota_rule") {
		t.Errorf("ohne quota steht trotzdem eine regel da:\n%s", users)
	}
}

// Ein Catch-All ist "@domain" — die einzige Quelle, die kein @ vorne hat.
func TestCatchAllIstErlaubt(t *testing.T) {
	d := gueltigeMailData()
	d.Aliases = []AliasEntry{{Source: "@example.at", Destination: "post@example.at"}}
	out, err := RenderPostfixAliases(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "@example.at\tpost@example.at") {
		t.Errorf("der catch-all fehlt:\n%s", out)
	}

	// Aber "@" allein oder "@nicht-eine-domäne" nicht.
	for _, quelle := range []string{"@", "@example", "@ example.at", "@example.at\nx"} {
		d.Aliases = []AliasEntry{{Source: quelle, Destination: "post@example.at"}}
		if _, err := RenderPostfixAliases(d); err == nil {
			t.Errorf("%q wurde als catch-all angenommen", quelle)
		}
	}
}

// Die Maildirs gehören einem eigenen unprivilegierten Benutzer. Nicht root,
// und kein Systemkonto.
func TestMaildirsGehoerenKeinemSystemkonto(t *testing.T) {
	for _, uid := range []int{0, 1, 33, 999} {
		d := gueltigeMailData()
		d.VMailUID, d.VMailGID = uid, uid
		if _, err := RenderDovecotUsers(d); err == nil {
			t.Errorf("uid %d wurde angenommen", uid)
		}
	}
}

// Ohne diese Datei kennt Dovecot die Passwortdatei nicht, die das Panel
// schreibt: die Postfächer stünden da, und niemand käme herein.
func TestDovecotConfNenntDiePasswortdatei(t *testing.T) {
	out, err := RenderDovecotConf(DovecotData{
		GeneratedAt: "2026-09-02T12:00:00+02:00",
		MailRoot:    "/var/vmail",
		UsersFile:   "/etc/dovecot/volt/users",
		VMailUID:    5000,
		VMailGID:    5000,
		CertPath:    "/var/lib/volt/certs/panel/fullchain.pem",
		KeyPath:     "/var/lib/volt/certs/panel/privkey.pem",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, will := range []string{
		"args = scheme=SSHA512 username_format=%u /etc/dovecot/volt/users",
		"mail_uid = 5000",
		"first_valid_uid = 5000",
		"ssl_cert = </var/lib/volt/certs/panel/fullchain.pem",
		"ssl_min_protocol = TLSv1.2",
		"/var/spool/postfix/private/auth",
	} {
		if !strings.Contains(out, will) {
			t.Errorf("%q fehlt:\n%s", will, out)
		}
	}

	// scheme=SSHA512 ist die Zeile, die zählt: ohne sie liest Dovecot das
	// Feld als Klartext, und dann kommt niemand mehr herein.
	if !strings.Contains(out, "scheme=SSHA512") {
		t.Error("ohne scheme= liest dovecot den hash als klartext")
	}
}

// Ein Zertifikat ohne Schlüssel ergibt eine Konfiguration, mit der Dovecot
// nicht startet — und dann kommt niemand mehr an seine Post.
func TestDovecotConfLehntHalbesZertifikatAb(t *testing.T) {
	basis := DovecotData{
		GeneratedAt: "x", MailRoot: "/var/vmail",
		UsersFile: "/etc/dovecot/volt/users", VMailUID: 5000, VMailGID: 5000,
	}
	nurCert := basis
	nurCert.CertPath = "/a/fullchain.pem"
	if _, err := RenderDovecotConf(nurCert); err == nil {
		t.Error("ein zertifikat ohne schlüssel wurde angenommen")
	}
	nurKey := basis
	nurKey.KeyPath = "/a/privkey.pem"
	if _, err := RenderDovecotConf(nurKey); err == nil {
		t.Error("ein schlüssel ohne zertifikat wurde angenommen")
	}

	// Gar keines ist in Ordnung — dann läuft Dovecot mit dem des Pakets.
	out, err := RenderDovecotConf(basis)
	if err != nil {
		t.Fatalf("ohne zertifikat: %v", err)
	}
	if strings.Contains(out, "ssl_cert") {
		t.Error("ohne zertifikat steht trotzdem ssl_cert in der datei")
	}

	// Und ein Pfad mit Zeilenumbruch schreibt die nächste Direktive.
	kaputt := basis
	kaputt.CertPath = "/a/fullchain.pem\nssl_key = </etc/shadow"
	kaputt.KeyPath = "/a/privkey.pem"
	if _, err := RenderDovecotConf(kaputt); err == nil {
		t.Error("ein pfad mit umbruch wurde angenommen")
	}
}
