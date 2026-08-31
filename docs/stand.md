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
- `volt update`: Snapshot, Prüfsummenvergleich, atomarer Tausch, Migration,
  automatischer Rollback bei Fehler, `--check` und `--dry-run`

**Phase 1 — Core & Dashboard**

- Session-Auth mit TOTP-2FA, Ratelimit je IP, Kontosperre, Passwort-Policy
- Audit-Log für jede schreibende Aktion, inklusive fehlgeschlagener Logins
- Metrik-Collector (CPU je Kern, Load, RAM, Swap, Disk-Mounts, Netzdurchsatz)
  mit WebSocket-Stream und Verlauf für neue Verbindungen
- Dashboard: Ring-Gauges, Kennzahl-Kacheln, Traffic-Chart mit Fadenkreuz und
  Tabellensicht, Disk-Balken je Mount
- Dienstverwaltung: Liste, Start/Stop/Restart/Reload/Enable/Disable
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

**Phase 3 — Daten, Dateien, Cronjobs**

- MySQL/MariaDB: Datenbanken und Benutzer anlegen, Rechte in drei Stufen
  (ALL, READWRITE, READONLY), Passwort setzen und anzeigen, Live-Größen,
  Dump und Import. Der Agent spricht direkt über den Unix-Socket mit MariaDB,
  nicht über eine Shell.
- File Manager: Auflisten, Lesen, Schreiben, Anlegen, Umbenennen, Kopieren,
  Löschen, Rechte setzen, Archivieren und Entpacken (tar.gz und zip),
  blockweiser Up- und Download für große Dateien, Editor im Browser
- Cronjobs: je Job eine Datei in /etc/cron.d, laufend unter dem Systembenutzer
  der Site, mit eigener Logdatei und Anzeige der letzten Läufe im Panel
- Backups: Datenbank plus Konfiguration plus Site-Dateien als tar.gz, mit
  Prüfsumme, Eintrag in der Datenbank, nächtlichem Timer und Restore mit
  automatischer Sicherheitskopie davor

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
- Mandanten sperren und entsperren; Löschen nur, wenn nichts mehr daran hängt
- Reduzierte Oberfläche für Kunden: keine Server-Dienste, keine
  Mandantenverwaltung

## Offen

**Phase 1**

- Web-Terminal (xterm.js). Bewusst zurückgestellt: eine Shell im Browser hebelt
  die Trennung zwischen Web und Agent auf und braucht ein eigenes Konzept.
- Prozessliste

**Phase 2**

- PHP-Extension-Manager (Erweiterungen installieren und aktivieren)
- Panel-Zertifikat über die Oberfläche beantragen (über die CLI geht es)
- HSTS-Schalter in der Oberfläche (im Datenmodell und Template vorhanden)

**Phase 3**

- SQL-Browser (oder phpMyAdmin als Plugin)
- FTP mit Pure-FTPd und virtuellen Benutzern
- Remote-Whitelist für Datenbankzugriffe von außen
- Backup-Ziele S3, B2, FTP
- Import einer hochgeladenen SQL-Datei über die Oberfläche
  (die Agent-Operation steht, die UI fehlt)

**Phase 4**

- Echte Dateisystem-Quotas (XFS/ext4 Project Quota). Die Grenzen wirken derzeit
  auf Anwendungsebene: eine Aktion über der Quota wird abgelehnt. Ein Prozess,
  der am Panel vorbei schreibt — etwa PHP-Code der Site selbst —, wird davon
  nicht gebremst. Dafür bräuchte es Mount-Optionen und Quota-Werkzeuge.
- Traffic-Zähler aus den Nginx-Access-Logs. Die Spalten und die Fortschreibung
  je Abrechnungszeitraum stehen, das Einlesen der Logs fehlt.
- Eigene Anmeldeseite und Domain für den Kundenbereich (die reduzierte
  Navigation steht bereits)

**Phasen 5–8**

Docker, Node.js, Git-Deploy, Mailserver, App Store und die fokussierte
Härtungsphase stehen vollständig aus.

## Bekannte Einschränkungen

- Der `beta`-Kanal ist nicht eingerichtet. GitHub führt Vorabversionen nicht
  unter „latest", die Weiterleitung von `get.…/stable/` greift für sie also
  nicht.
- Das Panel liefert beim ersten Start ein selbstsigniertes Zertifikat aus.
  `volt cert issue <panel_domain>` ersetzt es; die Übernahme braucht keinen
  Neustart.
- Die Speicherquota greift gegen den Stand der letzten Messung (stündlich).
  Zwischen zwei Messungen lässt sie sich also knapp überschreiten — der bewusste
  Preis dafür, dass nicht jeder Upload einen Verzeichnisdurchlauf auslöst.
- SSRF-Filterung für ausgehende Aufrufe fehlt (relevant ab Phase 5).
- Die Release-Signatur wird erzeugt, aber vom Client noch nicht geprüft — dort
  wirkt bisher nur der SHA-256-Vergleich.
- Getestet gegen Debian 12/13 und Ubuntu 24.04. RHEL-Derivate sind laut Roadmap
  ohnehin später vorgesehen.
