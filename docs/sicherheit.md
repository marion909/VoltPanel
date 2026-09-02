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

## 4f. SQL-Import

**Risiko:** Eine hochgeladene SQL-Datei ist Programmtext, kein Datenblock. Sie
kann Anweisungen enthalten, die die Zieldatenbank verlassen — `USE`,
`CREATE DATABASE`, `GRANT`. Als root eingespielt, würde jede davon ausgeführt:
ein Kunde könnte über seinen eigenen Import in die Datenbank eines anderen
Kunden schreiben. Die Prüfungen aus 4d greifen hier nicht, denn geprüft wird
der Name der Zieldatenbank — nicht der Inhalt der Datei.

**Maßnahmen:** Der Import läuft **nicht** als root. Der Agent legt für jeden
Lauf ein Wegwerf-Konto an, gibt ihm Rechte auf genau die Zieldatenbank und
entfernt es danach wieder — auch wenn der Import scheitert oder in einen
Timeout läuft. Eine Anweisung außerhalb dieser Datenbank scheitert dann mit
"Access denied", und die Fehlermeldung erklärt, woran es lag.

Das Passwort des Kontos geht nicht über die Kommandozeile, sondern über eine
Optionsdatei mit 0600 im Laufzeitverzeichnis des Agents: Argumente stehen in
der Prozessliste und wären für jeden Benutzer des Servers lesbar. Reste eines
abgestürzten Laufs werden vor jedem Import aufgeräumt.

Die Datei selbst wird ausgepackt, wenn sie gzip-komprimiert ankommt — erkannt
an den ersten beiden Bytes, nicht an der Endung, die vom Browser kommt. Die
Größengrenze gilt für die **entpackte** Größe, sonst wäre ein kleines Archiv
mit riesigem Inhalt ein Weg, die Platte vollzuschreiben.

Der Export geht den umgekehrten Weg: der Dump wird beim Abruf erzeugt, über den
Socket blockweise zum Browser gestreamt und danach entfernt. Er enthält alle
Daten der Site im Klartext und soll nicht als Datei liegen bleiben.

## 4g. FTP-Zugänge

**Risiko:** Ein FTP-Zugang ist ein Login von außen, ohne Panel und ohne
Sitzung. Läuft er unter einem Systemkonto, ist er ein Vollzugriff auf den
Server über ein gewöhnliches FTP-Programm. Und FTP schickt das Passwort im
Klartext, wenn niemand es verhindert.

**Maßnahmen:** Die Zugänge sind virtuell — es entsteht kein Eintrag in
`/etc/passwd`. Jeder läuft unter dem Systembenutzer seiner Site, also mit genau
den Rechten, die dort ohnehin gelten.

UID und GID stehen **nicht** in der Anfrage: der Agent schlägt sie selbst zum
Systembenutzer nach. Kämen sie von außen, wäre "lege einen FTP-Zugang an" der
Weg zu einem Zugang mit der UID 0. Dazu drei Schranken, die sich überlappen:
der Name muss ein `site_`-Konto sein, die aufgelöste UID muss über dem Bereich
der Systemkonten liegen, und Pure-FTPd weist über `MinUID` jeden Login unter
1000 noch einmal ab.

`ChrootEveryone` hält jeden Zugang in seinem Verzeichnis fest; das
Heimatverzeichnis geht vorher durch dieselbe `jail()`-Prüfung wie jeder andere
Pfad. `TLS 2` heißt: ohne Verschlüsselung keine Verbindung — bei einem Panel,
das sonst jede Kleinigkeit absichert, wäre Klartext ein Widerspruch.

Benutzername und Passwort werden auf eine enge Zeichenmenge geprüft. Der Grund
ist die PureDB: dort trennt der Doppelpunkt die Felder, und der Zeilenumbruch
beendet die Eingabe an `pure-pw`. Das Passwort geht über die Standardeingabe,
nicht als Argument — Argumente stehen in der Prozessliste.

Der Dienst wird nicht mitinstalliert, sondern auf Wunsch eingerichtet. Ein
Dienst, der nur läuft, weil er beim Installieren dabei war, ist Angriffsfläche
ohne Nutzen.

