# Sicherheit

Die Roadmap nennt vier Risiken, die ein Hosting-Panel umbringen können. Hier
steht, was dagegen im Code steht — und wo die Grenzen liegen.

## 1. Der Root-Daemon

**Risiko:** Ein Bug im Agent bedeutet Totalschaden.

**Maßnahmen:**

*Keine Shell.* Es gibt im gesamten Agent keine Funktion, die einen String an
einen Interpreter übergibt. `run()` nimmt einen Binary-Namen aus einer festen
Map absoluter Pfade und ein explizites argv:

```go
run(ctx, timeout, "systemctl", "reload", name)   // argv, nie "systemctl reload "+name
```

Ein Dienstname `nginx; rm -rf /` kann so nichts auslösen: er wäre schlicht ein
Argument, das systemd nicht kennt. Zusätzlich fällt er vorher durch
`checkService()`. Getestet in `TestRunRejectsUnlistedBinary` und
`TestCheckServiceWhitelist`.

*Festes Environment.* `cmd.Env` wird gesetzt, nicht geerbt. Ein manipulierter
`PATH` oder `LD_PRELOAD` erreicht die Kindprozesse nicht.

*Dienst-Whitelist.* `systemctl stop sshd` ist nicht möglich — `sshd` steht
bewusst nicht auf der Liste. Wer sich über das Panel vom Server aussperren
könnte, hätte ein Panel mit eingebautem Fußschuss.

*Peer-Prüfung über den Kernel.* Der Agent fragt per `SO_PEERCRED` (Linux) bzw.
`LOCAL_PEERCRED` (macOS, nur Entwicklung), welche UID verbunden ist. Diese
Angabe kommt vom Kernel und ist nicht fälschbar. Ein gemeinsames Geheimnis im
Protokoll wäre schwächer: wer den Socket ansprechen darf, könnte auch die
Token-Datei lesen.

*Socket-Rechte.* `0660 root:volt`. `TestAgentSocketPermissions` prüft, dass
andere Benutzer keinen Zugriff haben.

**Grenze:** Der Agent läuft als root und kann per Definition alles, was seine
Operationen erlauben. Die Systemd-Unit engt ihn zusätzlich ein
(`ProtectSystem=full`, `ReadWritePaths=…`), aber ein Fehler *innerhalb* einer
erlaubten Operation bleibt ein Fehler mit Root-Rechten. Deshalb ist jede
Operation einzeln validiert und keine nimmt einen freien Pfad entgegen.

## 2. Pfad-Traversal

**Risiko:** Über den File Manager oder den Log-Viewer beliebige Systemdateien
lesen oder schreiben.

**Maßnahme:** `jail()` löst den Pfad auf, *bevor* es ihn gegen die erlaubten
Wurzeln prüft. Der Unterschied ist wesentlich:

```
/var/www/site/escape   →  Symlink nach /etc
Präfixprüfung auf dem rohen String:  "/var/www/…" ✓  → durchgelassen
Präfixprüfung nach EvalSymlinks:     "/etc"       ✗  → abgelehnt
```

Existiert der Pfad noch nicht (eine Datei soll angelegt werden), wird das
tiefste vorhandene Elternverzeichnis aufgelöst und der Rest angehängt — die
fehlenden Segmente können selbst keine Symlinks sein, weil es sie nicht gibt.

`TestJailBlocksEscapes` deckt ab: `..`-Traversal, Symlink nach draußen, Datei
hinter einem solchen Symlink, absoluter Fremdpfad, relativer Pfad, Nullbyte, und
die Präfixverwechslung (`/var/www-evil` gegen die Wurzel `/var/www`).
`TestAgentEnforcesJailOverSocket` prüft dasselbe noch einmal über den Socket,
damit die Prüfung nicht nur in der internen Funktion greift.

## 3. Config-Injection

**Risiko:** Ein Domainname mit Zeilenumbruch erzeugt zusätzliche
Nginx-Direktiven.

**Maßnahme:** `text/template` escaped nichts — eine Nginx-Config ist kein HTML.
Deshalb wird jeder Wert in `validate()` geprüft, bevor er in eine Vorlage geht:
Domains gegen eine Regex, Pfade auf absolut/einzeilig/kein `..`, Proxy-Ziele als
`http(s)`-URL mit Host, IP-Regeln als IP oder CIDR, Weiterleitungscodes gegen
eine feste Liste.

