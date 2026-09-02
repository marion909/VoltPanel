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
  systemd-Units, Firewall, Ersteinrichtung mit erzeugtem Passwort. Vor dem
  ersten Binary prüft er die Signatur über `latest.json` — er läuft als root,
  und der Prüfsummenvergleich allein hilft dort so wenig wie beim Update: die
  Summe steht in derselben Datei, die von derselben Adresse kommt. Der
  Schlüssel wird beim Veröffentlichen aus `internal/release/release.pub`
  eingesetzt, also aus derselben Datei, die im Binary steckt.
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
- Eigene Anmeldedomain je Mandant. Dort trägt die Anmeldeseite seinen Namen,
  das Panel liegt unter `/` statt hinter dem Zugriffspfad des Betreibers — und
  herein kommt nur, wer zu diesem Mandanten gehört. Das Zertifikat für diesen
  Namen wählt das Panel im Handshake selbst; holen lässt es sich mit einem
  Klick über dieselbe ACME-Strecke wie jedes andere.
- Ein gesperrter Mandant kommt nicht mehr herein. Bis dahin setzte „sperren"
  nur ein Feld: die Oberfläche zeigte ihn als gesperrt, seine Leute meldeten
  sich weiter an. Den eigenen Mandanten zu sperren lehnt das Panel ab — das
  wäre der kürzeste Weg, sich selbst auszusperren.

**Phase 5 — Docker, Node.js, Git-Deploy**

- Eine App ist eine systemd-Unit plus Reverse-Proxy: Laufzeitumgebung,
  Argumente und Umgebungsvariablen über die Oberfläche, Auto-Restart, Port und
  Unit-Name vom Panel vergeben. Die App läuft als Systembenutzer ihrer Site in
  einer eng gefassten Unit; die Umgebung liegt verschlüsselt in der Datenbank
  und auf dem Server in einer Datei mit 0640, nicht in der Unit.
- Node-Fassungen nebeneinander, systemweit unter `/opt/volt/node`. Geholt vom
  offiziellen Archiv, gegen die dortige Prüfsumme geprüft und mit eigenem Code
  ausgepackt statt mit `tar` — Pfade und Symlinks, die aus dem Verzeichnis
  führen, kommen nicht durch. Eine App wählt eine Fassung als „node22". Eine
  Fassung, auf der noch eine App läuft, lässt sich nicht entfernen.
- Eine App läuft wahlweise als systemd-Unit oder als Container. Beide belegen
  denselben Port, beide bekommen den Vhost auf sich gerichtet; der Unterschied
  ist eine Spalte, nicht eine zweite Hälfte des Panels. Der Container läuft
  unter der Kennung der Site, ohne jede Capability, mit Speicher- und
  CPU-Grenze und nur auf 127.0.0.1 erreichbar. Es gibt kein Eingabefeld für
  einen docker-Schalter — die Kommandozeile baut der Agent.
- Auslastung und Images. An der App steht, was ihr Container gerade zieht —
  CPU, belegter Speicher gegen die eigene Grenze. Daneben die Liste dessen, was
  auf der Platte liegt, mit der Angabe, welche App darauf zeigt; entfernen
  lässt sich nur, worauf keine zeigt, und erzwungen wird dabei nichts. Die
  Liste gilt für den ganzen Server und ist deshalb Administratoren
  vorbehalten — ein Image trägt keinen Mandanten.
- Git-Deploy je Site: Deploy-Key, Branch-Auswahl, Buildschritte aus einer
  festen Liste, Releases-Verzeichnis mit Symlink-Wechsel und Rollback per
  Klick. Der Webhook-Endpunkt liegt außerhalb des Zugriffspfads und weist sich
  über eine HMAC-Signatur aus; das Protokoll des letzten Laufs steht im Panel,
  auch wenn er fehlgeschlagen ist.

Phase 0 bis 2, Phase 4 und Phase 5 sind damit abgeschlossen — bei Phase 5
steht unter „Offen" nur noch, was dort mit Absicht steht.

## Offen

**Phase 5 — Docker, Node.js, Git-Deploy**

Bewusst nicht gebaut — offen, aber nicht als Rückstand gemeint:

- Compose-Projekte. Eine Compose-Datei kommt aus dem Repository des Kunden, und
  in ihr stünden genau die Schalter wieder, die im Panel nicht vorgesehen sind.
- `docker exec`. Für eine Shell gibt es das Terminal der Site, das unter
  derselben Kennung läuft und über dieselbe Whitelist.
