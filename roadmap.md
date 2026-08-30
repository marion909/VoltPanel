# VoltPanel — Roadmap

> Ein selbst gehostetes Linux Hosting Control Panel.
> Ein Binary. Ein Befehl zum Installieren. Ein Befehl zum Updaten.
> CLI: `volt` · Dienste: `volt-web`, `volt-agent`

```bash
bash <(curl -fsSL https://get.voltpanel.dev/install.sh)   # Install
volt update                                              # Update
```

---

## 1. Vision

Ein Panel im Stil von aaPanel/CloudPanel, aber:

- **ein einziges statisches Binary** (Frontend eingebettet) → Install und Update sind trivial
- **echtes Multi-Tenant** von Anfang an, nicht nachträglich drangeschraubt
- **alles aus Templates generiert** → jede Konfiguration ist reproduzierbar und wiederherstellbar
- **Plugin-/App-Store-System**, damit Features nicht den Kern aufblähen

## 2. Prinzipien

1. Nichts wird manuell gepatcht — Nginx/PHP/Postfix-Configs kommen immer aus Templates.
2. Jede Aktion ist idempotent und im Audit-Log.
3. Kein User-Input geht ungeprüft in eine Shell. Nie `sh -c`.
4. Update darf niemals eine bestehende Site zerschießen → Migrationen + Config-Backup vor jedem Update.
5. Lieber ein Feature weniger, dafür sauber isoliert.

## 3. Tech-Stack

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Backend | Go (Fiber oder Echo) | statisches Binary, gute Prozess-/Systemd-Anbindung |
| Frontend | Vue 3 + Tailwind, via `embed.FS` ins Binary | kein Node am Zielserver nötig |
| Panel-DB | SQLite (WAL-Mode) | keine externe Abhängigkeit beim Install |
| Hosted-DB | MariaDB/MySQL | für die gehosteten Sites |
| Privilegien | `volt-web` (unprivilegiert) + `volt-agent` (root) über Unix-Socket | XSS im Panel darf nicht Root bedeuten |
| ACME | lego (Go-Library) | HTTP-01 + DNS-01 (Cloudflare) im selben Prozess |
| Docker | Docker SDK for Go | kein Shell-Wrapping |
| Ziel-OS | Debian 12/13, Ubuntu 24.04 (x86_64 + arm64) | Sury-Repo für Multi-PHP; RHEL-Derivate später |
| Build/Release | GoReleaser + GitHub Actions | signierte Releases, Update-Kanal stable/beta |

### Architektur

```
                 ┌─────────────────────────────┐
   Browser  ───▶ │  volt-web   (User: volt)    │  HTTP/WebSocket, Auth, UI, API
                 └──────────────┬──────────────┘
                                │  Unix-Socket (JSON-RPC, authentifiziert)
                 ┌──────────────▼──────────────┐
                 │  volt-agent (root)          │  nginx, php-fpm, systemd, users,
                 │  - Whitelist an Operationen │  docker, mysql, certs, files
                 └─────────────────────────────┘
```

Der Agent kennt **keine** generischen Shell-Kommandos, nur typisierte Operationen
(`CreateVhost`, `ReloadNginx`, `CreateSystemUser`, …).

### Repo-Struktur

```
/cmd/volt        CLI + Web-Server
/cmd/volt-agent  Root-Daemon
/internal/core       Domain-Logik (sites, tenants, certs, …)
/internal/templates  nginx/, php-fpm/, systemd/, postfix/, dovecot/
/internal/store      SQLite + Migrationen
/web                 Vue-Frontend
/packaging           install.sh, systemd-Units, GoReleaser
```

### CLI

```
volt status | update | restart
volt user add|passwd|2fa-reset
volt site add example.at --php 8.3 --tenant 4
volt cert renew --all
volt backup create|restore
volt doctor            # Selbstdiagnose: Ports, Dienste, Rechte, Zertifikate
```

---

## 4. Phasen

### Phase 0 — Fundament (2–4 Wochen)

- [ ] Repo, Monorepo-Struktur, Lizenz, CI mit GoReleaser
- [ ] Datenmodell v1: `tenants, users, sites, php_pools, databases, db_users, certs, ftp_accounts, cronjobs, backups, audit_log, settings`
- [ ] Migrationsframework + Schema-Versionierung
- [ ] Agent/Web-Trennung inkl. Socket-Protokoll und Whitelist-Design
- [ ] `install.sh`: OS-Detection, Pakete, User anlegen, systemd-Units, Firewall-Port, Init-Passwort ausgeben
- [ ] `volt update`: Binary-Self-Update + Migrationen + Rollback bei Fehler

**DoD:** Auf einer frischen Debian-12-VM läuft nach einem Befehl ein Panel mit Login.

---

### Phase 1 — Core & Dashboard (4–6 Wochen)