Die Zusatzzeilen aus dem Rewrite-Editor sind auf einzelne Direktiven begrenzt:
eine Zeile, keine geschweiften Klammern, abgeschlossen mit `;`, kein `include`.
Klammern zu *zählen* genügt nicht — `} server { listen 8080;` ist balanciert und
bricht trotzdem aus dem Server-Block in den `http`-Kontext aus. Dieser Fall
stammt aus einem Test, der die erste Fassung der Prüfung gekippt hat.

## 4. Kaputte Nginx-Config

**Risiko:** Ein Reload mit fehlerhafter Config nimmt alle Sites mit.

**Maßnahme:** Schreiben → `nginx -t` → bei Fehler zurück auf den vorherigen
Inhalt (und den Symlink entfernen, falls die Site neu war) → erst dann reload.
`nginx.reload` allein verweigert den Dienst ebenfalls, wenn der Test nicht
besteht. Dasselbe Muster gilt für FPM-Pools.

Geschrieben wird immer atomar: temporäre Datei im selben Verzeichnis, `fsync`,
`rename`. Ein Absturz mitten im Schreiben hinterlässt nie eine halbe Config.

## 4b. Dateizugriff über Tenant-Grenzen

**Risiko:** Der Agent erlaubt Dateizugriffe überall unter `/var/www`. Für einen
einzelnen Server ist das richtig — für Multi-Tenant wäre es ein Leck: jeder
Kunde könnte im Verzeichnis jedes anderen lesen.

**Maßnahme:** Der Dateimanager nimmt nie einen absoluten Pfad entgegen. Jede
Anfrage besteht aus `site_id` plus einem Pfad *relativ dazu*. Der Service lädt
die Site mit dem Scope des Anfragenden — eine fremde `site_id` liefert
`ErrNotFound`, bevor der Agent überhaupt gefragt wird — und setzt den relativen
Pfad erst dann an das Wurzelverzeichnis.

Ein Pfad, der aus der Site herausführen will, wird **abgelehnt**, nicht
stillschweigend hineinnormalisiert. Beides wäre sicher, aber
`../andere-site/config.php` als `andere-site/config.php` im eigenen Verzeichnis
anzulegen würde einen Angriffsversuch unsichtbar machen. Innerhalb der Site
bleibt `..` erlaubt: `public/../public/x` ist eine gewöhnliche Schreibweise.

`TestFileServiceTenantIsolation` fährt beide Angriffe: fremde `site_id` mit
eigenem Scope, und eigene `site_id` mit einem Pfad, der zum Nachbarn zeigt.

## 4c. Archive

**Risiko:** Ein hochgeladenes Archiv bestimmt die Pfade seiner Einträge selbst.
`../../etc/cron.d/x` würde als root außerhalb des Ziels landen (Zip-Slip).

**Maßnahmen:** Jeder Eintragsname wird an `/` verankert und normalisiert, bevor
er ans Ziel gehängt wird; das Ergebnis wird gegen das Zielverzeichnis geprüft.
Symlinks, Hardlinks und Gerätedateien im Archiv werden übersprungen — ein
Symlink aus einem fremden Archiv wäre ein Ausbruch mit Ansage. Die entpackte
Menge ist auf 2 GiB gedeckelt, gegen "zip bombs".

## 4d. SQL-Identifier

**Risiko:** `CREATE DATABASE` und `GRANT` lassen sich nicht parametrisieren —
der Name muss als Text in die Anweisung.

**Maßnahmen:** Drei Schichten. Die Benutzereingabe muss `ValidNameInput`
passieren (Buchstaben, Ziffern, Leerzeichen, Bindestrich, Unterstrich) und wird
sonst **abgelehnt**, nicht umgeschrieben. Der daraus abgeleitete Name muss
`^[a-z][a-z0-9_]{2,47}$` erfüllen. Im Agent gilt dieselbe Prüfung noch einmal,
und erst dann geht der Name in Backticks.

Passwörter gehen denselben Weg: `IDENTIFIED BY '…'` verträgt keinen Parameter,
deshalb muss ein Passwort eine Zeichenmenge ohne Anführungszeichen, Backslash
und Steuerzeichen erfüllen — sonst wird es abgelehnt. Selbst erzeugte
Passwörter halten diese Menge ein.

