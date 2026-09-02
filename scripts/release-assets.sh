#!/usr/bin/env bash
#
# Erzeugt die Dateien, die install.sh und `volt update` erwarten, und hängt sie
# an das GitHub-Release.
#
# GoReleaser baut die tar.gz-Archive für Menschen. Die beiden Programme laden
# dagegen einzelne Dateien: das nackte Binary, eine Prüfsumme daneben, und
# latest.json mit den Adressen beider Binaries. Genau das entsteht hier.
#
#   scripts/release-assets.sh v0.1.0
#   UPLOAD=0 scripts/release-assets.sh v0.1.0   # nur bauen, nichts hochladen

set -euo pipefail
cd "$(dirname "$0")/.."

TAG="${1:-${GITHUB_REF_NAME:-}}"
[ -n "$TAG" ] || { echo "Aufruf: $0 <tag>, etwa v0.1.0" >&2; exit 1; }
case "$TAG" in
    v*) ;;
    *)  echo "Der Tag muss mit v beginnen: $TAG" >&2; exit 1 ;;
esac

VERSION="${TAG#v}"
REPO="${GITHUB_REPOSITORY:-marion909/VoltPanel}"
BASE="https://github.com/${REPO}/releases/download/${TAG}"
OUT="dist/channel"

# Der Kanal steckt im Tag: v1.2.3 ist stabil, v1.2.3-beta.1 nicht.
CHANNEL=stable
case "$VERSION" in *-*) CHANNEL=beta ;; esac

sha() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

size() {
    # GNU und BSD stat sind sich hier nicht einig.
    stat -c%s "$1" 2>/dev/null || stat -f%z "$1"
}

rm -rf "$OUT"
mkdir -p "$OUT"
make linux VERSION="$VERSION"

