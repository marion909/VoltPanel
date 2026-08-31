#!/usr/bin/env bash
#
# VoltPanel-Installer.
#
#   bash <(curl -fsSL https://get.voltpanel.dev/install.sh)
#
# Das Skript ist idempotent: ein zweiter Durchlauf repariert eine
# unvollständige Installation, statt sie zu verdoppeln.

set -euo pipefail

VOLT_CHANNEL="${VOLT_CHANNEL:-stable}"
VOLT_BASE_URL="${VOLT_BASE_URL:-https://get.voltpanel.dev}"
VOLT_PORT="${VOLT_PORT:-8443}"
# Die Domain, unter der das Panel erreichbar sein soll. Ohne sie laeuft das
# Panel unter der IP und behaelt sein selbstsigniertes Zertifikat.
VOLT_PANEL_DOMAIN="${VOLT_PANEL_DOMAIN:-}"
VOLT_ACME_EMAIL="${VOLT_ACME_EMAIL:-}"
VOLT_USER="volt"
VOLT_BIN_DIR="/usr/local/bin"
VOLT_CONFIG_DIR="/etc/volt"
VOLT_DATA_DIR="/var/lib/volt"
VOLT_LOG_DIR="/var/log/volt"
VOLT_BACKUP_DIR="/var/backups/volt"
VOLT_SITES_DIR="/var/www"

# VOLT_LOCAL_DIR überspringt den Download und nimmt bereits gebaute Binaries —
# der Weg, den die Entwicklung und die CI-Tests gehen.
VOLT_LOCAL_DIR="${VOLT_LOCAL_DIR:-}"

# --- Ausgabe ---------------------------------------------------------------

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
    C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'
    C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'
else
    C_RESET=""; C_BOLD=""; C_DIM=""; C_RED=""; C_GREEN=""; C_YELLOW=""
fi

step() { printf '%s==>%s %s\n' "$C_BOLD" "$C_RESET" "$1"; }
info() { printf '    %s%s%s\n' "$C_DIM" "$1" "$C_RESET"; }
warn() { printf '%s !  %s%s\n' "$C_YELLOW" "$1" "$C_RESET" >&2; }
die()  { printf '%sFehler:%s %s\n' "$C_RED" "$C_RESET" "$1" >&2; exit 1; }

# --- Vorprüfungen ----------------------------------------------------------

[ "$(id -u)" -eq 0 ] || die "Bitte als root ausführen (sudo)."

command -v systemctl >/dev/null 2>&1 || die "systemd wird benötigt."

if [ ! -r /etc/os-release ]; then
    die "/etc/os-release fehlt — die Distribution lässt sich nicht bestimmen."
fi
# shellcheck disable=SC1091
. /etc/os-release

case "${ID:-}:${VERSION_ID:-}" in
    debian:12*|debian:13*|ubuntu:24.04|ubuntu:24.10|ubuntu:25.*)
        ;;
    debian:*|ubuntu:*)
        warn "${PRETTY_NAME:-$ID $VERSION_ID} ist nicht getestet — die Installation läuft trotzdem weiter."
        ;;
    *)
        die "Nicht unterstützt: ${PRETTY_NAME:-$ID}. Erwartet werden Debian 12/13 oder Ubuntu 24.04."
        ;;
esac

case "$(uname -m)" in
    x86_64)  VOLT_ARCH="amd64" ;;
    aarch64) VOLT_ARCH="arm64" ;;
    *)       die "Nicht unterstützte Architektur: $(uname -m). Erwartet werden x86_64 oder aarch64." ;;
esac

printf '\n%sVoltPanel-Installation%s\n' "$C_BOLD" "$C_RESET"
info "${PRETTY_NAME:-$ID} · linux_${VOLT_ARCH} · Kanal ${VOLT_CHANNEL}"
printf '\n'

# --- Pakete ----------------------------------------------------------------

step "Systempakete"
export DEBIAN_FRONTEND=noninteractive

apt-get update -qq
apt-get install -y -qq --no-install-recommends \
    ca-certificates curl gnupg lsb-release \
    nginx openssl cron >/dev/null