- [ ] Auth: Session/JWT, TOTP-2FA, Login-Ratelimit, Passwort-Policy
- [ ] Audit-Log für jede schreibende Aktion
- [ ] Metrics-Collector: Load, CPU, RAM, Disk-Mounts, Netz (gopsutil) → WebSocket-Stream
- [ ] Dashboard-Layout wie Vorbild: Ring-Gauges, Overview-Cards (Sites/FTP/DB/Security), Traffic-Chart, Software-Kacheln
- [ ] Dienstverwaltung (start/stop/restart/enable), Prozessliste
- [ ] Web-Terminal (xterm.js über WebSocket, an Tenant gebunden)
- [ ] Dark Mode, i18n-Gerüst (DE/EN)

**DoD:** Panel zeigt den Server live, kann Dienste steuern und sich selbst updaten.

---

### Phase 2 — Websites, PHP, SSL (6–10 Wochen)

- [ ] Nginx-Vhost-Generator aus Templates, Reload nur nach `nginx -t`
- [ ] Site-Typen: Static, PHP, Reverse-Proxy
- [ ] Pro Site: eigener Linux-User + eigener PHP-FPM-Pool (`open_basedir`, `disable_functions`, Limits)
- [ ] Multi-PHP 7.4–8.4 über Sury, Extension-Manager, php.ini pro Site
- [ ] Log-Viewer (access/error), Rewrite-Editor mit Syntax-Check
- [ ] **SSL:** ACME über lego, HTTP-01 + **Cloudflare DNS-01** (Wildcard), Auto-Renew, Ablaufwarnung
- [ ] Cloudflare-Integration: API-Token pro Tenant verschlüsselt speichern, DNS-Record beim Site-Anlegen optional automatisch setzen
- [ ] Redirects, HSTS, Basic-Auth, IP-Sperren pro Site

**DoD:** Eigene Domains laufen produktiv über das Panel, Zertifikate erneuern sich ohne Zutun.

---

### Phase 3 — Daten, Dateien, FTP (4–6 Wochen)

- [ ] MySQL/MariaDB: DB + User + Grants, Remote-Whitelist, Import/Export, Größenanzeige
- [ ] SQL-Browser (oder phpMyAdmin als Plugin)
- [ ] File Manager: Path-Jail (Symlinks auflösen und prüfen!), Upload (chunked), Download, Archive, Rechte/Owner, Monaco-Editor
- [ ] FTP: Pure-FTPd mit virtuellen Usern aus der DB, TLS erzwungen, Quota
- [ ] Cronjobs pro Tenant (mit Logausgabe)
- [ ] Backups: Site + DB + Configs, Ziele lokal / S3 / B2 / FTP, Zeitplan, Restore-Test

**DoD:** Ein Kunde könnte damit arbeiten, ohne SSH zu brauchen.

---

### Phase 4 — Multi-Tenant / Multi-User (4–8 Wochen)

> Bewusst **vor** Docker/Node. Nachträglich `tenant_id` durch ein gewachsenes Schema zu ziehen ist eine Umschreibaktion.

- [ ] Rollen: Owner, Admin, Reseller, Kunde + granulare Permissions
- [ ] Jede Query erzwingt `tenant_id` (Repository-Layer, keine Ausnahmen)
- [ ] Quotas: Disk (XFS/ext4 Project Quota), Anzahl Sites/DBs/Mailboxen/FTP, Traffic-Zähler
- [ ] Hosting-Pakete/Pläne als Vorlage für Quotas
- [ ] Getrennter Kundenbereich mit reduzierter UI
- [ ] IDOR-Testsuite: jeder Endpoint wird mit fremder ID getestet

**DoD:** Zwei Tenants auf einem Server sehen und können nichts voneinander.

---

### Phase 5 — Docker, Node.js, Git-Deploy (6–8 Wochen)

- [ ] Docker: Container, Images, Volumes, Netzwerke, Logs, Stats, Exec
- [ ] Compose-Projekte anlegen/starten, Ports automatisch auf Nginx-Proxy mappen
- [ ] Node.js: Versionen via fnm, App = systemd-Unit + Reverse-Proxy, Auto-Restart, Log-Stream, ENV-Verwaltung
- [ ] Git-Deploy: Deploy-Keys, Webhook-Endpoint pro Site, Branch-Auswahl
- [ ] Build-Pipeline: definierbare Steps (`npm ci`, `npm run build`, `composer install`)
- [ ] Releases-Ordner + Symlink-Switch → Rollback per Klick

**DoD:** Ein Push auf `main` deployt eine Node- und eine PHP-Site automatisch, Rollback funktioniert.

---

### Phase 6 — Mailserver (8–12 Wochen, der Härtefall)

