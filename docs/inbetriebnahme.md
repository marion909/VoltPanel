# Inbetriebnahme

Der Weg von einem frischen Hetzner-Server zu einem erreichbaren Panel unter
einer eigenen Domain. Beispielhaft mit `voltpanel.dev`; ersetze sie überall
durch deine eigene.

Zwei Namen werden gebraucht, und sie tun Verschiedenes:

| Name | Wofür |
|---|---|
| `panel.voltpanel.dev` | die Oberfläche, auf Port 8443 |
| `get.voltpanel.dev` | die Bezugsquelle, aus der `install.sh` und `volt update` laden |

## 0. Was `.dev` besonders macht

Die gesamte Top-Level-Domain `.dev` steht auf der HSTS-Preload-Liste der
Browser. Jeder Aufruf wird ohne Rückfrage auf HTTPS umgeschrieben — ein
Panel ohne TLS wäre unter einer `.dev`-Domain im Browser schlicht nicht
benutzbar. Deshalb bringt volt-web sein eigenes Zertifikat mit: beim ersten
Start ein selbstsigniertes, nach `volt cert issue` ein gültiges.

Für Let's Encrypt ist das kein Hindernis. Die HTTP-01-Prüfung kommt nicht aus
einem Browser und akzeptiert Port 80.

## 1. DNS

Beim Registrar von `voltpanel.dev` anlegen, mit der IP des Servers aus der
Hetzner-Konsole:

```
panel   A     203.0.113.10
panel   AAAA  2a01:4f8:c17:1234::1
get     A     203.0.113.10
get     AAAA  2a01:4f8:c17:1234::1
```

Die AAAA-Einträge sind kein Beiwerk: Hetzner vergibt jedem Server ein
IPv6-Netz, und Let's Encrypt fragt bevorzugt darüber an. Ein AAAA-Eintrag auf
eine Adresse, unter der nichts antwortet, lässt die Zertifikatsprüfung
scheitern, obwohl über IPv4 alles läuft. Entweder beide Einträge setzen und
beide erreichbar halten — oder keinen AAAA-Eintrag.

Vor dem nächsten Schritt prüfen, dass die Namen aufgelöst werden:

```bash
dig +short panel.voltpanel.dev A AAAA
```

## 2. Firewall

Hetzner hat zwei Firewalls, und nur eine davon kennt der Installer.

**Cloud Firewall** (in der Hetzner-Konsole, sitzt vor dem Server): eingehend
freigeben.

| Port | Wofür |
|---|---|
| 22 | SSH |
| 80 | ACME-Prüfung und Weiterleitung |
| 443 | die gehosteten Websites |
| 8443 | das Panel |

**ufw auf dem Server**: der Installer gibt 80, 443 und den Panel-Port frei,
sofern ufw aktiv ist. Ist keine Firewall aktiv, sagt er das.

Ist die Cloud Firewall nicht eingerichtet, ist der Server offen und nur ufw
schützt ihn. Beides gleichzeitig zu pflegen ist Absicht, keine Doppelung.

## 3. Installation

Paket auf dem Entwicklungsrechner bauen und hochladen:

```bash
make dist VERSION=0.1.0
scp dist/voltpanel_0.1.0_linux_amd64.tar.gz root@203.0.113.10:/tmp/
```

Auf dem Server, als root:

```bash
cd /tmp && tar xzf voltpanel_0.1.0_linux_amd64.tar.gz
cd voltpanel_0.1.0_linux_amd64

VOLT_PANEL_DOMAIN=panel.voltpanel.dev \
VOLT_ACME_EMAIL=du@example.at \
VOLT_LOCAL_DIR="$PWD" bash install.sh
```

`VOLT_PANEL_DOMAIN` landet als `panel_domain` in `/etc/volt/config.yaml`,
steht im selbstsignierten Zertifikat und bestimmt, welches Zertifikat
volt-web später übernimmt. `VOLT_ACME_EMAIL` ist für Let's Encrypt Pflicht —
ohne sie verweigert `volt cert issue` den Dienst.

Am Ende stehen die URL mit dem zufälligen Zugriffspfad und das erzeugte
Administratorpasswort in der Ausgabe. **Beides kommt genau einmal.**

## 4. Erster Login

```
https://panel.voltpanel.dev:8443/<zugriffspfad>
```

