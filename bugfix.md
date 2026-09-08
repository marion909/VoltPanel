# VoltPanel — Bugfix & Verbesserungsliste

Diese Datei wird laufend durch automatisierte Code-Analyse befüllt. Es werden
ausschließlich Code- und Design-Fixes eingetragen — keine Vorschläge, die
bestehende Funktionalität entfernen oder brechen würden.

Format je Fund: Datei:Zeile, Beschreibung, Einschätzung (Bug/Sicherheit/Design), Vorschlag.

Stand: alle Funde bis einschließlich v0.4.59 sind umgesetzt und aus dieser
Datei entfernt (siehe CHANGELOG.md für die einzelnen Einträge). Was folgt,
ist ausschließlich noch offen.

## Go-Code

### internal/store — Tenant-Scoping-Schicht

**Erledigt:** Die zehn `Create*`-Funktionen, die eine mitgegebene Fremd-ID
(`site_id`, `database_id`, …) nicht gegen den Mandanten prüften, sind
gehärtet (v0.4.33). Die fehlende `RowsAffected`-Prüfung in
`UpdateMailDomain`/`UpdateMailbox`/`DeleteMailDomain`/`DeleteMailbox`/
`DeleteMailAlias` ist ebenfalls behoben (v0.4.60). `GetApp`/`AppForSite`/
`GetDeploy`/`DeployForSite` nutzen jetzt `scope.where()` statt einer
Post-hoc-Prüfung (v0.4.88).

**Erledigt (v0.4.87):** `php_pools`-UNIQUE-Index auf `site_id` (Migration
0016). **Bereits erledigt vorgefunden:** `repo_ftp.go`s `CreateFTPAccount`
nutzt `nilIfEmpty(a.SiteID)` bereits (offenbar im selben Zug wie v0.4.33
mitgezogen) — dieser Punkt war beim Nachprüfen bereits gelöst.

**Zusätzliche Härtung (optional, ergänzend zu v0.4.33):**
`internal/store/migrations/0001_init.sql:105-133` — Die SQL-Fremdschlüssel
(`site_id INTEGER REFERENCES sites(id)`, …) prüfen nur die *Existenz* der
referenzierten Zeile, nicht deren `tenant_id`. Für die betroffenen Tabellen
je einen `CREATE TRIGGER ... BEFORE INSERT` ergänzen, der bei abweichendem
`tenant_id` der referenzierten Zeile mit `RAISE(ABORT, ...)` ablehnt (z. B.
`CREATE TRIGGER trg_databases_tenant_check BEFORE INSERT ON databases WHEN
NEW.site_id IS NOT NULL AND (SELECT tenant_id FROM sites WHERE id =
NEW.site_id) != NEW.tenant_id BEGIN SELECT RAISE(ABORT, 'site gehört anderem
mandanten'); END;`). Das wäre die im Projekt selbst propagierte "Gürtel zum
Hosenträger"-Philosophie konsequent bis auf die Schema-Ebene weitergedacht —
fängt auch einen künftigen, heute noch unbekannten Aufrufer ohne eigene
Prüfung ab.

### internal/agent — Kern-Infrastruktur

`internal/agent/terminal.go:196` (`opTerminalResize`) — bereits behoben
(v0.4.53). *Self-Korrektur-Hinweis:* zwei ursprünglich gemeldete Funde
wurden beim Gegenchecken als falsch-positiv verworfen: `git_target.go`s
angebliches Fehlen von IPv4-in-IPv6-Unmapping (das übernimmt bereits
`transfer.CheckAddr` uniform für jeden Aufrufer) sowie `client.go:73`s
angebliches Retry-Risiko bei "nicht-idempotenten" Operationen — die dort
genannten Beispiele (`mysql.create_db`, `user.create`) sind tatsächlich
beide bewusst idempotent (`CREATE DATABASE IF NOT EXISTS`, `opUserCreate`
behandelt "existiert bereits" explizit als Erfolg, vgl. `roadmap.md`
Prinzip 2).

### Betriebs-Resilienz bei Teilausfällen

