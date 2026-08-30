#!/usr/bin/env bash
#
# Baut das Vue-Frontend nach internal/webui/dist, von wo embed.FS es ins
# Binary nimmt. Ohne diesen Schritt liefert das Panel nur die API aus.

set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v npm >/dev/null 2>&1; then
    echo "npm wird zum Bauen des Frontends benötigt." >&2
    exit 1
fi

cd web

# npm ci verlangt eine Lockdatei und ist reproduzierbar; ohne sie tut es
# npm install auch.
if [ -f package-lock.json ]; then
    npm ci --silent
else
    npm install --silent
fi

npm run build

echo "Frontend gebaut: internal/webui/dist"