info "nginx, curl, cron installiert"

# Sury liefert die parallel installierbaren PHP-Versionen, die Debian selbst
# nicht mitbringt. Ohne dieses Repo gäbe es nur genau eine PHP-Version.
if [ ! -f /etc/apt/sources.list.d/php.sources ] && [ ! -f /etc/apt/sources.list.d/php.list ]; then
    step "PHP-Repository (Sury)"
    curl -fsSL https://packages.sury.org/php/apt.gpg \
        -o /etc/apt/trusted.gpg.d/sury-php.gpg
    echo "deb https://packages.sury.org/php/ $(lsb_release -sc) main" \
        > /etc/apt/sources.list.d/php.list
    apt-get update -qq
    info "Sury eingebunden — Multi-PHP verfügbar"
fi

if ! command -v php-fpm8.3 >/dev/null 2>&1 && [ ! -d /etc/php/8.3 ]; then
    step "PHP 8.3"
    apt-get install -y -qq --no-install-recommends \
        php8.3-fpm php8.3-cli php8.3-mysql php8.3-curl php8.3-gd \
        php8.3-mbstring php8.3-xml php8.3-zip php8.3-intl >/dev/null
    info "PHP 8.3 mit den gängigen Erweiterungen installiert"
fi

# MariaDB gehört zur Grundinstallation: der Agent spricht für jede
# Datenbank-Operation direkt über /var/run/mysqld/mysqld.sock, nicht über eine
# Shell. Ohne Server scheitert `volt db add` beim ersten Aufruf.
# VOLT_SKIP_MARIADB=1 überspringt ihn, wenn die Datenbank woanders läuft.
if [ -z "${VOLT_SKIP_MARIADB:-}" ] \
   && ! command -v mysqld >/dev/null 2>&1 \
   && ! command -v mariadbd >/dev/null 2>&1; then
    step "MariaDB"
    apt-get install -y -qq --no-install-recommends \
        mariadb-server mariadb-client >/dev/null
    systemctl enable --now mariadb >/dev/null 2>&1 || true
    # root meldet sich auf Debian über den Socket an — genau der Weg, den der
    # Agent nimmt. Es gibt damit kein Datenbankpasswort, das irgendwo liegt.
    info "MariaDB installiert, root über Socket-Auth"
fi

# --- Benutzer und Verzeichnisse -------------------------------------------

step "Benutzer und Verzeichnisse"

if ! id -u "$VOLT_USER" >/dev/null 2>&1; then
    useradd --system --home-dir "$VOLT_DATA_DIR" --shell /usr/sbin/nologin \
        --user-group --comment "VoltPanel" "$VOLT_USER"
    info "Systembenutzer $VOLT_USER angelegt"
else
    info "Systembenutzer $VOLT_USER existiert bereits"
fi

install -d -o "$VOLT_USER" -g "$VOLT_USER" -m 0750 \
    "$VOLT_DATA_DIR" "$VOLT_LOG_DIR" "$VOLT_BACKUP_DIR" \
    "$VOLT_DATA_DIR/certs" "$VOLT_DATA_DIR/acme" "$VOLT_LOG_DIR/sites"
install -d -o root -g "$VOLT_USER" -m 0750 "$VOLT_CONFIG_DIR"
install -d -m 0755 "$VOLT_SITES_DIR"

# Das ACME-Webroot muss der Nginx-Benutzer lesen können.
chmod 0755 "$VOLT_DATA_DIR" "$VOLT_DATA_DIR/acme"

# --- Binaries --------------------------------------------------------------

step "VoltPanel"