**Eine stille Falle in der Konfiguration.** Pure-FTPd wird auf Debian nicht
direkt gestartet, sondern über `pure-ftpd-wrapper`. Der liest
`/etc/pure-ftpd/conf`, wo der Dateiname die Option ist und der Inhalt der Wert,
und baut daraus die Kommandozeile. Dabei tut er zweierlei, und nur eines davon
fällt auf:

Einen **Wert**, den er nicht versteht, quittiert er mit `die` — der Dienst
startet nicht. Unangenehm, aber sichtbar.

Einen **Dateinamen**, der nicht auf `^[A-Za-z][A-Za-z0-9]+$` passt, überspringt
er wortlos. Das ist die gefährlichere Hälfte: aus `ChrootEveryone` müsste nur
`Chroot-Everyone` werden, und jeder FTP-Zugang stünde ohne Sperre im
Dateisystem aller anderen — ohne Fehler, ohne Warnung, ohne Eintrag im Log.

Deshalb steht die Tabelle im Agent nicht als `map[string]string` da, sondern
trägt zu jedem Wert die Regel, nach der der Wrapper ihn liest. Vor dem
Schreiben wird beides geprüft, Name und Wert, und ein Test hält jeden Eintrag
gegen die Ausdrücke aus `debian/pure-ftpd-wrapper` — unabhängig abgeschrieben,
sonst prüfte er die Tabelle gegen sich selbst.

## 4h. Shell im Browser

**Risiko:** Ein Terminal im Panel ist die naheliegendste Art, die Trennung
zwischen Web-Prozess und Agent zu unterlaufen. Läuft die Shell als root, ist
jede Lücke im Panel — ein XSS genügt — eine Root-Shell.

**Maßnahmen:** Die Shell läuft als **Systembenutzer der Site**, nie als root
und nie als ein Konto, das der Browser benennen könnte. Der Web-Prozess leitet
den Benutzer aus der Site ab, die er im Namen des angemeldeten Kontos aufgelöst
hat; der Agent prüft anschließend noch einmal, dass der Name mit `site_`
beginnt, und weigert sich zusätzlich, eine Shell mit UID 0 zu starten.

Damit gibt das Terminal nichts her, was nicht ohnehin ginge: ein Cronjob
derselben Site führt beliebige Befehle unter demselben Konto aus, und der
PHP-Prozess der Site ebenso. Bequemer ist es allemal, deshalb ist es vorerst
Administratoren vorbehalten.

Das Arbeitsverzeichnis geht durch dieselbe `jail()`-Prüfung wie jeder andere
Pfad. Die Shell bekommt ein festes, knappes Environment und keine Nebengruppen
— auch nicht die des Agents.

Je Sitzung entsteht ein eigener Socket mit 0660 `root:volt`, der genau **eine**
Verbindung annimmt und danach verschwindet; ein zweiter Verbinder würde sonst
am selben Terminal mitlesen. Wird die Sitzung nicht binnen 15 Sekunden
abgeholt, wird sie beendet — sonst liefe eine Shell weiter, die niemand mehr
erreichen kann. Nach 30 Minuten ohne Eingabe endet sie ebenfalls, und beim
Herunterfahren des Agents wird die ganze Prozessgruppe beendet, nicht nur die
Shell.

## 4i. Prozesse beenden

**Risiko:** Eine Operation, die ein Signal an eine beliebige PID schickt, ist
ein Weg, sshd, den Agent oder die Datenbank abzuschießen.

**Maßnahmen:** Zwei Schranken, die zusammengehören. Der Web-Prozess bestimmt
den erwarteten Eigentümer aus den Sites des Mandanten — der Aufrufer schickt
nur die PID. Der Agent prüft, dass der Prozess wirklich diesem Benutzer gehört
und dass es überhaupt ein `site_`-Konto ist. Erlaubt sind nur TERM und KILL.

Für Systemdienste ist die Dienstverwaltung zuständig: ein per Signal
abgeräumter nginx hinterlässt eine Unit, die nicht mehr weiß, was sie tun soll.

Die Liste selbst ist ebenfalls gefiltert. Nicht wegen der Zahlen, sondern wegen
der Kommandozeilen: dort stehen Domainnamen, Pfade und gelegentlich Argumente
eines Skripts — die Tätigkeit anderer Mandanten.

