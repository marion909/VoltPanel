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

- DatabaseService.tenantPrefix und FTPService.buildName leiteten aus
  demselben Tenant-Slug Zeile für Zeile dieselbe Präfix-Form ab,
  einmal als Methode, einmal inline — neue gemeinsame Hilfsfunktion
  tenantSlugPrefix(slug); damit ist der gesamte
  "Wiederverwendung, Vereinfachung, Effizienz"-Abschnitt der
  Bugfix-Liste abgearbeitet

## v0.4.114 — 2026-09-08

- Der Aufbau einer site_id → domain-Map stand dreimal wortgleich in
  apps.go und deploys.go — neue Hilfsfunktion domainsByID(sites)
  für alle drei Stellen

## v0.4.113 — 2026-09-08

- boolToInt hatte keine Umkehrfunktion — 13 scanX-Funktionen in 10
  Dateien rekonstruierten die Rückrichtung von Hand und uneinheitlich
  (mal != 0, mal == 1). Neues symmetrisches intToBool(i int) bool in
  helpers.go, alle Stellen darauf umgestellt

## v0.4.112 — 2026-09-08

- CheckCount/countFor liefen über zwei parallele switch-Anweisungen
  über dieselbe Resource-Aufzählung — eine neue Ressource musste an
  zwei Stellen ergänzt werden. Jetzt eine Lookup-Tabelle
  resourceCounters (Limit- und Zählfunktion je Ressource in einer
  Zeile), Zählfunktionen als Methodenausdrücke auf *store.Store

## v0.4.111 — 2026-09-08

- SetGrants, SetPassword und DeleteDatabase wiederholten je eine
  fast identische Schleife über die Herkunftsliste eines
  DB-Benutzers — neue Hilfsfunktion applyAcrossHosts bündelt die
  Continue-on-Failure-Iteration für alle drei (DeleteUser bewusst
  unverändert gelassen, siehe bugfix.md)

## v0.4.110 — 2026-09-08

- Create (backup.go) und ExportTenant (tenant_export.go) bauten je
  denselben ~20-zeiligen Block nach (Datei mit 0600 öffnen,
  sha256-Hasher + gzip.Writer + tar.Writer, Schließreihenfolge,
  Größe/Prüfsumme) — neuer gemeinsamer Hilfstyp archiveWriter in
  backup.go übernimmt das für beide; neuer Test
  TestCreateLiefertGroesseUndPruefsummeDesTatsaechlichenArchivs
  sichert ab, dass Größe und Prüfsumme weiterhin zur tatsächlich
  geschriebenen Datei passen

## v0.4.109 — 2026-09-08

- Restore (backup.go) und OpenBundle (tenant_export.go) rollten von
  Hand "Datei öffnen → gzip.NewReader → tar.NewReader →
  tr.Next()-Schleife" nach — eachEntry ist jetzt eine paketweite
  Funktion (statt einer Methode auf ExportService) mit einem
  Abbruchsignal für Callbacks, die nur einen bestimmten Eintrag
  suchen; Restore und OpenBundle nutzen sie jetzt beide. Neue Tests
  TestRestoreLehntArchivOhneVoltDBAb und
  TestOpenBundleLehntArchivOhneBundleAb schließen eine bisherige
  Testlücke (Archiv ohne den gesuchten Eintrag)

## v0.4.108 — 2026-09-08

- UsageForTenant fragte die Site-Summen ab, gefolgt von sieben
  Einzelabfragen (eine je Zähltabelle) — 8 Round-Trips statt 1. Jetzt
  ein einzelnes Statement mit sieben Subselects; neuer Test
  TestUsageForTenantZaehltAlleTabellen mit unterschiedlichen
  Zählständen je Tabelle sichert die Spaltenzuordnung ab

## v0.4.107 — 2026-09-08

- opDockerEnv holte uid von siteUserIDs, verwarf es aber nur mit
  `_ = uid` — jetzt direkt `_, gid, err := siteUserIDs(...)`

## v0.4.106 — 2026-09-08

- opMySQLSetPassword und opMySQLDropUser wiederholten je die beiden
  Prüfzeilen, die checkMySQLUser bereits bündelt — neuer Helfer
  checkMySQLUsernameHost(username, host) für beide, mit eigenem Test

## v0.4.105 — 2026-09-08