**Teilweise erledigt (v0.4.82):** `internal/core/tenant_import.go` — der
Zwischenstatus `TenantImporting` sorgt dafür, dass ein mittendrin
abgebrochener Import weder einen erneuten Versuch mit demselben Bündel noch
das Löschen über die API dauerhaft blockiert. **Weiterhin offen:**
`store.DeleteTenant` räumt dabei nach wie vor nur DB-Zeilen weg, keine über
den Agent angelegten Systemressourcen (Linux-Benutzer, Vhosts, echte
Datenbanken) eines abgebrochenen Imports — ein vollständiger Teardown wäre
ein deutlich größerer, eigener Eingriff (eigene "Import
abbrechen/aufräumen"-Funktion, die dieselben Agent-Teardown-Schritte wie
die regulären Lösch-Flows durchläuft).

## web/src — Vue-Frontend (vollständig geprüft, alle 39 Dateien)

**Erledigt:** alle Funde umgesetzt (v0.4.89–v0.4.101, siehe CHANGELOG.md).
Keine Sicherheitsbefunde: kein einziges `v-html`/`innerHTML`/`outerHTML` im
gesamten `web/src`, Token-Handling über Cookie + CSRF-Header statt
`localStorage`.

## Wiederverwendung, Vereinfachung, Effizienz

**Teilweise erledigt:** Der Zählertyp-Unterschied zwischen `extractTarGz`
(`int64`) und `extractZip` (`uint64`) ist behoben (`written` jetzt in beiden
`int64`, mit neuem `addArchiveSize`-Helfer, der den Vorzeichenüberlauf beim
Umwandeln einer riesigen `uint64`-Größe explizit abfängt — Test
`TestAddArchiveSizeUeberspringtVorzeichenUeberlauf`). **Bewusst nicht
umgesetzt:** ein gemeinsamer Walker mit Format-Callback für
`writeTarGz`/`writeZip`/`extractTarGz`/`extractZip` — tar (Symlinks per
Header nachgebildet, `TypeFlag`-Switch, Streaming-API) und zip
(Symlinks ausgelassen, Datei-Liste statt Stream) unterscheiden sich in der
Verarbeitung genug, dass eine erzwungene gemeinsame Abstraktion in dieser
sicherheitskritischen Datei (Zip-Slip-Schutz, Größendeckel) mehr Risiko für
eine stille Verhaltensänderung birgt, als die reine Code-Dopplung wert ist.

**Bewusst nicht umgesetzt:** `internal/core/tenant_bundle.go:125-155`
(`CollectTenant`) — N+1 über drei verschachtelte Ebenen (Site → FTP/PHP-Pool,
Datenbank → Benutzer → Herkunftsliste). Der Fund selbst stuft die praktische
Auswirkung als gering ein: SQLite ist lokales Datei-I/O statt ein
Netzwerk-Round-Trip, und `CollectTenant` läuft nur beim (seltenen,
admin-ausgelösten) Export/Import eines Mandanten, nie im Anfragepfad. Eine
Sammelabfrage über `site_id IN (...)`/`db_user_id IN (...)` bräuchte neue,
öffentliche Store-Methoden (`ListFTPAccountsForSites`,
`ListDBUsersForDatabases`, `ListRemoteHostsForUsers` o. ä.) und damit eine
größere API-Erweiterung des `store`-Pakets für einen Pfad, der in der Praxis
kaum ins Gewicht fällt.

**Teilweise erledigt:** `applyAcrossHosts(hosts []string, fn func(host
string) error) []string` gebündelt und in `SetGrants`, `SetPassword` und der
inneren Schleife von `DeleteDatabase` eingesetzt — dieselbe
Continue-on-Failure-Semantik, dieselben Fehlermeldungen, rein mechanisch.
**Bewusst nicht umgesetzt:** `DeleteUser` bricht bei der ersten
fehlgeschlagenen Herkunft sofort ab (`return`) und löscht die Store-Zeile
dann gar nicht — anders als die drei anderen, die weiterlaufen und die
Store-Änderung trotz einzelner Fehlschläge übernehmen. Das auf dieselbe
Semantik umzustellen wäre eine echte Verhaltensänderung (das Panel würde
einen Benutzer als gelöscht führen, auch wenn ein MySQL-Konto auf einer
Herkunft stehen bleibt) — und `DatabaseService.agent` ist ein konkreter
`*agent.Client`, kein Interface wie `SiteService.agent` (`siteAgent`), es
gibt also keinen Fake, um das an einem echten Test zu verifizieren. Eine
Umstellung ohne Testabsicherung für eine Verhaltensänderung an einem
Lösch-Pfad wäre das falsche Risiko für diesen Fund.