fetch_binary() {
    local name="$1" dest="$2"

    if [ -n "$VOLT_LOCAL_DIR" ]; then
        [ -f "$VOLT_LOCAL_DIR/$name" ] || die "$VOLT_LOCAL_DIR/$name nicht gefunden."
        install -m 0755 "$VOLT_LOCAL_DIR/$name" "$dest"
        return
    fi

    local url="${VOLT_BASE_URL}/${VOLT_CHANNEL}/${name}_linux_${VOLT_ARCH}"
    local tmp
    tmp="$(mktemp)"
    # Erst vollständig herunterladen, dann an den Zielort schieben: ein
    # abgebrochener Download darf kein halbes Binary hinterlassen.
    curl -fsSL --retry 3 --retry-delay 2 "$url" -o "$tmp" \
        || die "Download fehlgeschlagen: $url"

    if curl -fsSL "${url}.sha256" -o "${tmp}.sha256" 2>/dev/null; then
        local want got
        want="$(cut -d' ' -f1 < "${tmp}.sha256")"
        got="$(sha256sum "$tmp" | cut -d' ' -f1)"
        [ "$want" = "$got" ] || die "Prüfsumme von $name stimmt nicht."
        rm -f "${tmp}.sha256"
    else
        warn "Keine Prüfsumme für $name gefunden."
    fi

    install -m 0755 "$tmp" "$dest"
    rm -f "$tmp"
}

fetch_binary volt "$VOLT_BIN_DIR/volt"
fetch_binary volt-agent "$VOLT_BIN_DIR/volt-agent"
info "$("$VOLT_BIN_DIR/volt" --version)"

# --- Konfiguration ---------------------------------------------------------

step "Konfiguration"

if [ ! -f "$VOLT_CONFIG_DIR/config.yaml" ]; then
    # Ein zufälliger Pfad hält das Panel aus den Ergebnislisten von
    # Portscannern heraus. Er ersetzt keine Anmeldung, aber er senkt das
    # Grundrauschen an Angriffsversuchen deutlich.
    ACCESS_PATH="$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"

    cat > "$VOLT_CONFIG_DIR/config.yaml" <<CONF
# VoltPanel-Konfiguration
# Nach jeder Änderung: systemctl restart volt-web

listen_addr: 0.0.0.0
port: $VOLT_PORT

# Das Panel ist nur unter diesem Pfad erreichbar.
access_path: $ACCESS_PATH

# Hostname des Panels. Er steht im selbstsignierten Zertifikat und bestimmt,
# welches Zertifikat volt-web nach einem `volt cert issue` uebernimmt.
panel_domain: $VOLT_PANEL_DOMAIN

# Das Panel terminiert TLS selbst — es muss auch dann erreichbar sein, wenn
# nginx gerade nicht laeuft. Auf false nur, wenn ein eigener Proxy davor sitzt.
tls: true

data_dir: $VOLT_DATA_DIR
config_dir: $VOLT_CONFIG_DIR
log_dir: $VOLT_LOG_DIR
sites_dir: $VOLT_SITES_DIR
backup_dir: $VOLT_BACKUP_DIR
db_path: $VOLT_DATA_DIR/volt.db
cert_dir: $VOLT_DATA_DIR/certs
socket_path: /run/volt/agent.sock
secret_key_path: $VOLT_CONFIG_DIR/secret.key

session_ttl_min: 720
update_channel: $VOLT_CHANNEL

# Für Let's Encrypt zwingend — hierhin gehen Ablaufwarnungen.
acme_email: "$VOLT_ACME_EMAIL"

# Nur diese Adressen dürfen ans Panel. Leer lassen = keine Einschränkung.
# ip_whitelist: 203.0.113.5, 198.51.100.0/24
CONF
    chown root:"$VOLT_USER" "$VOLT_CONFIG_DIR/config.yaml"
    chmod 0640 "$VOLT_CONFIG_DIR/config.yaml"
    info "Konfiguration angelegt"
else
    ACCESS_PATH="$(awk -F': *' '/^access_path:/ {print $2}' "$VOLT_CONFIG_DIR/config.yaml")"
    info "Bestehende Konfiguration beibehalten"
fi

# --- systemd ---------------------------------------------------------------

step "Dienste"

install_unit() {
    local name="$1"
    if [ -n "$VOLT_LOCAL_DIR" ] && [ -f "$VOLT_LOCAL_DIR/systemd/$name" ]; then
        install -m 0644 "$VOLT_LOCAL_DIR/systemd/$name" "/etc/systemd/system/$name"
    else
        curl -fsSL "${VOLT_BASE_URL}/${VOLT_CHANNEL}/systemd/${name}" \
            -o "/etc/systemd/system/${name}" || die "Unit $name nicht ladbar."
        chmod 0644 "/etc/systemd/system/${name}"
    fi
}