## 4k. Datenbankzugriff von außen

**Risiko:** Ein MySQL-Konto ist ein Paar aus Benutzer und Herkunft. Wer die
Herkunft bestimmen darf, bestimmt, von wo aus eine Anmeldung angenommen wird —
`'kunde'@'%'` heißt: von überall auf der Welt, ohne Panel, ohne Sitzung, ohne
zweiten Faktor. Das Passwort dazu steht entschlüsselbar in der Panel-Datenbank.

**Maßnahmen:** Ein Eintrag ist eine Adresse. Zugelassen sind eine einzelne
IP-Adresse und ein Netz; abgewiesen werden drei Dinge, jedes aus einem eigenen
Grund:

- **`%`** in jeder Form. Der Platzhalter macht die Whitelist in dem Moment
  leer, in dem sie angelegt wird. Für ein ganzes Netz gibt es die
  Netzmaskenschreibweise, und eine Schreibweise für dieselbe Sache genügt.
- **Hostnamen.** MariaDB löst sie beim Verbindungsaufbau rückwärts über DNS
  auf. Wer den PTR-Eintrag seiner Adresse setzen kann — bei den meisten
  Anbietern ein Formularfeld —, bestimmt damit selbst, für welchen Eintrag er
  gehalten wird. Eine Zugriffskontrolle auf fremdverwalteten DNS-Daten ist
  keine.
- **Zu weite Netze.** Unterhalb von /16 bei IPv4 und /64 bei IPv6 ist ein
  Eintrag keine Herkunft mehr, sondern deren Abwesenheit. Die Grenze fängt auch
  `0.0.0.0/0` ab, ohne dass jede neue Schreibweise für „überall“ einzeln
  nachgetragen werden müsste.

Geprüft wird zweimal, im Store und im Agent, mit denselben Regeln und
unabhängig voneinander. Der Agent ist der einzige Prozess, der root an MariaDB
ist; er darf sich nicht darauf verlassen, dass der Web-Prozess vorher geprüft
hat. Beide Prüfungen weisen `1.2.3.4/0.0.0.0` ab — eine wohlgeformte,
zusammenhängende Netzmaske, die trotzdem jede Adresse bedeutet.

**Der Server selbst** ist eine getrennte Entscheidung. Debian bindet MariaDB ab
Werk an 127.0.0.1; das zu ändern gilt für alle Mandanten gleichzeitig, startet
den Dienst neu und öffnet einen Port. Deshalb ist es Administratoren
vorbehalten und geschieht nicht nebenbei beim Anlegen einer Herkunft.

**Was zusammenbleiben muss:** Passwort, Rechte und Löschen treffen alle Konten
eines Benutzers. Bliebe beim Löschen ein Konto einer Herkunft stehen, wäre der
Zugang von außen weiter offen, während das Panel den Benutzer als entfernt
führt.

## 4l. SQL aus der Oberfläche

**Risiko:** Der SQL-Browser nimmt eine ganze, vom Kunden getippte Anweisung
entgegen. Prüfen lässt sie sich nicht — eine Whitelist erlaubter SQL wäre
entweder nutzlos oder ein zweiter SQL-Parser. Liefe sie über die
Root-Verbindung des Agents, wäre sie ein Vollzugriff auf alle Datenbanken aller
Mandanten, dazu `CREATE USER`, `SELECT … INTO OUTFILE` und `LOAD_FILE()`.

**Maßnahmen:** Nicht die Anweisung wird eingeschränkt, sondern das Konto, unter
dem sie läuft.

- Der Agent legt über seine Root-Verbindung ein Wegwerf-Konto an, das
  ausschliesslich auf die eine Datenbank Rechte hat, öffnet damit eine zweite
  Verbindung und wirft beides danach weg. Über die Root-Verbindung läuft die
  Anweisung nie. Reste eines abgebrochenen Laufs räumt der nächste auf.
- Rechte auf eine einzelne Datenbank schliessen die globalen aus: FILE, PROCESS
  und SUPER lassen sich in MariaDB gar nicht so vergeben. Der Dateizugriff ist
  damit keine Frage der Vorsicht, sondern nicht vorhanden.
