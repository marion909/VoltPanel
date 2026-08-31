# VoltPanel

Ein selbst gehostetes Linux Hosting Control Panel. Ein Binary. Ein Befehl zum
Installieren. Ein Befehl zum Updaten.

```bash
bash <(curl -fsSL https://get.voltpanel.dev/install.sh)   # Install
volt update                                              # Update
```

CLI: `volt` · Dienste: `volt-web`, `volt-agent`

---

## Was es ist

Ein Panel im Stil von aaPanel oder CloudPanel, aber mit drei Entscheidungen,
die von Anfang an feststehen:

- **Ein einziges statisches Binary.** Das Vue-Frontend liegt per `embed.FS` im
  Binary, SQLite ist ein reiner Go-Treiber. Kein Node, kein cgo, keine
  Laufzeitabhängigkeiten am Zielserver.
- **Echtes Multi-Tenant.** `tenant_id` wird im Repository-Layer erzwungen, nicht
  in den Handlern. Ein vergessener Scope liefert einen Fehler, keine fremden
  Daten.
- **Alles aus Templates.** Jede Nginx- und PHP-FPM-Config entsteht aus dem
  Datenbankstand. `volt site rebuild --all` erzeugt den kompletten
  Webserver-Zustand neu.

## Architektur

```
                 ┌─────────────────────────────┐
   Browser  ───▶ │  volt-web   (User: volt)    │  HTTP/WebSocket, Auth, UI, API
                 └──────────────┬──────────────┘
                                │  Unix-Socket (JSON, SO_PEERCRED-geprüft)
                 ┌──────────────▼──────────────┐
                 │  volt-agent (root)          │  nginx, php-fpm, systemd,
                 │  - Whitelist an Operationen │  users, certs, files
                 └─────────────────────────────┘
```

Der Agent kennt **keine** generischen Shell-Kommandos. Er kennt eine feste Liste
typisierter Operationen (`nginx.write_vhost`, `user.create`, `file.write`, …),
jede mit eigener Parameterstruktur und eigener Validierung. Was nicht in
[`internal/agent/protocol.go`](internal/agent/protocol.go) steht, kann der
Web-Prozess nicht auslösen — auch dann nicht, wenn er vollständig übernommen
wurde.

Details: [docs/architektur.md](docs/architektur.md) ·
[docs/sicherheit.md](docs/sicherheit.md)

## Stand der Umsetzung

| Phase | Inhalt | Stand |
|---|---|---|
| 0 | Fundament: Datenmodell, Migrationen, Agent/Web-Trennung, `install.sh`, `volt update` | **fertig** |
| 1 | Auth mit 2FA, Audit-Log, Metriken, Dashboard, Dienstverwaltung, Dark Mode, i18n | **fertig** (ohne Web-Terminal) |
| 2 | Vhost-Generator, Site-Typen, PHP-FPM-Pools, ACME mit HTTP-01 und Cloudflare-DNS-01 | **fertig** (ohne PHP-Extension-Manager) |
| 3 | MySQL, File Manager, Cronjobs, Backups | **weitgehend** (FTP und SQL-Browser offen) |
| 4 | Rollen, Quotas, Kundenbereich | **weitgehend** (Disk-Quota auf Anwendungsebene, nicht im Dateisystem) |
| 5–7 | Docker, Node, Git-Deploy, Mail, App Store | offen |
| 8 | Härtung, Doku, Beta | laufend |

Was genau fehlt, steht in [docs/stand.md](docs/stand.md).

## Entwicklung

```bash
make build        # Frontend + beide Binaries nach ./bin
make test         # alle Tests
make lint         # gofmt und go vet
make dev          # Panel lokal gegen ./tmp starten
make dev-web      # daneben: Vite mit Hot Reload
```

Beim ersten Start:

```bash
VOLT_CONFIG=$PWD/tmp/etc/config.yaml ./bin/volt setup --email du@example.at
```

Voraussetzungen: Go 1.24+, Node 22+ (nur zum Bauen des Frontends).

## CLI

