#!/usr/bin/env bash
#
# Macht aus "Unveroeffentlicht" den Abschnitt einer Fassung.
#
#   scripts/changelog-release.sh v0.4.5
#
# Das ist der Handgriff vor jedem Tag. scripts/release-assets.sh schneidet den
# Abschnitt heraus und schreibt ihn als Release-Notes in latest.json — also in
# das, was ein Betreiber in der Update-Karte liest, bevor er auf
# "Aktualisieren" drueckt. Ohne diesen Schritt faellt es auf die von GoReleaser
# erzeugte Liste von Commit-Ueberschriften zurueck.

set -euo pipefail
cd "$(dirname "$0")/.."

TAG="${1:?Aufruf: $0 <tag>, etwa v0.4.5}"
DATEI="CHANGELOG.md"
LEER="Nichts — der letzte Stand ist veröffentlicht."

grep -q '^## Unveröffentlicht$' "$DATEI" \
    || { echo "In $DATEI steht kein Abschnitt \"Unveröffentlicht\"." >&2; exit 1; }
grep -q "^## $TAG " "$DATEI" \
    && { echo "$TAG steht schon in $DATEI." >&2; exit 1; }

# Steht dort nichts, gibt es nichts zu veroeffentlichen. Ein Abschnitt mit
# einem "Nichts" darin waere in den Release-Notes eine Zumutung.
if scripts/changelog-section.sh Unveröffentlicht | grep -qx "$LEER"; then
    echo "Unter \"Unveröffentlicht\" steht nichts." >&2
    exit 1
fi

awk -v tag="$TAG" -v heute="$(date +%F)" -v leer="$LEER" '
    /^## Unveröffentlicht$/ && !getan {
        print "## Unveröffentlicht"
        print ""
        print leer
        print ""
        print "## " tag " — " heute
        getan = 1
        next
    }
    { print }
' "$DATEI" > "$DATEI.neu"

mv "$DATEI.neu" "$DATEI"
echo "CHANGELOG.md: Unveröffentlicht -> $TAG"