- `multiStatements` steht nicht im DSN. Der Treiber weist damit alles ab, was
  mehr als eine Anweisung ist — ein angehängtes `; DROP TABLE …` scheitert
  schon dort. Ein Test hält das fest, weil es nur eine Zeichenkette ist.
- Der **Name der Datenbank kommt nicht aus der Anfrage.** Der Aufrufer nennt
  eine ID, der Name wird im Zugriffsbereich des Mandanten nachgeschlagen. Käme
  er mit, wäre "führe diese Abfrage aus" der direkte Weg in fremde Daten.
- Zeilen, Zellengrösse, Länge der Anweisung und Laufzeit sind begrenzt; wird
  abgeschnitten, sagt die Antwort es. Eine abgeschnittene Menge für die
  vollständige zu halten wäre ein stiller Fehler.
- Jede Anweisung steht gekürzt im Audit-Log — gekürzt, damit keine Kundendaten
  hineingeraten.

## 4j. Pakete nachinstallieren

**Risiko:** Der Agent installiert auf Anforderung aus der Oberfläche Pakete
nach — PHP-Module und Pure-FTPd. Ein Paketname aus einer Anfrage wäre ein Weg,
beliebige Software auf den Server zu holen.

**Maßnahmen:** Es gibt keinen Paketnamen aus einer Anfrage. Bei PHP-Modulen
setzt der Agent ihn aus geprüfter Version und geprüftem Modulnamen zusammen;
bei FTP steht er als Konstante im Quelltext. Der Aufruf geht wie jeder andere
über `run()` — festes argv, feste Umgebung, keine Shell.

**Die Installation läuft ausserhalb der Sandbox des Agents.**
`volt-agent.service` läuft mit `ProtectSystem=true`; /usr ist für den Dienst
schreibgeschützt, damit ein übernommener Agent keine Systembinaries austauschen
kann. Ein Kindprozess erbt diese Sicht, und dpkg scheitert dann beim Auspacken
mit `Read-only file system`. Die naheliegende Antwort — `ReadWritePaths=/usr` —
wäre die falsche: /usr wäre dann dauerhaft beschreibbar, für jede Operation.

Stattdessen bittet der Agent über `systemd-run` PID 1, apt in einer eigenen,
kurzlebigen Unit zu starten. Die hat mit der Sandbox des Agents nichts zu tun;
der Agent selbst bleibt eingesperrt, und die Ausnahme gilt für die Dauer der
Installation und für nichts sonst.

**Dienste starten nicht nebenbei.** Während einer Installation liegt eine
`policy-rc.d` bereit, die `invoke-rc.d` mit 101 abweist — kein Paket fährt beim
Auspacken seinen Dienst hoch. Was der Agent wirklich braucht, startet er danach
selbst und ausdrücklich; was ein Paket nebenbei starten möchte, war nie seine
Entscheidung. Eine bereits vorhandene `policy-rc.d` bleibt unangetastet: sie
kann eine bewusste Einstellung des Serverbetreibers sein.

Das ist keine reine Vorsichtsmaßnahme. `pure-ftpd` hängt zwingend an
`openbsd-inetd`, dessen Postinstall den Dienst startet; gelingt das nicht — in
einem Container, oder weil Port 21 belegt ist —, bricht apt die gesamte
Installation ab. VoltPanel braucht inetd überhaupt nicht.

**Ein Zugeständnis:** apt wechselt zum Herunterladen auf den unprivilegierten
Benutzer `_apt`. Dieser Wechsel braucht `CAP_SETUID`, und in manchen Umgebungen
— Container etwa — hat der Dienst die Fähigkeit nicht. apt bricht dann mit
`E: seteuid 42 failed` ab, nachdem es die Paketliste schon aufgelöst hat.

Der Agent wiederholt den Aufruf in diesem Fall einmal mit
`APT::Sandbox::User=root`. Das kostet die Trennung beim Download: der HTTP-Teil
von apt liest die Antworten des Spiegels dann als root. Zwei Dinge halten den
Preis klein — die Signaturprüfung der Pakete ändert sich dadurch nicht, und der
zweite Versuch läuft nur nach genau diesem einen Abbruch. Wo der Wechsel
funktioniert, bleibt er unangetastet.