for unit in volt-agent.service volt-web.service \
            volt-renew.service volt-renew.timer \
            volt-backup.service volt-backup.timer; do
    install_unit "$unit"
done

systemctl daemon-reload
systemctl enable --now volt-agent.service >/dev/null 2>&1
# Der Agent legt den Socket beim Start an; das Web darf erst danach starten.
for _ in $(seq 1 20); do
    [ -S /run/volt/agent.sock ] && break
    sleep 0.25
done
systemctl enable --now volt-web.service >/dev/null 2>&1
systemctl enable --now volt-renew.timer volt-backup.timer >/dev/null 2>&1
info "volt-agent und volt-web laufen, Timer für Erneuerung und Backup aktiv"

# --- Firewall --------------------------------------------------------------

step "Firewall"

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "^Status: active"; then
    ufw allow "$VOLT_PORT/tcp" >/dev/null 2>&1 || true
    ufw allow 80/tcp >/dev/null 2>&1 || true
    ufw allow 443/tcp >/dev/null 2>&1 || true
    info "ufw: Ports $VOLT_PORT, 80 und 443 freigegeben"
elif command -v nft >/dev/null 2>&1; then
    warn "nftables gefunden — Port $VOLT_PORT bitte selbst freigeben."
else
    info "Keine aktive Firewall erkannt"
fi

# --- Ersteinrichtung -------------------------------------------------------

step "Ersteinrichtung"

SETUP_OUTPUT=""
if ! sudo -u "$VOLT_USER" "$VOLT_BIN_DIR/volt" user list >/dev/null 2>&1 \
   || [ -z "$(sudo -u "$VOLT_USER" "$VOLT_BIN_DIR/volt" user list 2>/dev/null | sed -n '2p')" ]; then
    SETUP_OUTPUT="$(sudo -u "$VOLT_USER" "$VOLT_BIN_DIR/volt" setup \
        --email "admin@$(hostname -f 2>/dev/null || hostname)" 2>&1 || true)"
else
    info "Es existiert bereits ein Benutzer — die Ersteinrichtung wird übersprungen."
fi

# --- Abschluss -------------------------------------------------------------

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "$IP" ] || IP="<server-ip>"

HOST="$IP"
[ -n "$VOLT_PANEL_DOMAIN" ] && HOST="$VOLT_PANEL_DOMAIN"

URL="https://${HOST}:${VOLT_PORT}"
[ -n "${ACCESS_PATH:-}" ] && URL="${URL}/${ACCESS_PATH}"

printf '\n%s%sVoltPanel ist installiert.%s\n\n' "$C_GREEN" "$C_BOLD" "$C_RESET"
printf '  Panel:  %s%s%s\n' "$C_BOLD" "$URL" "$C_RESET"

if [ -n "$SETUP_OUTPUT" ]; then
    printf '%s\n' "$SETUP_OUTPUT" | sed 's/^/  /'
fi

cat <<HINT

  Nächste Schritte (als Benutzer $VOLT_USER, dem die Datenbank gehört):
    sudo -u $VOLT_USER volt doctor                          Selbstdiagnose
    sudo -u $VOLT_USER volt site add example.at --php 8.3   erste Website
    sudo -u $VOLT_USER volt cert issue example.at           Zertifikat holen

HINT

if [ -n "$VOLT_PANEL_DOMAIN" ]; then
cat <<HINT
  Das Panel zeigt noch ein selbstsigniertes Zertifikat. Sobald
  $VOLT_PANEL_DOMAIN auf diesen Server zeigt:

    sudo -u $VOLT_USER volt cert issue $VOLT_PANEL_DOMAIN

  volt-web übernimmt es ohne Neustart.

HINT
else
cat <<'HINT'
  Das Panel zeigt ein selbstsigniertes Zertifikat, weil kein Hostname
  hinterlegt ist. Mit `panel_domain:` in /etc/volt/config.yaml und
  anschließendem `volt cert issue <domain>` gibt es ein gültiges.

HINT
fi