- extractZip zählte die entpackte Größe als uint64, extractTarGz als
  int64 — uneinheitlich typisiert. Jetzt beide int64, mit neuem
  addArchiveSize-Helfer, der eine als riesig deklarierte
  uint64-Eintragsgröße abfängt, bevor sie beim Umwandeln nach int64
  negativ umschlagen und den Größendeckel unbemerkt unterlaufen könnte

## v0.4.104 — 2026-09-08

- installWordPressFiles und installRoundcubeFiles bauten je einzeln
  "Temp-Verzeichnis anlegen, laden, Summe prüfen, per os.Rename
  einsetzen" nach — neuer gemeinsamer Helfer installArchiveInto in
  ops_node.go, beide Aufrufer nutzen ihn jetzt; die Platzhalterseite
  von WordPress-Installationen wird weiterhin nur bei erfolgreicher
  Prüfsumme entfernt

## v0.4.103 — 2026-09-08

- openFTPPorts, openMailPorts und setMySQLPort bauten dreimal dieselbe
  Logik nach (ufw-Status prüfen, bei aktivem ufw Regeln setzen, bei
  Fehler feste Ersatzmeldung) — neuer gemeinsamer Helfer
  ufwApplyRules(ctx, action, rules) (active, ok bool) im bestehenden
  ops_firewall.go, alle drei Aufrufer nutzen ihn jetzt

## v0.4.102 — 2026-09-08

- writeJSON (internal/agent/server.go) baute den JSON-Puffer per
  json.Marshal und hängte den Zeilenumbruch per append(b, '\n') an —
  letzteres erzwang bei jeder Anfrage/Antwort eine zweite Allokation
  samt Kopie; json.NewEncoder(w).Encode(v) schreibt jetzt direkt in
  den bufio.Writer und hängt den Zeilenumbruch selbst an

## v0.4.101 — 2026-09-08

- Sites.vue: Label "Proxy-Ziel" war hartcodiertes Deutsch statt t() —
  neuer Schlüssel sites.proxyTarget in de/en ergänzt; damit ist der
  gesamte web/src-Abschnitt der Bugfix-Liste abgearbeitet

## v0.4.100 — 2026-09-08

- Cronjobs.vue: das Zeitplan-Feld hatte required, prüfte aber nicht
  das Fünf-Felder-Format — ein unvollständiger Ausdruck fiel erst als
  Serverfehler auf; ein pattern für "fünf durch Leerraum getrennte
  Felder" prüft das jetzt schon im Browser

## v0.4.99 — 2026-09-08

- Mail.vue: der "Anlegen"-Button für ein neues Postfach war nur an
  !boxForm.local_part.trim() gekoppelt — das Passwortfeld hatte keine
  clientseitige Prüfung der vom Server verlangten Mindestlänge (10
  Zeichen), ein zu kurzes Passwort fiel erst nach dem Absenden als
  Serverfehler auf; Button und Feld prüfen die Länge jetzt vorab

## v0.4.98 — 2026-09-08

- AppStoreDialog.vue: loadCatalog() fing jeden Fehler mit leerem
  catch {} ab, ohne error.value zu setzen — schlug /appstore fehl,
  öffnete sich die Klappe ohne jeden Hinweis auf einen Serverfehler;
  der Fehler wird jetzt sichtbar gemacht

## v0.4.97 — 2026-09-08

- Route /services trug anders als /plugins/tenants kein meta: {
  minRole: 'admin' }, obwohl der zugehörige Navigationspunkt in
  App.vue bereits mit minRole: "admin" versehen ist — ein
  Nicht-Administrator, der die URL direkt aufrief, sah die voll
  bedienbare Dienste-Komponente; jetzt ergänzt

## v0.4.96 — 2026-09-08

- formatBytes prüfte nur Number(bytes) || 0 und ließ negative Zahlen
  unverändert durch — ergab z. B. "-5 B" bzw. "-53.2 KiB/s" statt 0;
  negative Werte werden jetzt auf 0 gekappt

## v0.4.95 — 2026-09-08

- UpdateCard.vue: waitForPanel() pollte bis zu drei Minuten ohne
  Abbruch-Mechanismus — verließ ein Administrator die
  Einstellungsseite während eines laufenden Updates, feuerte die
  Schleife trotzdem window.location.reload() und riss die inzwischen
  ganz andere, aktuell besuchte Seite unerwartet weg; ein
  cancelled-Flag über onUnmounted bricht Schleife und Reload jetzt ab