**Erledigt:** `tenantSlugPrefix(slug string) string` als gemeinsame
Hilfsfunktion — `DatabaseService.tenantPrefix` und `FTPService.buildName`
leiteten aus demselben Tenant-Slug Zeile für Zeile dieselbe Präfix-Form ab,
einmal als Methode, einmal inline. Damit ist dieser Abschnitt der
Bugfix-Liste vollständig abgearbeitet (v0.4.102–v0.4.115, siehe
CHANGELOG.md).

## CLI-Usability (cmd/volt)

**Erledigt:** `findCronjob`/`findPlan` (analog zu `findDatabase`) — `cron
remove`, `cron log` und `plan remove` akzeptieren jetzt zusätzlich zur ID
den Namen. Pakete haben einen `UNIQUE`-Namen, ein Treffer ist dort immer
eindeutig; Cronjob-Namen sind es nicht (kein `UNIQUE`-Index) — passt der
Name auf mehrere Jobs, verlangt der Fehler die ID statt stillschweigend
einen davon zu wählen. Damit ist dieser Abschnitt der Bugfix-Liste
vollständig abgearbeitet (v0.4.116–v0.4.125, siehe CHANGELOG.md).

## packaging/, scripts/

**Bewusst nicht umgesetzt:** `packaging/install.sh:111-112` — Der
GPG-Schlüssel des Sury-PHP-Repos wird per `curl` geladen und ohne
Fingerprint-Abgleich sofort als vertrauenswürdig eingebunden. Recherchiert:
der `DEB.SURY.ORG Automatic Signing Key` hat laut mehreren
Support-Threads (GitHub-Issues, Foren) bereits mindestens einmal ohne
Vorankündigung gewechselt/ist abgelaufen ("Apt key for Debian Package is
expiring on 16.02.2024", diverse "signatures were invalid"-Threads) — ein
im Skript hinterlegter, hart verglichener Fingerprint bräche dann bei der
nächsten Rotation jede Neuinstallation mit einer kryptischen
Fingerprint-Mismatch-Meldung, bis jemand das Skript von Hand nachzieht.
Das wäre ein schlechterer, weil überraschenderer Ausfallmodus als der
heutige (verhaltene) Zustand. Ein per Web-Recherche gefundener Fingerprint
ließ sich außerdem nicht gegen eine offizielle Sury-Quelle absichern — die
Projektseite selbst nennt keinen. Ein fest hinterlegter Fingerprint für
einen Schlüssel mit dokumentierter Rotationshistorie wäre das falsche
Risiko für diesen Fund.

**Erledigt, abweichend vom Vorschlag:** Der Unix-Socket entstand per
`net.Listen` mit den ererbten Prozessrechten, bevor `os.Chmod(0o660)` sie
gleich danach verengte — ein kurzes Zeitfenster mit weiteren Rechten. Der
vorgeschlagene Fix (dauerhaft `syscall.Umask(0o177)` bzw. `UMask=0177` in
der systemd-Unit) hätte aber den ganzen Prozess betroffen: der Agent
schreibt später Dateien mit `0o644` (u. a. nginx-Configs), die bewusst
world-readable sein müssen, und eine dauerhaft verengte Umask hätte sie
unbemerkt auf `0600` gekappt. Stattdessen verengt `Listen()` in
`internal/agent/server.go` die Umask nur um den einen `net.Listen`-Aufruf
herum und stellt sie sofort danach zurück — schließt dasselbe Zeitfenster,
ohne die übrigen Dateien des Agents zu beeinflussen. Test
`TestListenSchmaelertUmaskNurWaehrendDesSocketAufbaus` sichert ab, dass
nichts von der Umask-Änderung nach außen dringt.

**Erledigt:** `scripts/build-pages.sh` nutzte feste, vorhersagbare Pfade
(`/tmp/other-latest.json`/`/tmp/other-latest.sig`) statt `mktemp`, obwohl
andere Skripte im selben Projekt korrekt `mktemp`/`mktemp -d` verwenden —
in einer geteilten CI-Umgebung ließe sich über einen vorab angelegten
Symlink das Ziel der `mv`-Operation beeinflussen. Auf `mktemp` umgestellt,
mit `trap ... EXIT` für den Abbruchfall. Damit ist auch dieser Abschnitt
und die gesamte Bugfix-Liste vollständig abgearbeitet (v0.4.126–v0.4.129,
siehe CHANGELOG.md).

---

Alle Funde aus dieser Datei sind umgesetzt oder — mit begründeter
Abwägung, siehe oben — bewusst nicht umgesetzt. Nichts steht mehr offen.