## 4m. Ausgehende Verbindungen zu Backup-Zielen

**Risiko:** Ein Backup-Ziel ist eine Adresse, die der Kunde eingibt, und der
Panel-Server baut die Verbindung auf. Er steht in einem Netz, in dem der Kunde
nichts zu suchen hat. `169.254.169.254` ist bei praktisch jedem Cloud-Anbieter
der Metadaten-Dienst; eine Anfrage dorthin liefert die Zugangsschlüssel der
Maschine. Ein „Backup-Ziel" mit dieser Adresse wäre kein Backup-Ziel, sondern
ein Ausleseversuch — und ohne Prüfung würde er beantwortet.

**Maßnahmen:** Die Prüfung sitzt im `Control`-Hook des Wählers, also *nach* der
Namensauflösung und *vor* `connect()`. Das ist der Unterschied zwischen „der
Name zeigte auf etwas Erlaubtes" und „die Verbindung ging dorthin": wer den
DNS-Eintrag stellt, kann zwischen zwei Auflösungen die Antwort wechseln. Ein
Test prüft das an einem echten Lauscher — käme die Verbindung durch, würde er
sie annehmen.

Abgewiesen werden Loopback, link-local, Multicast und die unspezifische
Adresse. Private Netze bleiben erlaubt: ein MinIO im selben Rechenzentrumsnetz
ist ein üblicher Aufbewahrungsort für Sicherungen, und ihn zu verbieten hiesse,
das Feature für die abzuschalten, die es am ehesten richtig benutzen.

Weiter:

- **Der lokale Pfad kommt nicht aus der Anfrage.** Der Aufrufer nennt einen
  Dateinamen; der Verzeichnisanteil wird abgeworfen und das Ergebnis gegen das
  Backup-Verzeichnis gehalten. Sonst wäre „lade dieses Backup hoch" der Weg,
  jede Datei des Servers in einen fremden Bucket zu schieben.
- **Nur Administratoren dürfen hochladen.** Das Archiv enthält die
  Panel-Datenbank, also die Daten aller Mandanten. Sie an einen Speicher zu
  schicken, den ein Kunde eingerichtet hat, wäre die vollständige Weitergabe
  des Servers an ihn.
- **Das Geheimnis wird nie ausgeliefert.** Es liegt entschlüsselbar in der
  Datenbank, weil eine Signatur es im Klartext braucht; in der API-Antwort
  steht nur, *ob* eines hinterlegt ist. Ein leeres Feld beim Speichern heisst
  „unverändert", nicht „löschen" — sonst verlöre jedes Speichern des Formulars
  die Zugangsdaten, und das Ziel scheiterte ab dann still.
- **Zeilenumbrüche sind überall ausgeschlossen.** In S3 wäre einer eine zweite
  HTTP-Kopfzeile, in FTP ein zweites Kommando auf derselben Verbindung.
- **Kein Proxy aus der Umgebung.** Ein `HTTPS_PROXY` aus einer Variablen wäre
  ein Weg an der Adressprüfung vorbei.

## 4n. Echte Dateisystem-Quotas

**Risiko:** Eine Grenze, die nur auf Anwendungsebene wirkt, ist keine Grenze
gegen den, der sie reißen will. Sie lehnt eine Aktion über das Panel ab — und
der PHP-Code der Site schreibt daneben weiter, bis die Platte voll ist. Voll ist
sie dann für alle Mandanten auf dem Server, nicht nur für den einen.

**Maßnahme:** Project Quota. Die Verzeichnisse eines Mandanten bekommen eine
Projektnummer, seine Grenze hängt an der Nummer, und der Kernel bucht jeden
Block darauf — gleichgültig, welcher Prozess ihn schreibt.

Ein Mandant ist ein Projekt, nicht eine Site. Die Quota des Panels gilt je
Mandant; wer fünf Sites hat, hat eine Grenze über alle fünf. Deshalb geht der
Agent alle Verzeichnisse eines Mandanten in einem Aufruf durch, und deshalb
lehnt er ab, wenn sie auf zwei Dateisystemen liegen: der Kernel kennt keine
Grenze darüber hinweg, und dieselbe Zahl auf beiden zu setzen gäbe dem Mandanten
das Doppelte — bei einer Anzeige, die etwas anderes behauptet.

