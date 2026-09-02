#!/usr/bin/env bash
#
# Schneidet einen Abschnitt aus CHANGELOG.md heraus.
#
#   scripts/changelog-section.sh v0.3.9
#
# Gebraucht von scripts/release-assets.sh: was hier herauskommt, steht danach
# als Release-Notes im Fahrplan und damit in der Update-Karte des Panels. Wer
# den Abschnitt schreibt, schreibt also das, was ein Betreiber vor dem Update
# liest — und nicht eine Liste von Commit-Hashes.
#
# Ohne passenden Abschnitt: Rueckgabewert 1 und keine Ausgabe. Der Aufrufer
# entscheidet dann, was er stattdessen nimmt.

set -euo pipefail
cd "$(dirname "$0")/.."

TAG="${1:?Aufruf: $0 <tag>, etwa v0.3.9}"
DATEI="${2:-CHANGELOG.md}"

[ -f "$DATEI" ] || { echo "$DATEI fehlt." >&2; exit 1; }

# Von der Ueberschrift bis zur naechsten Ueberschrift derselben Ebene.
#
# Der Vergleich steht auf dem zweiten Feld: "## v0.3.9 — 2026-09-02" passt auf
# "v0.3.9", aber "## v0.3.90" nicht. Ein Praefixvergleich taete genau das
# Falsche, und zwar erst bei der zehnten Fassung eines Zweigs.
ABSCHNITT="$(awk -v tag="$TAG" '
    /^## / {
        if (drin) exit
        drin = ($2 == tag)
        next
    }
    drin { print }
' "$DATEI")"

# Fuehrende Leerzeilen weg; die abschliessenden nimmt die Kommandosubstitution
# schon.
ABSCHNITT="$(printf '%s\n' "$ABSCHNITT" | sed -e '/./,$!d')"

[ -n "$ABSCHNITT" ] || exit 1
printf '%s\n' "$ABSCHNITT"
