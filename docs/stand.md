# Stand der Umsetzung

Ehrliche Liste dessen, was läuft und was nicht. Die Phasennummern folgen
[roadmap.md](../roadmap.md).

## Fertig

**Phase 0 — Fundament**

- Monorepo nach der Struktur der Roadmap, CI mit Tests, Lint, Cross-Build und
  Prüfung auf statisches Linken
- Schema v1 mit allen genannten Tabellen: `tenants, plans, users, sessions,
  sites, php_pools, databases, db_users, certs, ftp_accounts, cronjobs,
  backups, audit_log, settings`
- Migrationsframework, vorwärts-only, jede Migration in eigener Transaktion,
  Schema-Versionierung mit Abbruch bei zu neuer Datenbank
- Agent/Web-Trennung mit typisiertem Socket-Protokoll und Whitelist
- `install.sh`: OS- und Architekturerkennung, Sury-Repo, Benutzer, Verzeichnisse,
  systemd-Units, Firewall, Ersteinrichtung mit erzeugtem Passwort
- `volt update`: Snapshot, Prüfsummenvergleich, atomarer Tausch beider
  Binaries, Migration, automatischer Rollback bei Fehler, `--check` und
  `--dry-run`. Über die Oberfläche auslösbar: das Panel meldet neue Versionen
  samt Release-Notes, der Agent führt den Tausch aus und nimmt dafür keine
  Quelle entgegen.

**Phase 1 — Core & Dashboard**

- Session-Auth mit TOTP-2FA, Ratelimit je IP, Kontosperre, Passwort-Policy
- Audit-Log für jede schreibende Aktion, inklusive fehlgeschlagener Logins
- Metrik-Collector (CPU je Kern, Load, RAM, Swap, Disk-Mounts, Netzdurchsatz)
  mit WebSocket-Stream und Verlauf für neue Verbindungen
- Dashboard: Ring-Gauges, Kennzahl-Kacheln, Traffic-Chart mit Fadenkreuz und
  Tabellensicht, Disk-Balken je Mount
- Dienstverwaltung: Liste, Start/Stop/Restart/Reload/Enable/Disable
- Prozessliste aus /proc mit CPU, Speicher und Kommandozeile. Beenden lässt
  sich nur ein Prozess, der einer Site gehört — für Systemdienste ist die
  Dienstverwaltung zuständig. Ein Kunde sieht ausschließlich die Prozesse
  seiner eigenen Sites.
- Web-Terminal (xterm.js) je Site: die Shell läuft als unprivilegierter
  Systembenutzer der Site, nie als root. Vorerst Administratoren vorbehalten.
- Dark Mode (System/Hell/Dunkel), i18n-Gerüst mit DE und EN

**Phase 2 — Websites, PHP, SSL**

- Vhost-Generator aus Templates, Reload nur nach `nginx -t`, Rücknahme bei Fehler
- Site-Typen static, php, proxy
- Pro Site: eigener Linux-User, eigener FPM-Pool mit `open_basedir`,
  `disable_functions`, eigenem tmp- und Session-Verzeichnis
- ACME über lego: HTTP-01 über das Webroot, DNS-01 über Cloudflare für
  Wildcards, Auto-Renew per systemd-Timer, Ablaufwarnung im Dashboard und in
  `volt doctor`. Der Cloudflare-Token liegt verschlüsselt beim Mandanten und
  wird nie wieder herausgegeben — nur die Angabe, ob einer hinterlegt ist.
- Site-Einstellungen über Oberfläche und API: Weiterleitungen, IP-Sperren und
  -Freigaben, Passwortschutz (bcrypt, htpasswd außerhalb des Site-Verzeichnisses),
  eigene Nginx-Direktiven, maximale Anfragegröße, PHP-Zeitlimit
- PHP je Site einstellbar: Version, Prozessmanager, Prozesszahl, memory_limit,
  Ausführungszeit, Upload-Größe. `disable_functions` und eigene ini-Werte
  bleiben Administratoren vorbehalten — sie heben die Isolation der Site auf.
- Log-Viewer für Access- und Error-Log in der Site-Detailansicht
- HTTPS-Verhalten je Site über die Oberfläche: Umleitung erzwingen und HSTS.
  HSTS ohne Zertifikat wird abgelehnt — es würde die Site für jeden Browser,
  der sie einmal besucht hat, ein Jahr lang unerreichbar machen.
- Zertifikat des Panels über die Oberfläche anfordern. Es gilt sofort, ohne
  Neustart: der Webserver prüft bei jedem Handshake, welche Datei der Kette
  lesbar ist.
