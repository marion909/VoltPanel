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

`internal/core/tenant_bundle.go:125-155` (`CollectTenant`) — Für jede Site
ein eigener `ListFTPAccounts`-/`PHPPoolBySite`-Aufruf, für jede Datenbank ein
`ListDBUsers`-Aufruf, für jeden Datenbankbenutzer ein
`ListRemoteHosts`-Aufruf — N+1 über drei verschachtelte Ebenen. — Design
(Effizienz, geringe praktische Auswirkung) — Sammelabfragen über `site_id IN
(...)`/`db_user_id IN (...)`.

`internal/core/backup.go:145-177` (`Restore`) und
`internal/core/tenant_export.go:372-396` (`OpenBundle`) — Beide rollen von
Hand "Datei öffnen → `gzip.NewReader` → `tar.NewReader` →
`tr.Next()`-Schleife" nach, obwohl `internal/core/tenant_import.go:765-791`
mit `eachEntry` bereits eine generische Version bereitstellt. — Design —
`eachEntry` zu einer paketweiten Funktion machen und auch von
`Restore`/`OpenBundle` aufrufen lassen.

`internal/core/backup.go:79-126` (`Create`) und
`internal/core/tenant_export.go:146-212` (`ExportTenant`) — Beide bauen
denselben ca. 20-zeiligen Block (Datei mit 0600 öffnen, `sha256`-Hasher +
`gzip.Writer` + `tar.Writer`, Schließreihenfolge, Größe/Prüfsumme) nach. —
Design — Ein gemeinsamer Hilfstyp, der Datei+Hasher+gzip+tar öffnet und beim
Schließen Größe/Prüfsumme zurückgibt.

`internal/core/databases.go:210-226,260-273,298-306,322-333`
(`SetGrants`/`SetPassword`/`DeleteUser`/`DeleteDatabase`) — Vier fast
identische Schleifen über die Herkunftsliste eines DB-Benutzers, nur die
Fehlerbehandlung variiert unmotiviert zwischen den vier Kopien. — Design —
Gemeinsame Hilfsfunktion `applyAcrossHosts(hosts []string, fn func(host
string) error) []string`.

`internal/core/quota.go:140-156,172-186` (`CheckCount`/`countFor`) — Zwei
parallele `switch`-Anweisungen über dieselbe `Resource`-Aufzählung — eine
neue Ressource muss an zwei Stellen ergänzt werden. — Design — Lookup-Tabelle
`map[Resource]struct{limit func(*store.Plan) int; count func(context.Context,
store.Scope) (int, error)}`.

`internal/store/helpers.go:62-67` (`boolToInt`, ohne Umkehrfunktion) —
Betrifft mindestens 12 Stellen in 10 Dateien — jede `scanX`-Funktion
rekonstruiert die Rückrichtung von Hand und uneinheitlich. — Design —
Symmetrisches `intToBool(i int) bool { return i != 0 }` in `helpers.go`.

`internal/core/apps.go:313-316,411-414`, `deploys.go:332-335` — Der Aufbau
einer `site_id → domain`-Map steht dreimal wortgleich in zwei Dateien. —
Design — Hilfsfunktion `domainsByID(sites []*store.Site) map[int64]string`.

`internal/core/databases.go:507-514` (`tenantPrefix`) und `ftp.go:283-288`
(Teil von `buildName`) — Beide leiten aus demselben Tenant dieselbe
Präfix-Form ab — Zeile für Zeile identisch, einmal als Methode, einmal
inline. — Design — `FTPService.buildName` könnte `tenantPrefix` aus
`DatabaseService` wiederverwenden.

## CLI-Usability (cmd/volt)

`cmd/volt/site.go:57-63` (`siteAddCmd`) — `if phpVer != "" &&
!cmd.Flags().Changed("type") { siteType = store.SitePHP }` gefolgt direkt
von derselben Prüfung für `proxyTo` — sind beide Flags ohne explizites
`--type` gesetzt, überschreibt die zweite Prüfung `siteType` stillschweigend
auf `proxy`. — Design — Bei gleichzeitig gesetztem `phpVer` und `proxyTo`
ohne explizites `--type` einen Fehler ausgeben, der die Mehrdeutigkeit
benennt.

`cmd/volt/tenant_move.go:113-127` (`tenantImportCmd`) — Gibt Warnungen bei
`res.Rebuilt < res.Sites` nur auf stderr aus und liefert danach unbedingt
`return nil` — Exitcode 0 selbst bei einem nur teilweise geglückten Import.
— Design — Fix: bei `res.Rebuilt < res.Sites` (oder vorhandenen
`res.Warnings`) ebenfalls einen Fehler zurückgeben, wie vergleichbare
Befehle (`certRenewCmd`, `cronSyncCmd`, `siteRebuildCmd`).

`cmd/volt/tenant.go:130,171,175,208` — Mandanten werden bei `set-plan`,
`suspend` und `usage` ausschließlich über die numerische ID angesprochen,
obwohl `tenant add` einen sprechenden `--slug` vergibt. — Design —
`findTenant`-Helfer analog zu `findDatabase` (db.go:222) einführen, der Slug
oder ID akzeptiert.