Der Agent verbindet sich über den Unix-Socket als root und nutzt dort die
`unix_socket`-Authentifizierung: es gibt kein MySQL-Passwort, das irgendwo
hinterlegt werden müsste.

## 4e. Cronjobs

**Risiko:** Eine Zeile in `/etc/cron.d` besteht aus Zeitplan, **Benutzername**
und Kommando. Ein sechstes Feld im Zeitplan würde als Benutzername gelesen —
der Job liefe dann unter einem fremden Konto, womöglich als root.

**Maßnahmen:** Der Zeitplan wird auf genau fünf Felder geprüft, jedes gegen
seinen Wertebereich; Kurzformen nur aus einer festen Liste (`@reboot` ist
bewusst nicht dabei). Zeitplan und Kommando dürfen keinen Zeilenumbruch
enthalten — eine zweite Zeile wäre ein zweiter, ungeprüfter Job. Der Benutzer
wird nicht aus der Anfrage übernommen, sondern aus der Site abgeleitet, an der
der Job hängt. Ein Job ohne Site gibt es nicht.

Ein abgeschalteter Job wird aus `/etc/cron.d` **entfernt**, nicht nur im Panel
als inaktiv markiert.

## 5. Multi-Tenant-Lecks (IDOR)

**Risiko:** Ein Kunde liest über eine geratene ID die Daten eines anderen.

**Maßnahme:** Der Scope ist Pflichtparameter jeder Repository-Methode, sein
Nullwert schlägt fehl. `TestCrossTenantAccessDenied` ruft jede Leseoperation mit
einer fremden ID auf und prüft, dass sie scheitert — einschließlich des
klassischen Angriffs, ein fremdes Objekt zu laden und die eigene `tenant_id`
einzutragen.

In der API wird `ErrForbidden` bewusst zu **404**, nicht zu 403: eine 403 würde
bestätigen, dass die fremde ID existiert. `TestCrossTenantReturnsNotFound` prüft
das auf HTTP-Ebene, `TestCrossTenantAccessDenied` im Repository.

## Authentifizierung

| | Umsetzung | Warum so |
|---|---|---|
| Passwörter | Argon2id, 19 MiB / t=2 / p=1, PHC-String | OWASP-Empfehlung; die Parameter stehen im Hash und lassen sich später erhöhen, ohne alte Hashes zu entwerten |
| Sessions | 256 Bit aus dem CSPRNG, gespeichert wird nur SHA-256 | ein DB-Leak erlaubt keine Sitzungsübernahme; kein Salt nötig, weil das Token nicht ratbar ist |
| 2FA | TOTP, SHA-1/6 Stellen/30 s, Skew 1 | das, was Authenticator-Apps erwarten; Skew 1 fängt ungenaue Handy-Uhren ab |
| Secrets at rest | AES-256-GCM, Schlüssel als Datei mit 0600 | ein DB-Backup allein gibt die TOTP-Secrets und Cloudflare-Tokens nicht her |
| CSRF | Double-Submit-Cookie plus Header | eine fremde Seite kann das Cookie mitschicken lassen, aber den Header nicht setzen |
| Ratelimit | 5 Versuche/Minute je IP **und** Kontosperre nach 8 Versuchen | die IP-Grenze allein hilft nicht gegen verteilte Angriffe |

Beim Login ist die Antwort auf eine unbekannte Adresse identisch mit der auf ein
falsches Passwort — sonst verrät das Panel, welche Adressen es kennt.

2FA abzuschalten verlangt einen gültigen Code: eine übernommene Sitzung soll den
zweiten Faktor nicht einfach entfernen können. Der Notausgang ist
`volt user 2fa-reset` und damit ein Zugang zum Server selbst.

## Site-Einstellungen aus der Oberfläche

Die Zusatzeinstellungen einer Site — Weiterleitungen, IP-Regeln, eigene
Direktiven — sind die einzige Stelle, an der ein Kunde Text schreibt, der
unverändert in einer Nginx-Config landet. Sie werden **zweimal** geprüft: beim
Speichern (`SiteSettings.Validate`, damit die Meldung verständlich ist) und noch
einmal beim Rendern (`templates.validate`, weil dort nichts escaped wird).