- PHP-Erweiterungen: installierte Module je Version anzeigen, ein- und
  ausschalten, neue über die Paketverwaltung nachrüsten. Der Paketname
  entsteht im Agent aus Version und Modulname — er kommt nie aus der Anfrage.

**Phase 3 — Daten, Dateien, Cronjobs**

- MySQL/MariaDB: Datenbanken und Benutzer anlegen, Rechte in drei Stufen
  (ALL, READWRITE, READONLY), Passwort setzen und anzeigen, Live-Größen.
  Der Agent spricht direkt über den Unix-Socket mit MariaDB, nicht über eine
  Shell.
- Export und Import über die Oberfläche: der Export erzeugt den Dump erst beim
  Abruf und lässt ihn nicht liegen. Der Import nimmt .sql und gzip-gepackte
  Dateien an — erkannt am Inhalt, nicht an der Endung — und läuft unter einem
  Wegwerf-Konto, das nur auf die Zieldatenbank Rechte hat. Ein "USE fremde_db"
  im Dump scheitert damit, statt als root durchzulaufen.
- File Manager: Auflisten, Lesen, Schreiben, Anlegen, Umbenennen, Kopieren,
  Löschen, Rechte setzen, Archivieren und Entpacken (tar.gz und zip),
  blockweiser Up- und Download für große Dateien, Editor im Browser
- FTP mit Pure-FTPd und virtuellen Benutzern: kein zusätzlicher Linux-Benutzer,
  jeder Zugang läuft unter dem Systembenutzer seiner Site und sitzt in seinem
  Verzeichnis fest. Verschlüsselung ist Pflicht (TLS 2), UID und GID schlägt
  der Agent selbst nach. Der Dienst wird nicht mitinstalliert, sondern auf
  Wunsch eingerichtet.
- SQL-Browser: Tabellenliste, Konsole und Ergebnisanzeige je Datenbank. Die
  Anweisung läuft nicht über die Root-Verbindung des Agents, sondern über ein
  Wegwerf-Konto, das nur auf diese eine Datenbank Rechte hat, und es geht genau
  eine Anweisung auf einmal. Jede steht gekürzt im Audit-Log.
- Datenbankzugriff von außen: je Datenbankbenutzer eine Herkunftsliste. Ein
  Eintrag ist eine IP-Adresse oder ein Netz, nie ein Hostname und nie %. Alle
  Konten eines Benutzers tragen dasselbe Passwort und dieselben Rechte; das
  Panel ändert sie zusammen. Ob MariaDB überhaupt im Netz horcht, ist eine
  eigene, serverweite Entscheidung des Administrators.
- Cronjobs: je Job eine Datei in /etc/cron.d, laufend unter dem Systembenutzer
  der Site, mit eigener Logdatei und Anzeige der letzten Läufe im Panel
- Backups: Datenbank plus Konfiguration plus Site-Dateien als tar.gz, mit
  Prüfsumme, Eintrag in der Datenbank, nächtlichem Timer und Restore mit
  automatischer Sicherheitskopie davor
- Backup-Ziele S3, B2 und FTP: eigener SigV4-Signierer und eigener
  FTPS-Client, beide ohne zusätzliche Abhängigkeit. Jeder ausgehende
  Verbindungsaufbau prüft die Adresse, mit der wirklich gesprochen wird —
  Loopback und link-local sind ausgeschlossen, damit ein Backup-Ziel nicht der
  Weg zum Metadaten-Dienst des Anbieters wird.

**Phase 4 — Multi-Tenant**

- Rollen owner/admin/reseller/customer mit Rangordnung; niemand kann eine Rolle
  über der eigenen vergeben
- `tenant_id` im Repository-Layer erzwungen, Nullwert schlägt fehl
- IDOR-Testsuite auf drei Ebenen: Repository, Service und HTTP
- Hosting-Pakete anlegen, zuordnen, als Standard markieren; ein neuer Mandant
  bekommt das Standardpaket automatisch
- Quotas für Websites, Datenbanken, Cronjobs, FTP-Zugänge und Speicherplatz;
  0 bedeutet überall "unbegrenzt", damit ein lückenhaft gepflegtes Paket keine
  stille Sperre ist
- Verbrauchsmessung stündlich im Hintergrund: belegte Blöcke statt nomineller
  Größe, Hardlinks nur einmal gezählt, Symlinks nicht verfolgt
- Traffic aus den Nginx-Access-Logs, im selben Takt. Gelesen wird ab einem
  Lesestand je Site, nicht von vorn; Logrotation wird an der Inode erkannt und
  die eben rotierte Datei nachgelesen. Gezählt werden nur vollständige Zeilen —
  eine halbe, die nginx gerade schreibt, käme sonst beim nächsten Lauf ein
  zweites Mal.
