# Veröffentlichen

Wie ein Release entsteht — und warum ein Schritt darin nicht übersprungen
werden darf.

## Der Schlüssel

`volt update` lädt `latest.json` vom Kanal, vergleicht die dort genannten
Prüfsummen mit den heruntergeladenen Binaries und tauscht dann das Panel *und*
den Root-Daemon aus.

Der Prüfsummenvergleich allein schützt dabei nichts. Die Summe steht in
derselben Datei, die von derselben Adresse kommt: wer den Server oder die
Leitung dorthin beherrscht, liefert ein anderes Binary und die passende Summe
gleich mit. Bei einem Update ist das der Totalschaden — das nächste `volt-agent`
läuft als root und tut, was in ihm steht.

Deshalb wird `latest.json` signiert. Die Datei trägt die Prüfsummen aller
Bestandteile; wer sie signiert, signiert damit auch die Binaries. Der
öffentliche Schlüssel ist ins Binary eingebettet — ein Schlüssel, den man sich
bei derselben Adresse holt wie die Datei, die er beglaubigen soll, beglaubigt
gar nichts.

### Einmalig einrichten

Der kurze Weg, ohne zusätzliches Werkzeug — openssl liegt auf jedem Rechner,
auf dem VoltPanel gebaut oder betrieben wird:

```sh
openssl ecparam -genkey -name prime256v1 -noout -out release.key
openssl ec -in release.key -pubout -out internal/release/release.pub
```

Danach `release.key` als Repository-Secret `RELEASE_SIGNING_KEY` hinterlegen
(der ganze Dateiinhalt, mit den BEGIN/END-Zeilen) — und die Datei selbst
**nicht** einchecken. Wer sie hat, kann Updates für jede Installation
ausliefern.

Das war es. `internal/release/release.pub` wird beim nächsten Build eingebettet
und beim Veröffentlichen in `install.sh` eingesetzt; `scripts/release-assets.sh`
signiert `latest.json` mit dem privaten Teil.

> Der Schlüssel liegt unverschlüsselt im Secret-Speicher. Das ist derselbe
> Schutz, den auch `COSIGN_PASSWORD` hätte: wer den Secret-Speicher lesen kann,
> liest beides. Eine Passphrase, die im Secret daneben steht, ist keine.

#### Der cosign-Weg

Wer cosign ohnehin benutzt, kann dabei bleiben:

```sh
cosign generate-key-pair
```

Das ergibt `cosign.key` (verschlüsselt mit der eingegebenen Passphrase) und
`cosign.pub`.

1. Den **öffentlichen** Teil in den Quelltext legen:

   ```sh
   cp cosign.pub internal/release/release.pub
   ```

   Er wird über `//go:embed` in jedes Binary aufgenommen. Ohne ihn lehnt
   `volt update` jeden Kanal ab, und `volt doctor` sagt es.

   Dieselbe Datei landet beim Veröffentlichen im Installer:
   `scripts/build-pages.sh` setzt sie über `scripts/embed-release-key.sh` in
   die Kopie ein, die unter `get.voltpanel.dev/install.sh` liegt. Eine zweite
   gepflegte Kopie des Schlüssels gibt es also nicht — sie wäre die Stelle, an
   der die beiden auseinanderlaufen.

2. Den **privaten** Teil und die Passphrase als Secrets im Repository
   hinterlegen:

   - `COSIGN_PRIVATE_KEY` — der Inhalt von `cosign.key`
   - `COSIGN_PASSWORD` — die Passphrase

   Ist `RELEASE_SIGNING_KEY` gesetzt, hat es Vorrang; beide zugleich zu setzen
   ergibt keinen Sinn, weil dann zwei verschiedene Schlüssel signieren würden
   und nur einer eingebettet ist.

3. `cosign.key` **nicht** einchecken. Wer sie hat, kann Updates für jede
   Installation ausliefern.

### Vor dem Tag: den Changelog-Abschnitt setzen

In [CHANGELOG.md](../CHANGELOG.md) sammelt sich unter „Unveröffentlicht", was
seit dem letzten Tag dazugekommen ist. Vor dem Taggen wird daraus die
Überschrift der neuen Fassung:

```markdown
## v0.4.0 — 2026-09-10
```