## v0.4.94 — 2026-09-08

- SiteTerminal.vue: onMounted lief komplett ohne try/catch — schlug der
  dynamische Import von @xterm/xterm oder @xterm/addon-fit fehl, blieb
  ein leerer Rahmen ohne jeden Hinweis stehen; ein Fehlschlag zeigt
  jetzt term.failed an, und restart() bricht sauber ab, wenn nie ein
  Terminal entstanden ist

## v0.4.93 — 2026-09-08

- Files.vue: der Site-Wechsel löste denselben Verzeichnisinhalt
  zweimal aus (watch(siteId) setzt path zurück und ruft zusätzlich
  selbst load() auf) — ohne Abbruch konnte eine noch laufende, ältere
  Anfrage nach der neueren auflösen und entries.value mit dem Inhalt
  der vorherigen Site überschreiben; load() bricht jetzt eine noch
  laufende Anfrage per AbortController ab, bevor es neu lädt

## v0.4.92 — 2026-09-08

- SiteDetail.vue: siteId (aus route.params.id) hatte keinen eigenen
  watch() — da /frontend/sites/:id dieselbe Komponenteninstanz für
  jede Site wiederverwendet, blieben site/settings/php/certs/
  authUsers und der aktive Reiter der vorherigen Site sichtbar, bis
  ein voller Remount kam; neuer watch(siteId, …) lädt neu und setzt
  den Reiter zurück

## v0.4.91 — 2026-09-08

- Settings.vue/RingGauge.vue/QuotaBar.vue/Dashboard.vue: hartcodierte
  deutsche Texte (Passwort-geändert-Meldung, Feldbezeichnungen
  E-Mail/Rolle/Tenant, aria-label von Ring- und Quota-Anzeigen, die
  "Kerne"-Beschriftung des CPU-Gauges) liefen an der Sprachauswahl
  vorbei — jetzt über t() mit neuen i18n-Schlüsseln geführt

## v0.4.90 — 2026-09-07

- Deploys.vue: zurueck() (Rollback) rief die als reinen Toggle
  implementierte staendeLaden(d) zweimal hintereinander auf und
  funktionierte nur zufällig, weil offen[d.id] vor dem Klick immer true
  war — neue ladeStaende(d) lädt ohne Toggle-Nebeneffekt nach

## v0.4.89 — 2026-09-07

- FirewallPanel.vue: der Regler für die Portscan-Empfindlichkeit wurde
  beim Laden nie mit dem Server-Wert synchronisiert — stand der Server
  z. B. auf "streng", zeigte das Dropdown weiterhin "Normal", und "Stufe
  übernehmen" ohne bewusste Auswahl setzte die Sicherheitsstufe
  stillschweigend zurück

## v0.4.88 — 2026-09-07

- GetApp/AppForSite/GetDeploy/DeployForSite luden die Zeile bisher ohne
  tenant_id im SQL und prüften erst danach per sc.owns() — funktional
  korrekt, aber abweichend vom sonst im Paket verwendeten
  scope.where()-Muster, das den Tenant-Filter direkt ins SQL bäckt;
  jetzt vereinheitlicht

## v0.4.87 — 2026-09-07

- php_pools hatte keinen UNIQUE-Index auf site_id — zwei nahezu
  gleichzeitige CreatePHPPool-Aufrufe für dieselbe Site mit
  unterschiedlichem pool_name konnten zwei Zeilen anlegen; die zweite
  blieb im Panel unsichtbar, lief aber als eigener PHP-FPM-Pool weiter

## v0.4.86 — 2026-09-07

- Testlücke geschlossen: internal/core/ftp.go hatte überhaupt keine
  eigene Testdatei — weder Tenant-Isolation noch der bereits in v0.4.31
  behobene Symlink-Fund (gemeinsam mit files.go über joinInside) waren
  für das FTP-Home-Verzeichnis abgedeckt

## v0.4.85 — 2026-09-07

- Testlücke geschlossen: jail() lief bisher nur mit einer einzelnen
  Wurzel getestet, obwohl es in Produktion immer mit der vollen
  Wurzel-Liste läuft — neuer Test hält fest, dass ein Symlink über
  Wurzelgrenzen hinweg (und zu einer Nachbar-Site derselben Wurzel)
  absichtlich erlaubt bleibt, damit eine künftige Änderung daran auffällt

