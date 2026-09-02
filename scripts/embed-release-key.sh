#!/usr/bin/env bash
#
# Setzt den oeffentlichen Release-Schluessel in den Installer ein.
#
#   scripts/embed-release-key.sh packaging/install.sh internal/release/release.pub > install.sh
#
# Eigenes Skript, weil es zwei Aufrufer hat: scripts/build-pages.sh beim
# Veroeffentlichen und scripts/test-install-signature.sh beim Pruefen. Eine
# zweite, nachgebaute Einsetzung waere die Stelle, an der der Test etwas
# anderes prueft als das, was ausgeliefert wird.
#
# Die Quelle ist dieselbe Datei, die ueber //go:embed im Binary steckt:
# Installer und `volt update` pruefen damit gegen denselben Schluessel.

set -euo pipefail

INSTALLER="${1:?Aufruf: $0 <install.sh> <release.pub>}"
KEY="${2:?Aufruf: $0 <install.sh> <release.pub>}"

[ -s "$KEY" ] || { echo "$KEY ist leer — es gibt nichts einzusetzen." >&2; exit 1; }
grep -q "BEGIN PUBLIC KEY" "$KEY" || { echo "$KEY sieht nicht wie ein PEM aus." >&2; exit 1; }

# Das Hochkomma kommt ueber -v herein: es in ein einfach gequotetes
# awk-Programm zu schreiben ginge nicht, und \x27 versteht nicht jedes awk —
# mawk auf Ubuntu ist hier das Mass, nicht gawk.
awk -v keyfile="$KEY" -v q="'" '
    $0 == "VOLT_RELEASE_KEY_PEM=" q q {
        printf "%s", "VOLT_RELEASE_KEY_PEM=" q
        while ((getline zeile < keyfile) > 0) print zeile
        print q
        gefunden = 1
        next
    }
    { print }
    END {
        if (!gefunden) {
            print "Im Installer steht keine leere Zeile fuer den Schluessel." > "/dev/stderr"
            exit 1
        }
    }
' "$INSTALLER"