Der Rewrite-Editor nimmt nur **einzelne Direktiven** an: eine Zeile, keine
geschweiften Klammern, abgeschlossen mit `;`, kein `include`. Ein `include`
würde beliebige Dateien einbinden und die gesamte Prüfung aushebeln.

Beim **Passwortschutz** wird im Web-Prozess gehasht (bcrypt), nicht im Agent —
so verlässt ein Klartextpasswort den Web-Prozess nie und kann in keinem
Agent-Log landen. Die htpasswd-Datei liegt unter `/etc/nginx/volt-auth/`, nicht
im Site-Verzeichnis: läge sie dort, könnte der PHP-Prozess der Site die Hashes
aller Benutzer lesen, die die Site schützen sollen. Ihr Pfad wird auf beiden
Seiten aus der Konfiguration abgeleitet und nie gespeichert — sonst könnte er
auf eine fremde Datei zeigen.

`disable_functions` und eigene ini-Werte sind **Administratoren vorbehalten**.
Diese Liste isoliert die Site; sie zu leeren erlaubt der Site, Shell-Kommandos
abzusetzen. Das darf ein Kunde nicht für sich selbst entscheiden.

Der **Cloudflare-Token** liegt AES-256-GCM-verschlüsselt beim Mandanten und wird
über die API nie wieder herausgegeben — `Tenant.MarshalJSON` ersetzt ihn durch
das abgeleitete `has_cloudflare_token`.

## Quotas

Die Grenzen aus dem Hosting-Paket wirken auf **Anwendungsebene**: `CheckCount`
vor jedem Anlegen, `CheckDisk` vor jedem Upload. Das ist eine Begrenzung, keine
Absicherung — PHP-Code der Site selbst schreibt am Panel vorbei und wird davon
nicht gebremst. Eine echte Dateisystem-Quota wäre die stärkere Maßnahme; sie
steht in [stand.md](stand.md) als offener Punkt.

Zwei Details, die leicht falsch herum laufen:

- **0 bedeutet unbegrenzt**, nicht "nichts erlaubt". Andersherum wäre ein
  lückenhaft gepflegtes Paket eine stille Sperre für den Kunden.
- **Ein gelöschtes Paket lockert**, es verschärft nicht: die zugeordneten
  Mandanten stehen danach ohne Grenzen da (`ON DELETE SET NULL`). Das ist die
  harmlosere Richtung, steht aber im Audit-Log, weil es leicht übersehen wird.

Die Rollenprüfung im Frontend (`hasRole`) blendet nur aus. Durchgesetzt wird sie
im Server bei jeder Anfrage — eine ausgeblendete Route ist Bequemlichkeit, kein
Schutz.

## TLS des Panels

Das Panel terminiert TLS selbst, statt sich hinter nginx zu stellen. Der Grund
ist der Notfall: wer eine kaputte nginx-Konfiguration reparieren will, braucht
das Panel gerade dann, wenn nginx nicht mehr ausliefert. Ein Reverse-Proxy
davor bliebe möglich (`tls: false` plus `trust_proxy: true`), ist aber nicht
der Standardweg.

Beim ersten Start entsteht ein selbstsigniertes Zertifikat unter
`{cert_dir}/panel/`. Es ist nicht vertrauenswürdig und soll es nicht sein — es
schließt nur das Fenster zwischen Installation und erstem `volt cert issue`,
in dem sonst das Administratorpasswort im Klartext über die Leitung ginge.
Solange es gilt, warnt der Browser. Das ist die richtige Warnung.

Ein über ACME geholtes Zertifikat für die `panel_domain` hat Vorrang. Der
Webserver prüft bei jedem Handshake, ob die Datei neuer ist, und übernimmt sie
ohne Neustart — sonst würde jede Erneuerung die offenen Metrik-Streams
abreißen lassen. Sind beim Erneuern beide Dateien unlesbar, behält er das
Zertifikat im Speicher: ein misslungenes Erneuern darf nicht genau den
aussperren, der es reparieren müsste.

Der private Schlüssel gehört dabei dem Panel-Benutzer, nicht root. Der Agent
schreibt Zertifikate sonst als root mit 0600 — richtig für nginx, aber
unlesbar für volt-web. Welche Domain die des Panels ist, liest der Agent aus
seiner eigenen Konfiguration, nicht aus der Anfrage. Sonst könnte ein
übernommener Web-Prozess sich zum Eigentümer eines beliebigen fremden
Schlüssels erklären.