`cmd/volt/tenant.go:171` (`tenantSuspendCmd`) — Keine Rückfrage
(`confirm()`/`--yes`), obwohl das Sperren eines Mandanten ähnlich folgenreich
ist wie `db remove`/`site remove`/`cron remove`/`plan remove`. — Design —
`confirm()`-Abfrage plus `--yes`-Flag ergänzen (nicht bei `--resume`).

`cmd/volt/tenant.go:145-156` (`set-plan ... 0`) — Hebt wie `plan remove`
alle Ressourcengrenzen eines Mandanten auf, läuft aber ohne Rückfrage durch.
— Design — Für `args[1] == "0"` dieselbe Warn-/Bestätigungslogik wie bei
`plan remove` einbauen.

`cmd/volt/user.go:167` (`user2FAResetCmd`) — Ruft `confirm()` auf, definiert
aber kein `--yes`-Flag. — Design — `--yes`-Flag nach demselben Muster
ergänzen.

`cmd/volt/site.go:106` u. a. (auch tenant.go:140/185/221, plan.go:128,
cron.go:122, user.go:110/159) — Store-Fehler (`store.ErrNotFound`) wird
unverändert durchgereicht; ein Admin sieht bei einem Tippfehler nur "fehler:
nicht gefunden" ohne Hinweis, wonach gesucht wurde. — Design — Alle
betroffenen Stellen nach dem Muster von `cron.go:76` (`fmt.Errorf("site %q:
%w", site, err)`) umschreiben.

`cmd/volt/site.go:85` — `--type` wird als roher String ungeprüft an
`store.SiteType(siteType)` weitergereicht, ohne eigene Validierung — anders
als `user add --role`. — Design — `Valid()`-Prüfung für die drei Site-Typen
analog zu `store.Role.Valid()` ergänzen.

`cmd/volt/site.go:74-81`, `db.go:82-89`, `cert.go:82-85` —
Erfolgsmeldungen von `site add`/`db add`/`cert issue` nennen nirgends den
Tenant, obwohl `--tenant` bei allen dreien still auf `1` fällt, falls
vergessen. — Design — Tenant-ID/-Name auch in den Erfolgsmeldungen ausgeben.

`cmd/volt/cron.go:62,112`, `plan.go:80,119` — Cronjobs/Pakete werden mit
sprechendem Namen angelegt, aber nur über die numerische ID entfernt/
abgefragt. — Design — `cron remove`/`cron log`/`plan remove` zusätzlich per
Name auflösen lassen, wie `db.go:222` es für Datenbanken vormacht.

## packaging/, scripts/

`packaging/install.sh:137` — `systemctl enable --now mariadb >/dev/null
2>&1 || true` verschluckt jeden Startfehler von MariaDB; anders als bei
volt-agent/volt-web gibt es keine spätere `is-active`-Prüfung. — Bug — Ein
kaputtes MariaDB fällt so erst bei `volt db add` auf. Fix: nach demselben
Muster den Dienststatus prüfen und bei Fehlschlag warnen (`journalctl -u
mariadb -n 15`).

`packaging/install.sh:111-112` — Der GPG-Schlüssel des Sury-PHP-Repos wird
per `curl` geladen und ohne Fingerprint-/Prüfsummenabgleich sofort als
vertrauenswürdig eingebunden. — Sicherheit — Einen bekannten Fingerprint des
Sury-Schlüssels im Skript hinterlegen und nach dem Download gegenprüfen.

`cmd/volt-agent/main.go:43-52` — Kein restriktiver Prozess-`umask` vor
`srv.Listen()`; der Unix-Socket wird per `net.Listen` erzeugt und die Rechte
erst danach per `os.Chmod(0o660)` gesetzt — dazwischen ein kurzes Zeitfenster
mit der ererbten Prozess-umask. — Sicherheit (geringes Zeitfenster) — Früh
`syscall.Umask(0o177)` setzen (oder `UMask=0177` in der systemd-Unit).

`packaging/systemd/volt-backup.service`, `packaging/systemd/volt-renew.service`
— Laufen zwar unprivilegiert als `User=volt`, verzichten aber komplett auf
die Sandboxing-Direktiven, die `volt-web.service` bereits nutzt
(`NoNewPrivileges`, `ProtectSystem`, `ProtectHome`, `RestrictNamespaces`
usw.). — Design — Dieselben Hardening-Zeilen ergänzen.

`scripts/build-pages.sh:53,55` — Nutzt feste, vorhersagbare Pfade
`/tmp/other-latest.json`/`/tmp/other-latest.sig` statt `mktemp`, obwohl
andere Skripte im selben Projekt korrekt `mktemp`/`mktemp -d` verwenden. —
Sicherheit (CI-Kontext) — In einer geteilten CI-Umgebung ließe sich über
einen vorab angelegten Symlink das Ziel der `mv`-Operation beeinflussen.
Fix: auf `mktemp` umstellen.