Drei Schranken auf dem Weg dorthin:

Jeder Pfad geht durch `jail()`. Ein Pfad außerhalb der Wurzeln bekäme sonst die
Projektnummer eines fremden Mandanten, und dessen Verbrauch zählte dort mit —
oder, unangenehmer, stünde unter dessen Grenze.

Die Projektnummer entsteht aus der Mandantennummer mit festem Versatz. Der
Abstand zu 0 ist kein Schönheitsfehler: 0 trägt jede Datei, die zu keinem
Projekt gehört, und eine Grenze darauf träfe das halbe Dateisystem.

Pfade, die an `xfs_quota` gehen, dürfen kein Leerzeichen und keinen Umbruch
enthalten. Das ist keine Shell — aber `xfs_quota` nimmt seine eigenen Befehle
als eine Zeichenkette hinter `-c` und zerlegt sie selbst. In diese zweite
Zerlegung darf nichts geraten, was sie verschiebt.

**Was der Agent nicht tut:** an `/etc/fstab` schreiben oder ein Dateisystem neu
einhängen. Project Quota hängt an einer Mount-Option, und die lässt sich im
Betrieb nicht setzen. Ein Panel, das das automatisch versucht, riskiert einen
Server, der nicht mehr hochkommt — für ein Feature, das den Betrieb nicht
aufhält, wenn es fehlt. Stattdessen steht im Paketebereich, was der Server kann
und was dafür zu tun wäre.

## 4o. Eigene Anmeldedomain je Mandant

**Risiko:** Eine zweite Adresse, unter der dieselbe Anmeldung erscheint, ist ein
zweiter Eingang. Steht sie unter einem Namen, den der Kunde selbst bestimmt, und
an einer Stelle, an der niemand sie vermutet, ist sie der bequemere.

**Maßnahmen:** Unter der Anmeldedomain eines Mandanten kommt nur herein, wer zu
diesem Mandanten gehört. Das ist die Zusage, an der die ganze Funktion hängt —
ohne sie wäre die Domain des Kunden ein Weg zum Konto des Betreibers.

Die Antwort auf ein fremdes, aber richtiges Konto ist dieselbe wie auf ein
erfundenes. Sonst wäre die Anmeldeseite eines Kunden ein Werkzeug, um
herauszufinden, wer sonst noch ein Konto auf diesem Server hat.

Der Host kommt aus `Request.Host`, nie aus `X-Forwarded-Host`. Den Kopf setzt,
wer die Anfrage schickt; würde er hier gelten, bestimmte der Absender, als
welcher Mandant die Anmeldeseite auftritt — und damit, wer sich anmelden darf.
Der optionale Reverse-Proxy reicht den echten Host durch.

Der Zugriffspfad bleibt verborgen. Auf der Kundendomain liegt das Panel unter
`/`, und zwar durch Ergänzen des Pfads nach innen, nicht durch eine
Weiterleitung: die verriete mit der neuen Adresse genau den Pfad, den der Kunde
nicht kennen soll. Aus demselben Grund steht auf dieser Domain `<base href="/">`
statt des Präfixes.

Kein Wildcard als Anmeldedomain. `*.kunde.de` beantwortete jede Adresse darunter
und wäre ein bequemer Ort für eine gefälschte Anmeldung — mit gültigem
Zertifikat, weil das Panel es für diesen Namen ausliefern würde.

Die öffentliche Auskunft vor der Anmeldung nennt nur den Namen des Mandanten.
Nichts über sein Paket, seine Sites oder seine Leute.

Im TLS-Handshake wird nur für eingetragene Anmeldedomains ein eigenes
Zertifikat gesucht. Ohne diese Schranke wäre der Name aus dem Handshake ein
Verzeichnisname unter `certDir` — und die Frage, welche Zertifikate auf diesem
Server liegen, ließe sich durch Ausprobieren beantworten.

**Und eine Sperre, die sperrt.** Ein gesperrter Mandant kommt nicht mehr herein;
bis dahin setzte „sperren" nur ein Feld. Den eigenen Mandanten zu sperren lehnt
das Panel ab — danach käme niemand mehr herein, der es zurücknimmt.