## v0.4.84 — 2026-09-07

- Der SNI-Zertifikats-Cache bekam bei jedem erstmaligen TLS-Handshake
  für eine Anmeldedomain einen neuen Eintrag, aber nichts entfernte ihn
  wieder, wenn die Domain später gelöscht oder geändert wurde — der
  Eintrag blieb für die Prozesslaufzeit im Speicher; wird jetzt beim
  Ändern/Löschen der Anmeldedomain mitentfernt

## v0.4.83 — 2026-09-07

- installWordPressFiles rief os.Rename ohne vorheriges Aufräumen des
  Ziels auf, anders als installRoundcubeFiles im selben Package — ein
  mitten in der Installationsschleife abgebrochener Versuch hinterließ
  ein Zielverzeichnis, an dem jeder Retry mit "directory not empty"
  scheiterte

## v0.4.82 — 2026-09-07

- ImportTenant legte den Mandanten sofort mit dem endgültigen Status an
  — brach der Import mittendrin ab, blockierte der Konflikt-Check jeden
  erneuten Versuch mit demselben Bündel dauerhaft, und die API
  verweigerte auch das Löschen. Neuer Zwischenzustand "importing": ein
  hängen gebliebener Import blockiert weder einen Neuversuch noch das
  Löschen mehr

## v0.4.81 — 2026-09-07

- Stirbt volt-agent zwischen dem Schreiben einer neuen nginx-Config und
  ihrem eigenen nginx -t, bleibt eine ungeprüfte Datei live liegen, ohne
  dass es auffällt — der Agent testet die aktive Config jetzt einmal
  beim eigenen Start und loggt eine Warnung, wenn sie nicht besteht

## v0.4.80 — 2026-09-07

- ftpConn.readLine begrenzte die Größe einer Antwortzeile erst nach dem
  vollständigen Lesen, nicht während des Lesens — ein bösartiger oder
  kompromittierter FTP-Zielserver konnte bis zum Verbindungstimeout
  beliebig viele Daten ohne Zeilenumbruch senden und dadurch
  unkontrolliert Speicher belegen

## v0.4.79 — 2026-09-07

- RenderShared prüfte ACMEWebroot nur mit filepath.IsAbs() statt mit
  checkPath() wie jede andere Render*-Funktion im Paket — Zeilenumbrüche,
  ";", "{", "}" und ".." gelangten so ungeprüft in die für alle Vhosts
  geltende Default-Server-Config

## v0.4.78 — 2026-09-07

- opMySQLCreateUser nahm ein gerade angelegtes Konto nicht zurück, wenn
  ALTER USER oder das Setzen der Rechte danach fehlschlug — ein Konto
  ohne (oder mit veraltetem) Passwort und ohne die vorgesehenen Rechte
  blieb auf dem Server stehen, obwohl die Erstellung als gescheitert galt

## v0.4.77 — 2026-09-07

- Deploy-Release-Namen hatten nur Sekundenauflösung — zwei
  opDeployRun-Aufrufe für dieselbe Site innerhalb derselben Sekunde
  (Doppelklick, doppelt zugestellter Webhook) erzeugten denselben
  Zielpfad; jetzt nanosekundengenau, alte Stände bleiben erkennbar

## v0.4.76 — 2026-09-07

- opPortScanSet sicherte im Enable-Pfad den bisherigen Inhalt nicht,
  bevor Filter/Jail überschrieben wurden — lehnte fail2ban die neue
  Regel beim Reload ab, wurde eine zuvor aktive Erkennung komplett
  gelöscht statt auf die letzte funktionierende Stufe zurückzufallen

## v0.4.75 — 2026-09-07

- MailService.collect() brach beim ersten nicht entschlüsselbaren
  Postfach-Passwort oder DKIM-Schlüssel komplett ab — da praktisch jede
  Mail-Änderung Apply() aufruft, blockierte ein defekter Secret-Eintrag
  eines Mandanten die Mail-Änderungen aller anderen; ein defekter
  Eintrag wird jetzt übersprungen und nur noch geloggt

## v0.4.74 — 2026-09-07