# Die systemd-Units gehen mit ans Release. Ohne sie laeuft nach einem Update
# ein neues Programm unter einer alten Unit weiter - eine Drift, die erst
# auffaellt, wenn eine Operation aus einem Grund scheitert, den niemand in
# einer Unit-Datei sucht.
for unit in packaging/systemd/*; do
    install -m 0644 "$unit" "$OUT/$(basename "$unit")"
    sha "$OUT/$(basename "$unit")" > "$OUT/$(basename "$unit").sha256"
done

for arch in amd64 arm64; do
    for name in volt volt-agent; do
        src="bin/${name}_linux_${arch}"
        [ -f "$src" ] || { echo "$src fehlt — lief make linux durch?" >&2; exit 1; }
        install -m 0755 "$src" "$OUT/${name}_linux_${arch}"
        sha "$OUT/${name}_linux_${arch}" > "$OUT/${name}_linux_${arch}.sha256"
    done
done

# Die Release-Notes wandern in den Fahrplan, damit das Panel sie anzeigen kann,
# ohne nach aussen zu telefonieren.
#
# Zuerst CHANGELOG.md: was dort steht, hat jemand geschrieben, damit ein
# Betreiber es vor dem Update liest. Was GoReleaser erzeugt, ist eine Liste von
# Commit-Ueberschriften mit Hashes davor — brauchbar als Notnagel, aber nicht
# als Auskunft darueber, ob dieses Update etwas verlangt.
NOTES=""
if NOTES="$(scripts/changelog-section.sh "$TAG" 2>/dev/null)"; then
    echo "Release-Notes aus CHANGELOG.md ($TAG)"
elif command -v gh >/dev/null 2>&1; then
    NOTES="$(gh release view "$TAG" --repo "$REPO" --json body --jq '.body' 2>/dev/null || true)"
    echo "Kein CHANGELOG-Abschnitt fuer $TAG — Release-Notes von GitHub uebernommen" >&2
fi
# Gedeckelt: der Fahrplan wird bei jedem Dashboard-Aufruf geladen, und ein
# ausuferndes Changelog gehoert auf die Release-Seite, nicht ins Panel.
NOTES="$(printf '%s' "$NOTES" | head -c 8000)"

# JSON-gerecht kodieren. jq uebernimmt das Escapen von Zeilenumbruechen und
# Anfuehrungszeichen - von Hand waere das genau die Sorte Fehler, die erst beim
# ersten Release mit einem Apostroph auffaellt.
NOTES_JSON="$(printf '%s' "$NOTES" | jq -Rs .)"

# latest.json ist der einzige Fahrplan, den `volt update` liest. Die Adressen
# stehen absolut darin: der Kanal sagt nur, wo der Fahrplan liegt, nicht wo
# die Binaries liegen.
{
    printf '{\n'
    printf '  "version": "%s",\n' "$VERSION"
    printf '  "notes": %s,\n' "$NOTES_JSON"
    printf '  "url": "https://github.com/%s/releases/tag/%s",\n' "$REPO" "$TAG"
    printf '  "assets": {\n'
    sep=""
    for arch in amd64 arm64; do
        v="$OUT/volt_linux_$arch"
        a="$OUT/volt-agent_linux_$arch"
        printf '%b    "linux_%s": {\n' "$sep" "$arch"
        printf '      "url": "%s/volt_linux_%s",\n' "$BASE" "$arch"
        printf '      "sha256": "%s",\n' "$(sha "$v")"
        printf '      "size": %s,\n' "$(size "$v")"
        printf '      "agent": {\n'
        printf '        "url": "%s/volt-agent_linux_%s",\n' "$BASE" "$arch"
        printf '        "sha256": "%s",\n' "$(sha "$a")"
        printf '        "size": %s\n' "$(size "$a")"
        printf '      }\n'
        printf '    }'
        sep=",\n"
    done
    printf '\n  },\n'

    printf '  "units": {\n'
    sep=""
    for unit in packaging/systemd/*; do
        name="$(basename "$unit")"
        printf '%b    "%s": {\n' "$sep" "$name"
        printf '      "url": "%s/%s",\n' "$BASE" "$name"
        printf '      "sha256": "%s",\n' "$(sha "$OUT/$name")"
        printf '      "size": %s\n' "$(size "$OUT/$name")"
        printf '    }'
        sep=",\n"
    done
    printf '\n  }\n}\n'
} > "$OUT/latest.json"

# latest.json signieren.
#
# Die Datei traegt die Pruefsummen aller Bestandteile — wer sie signiert,
# signiert damit auch die Binaries. Ohne Signatur ist der Pruefsummenvergleich
# in `volt update` eine Pruefung gegen dieselbe Quelle: wer den Server
# beherrscht, liefert ein anderes Binary *und* die passende Summe.
#
# Zwei Wege, dasselbe Ergebnis: eine base64-kodierte ECDSA-Signatur im
# DER-Format ueber den SHA-256 des Rumpfs.
#
#   COSIGN_PRIVATE_KEY  ein von cosign erzeugter Schluessel, dazu COSIGN_PASSWORD
#   RELEASE_SIGNING_KEY ein blanker EC-Schluessel im PEM-Format, den openssl
#                       erzeugt hat — ohne cosign, ohne Passphrase
#
# Der zweite Weg steht hier, weil der erste ein Werkzeug verlangt, das auf
# keinem Rechner vorinstalliert ist und im Workflow erst geholt werden muss.
# Fuer eine Signatur ueber eine Datei ist das viel Umstand: `openssl dgst`
# erzeugt genau dasselbe, und geprueft wird ohnehin mit der Standardbibliothek
# von Go, nicht mit cosign.
#
# Der oeffentliche Teil gehoert nach internal/release/release.pub — eingebettet,
# nicht heruntergeladen: ein Schluessel von derselben Adresse wie die Datei, die
# er beglaubigen soll, beglaubigt nichts.
if [ -n "${RELEASE_SIGNING_KEY:-}" ]; then
    command -v openssl >/dev/null 2>&1 || { echo "openssl fehlt." >&2; exit 1; }
    KEYFILE="$(mktemp)"
    trap 'rm -f "$KEYFILE"' EXIT
    printf '%s\n' "$RELEASE_SIGNING_KEY" > "$KEYFILE"

    # Ueber eine Zwischendatei und nicht ueber eine Pipe: der Rueckgabewert
    # einer Pipe ist der des letzten Glieds, und ein gescheitertes Signieren
    # ergaebe sonst eine leere, aber gueltig aussehende Signatur.
    openssl dgst -sha256 -sign "$KEYFILE" -out "$OUT/latest.json.der" "$OUT/latest.json"
    base64 < "$OUT/latest.json.der" | tr -d '\n' > "$OUT/latest.json.sig"
    rm -f "$OUT/latest.json.der"
    echo "latest.json signiert (openssl)."
elif [ -n "${COSIGN_PRIVATE_KEY:-}" ]; then
    command -v cosign >/dev/null 2>&1 || { echo "cosign fehlt." >&2; exit 1; }
    cosign sign-blob --yes --key env://COSIGN_PRIVATE_KEY \
        --output-signature "$OUT/latest.json.sig" "$OUT/latest.json"
    echo "latest.json signiert (cosign)."
else
    echo "WARNUNG: weder RELEASE_SIGNING_KEY noch COSIGN_PRIVATE_KEY ist gesetzt —" >&2
    echo "         latest.json bleibt unsigniert. \`volt update\` lehnt diesen Kanal" >&2
    echo "         ab, solange update_allow_unsigned nicht gesetzt ist, und" >&2
    echo "         install.sh bricht ab. Siehe docs/release.md." >&2
fi

echo
echo "Kanal:    $CHANNEL"
echo "Version:  $VERSION"
ls -1 "$OUT"

if [ "${UPLOAD:-1}" = "1" ]; then
    command -v gh >/dev/null 2>&1 || { echo "gh fehlt — nichts hochgeladen." >&2; exit 0; }
    gh release upload "$TAG" "$OUT"/* --clobber
    echo "An Release $TAG gehängt."
fi