Der Browser warnt vor dem selbstsignierten Zertifikat. Das ist an dieser
Stelle richtig so — die Ausnahme gilt nur bis Schritt 5.

Sofort danach: Passwort ändern und 2FA einschalten. Das Panel darf root auf
diesem Server, ein übernommener Zugang also auch.

## 5. Gültiges Zertifikat für das Panel

Sobald `panel.voltpanel.dev` auf den Server zeigt:

```bash
sudo -u volt volt cert issue panel.voltpanel.dev
```

Die Prüfung läuft über Port 80. Ein Vhost dafür ist nicht nötig: der
Standardserver aus `volt-shared.conf` liefert `/.well-known/acme-challenge/`
für jeden unbekannten Hostnamen aus — genau für diesen Fall.

volt-web übernimmt das neue Zertifikat **ohne Neustart**. Es prüft bei jedem
Handshake, ob eine neuere Datei vorliegt; ein Neustart würde die offenen
Metrik-Streams abreißen lassen. Die Browserwarnung ist nach einem Neuladen weg.

Verlängert wird automatisch durch `volt-renew.timer`.

## 6. `get.voltpanel.dev` als Bezugsquelle

Ab hier ist das Panel selbst das Werkzeug — die Bezugsquelle ist eine ganz
normale Site mit drei Weiterleitungen. Nichts davon wird von Hand in eine
nginx-Datei geschrieben.

```bash
sudo -u volt volt site add get.voltpanel.dev --type static
sudo -u volt volt cert issue get.voltpanel.dev
```

Dann im Panel unter *Site → Einstellungen → Zusätzliche Direktiven*:

```nginx
rewrite ^/install\.sh$ https://raw.githubusercontent.com/marion909/VoltPanel/main/packaging/install.sh redirect;
rewrite ^/stable/systemd/(.*)$ https://raw.githubusercontent.com/marion909/VoltPanel/main/packaging/systemd/$1 redirect;
rewrite ^/stable/(.*)$ https://github.com/marion909/VoltPanel/releases/latest/download/$1 redirect;
```

Die Reihenfolge zählt: nginx nimmt die erste passende Regel, und die dritte
würde sonst auch die systemd-Units schlucken.

Warum Weiterleitungen statt Dateien: GitHub kennt unter
`/releases/latest/download/` immer die neueste stabile Version. Damit muss
nach einem Release nichts auf den Server kopiert werden, und es gibt keinen
zweiten Ort, an dem eine veraltete Datei liegen bleiben könnte.
Vorabversionen zählen dort nicht als „latest" — der `beta`-Kanal braucht
darum eine eigene Regel und ist noch nicht eingerichtet.

Danach funktioniert der Einzeiler aus dem README:

```bash
bash <(curl -fsSL https://get.voltpanel.dev/install.sh)
```

## 7. Das erste Release

```bash
git tag v0.1.0 && git push origin v0.1.0
```

Die Action baut mit GoReleaser die Archive und hängt anschließend über
`scripts/release-assets.sh` an, was die Programme wirklich laden: die nackten
Binaries, je eine Prüfsumme daneben und `latest.json`.

Prüfen, ob der Kanal steht:

```bash
curl -fsSL https://get.voltpanel.dev/stable/latest.json
sudo -u volt volt update --check
```

Ab dann genügt auf jedem Server:

```bash
volt update
systemctl restart volt-agent volt-web
```

`volt update` tauscht beide Binaries und legt vorher einen Snapshot aus
Binary, Datenbank und Konfiguration an. Scheitert etwas, ist der alte Stand
wieder da, bevor die Dienste neu starten.

## 8. Zum Schluss prüfen

```bash
sudo -u volt volt doctor
```

Geprüft werden Ports, Rechte, Schemastand und Restlaufzeit der Zertifikate.

## Was auf Hetzner noch auffällt

- **Port 25 ist ausgehend gesperrt.** Für den Mailserver aus Phase 6 muss die
  Sperre im Hetzner-Support-Formular aufgehoben werden. Für alles bis dahin
  spielt es keine Rolle.
- **Snapshots sind kein Backup-Ersatz**, aber vor einem Update das billigste
  Sicherheitsnetz: einer kostet Sekunden und macht den ganzen Server umkehrbar.
- **Der Panel-Port ist frei wählbar.** `VOLT_PORT=9443` vor dem Installer
  setzen, wenn 8443 belegt ist.