Das ist kein Formalismus. `scripts/release-assets.sh` schneidet genau diesen
Abschnitt heraus und schreibt ihn als Release-Notes in `latest.json` — also in
das, was ein Betreiber in der Update-Karte des Panels liest, bevor er auf
„Aktualisieren" drückt. Fehlt der Abschnitt, fällt das Skript auf die von
GoReleaser erzeugte Liste zurück: Commit-Überschriften mit Hashes davor.
Brauchbar als Notnagel, aber keine Auskunft darüber, ob dieses Update etwas
verlangt.

Was etwas verlangt, gehört unter „Achtung" in den Abschnitt. Eine geänderte
Voreinstellung, ein Handgriff, der vorher nicht nötig war — das ist der Satz,
für den die Karte da ist.

### Was beim Release passiert

`scripts/release-assets.sh` erzeugt `latest.json` und daneben die Signatur —
mit `RELEASE_SIGNING_KEY`:

```sh
openssl dgst -sha256 -sign release.key -out latest.json.der latest.json
base64 < latest.json.der | tr -d '\n' > latest.json.sig
```

oder mit cosign:

```sh
cosign sign-blob --yes --key env://COSIGN_PRIVATE_KEY \
    --output-signature latest.json.sig latest.json
```

Beides ergibt dasselbe: eine base64-kodierte ECDSA-Signatur im DER-Format über
den SHA-256 des Rumpfs. Dass die beiden Seiten wirklich zusammenpassen, prüft
`TestOpenSSLSignaturWirdAngenommen` in `internal/release` — mit denselben
openssl-Aufrufen, die hier stehen. Eine falsche Behauptung an dieser Stelle
hieße: der Kanal ist signiert, jedes Panel lehnt ihn ab, und niemand kann mehr
aktualisieren.

Beide Dateien hängen am Release. `volt update` holt sie, prüft die Signatur über
den **Rumpf** der Datei — nicht über das Ergebnis des Parsens, denn wer erst
parst und dann prüft, prüft etwas anderes als das, wonach er sich richtet — und
liest erst danach das JSON.

Fehlt `COSIGN_PRIVATE_KEY`, warnt das Skript und liefert unsigniert aus. Der
Kanal ist dann für jedes Panel unbrauchbar, dessen Betreiber nicht ausdrücklich
`update_allow_unsigned: true` gesetzt hat. Das ist Absicht: eine
Signaturprüfung, die ohne Schlüssel stillschweigend durchwinkt, ist keine.

Der Installer prüft dieselbe Signatur, bevor er das erste Binary anfasst — er
läuft als root, und der Prüfsummenvergleich allein hilft dort so wenig wie
beim Update. Trägt das Skript keinen Schlüssel (der Fall im Quelltext), bricht
es ab, statt weiterzumachen; wer bewusst einen unsignierten Kanal betreibt,
setzt `VOLT_ALLOW_UNSIGNED=1`.

`scripts/test-install-signature.sh` hält diese Prüfung gegen vier Fälle: eine
gültige Signatur, ein verändertes `latest.json`, eine fehlende Signatur und
eine mit fremdem Schlüssel. Herausgeschnitten wird die Funktion aus der
erzeugten Datei, nicht nachgebaut — eine Kopie im Test prüfte die Kopie. Der
Lauf hängt in der CI hinter shellcheck.

### Einen Schlüssel wechseln

Der eingebettete Schlüssel wird von *installierten* Binaries benutzt. Ein
Wechsel bricht deshalb die Update-Strecke aller Installationen, die noch den
alten tragen — sie lehnen das neue `latest.json` ab.

Der gangbare Weg: die neue Fassung mit dem **alten** Schlüssel signieren und im
Binary schon den **neuen** einbetten. Wer aktualisiert hat, prüft danach gegen
den neuen. Erst der übernächste Release wird mit dem neuen signiert.

## Der eigene Kanal

`update_base_url` und `update_channel` in der `config.yaml` zeigen auf einen
anderen Kanal. Wer einen eigenen betreibt und keinen Schlüssel führt, setzt

```yaml
update_allow_unsigned: true
```

Dann steht die Warnung in `volt doctor`, und das Update läuft wie zuvor: mit
Prüfsummenvergleich gegen dieselbe Quelle.