- [ ] Entscheidung vorab: **eigener Stack** (Postfix + Dovecot + Rspamd + OpenDKIM, virtuelle Domains aus DB) **oder Mailcow als Docker-Stack einbinden** und nur verwalten
- [ ] Multidomain: Domains, Mailboxen, Aliase, Catch-All, Weiterleitungen, Quota
- [ ] DKIM-Key-Generierung + automatische SPF/DKIM/DMARC-Records über die Cloudflare-Integration aus Phase 2
- [ ] Webmail (Roundcube/SnappyMail) als Plugin
- [ ] Autoconfig/Autodiscover für Thunderbird/Outlook
- [ ] Deliverability-Check im Panel: PTR, Blacklists, offene Relays, TLS

**DoD:** Mail an Gmail/Outlook landet im Posteingang, nicht im Spam.

---

### Phase 7 — App Store / Plugin-System (4–6 Wochen)

- [ ] Plugin-Format: Manifest (Name, Version, Abhängigkeiten, Hooks, benötigte Permissions) + Install/Uninstall/Update-Script + optionales UI-Bundle
- [ ] Stabile interne Plugin-API (erst jetzt sinnvoll — vorher bricht man ständig die eigenen Plugins)
- [ ] Signierte Pakete, eigenes Repo (statisches JSON + Tarballs)
- [ ] Erste Plugins: Redis, phpMyAdmin, Fail2ban-Manager, Backup-Tool, Webmail, WordPress-One-Click
- [ ] Später: Game-Server-Verwaltung als Plugin statt als zweites System

**DoD:** Ein Plugin lässt sich aus dem Store installieren, updaten und rückstandslos entfernen.

---

### Phase 8 — Härtung & Release (laufend, dann 4 Wochen fokussiert)

- [ ] Fail2ban-Integration, nftables-Firewall-UI, Port-Scan-Schutz
- [ ] Panel-Absicherung: eigener Port, Access-Key-Pfad, IP-Whitelist, optional nur über VPN
- [ ] Voll-Backup und Restore eines kompletten Tenants, Server-zu-Server-Migration
- [ ] Security-Review: Command-Injection, Path-Traversal, IDOR, SSRF (Cloudflare/Webhook-Calls), CSRF
- [ ] `volt doctor` + strukturierte Logs
- [ ] Doku-Site, Changelog, Update-Kanäle stable/beta
- [ ] Closed Beta mit 2–3 fremden Nutzern → erst danach öffentlich

---

## 5. Meilensteine

| Meilenstein | Inhalt | Nach Phase |
|---|---|---|
| **M1 — "Es lebt"** | Install per One-Liner, Login, Dashboard | 1 |
| **M2 — "Eigennutzung"** | eigene Domains produktiv, SSL automatisch | 2–3 |
| **M3 — "Fremdnutzung"** | Multi-Tenant, Quotas, Kundenbereich | 4 |
| **M4 — "Modern Stack"** | Docker, Node, Git-Deploy | 5 |
| **M5 — "Full Hosting"** | Mail funktioniert | 6 |
| **M6 — "Public v1.0"** | App Store, Härtung, Doku, Beta | 7–8 |

Realistisch nebenberuflich: **M2 nach ~4 Monaten, M3 nach ~7, v1.0 in 12–24 Monaten.**

## 6. Risiken

| Risiko | Gegenmaßnahme |
|---|---|
| Mailserver frisst Monate (Deliverability, Blacklists, PTR) | Mailcow-Variante ernsthaft prüfen; Phase 6 spät ansetzen |
| Root-Daemon = Totalschaden bei einem Bug | typisierte Whitelist-Operationen, kein Shell-Passthrough, Fuzzing der Socket-API |
| Multi-Tenant-Lecks (IDOR) | `tenant_id` im Repository-Layer erzwingen, automatisierte Cross-Tenant-Tests |
| Update zerschießt Configs | Config-Snapshot vor Update, Rollback, `--dry-run` |
| Scope-Explosion | alles Optionale wird Plugin, nicht Kern |
| Nginx-Reload mit kaputter Config | immer `nginx -t` vor Reload, sonst automatisches Zurückrollen |

## 7. Bewusst nicht im Scope (v1)

- Billing/Abrechnung, Reseller-Shop
- DNS-Server (nur Cloudflare-API-Anbindung)
- Windows-Server, Apache
- Kubernetes/Clustering, Load-Balancing über mehrere Nodes
- Eigene Mail-Filter-Engine

## 8. Nächste konkrete Schritte

1. Namen sichern: Domain, GitHub-Org, npm-Scope.
2. Datenbankschema für Phase 0–2 ausformulieren.
3. `install.sh` + Agent/Web-Trennung als Skelett bauen.
4. Nginx- und PHP-FPM-Templates schreiben und von Hand gegen eine Test-VM verifizieren.
5. Erst danach mit dem UI beginnen.

Domain: voltpanel.dev
Github: https://github.com/marion909/VoltPanel
NPM: https://www.npmjs.com/settings/marion808/staged-packages