## Rechte auf den Site-Verzeichnissen

Jede Site gehört ihrem eigenen Systembenutzer. Das ist die Trennung, die
verhindert, dass PHP einer Site die Dateien einer anderen liest — sie hängt
nicht an `open_basedir`, sondern an den Dateirechten darunter.

Damit gehört die Site aber auch nicht dem Webserver, und nginx läuft als
eigener Benutzer. Ohne Zutun endet jede Anfrage in
`stat() failed (13: Permission denied)`.

Der Ausweg ist nicht, die Verzeichnisse für alle zu öffnen: dann könnte jeder
Site-Benutzer die Dateien jeder anderen lesen, und die Trennung wäre weg.
Stattdessen bleibt der Systembenutzer Eigentümer, die Gruppe wird die des
Webservers (`web_group`, Vorgabe `www-data`), und Weltrechte gibt es keine —
0750 auf der Wurzel, 2750 auf dem Dokumentenstamm.

Das setgid-Bit dort ist kein Beiwerk: ohne es bekommt eine Datei, die PHP
anlegt, die Gruppe des Site-Benutzers, und ob der Webserver sie noch lesen
darf, hinge an der umask des FPM-Pools. Beim Setzen zählt außerdem die
Reihenfolge — `chown` löscht setgid wieder, es muss also nach dem Eigentümer
gesetzt werden.

`tmp`, `tmp/sessions` und `logs` bleiben ausdrücklich außen vor: 0750 für den
Systembenutzer allein. Der Webserver hat dort nichts zu suchen, und
Sitzungsdateien schon gar nicht.

## Update aus der Oberfläche

Ein Update von der Weboberfläche aus ist der naheliegendste Weg, aus einer
Übernahme des Panels root zu machen: wer bestimmen darf, welches Programm als
root installiert wird, hat gewonnen.

Deshalb nimmt die Agent-Operation `system.update` **keine Parameter**. Keine
Adresse, keine Prüfsumme, keine Version. Der Web-Prozess kann das Update
anstoßen, aber nicht sagen, was installiert werden soll. Woher die Dateien
kommen, steht im Kanal in `/etc/volt/config.yaml` — einer Datei, die der
Web-Prozess nicht schreiben darf, weder über die Rechte noch durch die
Einhängung seiner Unit. Mitgeschickte Parameter werden abgewiesen statt
ignoriert.

Der Tausch selbst läuft über `volt update` und nicht über eine Nachbildung im
Agent: Snapshot vor dem Tausch, Prüfsumme gegen den Fahrplan und automatischer
Rollback bei fehlgeschlagener Migration stecken dort und sind getestet.

Auslösen darf die Operation nur, wer auch Dienste neu starten darf. Der Stand
selbst — installierte Version, ob etwas Neueres vorliegt — ist für jeden
Angemeldeten sichtbar; er steht als Hinweis in der Oberfläche.

Was fehlt: die Release-Signatur wird erzeugt, aber nicht geprüft. Es wirkt
bisher nur der SHA-256-Vergleich gegen `latest.json`, und der ist genau so
vertrauenswürdig wie die HTTPS-Verbindung zum Kanal.

## Was noch offen ist

- **SSRF** bei Webhook- und Cloudflare-Aufrufen: die ausgehenden Ziele werden
  noch nicht gegen interne Netze gefiltert. Relevant ab Phase 5 (Git-Deploy).
- **Fuzzing der Socket-API**: die Roadmap nennt es unter den Gegenmaßnahmen zum
  Root-Daemon; bisher gibt es nur Beispieltests, keinen Fuzzer.
- **Ratelimit für Dateioperationen**: ein angemeldeter Kunde kann derzeit
  beliebig viele Uploads gleichzeitig starten. Der Deckel je Datei steht
  (512 MiB), eine Gesamtquote noch nicht.
- **Web-Terminal**: bewusst noch nicht gebaut. Eine Shell im Browser hebelt die
  Trennung zwischen Web und Agent auf und braucht ein eigenes Konzept.
- **Release-Signatur**: `install.sh` und `volt update` prüfen die SHA-256-Summe.
  Die Signatur über cosign ist in der Release-Pipeline vorgesehen, aber die
  Prüfung beim Client fehlt noch.
