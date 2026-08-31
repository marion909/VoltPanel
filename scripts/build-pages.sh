#!/usr/bin/env bash
#
# Baut die Bezugsquelle get.voltpanel.dev als statische Seite für GitHub Pages.
#
# Sie darf ausdrücklich nicht auf dem Server liegen, der mit ihr installiert
# wird: sonst bräuchte man ein laufendes VoltPanel, um VoltPanel zu
# installieren. Pages hängt an nichts, was hier ausfallen könnte.
#
# Ausgeliefert werden nur Textdateien. Die Binaries bleiben am GitHub-Release,
# latest.json zeigt mit absoluten Adressen dorthin — es gibt also keinen
# zweiten Ort, an dem eine veraltete Kopie liegen bleiben könnte.
#
#   scripts/build-pages.sh v0.1.0

set -euo pipefail
cd "$(dirname "$0")/.."

# Ohne Argument das neueste Release nehmen. Der Aufruf kommt auch aus einem
# Lauf auf main, wo GITHUB_REF_NAME "main" waere und kein Tag.
TAG="${1:-}"
case "$TAG" in
    v*) ;;
    *)  command -v gh >/dev/null 2>&1 \
            || { echo "Aufruf: $0 <tag>, etwa v0.1.0 (oder gh installieren)" >&2; exit 1; }
        TAG="$(gh release list --limit 1 --json tagName --jq '.[0].tagName' 2>/dev/null || true)"
        [ -n "$TAG" ] || { echo "Kein Release gefunden — erst eines veroeffentlichen." >&2; exit 1; }
        echo "Neuestes Release: $TAG"
        ;;
esac

VERSION="${TAG#v}"
REPO="${GITHUB_REPOSITORY:-marion909/VoltPanel}"
DOMAIN="${PAGES_DOMAIN:-get.voltpanel.dev}"
BASE="${PAGES_BASE:-https://$DOMAIN}"
OUT="dist/pages"

CHANNEL=stable
case "$VERSION" in *-*) CHANNEL=beta ;; esac

rm -rf "$OUT"
mkdir -p "$OUT/$CHANNEL/systemd"

# Pages ersetzt bei jedem Deploy die ganze Seite. Ohne diesen Schritt würde
# ein Beta-Release den stabilen Kanal von der Seite putzen — und jedes
# installierte Panel liefe in einen 404.
for other in stable beta; do
    [ "$other" = "$CHANNEL" ] && continue
    if curl -fsSL "$BASE/$other/latest.json" -o /tmp/other-latest.json 2>/dev/null; then
        mkdir -p "$OUT/$other"
        mv /tmp/other-latest.json "$OUT/$other/latest.json"
        echo "Kanal $other unverändert übernommen"
    fi
done

echo "$DOMAIN" > "$OUT/CNAME"

# Der Einzeiler holt sich genau diese Datei.
install -m 0644 packaging/install.sh "$OUT/install.sh"
install -m 0644 packaging/systemd/* "$OUT/$CHANNEL/systemd/"

# latest.json kommt aus dem Release, damit Fahrplan und Binaries garantiert
# aus demselben Lauf stammen.
if [ -f dist/channel/latest.json ]; then
    install -m 0644 dist/channel/latest.json "$OUT/$CHANNEL/latest.json"
else
    command -v gh >/dev/null 2>&1 || { echo "gh fehlt und dist/channel/latest.json auch." >&2; exit 1; }
    gh release download "$TAG" --repo "$REPO" --pattern latest.json --dir "$OUT/$CHANNEL"
fi

cat > "$OUT/index.html" <<HTML
<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>VoltPanel</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 16px/1.6 system-ui, sans-serif; max-width: 42rem;
         margin: 4rem auto; padding: 0 1.5rem; }
  pre { background: #f4f4f2; padding: 1rem; border-radius: .5rem;
        overflow-x: auto; }
  @media (prefers-color-scheme: dark) { pre { background: #23231f; } }
  a { color: #2a78d6; }
</style>
<h1>VoltPanel</h1>
<p>Ein selbst gehostetes Linux Hosting Control Panel. Installation auf
   Debian 12/13 oder Ubuntu 24.04:</p>
<pre>bash &lt;(curl -fsSL https://$DOMAIN/install.sh)</pre>
<p>Aktuelle Version im Kanal <code>$CHANNEL</code>: <strong>$VERSION</strong> &middot;
   <a href="https://github.com/$REPO">Quellcode und Dokumentation</a></p>
HTML

echo
echo "Domain:  $DOMAIN"
echo "Kanal:   $CHANNEL ($VERSION)"
find "$OUT" -type f | sort
