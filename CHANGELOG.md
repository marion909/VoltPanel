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

Nichts — der letzte Stand ist veröffentlicht.

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
