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
  `volt doctor`
- Log-Viewer über `file.tail_log` (API vorhanden, UI noch nicht)

**Phase 3 — teilweise**

- Backups: Datenbank plus Konfiguration plus Site-Dateien als tar.gz, mit
  Prüfsumme, Eintrag in der Datenbank, nächtlichem Timer und Restore mit
  automatischer Sicherheitskopie davor

**Phase 4 — Grundlage**

- Rollen owner/admin/reseller/customer mit Rangordnung; niemand kann eine Rolle
  über der eigenen vergeben
- `tenant_id` im Repository-Layer erzwungen, Nullwert schlägt fehl
- IDOR-Testsuite über alle Leseoperationen
- Hosting-Pakete als Datenmodell, Site-Quota wird geprüft

## Offen

**Phase 1**

- Web-Terminal (xterm.js). Bewusst zurückgestellt: eine Shell im Browser hebelt
  die Trennung zwischen Web und Agent auf und braucht ein eigenes Konzept.
- Prozessliste

**Phase 2**

- PHP-Extension-Manager, php.ini-Editor pro Site in der UI
- Rewrite-Editor als Oberfläche (die Validierung dahinter steht)
- Redirects, HSTS-Schalter, Basic-Auth und IP-Sperren sind im Template
  umgesetzt, aber noch nicht über die API einstellbar
- Cloudflare-Token pro Tenant verschlüsselt speichern (die Verschlüsselung
  steht, die Verwaltung fehlt)

**Phase 3**

- MySQL/MariaDB-Verwaltung, SQL-Browser
- File Manager (Agent-Operationen stehen, UI fehlt)
- FTP mit Pure-FTPd und virtuellen Benutzern
- Cronjobs
- Backup-Ziele S3, B2, FTP

**Phase 4**

- Disk-Quotas über Project Quota, Traffic-Zähler
- Getrennter Kundenbereich mit reduzierter UI

**Phasen 5–8**

Docker, Node.js, Git-Deploy, Mailserver, App Store und die fokussierte
Härtungsphase stehen vollständig aus.

## Bekannte Einschränkungen

- Das Panel liefert beim ersten Start ein selbstsigniertes Zertifikat aus.
- SSRF-Filterung für ausgehende Aufrufe fehlt (relevant ab Phase 5).
- Die Release-Signatur wird erzeugt, aber vom Client noch nicht geprüft — dort
  wirkt bisher nur der SHA-256-Vergleich.
- Getestet gegen Debian 12/13 und Ubuntu 24.04. RHEL-Derivate sind laut Roadmap
  ohnehin später vorgesehen.
