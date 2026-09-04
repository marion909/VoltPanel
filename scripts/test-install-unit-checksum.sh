#!/usr/bin/env bash
#
# Prueft, dass install_unit() eine systemd-Unit gegen die im latest.json
# hinterlegte Pruefsumme haelt, bevor sie an ihren Zielort gelangt — mit den
# Zeilen, die wirklich ausgeliefert werden.
#
# Vorher lud install_unit() jede Unit ohne jeden Abgleich; anders als die
# Binaries, die durch verify_manifest+download_verified laufen, waere eine
# root-ExecStart-Zeile ueber den Weg zu ${VOLT_BASE_URL}/${VOLT_CHANNEL}/systemd/
# unterschiebbar gewesen.
#
# Herausgeschnitten und nicht nachgebaut: eine Kopie der Funktionen im Test
# prueft die Kopie, nicht den Installer. Einzige Anpassung am Text: der
# Zielpfad /etc/systemd/system wird auf ein Testverzeichnis umgebogen — ohne
# root waere er nicht beschreibbar.

set -euo pipefail
cd "$(dirname "$0")/.."

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/kanal/systemd" "$TMP/etc-systemd-system"
echo "[Service]
ExecStart=/usr/local/bin/volt-agent" > "$TMP/kanal/systemd/volt-agent.service"

sha() { sha256sum "$1" | cut -d' ' -f1; }
GUELTIG_SHA="$(sha "$TMP/kanal/systemd/volt-agent.service")"

printf '{"units":{"volt-agent.service":{"sha256":"%s"}}}\n' "$GUELTIG_SHA" \
    > "$TMP/latest-gut.json"
printf '{"units":{"volt-agent.service":{}}}\n' > "$TMP/latest-ohne-sha.json"

# Geschrieben wird ein Skript, $1 soll darin stehenbleiben statt beim
# Schreiben zu verschwinden — dieselbe Begründung wie in
# test-install-signature.sh.
# shellcheck disable=SC2016
{
    echo 'set -euo pipefail'
    echo 'warn() { echo "WARN: $1" >&2; }'
    echo 'info() { echo "INFO: $1"; }'
    echo 'die()  { echo "FEHLER: $1" >&2; exit 1; }'
    echo 'VOLT_LOCAL_DIR=""'
    echo "VOLT_BASE_URL=\"file://$TMP/kanal\""
    echo 'VOLT_CHANNEL="."'
    sed -n '/^download_verified() {$/,/^}$/p' packaging/install.sh
    sed -n '/^install_unit() {$/,/^}$/p' packaging/install.sh \
        | sed "s#/etc/systemd/system#$TMP/etc-systemd-system#g"
    echo 'MANIFEST_FILE="$1"'
    echo 'install_unit "volt-agent.service"'
} > "$TMP/probe.sh"
bash -n "$TMP/probe.sh" || { echo "Die herausgeschnittene Funktion ist unvollstaendig." >&2; exit 1; }

fehler=0
lauf() {
    local was="$1" soll="$2" manifest="$3" hatte_datei ok
    rm -f "$TMP/etc-systemd-system/volt-agent.service"

    if bash "$TMP/probe.sh" "$manifest" >/dev/null 2>&1; then
        ergebnis="angenommen"
    else
        ergebnis="abgelehnt"
    fi
    hatte_datei=0
    [ -f "$TMP/etc-systemd-system/volt-agent.service" ] && hatte_datei=1

    # Erwartet zusammenpassend: "angenommen" nur mit installierter Datei,
    # "abgelehnt" nur ohne — ein Fehlschlag darf keine halb geprüfte Unit
    # hinterlassen. Verschachtelt statt mit &&/|| verkettet: eine Kette aus
    # Befehlen ist keine Boolesche Formel, ihr Ergebnis ist nur der Exitcode
    # des zuletzt ausgeführten Glieds.
    ok=0
    if [ "$ergebnis" = "$soll" ]; then
        if [ "$soll" = "angenommen" ] && [ "$hatte_datei" = 1 ]; then
            ok=1
        elif [ "$soll" = "abgelehnt" ] && [ "$hatte_datei" = 0 ]; then
            ok=1
        fi
    fi

    if [ "$ok" = 1 ]; then
        printf '  ok   %-28s %s\n' "$was" "$ergebnis"
    else
        printf '  FEHL %-28s %s, erwartet %s (datei da: %s)\n' "$was" "$ergebnis" "$soll" "$hatte_datei"
        fehler=1
    fi
}

echo "Pruefsummenpruefung der systemd-Units:"
lauf "gueltige Pruefsumme"     angenommen "$TMP/latest-gut.json"

# Die Unit-Datei am Downloadpfad weicht von dem ab, was latest.json als
# Pruefsumme nennt — genau die Angriffsbewegung, die der Fix verhindert: ein
# manipulierter Weg zu .../systemd/, waehrend latest.json unangetastet bleibt.
cp "$TMP/kanal/systemd/volt-agent.service" "$TMP/kanal/systemd/volt-agent.service.orig"
echo "[Service]
ExecStart=/bin/sh -c boese
User=root" > "$TMP/kanal/systemd/volt-agent.service"
lauf "manipulierte unit-datei"  abgelehnt "$TMP/latest-gut.json"
cp "$TMP/kanal/systemd/volt-agent.service.orig" "$TMP/kanal/systemd/volt-agent.service"

lauf "latest.json ohne sha256" abgelehnt "$TMP/latest-ohne-sha.json"

exit "$fehler"
