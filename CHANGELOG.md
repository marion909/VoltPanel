# Changelog

Was sich zwischen den Fassungen geändert hat.

Die Einträge sind die Commit-Überschriften, unverändert. Das ist Absicht: hinter
jeder steht eine ausführliche Begründung im Commit selbst, und wer wissen will,
*warum* etwas so ist, findet es dort — nicht in einer zweiten, geglätteten
Fassung, die davon abweicht.

Die Versionsnummern folgen [Semantic Versioning](https://semver.org/lang/de/) —
solange die 0 vorne steht mit der üblichen Einschränkung: bis zur 1.0 kann auch
eine Nebenversion etwas verlangen. Was das betrifft, steht unter „Achtung".

## Unveröffentlicht

- VerifyPassword las memory/time/threads ungeprüft aus dem gespeicherten
  Hash und reichte sie direkt an argon2.IDKey weiter — ein Hash mit
  überhöhten Parametern hätte bei jedem Verifizierungsversuch beliebigen
  Ressourcenverbrauch verursacht; jetzt mit Obergrenzen

## v0.4.61 — 2026-09-05

- GeneratePassword zog Zeichen per b%len(alphabet) — da 256 kein Vielfaches
  der 63-Zeichen-Alphabetlänge ist, hatten die ersten vier Zeichen eine
  leicht höhere Trefferwahrscheinlichkeit; jetzt per Rejection-Sampling
  unverzerrt

## v0.4.60 — 2026-09-05

- Store: UpdateMailDomain/UpdateMailbox sowie DeleteMailDomain/DeleteMailbox/
  DeleteMailAlias prüften nach dem UPDATE/DELETE nirgends RowsAffected — ein
  Zugriff auf eine fremde/gelöschte ID kam bisher als Erfolg zurück

## v0.4.59 — 2026-09-05

- DeployService.RunAsync reservierte die Laufend-Sperre nur lesend
  (istLaufend) und ließ die tatsächliche Sperre erst die gestartete
  Goroutine setzen — zwei nahezu gleichzeitige Trigger (z. B. zwei
  Webhook-Zustellungen) konnten dadurch beide den Check passieren
- mehrere fehlgeschlagene RecordDeployRun-Aufrufe in DeployService.Run
  wurden bisher stillschweigend verworfen, ohne jedes Logging

## v0.4.58 — 2026-09-04

- SetGrants schrieb neue Rechte zuerst in den Store und erst danach an den
  MySQL-Server — schlug der Agent-Aufruf fehl, stand im Store bereits der
  neue Wert, obwohl er auf dem Server nie galt

## v0.4.57 — 2026-09-04

- acme.Issue behandelte jeden Store-Fehler beim Suchen eines bestehenden
  Zertifikats als "keins vorhanden" und legte im Fehlerfall einen zweiten,
  doppelten Datensatz an

## v0.4.56 — 2026-09-04

- CreateDatabase und Zertifikatsausstellung prüften eine mitgegebene site_id
  nicht gegen den Mandanten der Anfrage — Defense-in-Depth auf Service-Ebene,
  ergänzend zur bereits gehärteten Store-Ebene

## v0.4.55 — 2026-09-04

- Agent-Protokoll: ein neuer json.Decoder pro Anfrage konnte bereits gepufferte
  Bytes einer unmittelbar nachfolgenden Anfrage verlieren — jetzt ein Decoder
  über die ganze Verbindung, mit zurücksetzbarem Größenlimit je Anfrage

## v0.4.54 — 2026-09-04

- agent.Client: lang laufende Operationen (Update, Paket-/WordPress-/Webmail-
  Installation) blockierten über die gemeinsame Verbindung jeden anderen
  Agent-Aufruf im Panel — laufen jetzt über eine eigene zweite Verbindung

## v0.4.53 — 2026-09-04

- Web-Terminal: zwei Race Conditions behoben — mehr als maxTerminals
  gleichzeitige Sitzungen möglich, und resize griff ohne Sperre auf ptmx zu

## v0.4.52 — 2026-09-04

- checkDomain gibt den kleingeschriebenen Domainnamen jetzt zurück — sonst
  könnten "Example.com"/"example.com" auf ext4/XFS zwei getrennte Vhost-/Log-Dateien treffen

## v0.4.51 — 2026-09-04

- mail.setup: drei os.Chown-Fehler (Dovecot-Passwortdatei, DKIM-Schlüssel)
  wurden bisher verschluckt — genau der Fehler, der früher schon einmal zu ~2s
  Anmeldeverzögerung führte, wäre so unbemerkt geblieben

## v0.4.50 — 2026-09-04

- file.copy baute Symlinks 1:1 nach, statt sie wie beim Archiv-Entpacken zu
  überspringen — ein nach außen zeigender Symlink ließ sich damit duplizieren

## v0.4.49 — 2026-09-04

- Datei-Manager: die Zielgruppe von chown/write/mkdir war ungeprüft (root,
  mysql, …); dabei kam ans Licht, dass die alte Sperre "root" auch als
  Eigentümer traf und interne App-/htpasswd-Schreibvorgänge brach

## v0.4.48 — 2026-09-04

- Git-Deploy: Schlüsselverzeichnis mit 0750 statt 0751 angelegt — die
  Site-UID konnte es nicht betreten, SSH-Deploys scheiterten grundsätzlich mit "Permission denied"

## v0.4.47 — 2026-09-04

- Cronjobs: RunAs ließ jeden existierenden Benutzer zu, nicht nur
  Site-Systembenutzer; ein unmaskiertes % im Kommando wurde von cron(8) als Zeilenumbruch gelesen

## v0.4.46 — 2026-09-04

- opMySQLDump: ein fehlgeschlagener Dump hinterließ bisher eine leere Datei
  statt des vorherigen Dumps — schreibt jetzt über Temp+Rename

## v0.4.45 — 2026-09-04

- MySQL: parallele Query-/Import-Wegwerf-Konten konnten sich gegenseitig
  wegräumen — dropStaleAccounts prüft jetzt einen im Namen eingebetteten Zeitstempel

## v0.4.44 — 2026-09-04

- internal/core/json.go: tote Funktion decodeJSON entfernt (per staticcheck
  bestätigt unbenutzt; update.go erzielt denselben Schutz längst direkt über io.LimitReader)

## v0.4.43 — 2026-09-04

- golang.org/x/crypto auf v0.56.0 angehoben — schließt zwei bisher nicht
  erreichbare DoS-Advisories in x/crypto/ssh präventiv

## v0.4.42 — 2026-09-04

- install.sh: systemd-Units luden bisher ohne Prüfsummenabgleich gegen
  latest.json — anders als die Binaries, die verify_manifest+download_verified durchlaufen

## v0.4.41 — 2026-09-04

- docs/sicherheit.md: "Was noch offen ist" behauptete zwei bereits erledigte
  Punkte (Web-Terminal, SSRF-Filterung) und dass die Release-Signatur
  ungeprüft bleibt — beides war seit Längerem überholt

## v0.4.40 — 2026-09-04

- Login-Ratelimiter: die fertige Cleanup()-Funktion wurde nirgends aufgerufen
  — die IP-Bucket-Map wuchs auf einem öffentlichen Panel unbegrenzt

## v0.4.39 — 2026-09-04

- Ein nach dem Binary-Tausch abgebrochenes Update wurde beim Retry fälschlich
  als "bereits aktuell" gemeldet; DB-Rollback/-Restore schreiben jetzt atomar

## v0.4.38 — 2026-09-04

- fail2ban/postfix/dovecot/opendkim/rspamd starteten nach der Installation
  nie von selbst; ein gescheiterter opendkim-Reload blieb dabei zusätzlich unsichtbar

## v0.4.37 — 2026-09-04

- TOTP: ein abgefangener 2FA-Code galt bis zu ~90s mehrfach (Login,
  2FA-Ein-/Ausschalten) — VerifyTOTP merkt sich jetzt den verbrauchten Zeitschritt

## v0.4.36 — 2026-09-04

- PHP-FPM: ExtraINI/DisableFunctions konnten per Zeilenumbruch einen neuen,
  unabhängigen Pool in derselben Datei eröffnen (z. B. mit user = root)

## v0.4.35 — 2026-09-04

- IP-Whitelist und Login-Ratelimit ließen sich über einen selbst gesetzten
  X-Forwarded-For-Header umgehen — Echo hatte keinen IPExtractor gesetzt

## v0.4.34 — 2026-09-04

- POST /apps/pull hatte als einzige Docker-Route keine Admin-Rollenprüfung —
  jeder angemeldete Nutzer konnte den Docker-Daemon des Hosts Images ziehen lassen

## v0.4.33 — 2026-09-04

- Store: zehn Create*-Funktionen prüften eine mitgegebene Fremd-ID (site_id,
  database_id, db_user_id, target_id) nie gegen den Mandanten der neuen Zeile

## v0.4.32 — 2026-09-04

- MySQL: mysql/information_schema/performance_schema/sys waren über die
  normale Datenbank-Verwaltung erreichbar (DROP DATABASE, GRANT ALL)

## v0.4.31 — 2026-09-04

- Datei-Manager und FTP-Home: ein Symlink in der eigenen Site auf eine fremde
  Site wurde bisher aufgelöst statt abgelehnt (Cross-Tenant-Datenzugriff)

## v0.4.30 — 2026-09-03

- mail.setup schaltet PAM als passdb ab — sonst verzögert jede IMAP-Anmeldung um ~2s

## v0.4.29 — 2026-09-03

- Dovecot 2.4: mail_inbox_path überschreibt Debians mbox-Vorgabe
- Dovecot 2.4: protocol lmtp setzt auth_username_format selbst

## v0.4.28 — 2026-09-03

- mail.setup setzt mydestination auf localhost — eingehende Post kam sonst nicht an

## v0.4.27 — 2026-09-03

- Dovecot liest die Passwortdatei jetzt wirklich als eigene Gruppe
- dovecot-lmtpd zur Nachinstallation von Dovecot hinzugefügt
- Dovecot 2.4: eigene Vorlage, denn die von 2.3 wird komplett verworfen
- Webmail: static.php-Adressen trafen die falsche Regel, keine Bilder, kein Stil

## v0.4.26 — 2026-09-03

- Webmail-Vhost zeigte auf die Archivwurzel statt auf public_html — ab
  Roundcube 1.6 nur noch die eingebaute Warnung statt der Anmeldung

## v0.4.25 — 2026-09-03

- Webmail-Installation: derselbe Fehlschlag-dann-Neuversuch traf als
  Nächstes auf das Datenbankschema — "table already exists"

## v0.4.24 — 2026-09-03

- Webmail-Installation: ein zweiter Versuch nach einem Fehlschlag scheiterte
  an den eigenen Resten des ersten — "SQL einsetzen: ... file exists"

## v0.4.23 — 2026-09-03

- Webmail: Roundcube als server-weite Installation, ein Klick auf der
  Plugins-Seite — Systembenutzer, Datenbank, PHP-Pool, Vhost und
  Zertifikat entstehen zusammen, ohne dass Webmail einem Mandanten gehört

## v0.4.22 — 2026-09-03

- Mail: Autoconfig für Thunderbird und Autodiscover für Outlook — ein Klick
  trägt Zertifikat, Vhost und DNS-Einträge ein

## v0.4.21 — 2026-09-03

- Mail: was Rspamd tatsächlich aussortiert, steht jetzt im Panel

## v0.4.20 — 2026-09-03

- App-Store: WordPress mit einem Klick — Site, Datenbank und der
  WordPress-Kern in einem Schritt, geprüft wie eine Node-Fassung

## v0.4.19 — 2026-09-03

- Phase 7 angefangen: fester Plugin-Katalog, Redis als erster Eintrag —
  installieren, ein-/ausschalten, entfernen, ohne offenes Repository für
  Fremdcode

## v0.4.18 — 2026-09-02

- Apps und Deploys optisch an Websites angleichen
- Docker installiert auf Debian 13 auch das Paket docker-cli

## v0.4.17 — 2026-09-02

- Docker-Installation meldet klar, ob der Daemon danach startklar ist
- Docker-Fehler ohne Ausgabe zeigen nicht mehr nur einen leeren Doppelpunkt

## v0.4.16 — 2026-09-02

- Datenbanken und SQL zusammenfassen

## v0.4.15 — 2026-09-02

- Websites, Apps und Deploys unter Frontend zusammenfassen

## v0.4.14 — 2026-09-02

- Docker-Warnung unterscheidet Installation und laufenden Daemon

## v0.4.13 — 2026-09-02

- Docker-Installation startet den Dienst
- Node-Warnung zählt eigene Node-Fassungen mit

## v0.4.12 — 2026-09-02

- OpenDKIM-Installation wartet lang genug

## v0.4.11 — 2026-09-02

- Proxy-Sites schreiben die gemeinsame Nginx-Config vor dem Vhost
- Mail zieht beim Mandanten-Export und -Import mit um
- Mail-Setup behandelt vmail nicht mehr wie einen Site-Benutzer
- Doku-Startseite für GitHub Pages

## v0.4.10 — 2026-09-02

- SPF, DKIM und DMARC über Cloudflare setzen — statt sie abzuschreiben

## v0.4.9 — 2026-09-02

- Zustellung über Dovecot, damit die Quota eines Postfachs überhaupt greift

## v0.4.8 — 2026-09-02

- Rspamd als zweiter Milter, und die Angaben fürs Mailprogramm im Panel

## v0.4.7 — 2026-09-02

- Zustellbarkeitsprüfung: PTR, MX, SPF, DKIM, DMARC, TLS, offenes Relay
- Fehlt ein Dienst, steht jetzt der Knopf daneben
- Mailports in der Firewall freigeben

## v0.4.6 — 2026-09-02

- Dovecot kennt die Postfächer, Postfix den Ausweis

## v0.4.5 — 2026-09-02

- DKIM: Schlüssel je Domäne, OpenDKIM-Tabellen und der DNS-Eintrag im Panel

## v0.4.4 — 2026-09-02

- Mail: Dienst, Maps und API
- Die Seite nahm die Signatur nie mit
- Mail in der Oberfläche

## v0.4.3 — 2026-09-02

- install.sh: VOLT_ALLOW_UNSIGNED wirkte nur im halben Fall

## v0.4.2 — 2026-09-02

- Phase 6: die Datenhaltung für Mail
- Der Release-Schlüssel steht

> **Achtung:** Ab hier trägt der Quelltext einen Release-Schlüssel. Ein Panel,
> das noch ohne gebaut wurde, lehnt jeden Kanal ab; einmal
> `update_allow_unsigned: true` in der config.yaml, aktualisieren, Flag wieder
> heraus.

## v0.4.1 — 2026-09-02

- Signieren ohne cosign — zwei openssl-Zeilen genügen

## v0.4.0 — 2026-09-02

- Port-Scan-Erkennung: die abgewiesenen Pakete zählen
- Changelog — und er wird zu dem, was vor dem Update dasteht

## v0.3.9 — 2026-09-02

- install.sh: die Meldung sagte etwas Falsches über sich selbst

## v0.3.8 — 2026-09-02

- install.sh prüft die Signatur, bevor es das erste Binary anfasst
- tenant import stellt den Mandanten auch auf dem Server her

> **Achtung:** Ohne Release-Schlüssel im Kanal bricht `install.sh` jetzt ab,
> statt ungeprüft zu installieren. Wer bewusst einen unsignierten Kanal
> betreibt, setzt `VOLT_ALLOW_UNSIGNED=1`; wie ein Kanal signiert wird, steht
> in [docs/release.md](docs/release.md).

## v0.3.7 — 2026-09-02

- Git-Deploy: prüfen, wohin der Name zeigt
- Update-Karte: kein Schlüssel ist kein Netzproblem

## v0.3.6 — 2026-09-02

- stand.md: trennt "noch nicht" von "nicht vorgesehen"
- Container: was sie verbrauchen, und was auf der Platte liegt

## v0.3.5 — 2026-09-02

- volt update prüft jetzt eine Signatur — vorher prüfte es sich selbst
- Git-Deploy holt nicht mehr vom Server selbst
- Stand der SSRF-Filterung nachziehen
- Firewall und Fail2ban in der Oberfläche
- Einen Mandanten umziehen: Bündel statt serverweites Backup
- site rebuild legt den Systembenutzer mit an

> **Achtung:** `volt update` prüft ab hier eine Signatur über `latest.json`.
> Ein Binary ohne eingebetteten Release-Schlüssel lehnt jeden Kanal ab, statt
> ungeprüft zu aktualisieren — siehe [docs/release.md](docs/release.md).

## v0.3.4 — 2026-09-02

- Docker: keine Schalter, sondern eine Beschreibung
- Node-Fassungen nebeneinander — und ein Archiv als Eingabe behandelt

## v0.3.3 — 2026-09-02

- Git-Deploy im Agent: klonen, bauen, umschalten
- Git-Deploy vollständig: Schema, Dienst, Webhook, Ansicht

## v0.3.2 — 2026-09-02

- Phase 5: eine App ist eine systemd-Unit — der Teil, der als root schreibt
- Apps über die Oberfläche: Schema, Dienst, API, Ansicht

## v0.3.1 — 2026-09-01

- Echte Dateisystem-Quotas über Project Quota
- Eigene Anmeldedomain je Mandant — Phase 4 ist damit fertig

## v0.3.0 — 2026-09-01

- FTP: die zwei Werte, an denen pure-ftpd starb

## v0.2.9 — 2026-09-01

- Grund fuer einen fehlgeschlagenen Dienststart nennen; Traffic-Zaehler

## v0.2.8 — 2026-09-01

- SQL-Browser, und apt laeuft ausserhalb der Agent-Sandbox
- Ziele S3, B2 und FTP — damit ist Phase 3 abgeschlossen

## v0.2.7 — 2026-09-01

- dpkg-Fehler benennen und nicht daran scheitern

## v0.2.6 — 2026-09-01

- Remote-Whitelist fuer Datenbankzugriffe von aussen

## v0.2.5 — 2026-09-01

- apt-Aufrufe, die auch aus einem Dienst heraus durchlaufen

## v0.2.4 — 2026-08-31

- Phase 3: FTP mit Pure-FTPd und virtuellen Benutzern

## v0.2.3 — 2026-08-31

- das Terminal war kein offener Punkt, sondern eine Entscheidung
- Phase 5 bis 8 ausgeschrieben statt in einem Satz abgetan
- nach dem Update laedt die Oberflaeche sich neu

## v0.2.2 — 2026-08-31

- Phase 1 und 2 abgeschlossen: Terminal, Prozessliste, HSTS, Panel-Zertifikat

## v0.2.1 — 2026-08-31

- Datenbank-Export und -Import ueber die Oberflaeche

## v0.2.0 — 2026-08-31

- Phase 2 abgeschlossen: PHP-Erweiterungen ueber die Oberflaeche

## v0.1.9 — 2026-08-31

- volt update bringt jetzt auch die systemd-Units mit

## v0.1.8 — 2026-08-31

- nginx kommt in die Site-Verzeichnisse, sonst niemand

## v0.1.7 — 2026-08-31

- ProtectSystem=full und ReadWritePaths=/etc heben sich gegenseitig auf

## v0.1.6 — 2026-08-31

- "cannot lock /etc/passwd" sagt jetzt, woran es liegt
- die Diagnose nennt jetzt den Prozess, der /etc/.pwd.lock haelt

## v0.1.5 — 2026-08-31

- Updates aus der Oberflaeche: Hinweis, Release-Notes und ein Knopf

## v0.1.4 — 2026-08-31

- der Agent darf /etc beschreiben — useradd sperrt dort

## v0.1.3 — 2026-08-31

- das Panel liegt unter einem Pfadpraefix — jetzt findet der Browser es auch

## v0.1.2 — 2026-08-31

- Agent-Socket bekommt die Gruppe des Peers, nicht dessen Benutzernummer

## v0.1.1 — 2026-08-31

- Bezugsquelle als eigener Workflow, der auf main laeuft
- Schluessel dort ablegen, wo das Panel schreiben darf; Backtick im Heredoc
- Bezugsquelle folgt main automatisch
- volt-web darf sein Schluesselverzeichnis beschreiben
- die gemeinsame nginx-Config wird endlich geschrieben

## v0.1.0 — 2026-08-31

- Fundament: Datenmodell, Migrationen, Tenant-Scope und Agent-Protokoll
- Panel-Kern: Templates, Auth, API, Metriken, CLI, ACME und Backups
- Frontend, Packaging, Doku und CI
- Executable-Bit fuer install.sh und build-web.sh setzen
- Workflows
- Phase 3: Datenbanken, Dateimanager und Cronjobs
- Phase 4: Hosting-Pakete, Quotas und Kundenansicht
- Phase 2 abgeschlossen: Site-Einstellungen, PHP je Site, Zertifikate ueber die API
- Installation ohne Release: Offline-Paket, MariaDB, Rechte nach root-Aufrufen
- Das Panel spricht TLS, volt update tauscht auch den Agent
- Der Einzeiler installiert wirklich: Bezugsquelle auf Pages, Fahrplan statt geratener URLs
- x-Bit fuer die Release-Skripte, und eine Pruefung dagegen
- Platzhalter fuer //go:embed zurueck, Actions auf node24
- Go-Version aus go.mod ableiten statt sie in den Workflows zu wiederholen
