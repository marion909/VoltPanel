# VoltPanel — Bugfix & Verbesserungsliste

Diese Datei wird laufend durch automatisierte Code-Analyse befüllt. Es werden
ausschließlich Code- und Design-Fixes eingetragen — keine Vorschläge, die
bestehende Funktionalität entfernen oder brechen würden.

Format je Fund: Datei:Zeile, Beschreibung, Einschätzung (Bug/Sicherheit/Design), Vorschlag.

Stand: alle bis einschließlich v0.4.129 gefundenen Punkte sind abgearbeitet
(siehe CHANGELOG.md für die einzelnen Einträge und `git log` für die
Begründung je Fix). Was folgt, sind keine offenen Aufgaben, sondern Funde,
die bewusst *nicht* umgesetzt wurden — mit der Abwägung dahinter, damit ein
künftiger Analyse-Lauf sie nicht erneut vorschlägt, ohne dass jemand den
Grund kennt.

## Bewusst nicht umgesetzt

`internal/agent/ops_files_ext.go` (`writeTarGz`/`writeZip`/`extractTarGz`/
`extractZip`) — ein gemeinsamer Walker mit Format-Callback wurde
vorgeschlagen, um die parallele Struktur der vier Funktionen zu bündeln. tar
(Symlinks per Header nachgebildet, `TypeFlag`-Switch, Streaming-API) und zip
(Symlinks ausgelassen, Datei-Liste statt Stream) unterscheiden sich in der
Verarbeitung genug, dass eine erzwungene gemeinsame Abstraktion in dieser
sicherheitskritischen Datei (Zip-Slip-Schutz, Größendeckel) mehr Risiko für
eine stille Verhaltensänderung birgt, als die reine Code-Dopplung wert ist.

`internal/core/tenant_bundle.go` (`CollectTenant`) — N+1 über drei
verschachtelte Ebenen (Site → FTP/PHP-Pool, Datenbank → Benutzer →
Herkunftsliste). SQLite ist lokales Datei-I/O statt eines
Netzwerk-Round-Trips, und `CollectTenant` läuft nur beim seltenen,
admin-ausgelösten Export/Import eines Mandanten, nie im Anfragepfad. Eine
Sammelabfrage über `site_id IN (...)`/`db_user_id IN (...)` bräuchte neue,
öffentliche Store-Methoden und damit eine größere API-Erweiterung des
`store`-Pakets für einen Pfad, der in der Praxis kaum ins Gewicht fällt.

`internal/core/databases.go` (`DeleteUser`) — bricht bei der ersten
fehlgeschlagenen Herkunft sofort ab und löscht die Store-Zeile dann gar
nicht, anders als `SetGrants`/`SetPassword`/`DeleteDatabase`, die
weiterlaufen und die Store-Änderung trotz einzelner Fehlschläge übernehmen.
Das anzugleichen wäre eine echte Verhaltensänderung an einem Lösch-Pfad
(das Panel würde einen Benutzer als gelöscht führen, auch wenn ein
MySQL-Konto auf einer Herkunft stehen bleibt) — und `DatabaseService.agent`
ist ein konkreter `*agent.Client`, kein Interface wie `SiteService.agent`,
es gibt also keinen Fake, um das an einem echten Test zu verifizieren. Eine
Umstellung ohne Testabsicherung wäre das falsche Risiko für diesen Fund.

`packaging/install.sh` — der GPG-Schlüssel des Sury-PHP-Repos wird per
`curl` geladen und ohne Fingerprint-Abgleich als vertrauenswürdig
eingebunden. Recherchiert: der `DEB.SURY.ORG Automatic Signing Key` hat laut
mehreren Support-Threads (GitHub-Issues, Foren) bereits mindestens einmal
ohne Vorankündigung rotiert/ist abgelaufen. Ein im Skript hinterlegter, hart
verglichener Fingerprint bräche dann bei der nächsten Rotation jede
Neuinstallation mit einer kryptischen Mismatch-Meldung, bis jemand das
Skript von Hand nachzieht — ein schlechterer, weil überraschenderer
Ausfallmodus als der heutige Zustand. Ein per Web-Recherche gefundener
Fingerprint ließ sich außerdem nicht gegen eine offizielle Sury-Quelle
absichern.

`internal/store/migrations/0001_init.sql` — die SQL-Fremdschlüssel
(`site_id INTEGER REFERENCES sites(id)`, …) prüfen nur die *Existenz* der
referenzierten Zeile, nicht deren `tenant_id`. Zusätzliche, optionale
Härtung wäre je betroffener Tabelle ein `CREATE TRIGGER ... BEFORE INSERT`,
der bei abweichendem `tenant_id` mit `RAISE(ABORT, ...)` ablehnt — die
Anwendungsebene prüft das an jeder bekannten Stelle bereits (v0.4.33);
dieser Trigger wäre nur die zusätzliche Absicherung gegen einen künftigen,
heute noch unbekannten Aufrufer ohne eigene Prüfung.

`internal/core/tenant_import.go` — der Zwischenstatus `TenantImporting`
(v0.4.82) sorgt dafür, dass ein mittendrin abgebrochener Import weder einen
erneuten Versuch mit demselben Bündel noch das Löschen über die API
dauerhaft blockiert. `store.DeleteTenant` räumt dabei aber weiterhin nur
DB-Zeilen weg, keine über den Agent angelegten Systemressourcen
(Linux-Benutzer, Vhosts, echte Datenbanken) eines abgebrochenen Imports —
ein vollständiger Teardown wäre ein deutlich größerer, eigener Eingriff
(eigene "Import abbrechen/aufräumen"-Funktion, die dieselben
Agent-Teardown-Schritte wie die regulären Lösch-Flows durchläuft).
