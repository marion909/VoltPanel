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

## Installation

Debian 12/13 oder Ubuntu 24.04, x86_64 oder arm64, Root-Zugang, freie Ports
80, 443 und 8443.

```bash
VOLT_PANEL_DOMAIN=panel.example.at \
VOLT_ACME_EMAIL=du@example.at \
bash <(curl -fsSL https://get.voltpanel.dev/install.sh)
```

Der Installer liest `latest.json` aus dem Kanal, lädt beide Binaries und prüft
jede Datei gegen die dort hinterlegte Prüfsumme. Stimmt sie nicht oder fehlt
sie, bricht er ab. Er richtet ein: nginx, PHP 8.3 aus dem Sury-Repo, MariaDB,
cron, Systembenutzer und Verzeichnisse, beide Dienste, Timer für
Zertifikatserneuerung und nächtliches Backup, Firewall-Freigaben. Ein zweiter
Durchlauf repariert eine unvollständige Installation, statt sie zu verdoppeln.

Am Ende stehen Panel-URL samt zufälligem Zugriffspfad und das erzeugte
Administratorpasswort in der Ausgabe. Beides kommt genau einmal.

Den ganzen Weg von der DNS-Zone über die Bezugsquelle bis zum ersten Release
beschreibt [docs/inbetriebnahme.md](docs/inbetriebnahme.md).

### Ohne Internet

```bash
make dist VERSION=0.1.0                     # am Entwicklungsrechner
scp dist/voltpanel_0.1.0_linux_amd64.tar.gz root@server:/tmp/
```

```bash
cd /tmp && tar xzf voltpanel_0.1.0_linux_amd64.tar.gz
cd voltpanel_0.1.0_linux_amd64
VOLT_LOCAL_DIR="$PWD" bash install.sh
```

`VOLT_LOCAL_DIR` überspringt den Download und nimmt die mitgelieferten
Binaries und Units. Alles danach ist identisch.

### Danach

```bash
sudo -u volt volt doctor                          Selbstdiagnose
sudo -u volt volt site add example.at --php 8.3   erste Website
sudo -u volt volt cert issue example.at           gültiges Zertifikat
```

`sudo -u volt`, weil die Dienste unter diesem Benutzer laufen und die
Datenbank ihm gehört. Ein Aufruf als root funktioniert auch — `volt` gibt die
Datenbankdateien danach wieder frei —, aber der Umweg ist der sauberere Weg.

Das Panel terminiert TLS selbst und startet mit einem selbstsignierten
Zertifikat. Sobald die Domain darauf zeigt, ersetzt es
**Einstellungen → Zertifikat des Panels** durch ein gültiges — auf der
Kommandozeile geht dasselbe mit `volt cert issue panel.example.at`. Beides
wirkt ohne Neustart: das Panel prüft bei jedem Handshake auf eine neuere
Datei.

In `/etc/volt/config.yaml` lohnt sich noch `ip_whitelist`, wenn nur bestimmte
Adressen ans Panel dürfen. Danach `systemctl restart volt-web`.

### Stellschrauben des Installers

| Variable | Wirkung |
|---|---|
| `VOLT_PANEL_DOMAIN` | Hostname des Panels — steht im Zertifikat |
| `VOLT_ACME_EMAIL` | Adresse für Let's Encrypt, ohne sie kein Zertifikat |
| `VOLT_PORT` | Port des Panels (Vorgabe 8443) |
| `VOLT_CHANNEL` | `stable` oder `beta` |
| `VOLT_SKIP_MARIADB=1` | MariaDB nicht mitinstallieren, weil sie woanders läuft |
| `VOLT_LOCAL_DIR` | Binaries und Units aus diesem Verzeichnis statt aus dem Netz |
| `VOLT_BASE_URL` | eigene Bezugsquelle statt `get.voltpanel.dev` |

### Wo die Bezugsquelle liegt

