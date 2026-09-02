#!/usr/bin/env bash
#
# Prueft die Signaturpruefung des Installers — mit den Zeilen, die wirklich
# ausgeliefert werden.
#
# Der Installer laeuft als root und tauscht Binaries ein; eine Pruefung darin,
# die niemand je fallen gesehen hat, ist keine. Deshalb wird hier ein
# Schluesselpaar erzeugt, ueber scripts/embed-release-key.sh eingesetzt — also
# ueber denselben Weg wie beim Veroeffentlichen — und dann verify_manifest aus
# der erzeugten Datei herausgeschnitten und gegen vier Faelle gehalten:
#
#   1. gueltige Signatur          -> angenommen
#   2. veraendertes latest.json   -> abgelehnt
#   3. Signatur fehlt             -> abgelehnt
#   4. mit fremdem Schluessel     -> abgelehnt
#
# Herausgeschnitten und nicht nachgebaut: eine Kopie der Funktion im Test
# prueft die Kopie, nicht den Installer.

set -euo pipefail
cd "$(dirname "$0")/.."

command -v openssl >/dev/null 2>&1 || { echo "openssl fehlt." >&2; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

openssl ecparam -genkey -name prime256v1 -noout -out "$TMP/key.pem" 2>/dev/null
openssl ec -in "$TMP/key.pem" -pubout -out "$TMP/key.pub" 2>/dev/null
openssl ecparam -genkey -name prime256v1 -noout -out "$TMP/fremd.pem" 2>/dev/null

scripts/embed-release-key.sh packaging/install.sh "$TMP/key.pub" > "$TMP/install.sh"
bash -n "$TMP/install.sh" || { echo "Der erzeugte Installer ist kein gueltiges bash." >&2; exit 1; }

# verify_manifest und der eingesetzte Schluessel aus der erzeugten Datei.
#
# Die einfachen Anfuehrungszeichen sind hier der Zweck und nicht das Versehen:
# geschrieben wird ein Skript, und $1 soll in ihm stehenbleiben, nicht beim
# Schreiben verschwinden.
# shellcheck disable=SC2016
{
    echo 'set -euo pipefail'
    echo 'warn() { echo "WARN: $1" >&2; }'
    echo 'info() { echo "INFO: $1"; }'
    echo 'die()  { echo "FEHLER: $1" >&2; exit 1; }'
    grep '^VOLT_ALLOW_UNSIGNED=' "$TMP/install.sh"
    sed -n "/^VOLT_RELEASE_KEY_PEM=/,/^'$/p" "$TMP/install.sh"
    sed -n '/^verify_manifest() {$/,/^}$/p' "$TMP/install.sh"
    echo 'verify_manifest "$1" "$2"'
} > "$TMP/probe.sh"
bash -n "$TMP/probe.sh" || { echo "Die herausgeschnittene Funktion ist unvollstaendig." >&2; exit 1; }

printf '{"version":"9.9.9","assets":{}}\n' > "$TMP/latest.json"
printf '{"version":"6.6.6","assets":{}}\n' > "$TMP/boese.json"
openssl dgst -sha256 -sign "$TMP/key.pem" -out "$TMP/sig.der" "$TMP/latest.json"
base64 < "$TMP/sig.der" | tr -d '\n' > "$TMP/latest.json.sig"
openssl dgst -sha256 -sign "$TMP/fremd.pem" -out "$TMP/fremd.der" "$TMP/latest.json"
base64 < "$TMP/fremd.der" | tr -d '\n' > "$TMP/fremd.sig"

fehler=0
erwarte() {
    local was="$1" soll="$2" datei="$3" sig="$4"
    if bash "$TMP/probe.sh" "$datei" "file://$sig" >/dev/null 2>&1; then
        ergebnis="angenommen"
    else
        ergebnis="abgelehnt"
    fi
    if [ "$ergebnis" = "$soll" ]; then
        printf '  ok   %-28s %s\n' "$was" "$ergebnis"
    else
        printf '  FEHL %-28s %s, erwartet %s\n' "$was" "$ergebnis" "$soll"
        fehler=1
    fi
}

echo "Signaturpruefung des Installers:"
erwarte "gueltige Signatur"      angenommen "$TMP/latest.json" "$TMP/latest.json.sig"
erwarte "veraendertes latest.json" abgelehnt  "$TMP/boese.json"  "$TMP/latest.json.sig"
erwarte "Signatur fehlt"          abgelehnt  "$TMP/latest.json" "$TMP/gibtsnicht.sig"
erwarte "fremder Schluessel"      abgelehnt  "$TMP/latest.json" "$TMP/fremd.sig"

# Die Ansage schaltet die Pruefung ganz ab — jeden Fehlerfall, nicht nur einen.
#
# Das war einmal anders: VOLT_ALLOW_UNSIGNED=1 wirkte nur, wenn das Skript
# keinen Schluessel trug. Bei einem Kanal ohne Unterschrift brach es trotzdem
# ab und nannte in der Meldung dieselbe Variable, die gerade gesetzt war.
erwarte_mit_ansage() {
    local was="$1" soll="$2" datei="$3" sig="$4"
    if VOLT_ALLOW_UNSIGNED=1 bash "$TMP/probe.sh" "$datei" "file://$sig" >/dev/null 2>&1; then
        ergebnis="angenommen"
    else
        ergebnis="abgelehnt"
    fi
    if [ "$ergebnis" = "$soll" ]; then
        printf '  ok   %-28s %s\n' "$was" "$ergebnis"
    else
        printf '  FEHL %-28s %s, erwartet %s\n' "$was" "$ergebnis" "$soll"
        fehler=1
    fi
}
erwarte_mit_ansage "auf Ansage: Signatur fehlt" angenommen "$TMP/latest.json" "$TMP/gibtsnicht.sig"
erwarte_mit_ansage "auf Ansage: Signatur passt nicht" angenommen "$TMP/boese.json" "$TMP/latest.json.sig"

# Und ohne Schluessel im Skript: nur auf ausdrueckliche Ansage.
# shellcheck disable=SC2016
{
    echo 'set -euo pipefail'
    echo 'warn() { echo "WARN: $1" >&2; }'
    echo 'info() { echo "INFO: $1"; }'
    echo 'die()  { echo "FEHLER: $1" >&2; exit 1; }'
    echo 'VOLT_ALLOW_UNSIGNED="${VOLT_ALLOW_UNSIGNED:-0}"'
    grep "^VOLT_RELEASE_KEY_PEM=''$" packaging/install.sh
    sed -n '/^verify_manifest() {$/,/^}$/p' packaging/install.sh
    echo 'verify_manifest "$1" "$2"'
} > "$TMP/ohne.sh"

if bash "$TMP/ohne.sh" "$TMP/latest.json" "file://$TMP/latest.json.sig" >/dev/null 2>&1; then
    printf '  FEHL %-28s angenommen, erwartet abgelehnt\n' "ohne Schluessel"
    fehler=1
else
    printf '  ok   %-28s abgelehnt\n' "ohne Schluessel"
fi
if VOLT_ALLOW_UNSIGNED=1 bash "$TMP/ohne.sh" "$TMP/latest.json" "file://$TMP/latest.json.sig" >/dev/null 2>&1; then
    printf '  ok   %-28s angenommen\n' "ohne Schluessel, auf Ansage"
else
    printf '  FEHL %-28s abgelehnt, erwartet angenommen\n' "ohne Schluessel, auf Ansage"
    fehler=1
fi

exit "$fehler"