- Echte Dateisystem-Quotas über Project Quota, wo das Dateisystem sie führt:
  die Verzeichnisse eines Mandanten bekommen eine Projektnummer, die Grenze
  seines Pakets hängt daran, und der Kernel bremst damit auch, was am Panel
  vorbei schreibt — PHP-Code der Site, ein Upload über FTP, ein entpacktes
  Archiv. Ein Mandant ist ein Projekt, nicht eine Site: wer fünf Sites hat, hat
  eine Grenze über alle fünf. ext4 und XFS; wo die Mount-Option fehlt, bleibt es
  bei der Anwendungsebene, und das Panel sagt im Paketebereich, was dafür zu tun
  wäre. Automatisch geändert wird an /etc/fstab nichts.
- Mandanten sperren und entsperren; Löschen nur, wenn nichts mehr daran hängt
- Reduzierte Oberfläche für Kunden: keine Server-Dienste, keine
  Mandantenverwaltung

Phase 0 bis 2 sind damit abgeschlossen, Phase 4 bis auf den unten genannten
Punkt.

## Offen

**Phase 4 — Multi-Tenant**

- Eigene Anmeldeseite und Domain für den Kundenbereich (die reduzierte
  Navigation steht bereits)

**Phase 5 — Docker, Node.js, Git-Deploy**

- Docker: Container, Images, Volumes, Netzwerke, Logs, Stats, Exec
- Compose-Projekte anlegen und starten, Ports automatisch auf den Nginx-Proxy
  legen
- Node.js: Versionen über fnm; eine App ist eine systemd-Unit plus
  Reverse-Proxy, mit Auto-Restart, Log-Stream und ENV-Verwaltung
- Git-Deploy: Deploy-Keys, Webhook-Endpunkt je Site, Branch-Auswahl
- Build-Schritte definierbar (`npm ci`, `npm run build`, `composer install`)
- Releases-Verzeichnis mit Symlink-Wechsel, damit ein Rollback ein Klick ist

Die Hälfte, die den Verkehr betrifft, steht schon: der Site-Typ `proxy`
schreibt einen fertigen `proxy_pass`-Vhost, und `docker` steht auf der
Dienst-Whitelist des Agents. Auch die Unit-Vorlage für eine App liegt unter
`internal/templates/systemd/app.service.tmpl` — sie wird eingebettet und
geparst, aber von niemandem gerendert. Es fehlt alles dazwischen.

Zwei Dinge sind hier keine Fleißarbeit. Erstens Docker: ein Container mit
`--privileged` oder einem Bind-Mount auf `/` hebt jede Isolation auf, die
dieses Panel aufbaut. Die Operationen müssen also nicht nur "Container
starten" können, sondern vorgeben, was ein Container darf. Zweitens holt
Git-Deploy Daten von einer Adresse, die der Kunde bestimmt — genau der Fall,
für den die SSRF-Filterung fehlt.

**Phase 6 — Mailserver**

- Entscheidung vorab: eigener Stack (Postfix, Dovecot, Rspamd, OpenDKIM,
  virtuelle Domänen aus der Datenbank) oder Mailcow als Docker-Stack, den das
  Panel nur verwaltet
- Multidomain: Domänen, Postfächer, Aliase, Catch-All, Weiterleitungen, Quota
- DKIM-Schlüssel erzeugen, SPF-, DKIM- und DMARC-Einträge automatisch über die
  Cloudflare-Anbindung aus Phase 2 setzen
- Webmail (Roundcube oder SnappyMail) als Plugin
- Autoconfig und Autodiscover für Thunderbird und Outlook
- Deliverability-Prüfung im Panel: PTR, Blacklists, offene Relays, TLS

Vorbereitet ist zweierlei: `postfix`, `dovecot`, `rspamd` und `opendkim` stehen
bereits auf der Dienst-Whitelist, und der verschlüsselte Cloudflare-Token je
Mandant ist genau das, was die DNS-Einträge brauchen. Beides ist Vorarbeit,
keine Entscheidung.

Der schwierige Teil ist auch nicht der Code. Ob eine Mail bei Gmail im
Posteingang landet, hängt an PTR-Eintrag, Reputation der IP und daran, dass
kein Kunde über den Server Spam verschickt. Die Roadmap sagt dazu selbst:
Mailcow-Variante ernsthaft prüfen, Phase 6 spät ansetzen. Solange das nicht
entschieden ist, wäre jede Zeile Mailserver-Code eine Wette.