- Eigene Netzwerke und benannte Volumes. Bisher nur Bind-Mounts aus dem
  Verzeichnis der Site — mehr braucht keine der Anwendungen, um die es hier
  geht.

Alles andere steht (oben unter „Fertig").

Statt fnm liegen die Node-Fassungen systemweit unter `/opt/volt/node`. fnm
installiert je Benutzer, und in diesem Panel hat jede Site einen eigenen —
dieselbe Fassung läge dann zwanzigmal auf der Platte. Das System-Node bleibt
als „node" wählbar.

**Phase 6 — Mailserver** (angefangen)

Entschieden und gebaut ist der Unterbau: eigener Stack — Postfix, Dovecot,
Rspamd, OpenDKIM — mit virtuellen Domänen aus der Datenbank. Die Roadmap ließ
Mailcow als Alternative offen; die Entscheidung ist gegen sie gefallen: ein
Docker-Stack, den das Panel nur verwaltet, wäre ein zweites Panel mit eigener
Datenhaltung und eigener Vorstellung davon, wem was gehört.

Es steht: Domänen, Postfächer, Weiterleitungen, Catch-All und DKIM in
Datenbank, Dienst, API und Oberfläche. Dazu die Konfiguration, ohne die das
alles nur in Dateien stünde: Dovecot kennt die Passwortdatei des Panels,
Postfix fragt Dovecot nach dem Ausweis eines Absenders, TLS kommt aus dem
Zertifikat des Panels, und eingeliefert wird auf 587 — nur verschlüsselt und
nur mit Ausweis. Der Agent schreibt daraus die Map-Dateien für
Postfix und die Passwortdatei für Dovecot; die beiden lesen die Panel-Datenbank
nie. Ein gesperrter Mandant nimmt keine Post mehr an, ein Catch-All zeigt nur
auf ein eigenes Postfach, und Postfächer zählen zum Paket.

Offen ist der Rest:

- SPF-, DKIM- und DMARC-Einträge automatisch über die Cloudflare-Anbindung aus
  Phase 2 setzen. Den DKIM-Eintrag zeigt das Panel; ins DNS trägt ihn noch von
  Hand, wer die Domäne verwaltet.
- Rspamd einrichten — bisher richtet `mail.setup` Postfix, den Mailspeicher und
  den OpenDKIM-Milter ein, aber keine Spamprüfung.
- Quota wirklich durchsetzen: die Regel steht in der Dovecot-Datei, geprüft ist
  sie noch nicht auf einem laufenden Server.
- Webmail (Roundcube) als Plugin
- Autoconfig und Autodiscover für Thunderbird und Outlook
- Deliverability-Prüfung im Panel: PTR, Blacklists, offene Relays, TLS

Eine Grenze gehört benannt: alle Maildirs gehören einem Benutzer (`vmail`), so
wie es bei einem virtuellen Mailserver üblich ist. Die Trennung zwischen zwei
Mandanten liegt damit in Dovecot, nicht im Dateisystem — bei den Websites ist
es umgekehrt, dort hat jede Site ihre eigene Kennung.

Der schwierige Teil ist auch nicht der Code. Ob eine Mail bei Gmail im
Posteingang landet, hängt an PTR-Eintrag, Reputation der IP und daran, dass
kein Kunde über den Server Spam verschickt. Daran ändert die Entscheidung für
den eigenen Stack nichts — sie sagt nur, womit gebaut wird, nicht dass es schon
losgeht. Die Roadmap setzt Phase 6 spät an, und dabei bleibt es.

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
- Einen Mandanten umziehen: `volt tenant export` packt seine Zeilen, Dateien
  und Datenbankauszüge in ein Bündel, `volt tenant import` legt ihn auf einem
  anderen Server neu an. Die Geheimnisse darin werden auf eine Passphrase
  umgeschlüsselt — der Schlüssel des Quellservers wäre auf dem Zielserver
  unlesbar, und im Klartext wäre das Bündel eine Datei mit allen Passwörtern
  des Mandanten.
- `volt tenant import` stellt den Mandanten auch auf dem Server her, nicht
  nur in der Datenbank: Linux-Benutzer, Vhost, FPM-Pool, Cron-Dateien,
  FTP-Zugänge und Units entstehen im selben Lauf. Vorher stand dort ein
  Hinweis, man möge `volt site rebuild --all` nachschieben — und ein Hinweis
  ist kein Zustand. Was dabei nicht gelingt, steht als Warnung am Ende und
  bricht den Import nicht ab: die Daten liegen dann schon, und ein Abbruch
  ließe einen halben Mandanten zurück.
- Firewall und Fail2ban in der Oberfläche: ufw-Regeln setzen und entfernen,
  Jails und gesperrte Adressen ansehen, eine Sperre aufheben. Die Regel kommt
  in Teilen über die Leitung, nicht als Text — es gibt kein Feld für eine
  Quelladresse oder eine Kette. nftables wird nur gelesen: in ein gewachsenes
  Regelwerk lässt sich keine Zeile gefahrlos einfügen, ohne zu wissen, wie es
  aufgebaut ist.
- Port-Scan-Erkennung: fail2ban zählt die von ufw abgewiesenen Pakete je
  Adresse und sperrt für alle Ports — wer der Reihe nach anklopft, sucht nicht
  einen bestimmten Dienst. Drei Stufen statt dreier Zahlenfelder, Filter und
  Jail aus Vorlagen; es gibt kein Feld für einen regulären Ausdruck und keines
  für eine Aktion, denn ein fail2ban-Filter ist ausführbare Konfiguration. Die
  Adressen aus `ip_whitelist` sind ausgenommen. Nimmt fail2ban die Regel nicht
  an, wird sie zurückgenommen — eine Datei, die es nicht versteht, nähme sonst
  den ganzen Dienst mit, auch die Jails, die vorher liefen.
- `volt doctor` mit Prüfungen zu Schema, Pfaden, Agent, Diensten, Port,
  gemeinsamer Config, Benutzersperren und Zertifikaten; strukturierte Logs
- Update-Kanäle stable und beta sind in der Konfiguration angelegt und werden
  geprüft; ausgeliefert wird bisher nur stable
- Vom Security-Review sind Command-Injection, Path-Traversal, IDOR und CSRF
  umgesetzt und mit Tests belegt — siehe [sicherheit.md](sicherheit.md)

Offen:

- SSRF-Filterung für ausgehende Aufrufe. Für Backup-Ziele steht sie
  (`internal/transfer`, eigener Dialer mit Adressprüfung), für Git-Quellen
  ebenfalls (Loopback und Link-Local abgewiesen, private Netze erlaubt), und
  für die Cloudflare-API ist der Host fest. Was fehlt, ist die Auflösung von
  Namen: ein Hostname, der auf 169.254.169.254 zeigt, geht durch. Dagegen hilft
  nur ein Proxy, der die aufgelöste Adresse prüft.
- Doku-Site. Der Changelog steht (CHANGELOG.md, und der Abschnitt einer
  Fassung wird zu den Release-Notes im Panel); eine eigene Seite mit Anleitungen
  gibt es noch nicht.
- Closed Beta mit zwei bis drei fremden Nutzern, erst danach öffentlich

## Bekannte Einschränkungen

- Der Release-Schlüssel selbst ist im Quelltext leer. Er entsteht beim
  Einrichten der Veröffentlichung; bis dahin lehnt `volt update` jeden Kanal
  ab, statt ungeprüft zu aktualisieren. Siehe [release.md](release.md).
- Das Panel liefert beim ersten Start ein selbstsigniertes Zertifikat aus.
  Einstellungen → Zertifikat des Panels ersetzt es, `volt cert issue
  <panel_domain>` ebenso; die Übernahme braucht keinen Neustart.
- Die Speicherquota greift gegen den Stand der letzten Messung (stündlich).
  Zwischen zwei Messungen lässt sie sich also knapp überschreiten — der bewusste
  Preis dafür, dass nicht jeder Upload einen Verzeichnisdurchlauf auslöst.
- Das Web-Terminal ist Administratoren vorbehalten. Es für Kunden zu öffnen
  wäre vertretbar — ein Cronjob derselben Site läuft unter demselben Konto —,
  ist aber eine eigene Entscheidung und keine Nebenwirkung.
- Das Web-Terminal lässt sich nur auf einem Linux-Server wirklich erproben:
  das Pseudoterminal braucht /dev/ptmx, das Fallenlassen der Rechte braucht
  root. Die Prüfungen davor und die ioctls laufen im Test, die Shell selbst
  erst auf dem Zielsystem.
- Getestet gegen Debian 12/13 und Ubuntu 24.04. RHEL-Derivate sind laut Roadmap
  ohnehin später vorgesehen.
