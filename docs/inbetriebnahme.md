# Inbetriebnahme

Der Weg von einem frischen Hetzner-Server zu einem Panel, das sich mit einer
Zeile installieren lässt. Beispielhaft mit `voltpanel.dev`; ersetze sie überall
durch deine eigene Domain.

Zwei Namen werden gebraucht, und sie liegen an verschiedenen Orten:

| Name | Was dort liegt | Wo es gehostet wird |
|---|---|---|
| `panel.voltpanel.dev` | die Oberfläche, Port 8443 | auf deinem Server |
| `get.voltpanel.dev` | `install.sh` und `latest.json` | GitHub Pages |

**Warum die Bezugsquelle nicht auf den Server gehört:** sonst bräuchtest du
ein laufendes VoltPanel, um VoltPanel zu installieren. Und wenn der Server
ausfällt, käme kein zweiter mehr hoch. Sie liegt deshalb auf GitHub Pages und
hängt an nichts, was bei dir ausfallen kann.

Die Binaries liegen nicht auf Pages, sondern am GitHub-Release. Auf der Seite
stehen nur Textdateien, die mit absoluten Adressen dorthin zeigen — so gibt es
keinen zweiten Ort, an dem eine veraltete Kopie liegen bleiben könnte.

## 0. Was `.dev` besonders macht

Die gesamte Top-Level-Domain `.dev` steht auf der HSTS-Preload-Liste der
Browser. Jeder Aufruf wird ohne Rückfrage auf HTTPS umgeschrieben — ein Panel
ohne TLS wäre unter einer `.dev`-Domain schlicht nicht benutzbar. Deshalb
bringt volt-web sein eigenes Zertifikat mit: beim ersten Start ein
selbstsigniertes, nach `volt cert issue` ein gültiges.

Für Let's Encrypt ist das kein Hindernis. Die HTTP-01-Prüfung kommt nicht aus
einem Browser und akzeptiert Port 80.

## 1. DNS

Beim Registrar von `voltpanel.dev`, mit der IP des Servers aus der
Hetzner-Konsole:

```
panel   A      203.0.113.10
panel   AAAA   2a01:4f8:c17:1234::1
get     CNAME  marion909.github.io.
```

Die AAAA-Einträge sind kein Beiwerk: Hetzner vergibt jedem Server ein
IPv6-Netz, und Let's Encrypt fragt bevorzugt darüber an. Ein AAAA-Eintrag auf
eine Adresse, unter der nichts antwortet, lässt die Zertifikatsprüfung
scheitern, obwohl über IPv4 alles läuft. Entweder beide Einträge setzen und
beide erreichbar halten — oder keinen AAAA-Eintrag.

## 2. Bezugsquelle aufschalten

Einmalig in den Repository-Einstellungen unter *Settings → Pages* als Quelle
**GitHub Actions** wählen. Dann das erste Release bauen lassen:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Zwei Workflows arbeiten nacheinander. **Release** baut die Binaries und hängt
Prüfsummen und `latest.json` ans Release. **Bezugsquelle** läuft danach an und
veröffentlicht die Seite.

Dass das zwei sind, hat einen Grund: die Umgebung `github-pages` lässt
Deployments nur vom Standardzweig zu, und ein Release läuft auf einem Tag. Ein
`workflow_run` startet dagegen auf `main` und erfüllt die Regel.

Nach ein paar Minuten prüfen:

```bash
curl -fsSL https://get.voltpanel.dev/stable/latest.json
```

Antwortet das mit JSON, ist die Bezugsquelle in Betrieb. Das Zertifikat für
`get.voltpanel.dev` stellt GitHub selbst aus; darum musst du dich nicht
kümmern.

**Wenn die Seite fehlt oder veraltet ist:** unter *Actions → Bezugsquelle →
Run workflow* neu bauen lassen. Ohne Angabe nimmt sie das neueste Release.
Das geht auch, ohne ein Release zu wiederholen — die Seite enthält nichts,
was nicht auch aus dem Release rekonstruierbar wäre.

Kommt ein 404 statt der Datei, hat Pages die Domain noch nicht übernommen —
unter *Settings → Pages* muss `get.voltpanel.dev` als Custom Domain stehen.
Das CNAME-File dafür schreibt `scripts/build-pages.sh` bei jedem Lauf mit.

## 3. Firewall

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

## 4. Installation

Auf dem Server, als root — mehr ist es nicht:

```bash
VOLT_PANEL_DOMAIN=panel.voltpanel.dev \
VOLT_ACME_EMAIL=du@example.at \
bash <(curl -fsSL https://get.voltpanel.dev/install.sh)
```

