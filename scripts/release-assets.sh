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

for arch in amd64 arm64; do
    for name in volt volt-agent; do
        src="bin/${name}_linux_${arch}"
        [ -f "$src" ] || { echo "$src fehlt — lief make linux durch?" >&2; exit 1; }
        install -m 0755 "$src" "$OUT/${name}_linux_${arch}"
        sha "$OUT/${name}_linux_${arch}" > "$OUT/${name}_linux_${arch}.sha256"
    done
done

# Die Release-Notes wandern in den Fahrplan, damit das Panel sie anzeigen kann,
# ohne nach aussen zu telefonieren. GoReleaser hat das Release in diesem Lauf
# schon angelegt, der Text steht also bereit.
NOTES=""
if command -v gh >/dev/null 2>&1; then
    NOTES="$(gh release view "$TAG" --repo "$REPO" --json body --jq '.body' 2>/dev/null || true)"
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
    printf '\n  }\n}\n'
} > "$OUT/latest.json"

echo
echo "Kanal:    $CHANNEL"
echo "Version:  $VERSION"
ls -1 "$OUT"

if [ "${UPLOAD:-1}" = "1" ]; then
    command -v gh >/dev/null 2>&1 || { echo "gh fehlt — nichts hochgeladen." >&2; exit 0; }
    gh release upload "$TAG" "$OUT"/* --clobber
    echo "An Release $TAG gehängt."
fi