```
volt status                              Version, Dienste, Bestand
volt doctor                              Selbstdiagnose: Ports, Rechte, Schema, Zertifikate
volt update [--check|--dry-run]          Selbst-Update mit Snapshot und Rollback

volt site add example.at --php 8.3       Site anlegen (User, Pool, Vhost, Verzeichnisse)
volt site rebuild --all                  Configs aus dem Datenbankstand neu erzeugen
volt site remove example.at [--purge]

volt cert issue example.at               Zertifikat über HTTP-01
volt cert issue '*.example.at' --cloudflare-token …    Wildcard über DNS-01
volt cert renew --all

volt user add kunde@example.at --role customer --tenant 4
volt user passwd kunde@example.at
volt user 2fa-reset kunde@example.at

volt backup create [--all-sites]
volt backup restore <archiv>

volt db add wordpress --tenant 4      Datenbank samt Benutzer anlegen
volt db list                          mit Live-Größe und Rechten
volt db dump wordpress                SQL-Dump ins Backup-Verzeichnis
volt db passwd alice_wp               Passwort neu setzen

volt cron add scheduler --site example.at \
  --schedule "* * * * *" --command "/usr/bin/php8.3 …/artisan schedule:run"
volt cron list | log <id> | sync

volt plan add Klein --sites 5 --databases 5 --disk 5000 --default
volt plan list                        0 bedeutet überall "unbegrenzt"
volt tenant add "Kunde Meier"         bekommt das Standardpaket
volt tenant set-plan 2 1              Paket zuordnen (0 = keines)
volt tenant usage [id]                Verbrauch gegen die Grenzen
volt tenant suspend 2 [--resume]
```

## Sicherheit

Kurzfassung — ausführlich in [docs/sicherheit.md](docs/sicherheit.md):

- **Kein `sh -c`.** Der Agent führt nur Binaries von einer festen Liste aus, mit
  explizitem argv. Es existiert kein Codepfad, auf dem User-Input von einem
  Interpreter zerlegt wird.
- **Peer-Prüfung statt Token.** Der Agent liest über `SO_PEERCRED` beim Kernel
  nach, welcher Systembenutzer verbunden ist. Ein Token wäre schwächer: wer den
  Socket lesen darf, könnte auch die Token-Datei lesen.
- **Pfad-Gefängnis mit Symlink-Auflösung.** Jeder Pfad wird aufgelöst, bevor er
  gegen die erlaubten Wurzeln geprüft wird — sonst zeigt
  `/var/www/site/link` auf `/etc/shadow` und die Präfixprüfung merkt nichts.
- **Config-Injection ausgeschlossen.** `text/template` escaped nichts, deshalb
  wird jeder Wert vor dem Einsetzen validiert. Getestet gegen Domains mit
  Zeilenumbrüchen, Proxy-Ziele mit Semikolon und Zusatzzeilen, die den
  Server-Block aufbrechen wollen.
- **Nginx wird nie ungeprüft neu geladen.** Schreiben, `nginx -t`, und bei einem
  Fehler zurück auf den letzten funktionierenden Stand.
- **Argon2id** für Passwörter, Sessions nur als SHA-256-Hash gespeichert,
  TOTP-Secrets und API-Tokens mit AES-256-GCM verschlüsselt.
- **Zwei Gefängnisse für Dateien.** Der Agent sperrt auf `/var/www` — das
  trennt aber keine Kunden voneinander. Der Dateimanager arbeitet deshalb nie
  mit absoluten Pfaden, sondern mit `site_id` plus relativem Pfad, und prüft
  die Site gegen den Tenant-Scope.
- **Keine SQL-Identifier aus Benutzereingaben.** DDL lässt sich nicht
  parametrisieren, deshalb passieren Datenbank- und Benutzernamen eine sehr
  enge Whitelist. Eine Eingabe mit Sonderzeichen wird abgelehnt, nicht
  stillschweigend umgeschrieben.
- **Archive brechen nicht aus.** Jeder Eintrag wird einzeln gegen das
  Zielverzeichnis geprüft (Zip-Slip), Symlinks im Archiv werden übersprungen,
  und die entpackte Menge ist gedeckelt.
- **IDOR-Testsuite.** Jede Leseoperation wird mit einer fremden Tenant-ID
  aufgerufen und muss scheitern — auf Repository-, Service- und API-Ebene.

Eine Lücke gefunden? Bitte per E-Mail statt als öffentliches Issue.

## Lizenz

MIT — siehe [LICENSE](LICENSE).