Der Installer liest `latest.json`, lädt beide Binaries und prüft jede Datei
gegen die dort hinterlegte Prüfsumme. **Stimmt eine Prüfsumme nicht oder
fehlt sie, bricht er ab** — ein Binary aus unbekannter Quelle wird nicht
installiert.

`VOLT_PANEL_DOMAIN` landet als `panel_domain` in `/etc/volt/config.yaml`,
steht im selbstsignierten Zertifikat und bestimmt, welches Zertifikat volt-web
später übernimmt. `VOLT_ACME_EMAIL` ist für Let's Encrypt Pflicht — ohne sie
verweigert `volt cert issue` den Dienst.

Am Ende stehen die URL mit dem zufälligen Zugriffspfad und das erzeugte
Administratorpasswort in der Ausgabe. **Beides kommt genau einmal.**

Ein zweiter Durchlauf repariert eine unvollständige Installation, statt sie zu
verdoppeln.

### Ohne Internet oder vor dem ersten Release

```bash
make dist VERSION=0.1.0                    # am Entwicklungsrechner
scp dist/voltpanel_0.1.0_linux_amd64.tar.gz root@203.0.113.10:/tmp/
```

```bash
cd /tmp && tar xzf voltpanel_0.1.0_linux_amd64.tar.gz
cd voltpanel_0.1.0_linux_amd64
VOLT_PANEL_DOMAIN=panel.voltpanel.dev VOLT_LOCAL_DIR="$PWD" bash install.sh
```

`VOLT_LOCAL_DIR` überspringt den Download und nimmt die mitgelieferten
Dateien. Alles danach ist identisch.

## 5. Erster Login

```
https://panel.voltpanel.dev:8443/<zugriffspfad>
```

Der Browser warnt vor dem selbstsignierten Zertifikat. Das ist an dieser
Stelle richtig so — die Ausnahme gilt nur bis Schritt 6.

Sofort danach: Passwort ändern und 2FA einschalten. Das Panel darf root auf
diesem Server, ein übernommener Zugang also auch.

## 6. Gültiges Zertifikat für das Panel

Sobald `panel.voltpanel.dev` auf den Server zeigt:

```bash
sudo -u volt volt cert issue panel.voltpanel.dev
```

Die Prüfung läuft über Port 80. Ein Vhost dafür ist nicht nötig: der
Standardserver aus `volt-shared.conf` liefert `/.well-known/acme-challenge/`
für jeden unbekannten Hostnamen aus — genau für diesen Fall. Die Datei
entsteht bei der Ersteinrichtung; `volt doctor` meldet, wenn sie fehlt, und
`volt site rebuild --all` schreibt sie nach.

Antwortet Let's Encrypt mit `404` auf die Challenge, ist meist genau das die
Ursache: dann beantwortet noch die Standardseite der Distribution die
Anfrage.

volt-web übernimmt das neue Zertifikat **ohne Neustart**. Es prüft bei jedem
Handshake, ob eine neuere Datei vorliegt; ein Neustart würde die offenen
Metrik-Streams abreißen lassen. Die Browserwarnung ist nach einem Neuladen weg.

Verlängert wird automatisch durch `volt-renew.timer`.

## 7. Zum Schluss prüfen

```bash
sudo -u volt volt doctor
```

Geprüft werden Ports, Rechte, Schemastand und Restlaufzeit der Zertifikate.

## Aktualisieren

Ein neuer Tag genügt; die Action erledigt Release und Bezugsquelle. Auf jedem
Server dann:

```bash
volt update
systemctl restart volt-agent volt-web
```

`volt update` tauscht beide Binaries — Panel und Agent müssen zur selben
Version gehören, sonst sprechen sie irgendwann verschiedene Protokolle. Vorher
entsteht ein Snapshot aus Binary, Datenbank und Konfiguration; scheitert
etwas, ist der alte Stand wieder da, bevor die Dienste neu starten.

Vorabversionen (`v0.2.0-beta.1`) landen im Kanal `beta` und lassen den
stabilen Kanal unberührt. Ein Server zieht nur den Kanal, der in seiner
`config.yaml` unter `update_channel` steht.

## Was auf Hetzner noch auffällt

- **Port 25 ist ausgehend gesperrt.** Für den Mailserver aus Phase 6 muss die
  Sperre im Hetzner-Support-Formular aufgehoben werden. Für alles bis dahin
  spielt es keine Rolle.
- **Snapshots sind kein Backup-Ersatz**, aber vor einem Update das billigste
  Sicherheitsnetz: einer kostet Sekunden und macht den ganzen Server umkehrbar.
- **Der Panel-Port ist frei wählbar.** `VOLT_PORT=9443` vor dem Installer
  setzen, wenn 8443 belegt ist.