**Phase 7 — App Store und Plugin-System**

- Plugin-Format: Manifest mit Name, Version, Abhängigkeiten, Hooks und
  benötigten Rechten, dazu Install-, Uninstall- und Update-Skript und ein
  optionales UI-Bundle
- Stabile interne Plugin-API
- Signierte Pakete, eigenes Repository aus statischem JSON und Tarballs
- Erste Plugins: Redis, phpMyAdmin, Fail2ban-Verwaltung, Backup-Werkzeug,
  Webmail, WordPress mit einem Klick
- Später Game-Server-Verwaltung als Plugin statt als zweites System

Die Auslieferung existiert schon in der richtigen Form: `get.voltpanel.dev` ist
statisches JSON plus Tarballs mit Prüfsummen — dieselbe Bauart, die ein
Plugin-Repository braucht. Was dort fehlt, fällt hier aber ins Gewicht: die
cosign-Signatur wird erzeugt und von niemandem geprüft. Für das eigene Update
ist das ein Mangel, für fremden Code wäre es fahrlässig.

Die Reihenfolge ist Absicht. Eine stabile Plugin-API lohnt sich erst, wenn der
Kern sich nicht mehr täglich bewegt — sonst bricht jede Änderung die eigenen
Plugins. Zwei offene Punkte anderer Phasen hängen daran: phpMyAdmin aus
Phase 3 und Webmail aus Phase 6 sind in der Roadmap Plugins, keine
Kernfunktionen.

**Phase 8 — Härtung und Release**

Diese Phase läuft nebenher mit und ist deshalb teilweise erledigt.

Steht bereits:

- Panel-Absicherung: eigener Port, nicht erratbarer Zugriffspfad,
  IP-Whitelist
- `volt doctor` mit Prüfungen zu Schema, Pfaden, Agent, Diensten, Port,
  gemeinsamer Config, Benutzersperren und Zertifikaten; strukturierte Logs
- Update-Kanäle stable und beta sind in der Konfiguration angelegt und werden
  geprüft; ausgeliefert wird bisher nur stable
- Vom Security-Review sind Command-Injection, Path-Traversal, IDOR und CSRF
  umgesetzt und mit Tests belegt — siehe [sicherheit.md](sicherheit.md)

Offen:

- SSRF-Filterung für ausgehende Aufrufe. Heute betrifft das die Cloudflare-API,
  ab Phase 5 auch Git-Quellen und Webhooks.
- Fail2ban-Anbindung und eine Oberfläche für die Firewall. Derzeit öffnet
  `install.sh` Ports in ufw, sofern ufw überhaupt läuft; bei nftables gibt es
  eine Warnung und sonst nichts. Port-Scan-Schutz fehlt ganz.
- Voll-Backup und Restore eines einzelnen Mandanten, dazu Migration von Server
  zu Server. Das Backup ist heute serverweit — für einen Umzug ist das zu
  grob.
- Doku-Site und Changelog
- Closed Beta mit zwei bis drei fremden Nutzern, erst danach öffentlich

## Bekannte Einschränkungen

- Die Release-Signatur wird über cosign erzeugt, aber weder von `install.sh`
  noch von `volt update` geprüft. Dort wirkt bisher nur der SHA-256-Vergleich
  gegen `latest.json`.
- Das Panel liefert beim ersten Start ein selbstsigniertes Zertifikat aus.
  Einstellungen → Zertifikat des Panels ersetzt es, `volt cert issue
  <panel_domain>` ebenso; die Übernahme braucht keinen Neustart.
- Die Speicherquota greift gegen den Stand der letzten Messung (stündlich).
  Zwischen zwei Messungen lässt sie sich also knapp überschreiten — der bewusste
  Preis dafür, dass nicht jeder Upload einen Verzeichnisdurchlauf auslöst.
- SSRF-Filterung für ausgehende Aufrufe fehlt (relevant ab Phase 5).
- Das Web-Terminal ist Administratoren vorbehalten. Es für Kunden zu öffnen
  wäre vertretbar — ein Cronjob derselben Site läuft unter demselben Konto —,
  ist aber eine eigene Entscheidung und keine Nebenwirkung.
- Das Web-Terminal lässt sich nur auf einem Linux-Server wirklich erproben:
  das Pseudoterminal braucht /dev/ptmx, das Fallenlassen der Rechte braucht
  root. Die Prüfungen davor und die ioctls laufen im Test, die Shell selbst
  erst auf dem Zielsystem.
- Getestet gegen Debian 12/13 und Ubuntu 24.04. RHEL-Derivate sind laut Roadmap
  ohnehin später vorgesehen.