## 4p. Apps als systemd-Unit

**Risiko:** Eine Unit-Datei ist zeilenweise aufgebaut. Was einen Zeilenumbruch
in einen Wert bekommt, schreibt die nächste Direktive selbst — und `User=root`
in einer Zeile, die als Kommandozeile gedacht war, lässt die App als root
laufen. Das ist dieselbe Klasse wie Config-Injection in einer Nginx-Datei, nur
mit einer anderen Grammatik und einem schlimmeren Ergebnis.

**Maßnahmen:** Das Startkommando ist eine Liste, keine Zeichenkette. Jedes
Argument geht einzeln durch ein enges Muster: keine Leerzeichen, keine
Zeilenumbrüche, kein Prozentzeichen.

Kein Leerzeichen, weil systemd `ExecStart` selbst zerlegt — mit eigenen
Anführungszeichen und C-Fluchtfolgen. Ein Argument mit Leerzeichen zwänge dazu,
diese Zerlegung nachzubauen, und eine nachgebaute Zerlegung ist genau die
Stelle, an der so etwas schiefgeht. Ohne Leerzeichen gibt es nichts nachzubauen.

Kein Prozentzeichen, weil es in einer Unit einen Platzhalter einleitet:
`%h/bin/node` zeigte auf ein Heimatverzeichnis, nicht auf den gemeinten Pfad.

Das Programm muss ein absoluter Pfad sein. Ein relativer hinge am
Arbeitsverzeichnis, und das bestimmt jemand anderes.

**Der Unit-Name kommt nie aus der Anfrage.** Er entsteht aus dem App-Namen mit
festem Präfix `volt-app-`. Ohne das wäre „meine App neu starten" ein Weg an der
Dienst-Whitelist vorbei zu jedem Dienst des Servers — `systemctl stop ssh` über
einen Endpunkt, den ein Kunde bedienen darf. Aus demselben Grund entstehen auch
die Pfade der Unit- und der Umgebungsdatei aus dem Namen: käme der Pfad von
außen, wäre „eine App schreiben" ein Weg, jede Datei des Servers durch eine
systemd-Unit zu ersetzen.

**Die Umgebung steht nicht in der Unit.** Unit-Dateien sind für jeden Benutzer
des Servers lesbar, und `systemctl show` gibt sie ohnehin heraus; in einer
App-Umgebung stehen regelmäßig Datenbankpasswörter. Sie liegt deshalb in einer
eigenen Datei mit 0640, `root:<gruppe der site>` — lesen darf sie der Prozess,
schreiben nur root. Ein Zeilenumbruch in einem Wert wird abgewiesen: er schriebe
die nächste Zeile selbst und damit eine Variable, die niemand gesetzt hat.

**Die App läuft als Systembenutzer ihrer Site**, nie unter einem Systemkonto —
derselbe Nachschlag und dieselben drei Schranken wie beim FTP-Zugang. Die Unit
selbst ist eng gefasst: `ProtectSystem=strict`, `NoNewPrivileges`, leeres
`CapabilityBoundingSet`, `SystemCallFilter=@system-service`, und als einziger
schreibbarer Pfad das Verzeichnis der Site.

`MemoryDenyWriteExecute` steht bewusst **nicht** darin: die JIT-Übersetzung von
V8 braucht schreibbaren und ausführbaren Speicher, und mit der Sperre startet
keine Node-Anwendung. Eine Härtung, die das Ziel nicht laufen lässt, ist keine.

## 4q. Git-Deploy

**Risiko 1 — die Repository-Adresse.** Sie kommt vom Kunden und wird ein
Argument von `git`. git legt sie selbst noch einmal aus, und mehrere Formen tun
dabei etwas anderes als „irgendwo etwas herunterladen":

| Eingabe | Was git daraus macht |
|---|---|
| `ext::sh -c whoami` | führt das Kommando aus — der ext-Transport ist so gemeint |
| `--upload-pack=/bin/sh` | wird als Option gelesen und ruft das Programm auf |
| `ssh://-oProxyCommand=…/x` | der Hostname beginnt mit `-` und wird von ssh als Option gelesen (CVE-2017-1000117) |
| `file:///etc` | kein Angriff, aber ein Weg, jedes Verzeichnis des Servers in ein Kundenverzeichnis zu kopieren |