- UpdatePHP entfernte beim Versionswechsel den alten FPM-Pool, bevor
  UpdateSite/UpdatePHPPool/Rebuild überhaupt gelaufen waren — schlug
  einer davon fehl, blieb die Site ganz ohne FPM-Pool zurück (502);
  der alte Pool wird jetzt erst entfernt, nachdem der neue steht

## v0.4.73 — 2026-09-07

- CollectTraffic schrieb Byte-Zuwachs und Lesestand in zwei getrennten
  UPDATE-Anweisungen — schlug die zweite (Cursor) nach der ersten
  (Bytes) fehl, zählte der nächste Lauf denselben Log-Bereich doppelt;
  jetzt eine atomare Anweisung für beides

## v0.4.72 — 2026-09-06

- restoreDatabases baute seine Zulassungsliste aus allen Datenbanken des
  Bündels statt nur aus den tatsächlich neu angelegten — schlug
  CreateDatabase wegen einer Namenskollision mit einer fremden,
  bestehenden Datenbank fehl, wurde deren Inhalt trotzdem durch den
  importierten Dump überschrieben

## v0.4.71 — 2026-09-06

- BackupService.Restore überschrieb die Datenbankdatei auch dann, wenn
  das vorherige store.Close() fehlgeschlagen war (nur geloggt, Restore
  lief trotzdem weiter) — bricht jetzt ab, statt einen inkonsistenten
  Zustand zu riskieren

## v0.4.70 — 2026-09-06

- PATCH /sites/:id übernahm einen beliebigen String in site.Status ohne
  Validierung, obwohl das Feld nirgends gelesen/ausgewertet wird — sah
  nach einem Statusfeld mit definierten Übergängen aus, war aber
  wirkungslos; aus dem PATCH-Body entfernt

## v0.4.69 — 2026-09-06

- handleDeleteTenant prüfte vor dem Löschen nur Sites/Datenbanken/
  Cronjobs; mail_domains/certs/backup_targets hängen direkt (nicht über
  sites) mit ON DELETE CASCADE an tenants und wurden nie gezählt — ein
  Mandant ohne Sites, aber mit aktiver Maildomäne, ließ sich löschen und
  riss sie still über die Kaskade mit

## v0.4.68 — 2026-09-05

- Die Quota-Übersicht (QuotaService.Status) zeigte keinen Eintrag für
  Postfächer, obwohl CreateMailbox das Postfach-Limit längst durchsetzt —
  TenantUsage führte gar kein Zählfeld dafür; jetzt ergänzt

## v0.4.67 — 2026-09-05

- Sieben Audit-Aufrufe (Image entfernen, Plugin install/uninstall/set,
  Feature installieren, Webmail install/uninstall) übergaben bei
  Fehlschlag teils das rohe error als detail (json.Marshal ergibt "{}",
  die Fehlermeldung ging verloren) und nutzten uneinheitlich "fehler"
  statt "error" als result-Wert

## v0.4.66 — 2026-09-05

- handlePortScanSet schrieb den An/Aus-Zustand in das result-Feld des
  Audit-Logs statt in detail — jede erfolgreiche Portscan-Umschaltung
  erschien im Audit-Log fälschlich als fehlgeschlagen (Frontend färbt
  nur result === 'ok' grün)

## v0.4.65 — 2026-09-05

- handleCreateSite und handleInstallWordPress verwarfen die von
  sc.ForTenant(...) zurückgegebene, elevierte Scope und arbeiteten
  weiter mit dem ursprünglichen sc — ein Owner/Admin scheiterte dadurch
  zuverlässig mit 404, sobald er über tenant_id im Body für einen
  anderen Mandanten etwas anlegen wollte

## v0.4.64 — 2026-09-05

- 2fa/enable und 2fa/disable prüften den 6-stelligen TOTP-Code ohne jedes
  Rate-Limiting — bei einer gekaperten Session ließen sich die 1.000.000
  Kombinationen in kurzer Zeit durchprobieren; jetzt fünf Versuche/Minute
  je Benutzer, wie beim Login

## v0.4.63 — 2026-09-05

- Config.Validate() prüfte nur DataDir/ConfigDir/SitesDir auf einen
  absoluten Pfad; BackupDir/DBPath/SocketPath/NginxDir/PHPFPMDir/CertDir/
  SecretKeyPath/LogDir blieben unvalidiert, obwohl auch sie feste absolute
  Pfade sein sollen

## v0.4.62 — 2026-09-05

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