`get.voltpanel.dev` ist eine statische Seite auf GitHub Pages, die bei jedem
Release aus `scripts/build-pages.sh` neu entsteht. Sie liegt bewusst **nicht**
auf einem VoltPanel-Server: sonst bräuchte man ein laufendes Panel, um ein
Panel zu installieren, und ein Ausfall nähme einem die Möglichkeit, den
Ersatz aufzusetzen.

Dort liegen nur Textdateien — `install.sh`, `latest.json`, die systemd-Units.
Die Binaries bleiben am GitHub-Release, `latest.json` zeigt mit absoluten
Adressen dorthin. So gibt es keinen zweiten Ort, an dem eine veraltete Kopie
liegen bleiben kann.

### Aktualisieren

Über die Oberfläche unter *Einstellungen → Version und Aktualisierung*: das
Panel zeigt an, wenn der Kanal etwas Neueres führt, samt Release-Notes, und
ein Administrator kann das Update von dort auslösen. Der Web-Prozess tauscht
dabei nichts selbst — er ruft eine typisierte Operation beim Agent auf, und
die nimmt **keine Quelle entgegen**: welche Version kommt, steht allein im
Kanal aus der Konfiguration. Andernfalls wäre das Update die Hintertür, über
die eine Übernahme des Panels zu root führt.

Oder auf der Kommandozeile:

```bash
volt update
systemctl restart volt-agent volt-web
```

Getauscht werden beide Binaries — Panel und Agent müssen zur selben Version
gehören. Vorher entsteht ein Snapshot aus Binary, Datenbank und Konfiguration;
scheitert etwas, ist der alte Stand wieder da, bevor die Dienste neu starten.

## Stand der Umsetzung

| Phase | Inhalt | Stand |
|---|---|---|
| 0 | Fundament: Datenmodell, Migrationen, Agent/Web-Trennung, `install.sh`, `volt update` | **fertig** |
| 1 | Auth mit 2FA, Audit-Log, Metriken, Dashboard, Dienstverwaltung, Prozessliste, Web-Terminal, Dark Mode, i18n | **fertig** |
| 2 | Vhost-Generator, Site-Typen, PHP-FPM-Pools, PHP-Erweiterungen, HSTS, ACME mit HTTP-01 und Cloudflare-DNS-01 | **fertig** |
| 3 | MySQL, File Manager, FTP, Cronjobs, Backups | **weitgehend** (SQL-Browser und Backup-Ziele offen) |
| 4 | Rollen, Quotas, Kundenbereich | **weitgehend** (Disk-Quota auf Anwendungsebene, nicht im Dateisystem) |
| 5–7 | Docker, Node, Git-Deploy, Mail, App Store | offen |
| 8 | Härtung, Doku, Beta | laufend |

Was genau fehlt, steht in [docs/stand.md](docs/stand.md).

## Entwicklung

```bash
make build        # Frontend + beide Binaries nach ./bin
make dist         # Offline-Installationspakete nach ./dist
make test         # alle Tests
make lint         # gofmt und go vet
make dev          # Panel lokal gegen ./tmp starten
make dev-web      # daneben: Vite mit Hot Reload
```

Beim ersten Start:

```bash
VOLT_CONFIG=$PWD/tmp/etc/config.yaml ./bin/volt setup --email du@example.at
```

Voraussetzungen: Go 1.25+, Node 22+ (nur zum Bauen des Frontends). Die
genaue Untergrenze steht in `go.mod`; CI liest sie von dort, damit beide
nicht auseinanderlaufen.

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
volt db import wordpress dump.sql.gz  SQL-Datei einspielen (.sql oder .sql.gz)
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
- **Das Panel spricht nur TLS.** Es terminiert selbst statt hinter nginx —
  wer eine kaputte nginx-Config reparieren will, braucht das Panel gerade
  dann. Ohne gültiges Zertifikat erzeugt es beim ersten Start ein
  selbstsigniertes, damit zwischen Installation und `volt cert issue` kein
  Passwort im Klartext über die Leitung geht.
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