**Maßnahme:** Die Adresse wird nicht gefiltert, sondern zerlegt und neu
zusammengesetzt. Was herauskommt, besteht nur aus geprüften Teilen. Erlaubt sind
drei Formen — `https://`, `ssh://` und `git@host:pfad` —, mehr kommt bei keinem
Hoster vor. Der Hostname muss mit einem Buchstaben oder einer Ziffer beginnen;
diese eine Zeile schließt CVE-2017-1000117.

Dieselbe Prüfung im Store und im Agent, aus einem Paket (`internal/gitspec`).
Eine zweite, nachgebaute Prüfung wäre die Stelle, an der beide auseinanderlaufen.

**Risiko 2 — die Buildschritte.** Ein Build ist fremder Code. Eine
Kommandozeile vom Kunden müsste jemand zerlegen, und wer zerlegt, landet früher
oder später bei einer Shell.

**Maßnahme:** Buildschritte sind Namen, keine Kommandozeilen. `npm-ci` schlägt
eine feste Argumentliste nach, oder es wird abgelehnt. Klon und Build laufen
unter der Kennung des Site-Benutzers; als root wäre der fremde Code ein
Rootzugang, den sich der Kunde selbst mitbringt.

`GIT_SSH_COMMAND` ist die eine Stelle im ganzen Projekt, an der ein Wert später
doch von einer Shell gelesen wird — git übergibt ihn an `sh -c`. Dort steht
ausschließlich Text aus dem Quelltext und ein Pfad, den der Agent selbst aus dem
geprüften Namen gebildet hat.

**Risiko 3 — der Webhook.** Er ist von außen erreichbar, ohne Sitzung und ohne
CSRF-Token, und er löst einen Build aus.

**Maßnahmen:** Zwei Ausweise. Die Adresse ist zufällig (16 Byte aus
`crypto/rand`) — ratbar wäre sie eine Liste aller Sites des Servers. Und über
den Rumpf muss eine gültige Signatur kommen: HMAC-SHA256, verglichen in
konstanter Zeit. Ohne Signatur wird nichts angenommen; die Adresse allein steht
in den Einstellungen eines fremden Dienstes und in jedem Proxy-Log dazwischen.

Die Antwort auf jeden Fehlerfall ist dieselbe — 404, ohne Unterschied zwischen
„diese Adresse gibt es nicht" und „die Signatur passt nicht". Sonst wäre der
Endpunkt ein Weg, gültige Hook-Adressen durch Ausprobieren zu finden.

Der Branch aus dem Rumpf muss der eingestellte sein. Ohne das überschriebe jeder
Push auf einen Feature-Branch die Produktion.

Der Endpunkt liegt **außerhalb** des Zugriffspfads. Nicht aus Bequemlichkeit:
seine Adresse landet in den Einstellungen eines fremden Dienstes, und der
Zugriffspfad des Betreibers hat dort nichts verloren. Eine IP-Whitelist am Panel
sperrt ihn dagegen mit aus, und das bleibt so — ein Loch in die Whitelist für
einen Endpunkt ohne Sitzung wäre genau die Ausnahme, wegen der die Whitelist
danach nichts mehr wert ist.

**Und ein Symlink als Umschalter.** Ein Deploy, der in das laufende Verzeichnis
schreibt, hat zwischen „halb kopiert" und „fertig" einen Zustand, in dem die
Site kaputt ist. Aufgeräumt wird nur, was diesem Code selbst gehört: ein
Verzeichnis unter `releases/`, das nicht auf das Zeitmuster passt, bleibt
stehen, und der gerade gültige Stand in jedem Fall — nach einem Rollback ist der
neueste nicht der benutzte.

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

Zum Update gehören auch die systemd-Units. Ein Fahrplan darf dabei nur
Dateien ersetzen, deren Name mit `volt-` beginnt und auf `.service` oder
`.timer` endet, und der Name muss ein reiner Dateiname sein. Ohne diese
Schranke wäre ein Update ein Weg, eine beliebige Unit des Systems zu
überschreiben — etwa die von SSH. Die bisherige Fassung wandert vorher in den
Snapshot; ein Rollback holt sie zurück.

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
