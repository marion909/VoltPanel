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

# Wer ohne Signaturpruefung installieren will, sagt es ausdruecklich. Das ist
# dieselbe Entscheidung wie update_allow_unsigned in der config.yaml, nur
# einen Schritt frueher.
VOLT_ALLOW_UNSIGNED="${VOLT_ALLOW_UNSIGNED:-0}"

# Der oeffentliche Release-Schluessel.
#
# Im Quelltext leer; scripts/build-pages.sh setzt beim Veroeffentlichen den
# Inhalt von internal/release/release.pub ein — dieselbe Datei, die auch im
# Binary steckt, damit Installer und `volt update` gegen denselben Schluessel
# pruefen.
#
# Er steht hier und wird nicht nachgeladen: ein Schluessel, den man sich bei
# derselben Adresse holt wie die Datei, die er beglaubigen soll, beglaubigt
# gar nichts. Wer dieses Skript ueber https von get.voltpanel.dev laedt,
# vertraut ihm ohnehin schon — es laeuft gleich als root.
VOLT_RELEASE_KEY_PEM=''

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
    ca-certificates curl gnupg lsb-release jq \
    nginx openssl cron >/dev/null
info "nginx, curl, jq, cron installiert"

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
# /etc/volt gehoert root, damit ein uebernommener Web-Prozess die config.yaml
# nicht umschreiben kann: sie legt die Wurzeln fest, innerhalb derer der Agent
# ueberhaupt Dateien anfassen darf. Der Panel-Benutzer braucht aber einen Ort
# fuer seinen Schluessel — dafuer dieses Unterverzeichnis.
install -d -o "$VOLT_USER" -g "$VOLT_USER" -m 0700 "$VOLT_CONFIG_DIR/keys"
install -d -m 0755 "$VOLT_SITES_DIR"

# Das ACME-Webroot muss der Nginx-Benutzer lesen können.
chmod 0755 "$VOLT_DATA_DIR" "$VOLT_DATA_DIR/acme"

# --- Binaries --------------------------------------------------------------

step "VoltPanel"

# Alles, was geladen wird, steht in latest.json: Adresse, Prüfsumme und Größe
# beider Binaries. Ein einziger Fahrplan statt vier einzeln geratener URLs —
# und die Prüfsumme ist damit nicht mehr optional. Fehlte sie früher, lief die
# Installation mit einer Warnung weiter; genau dann hätte sie stehen müssen.
download_verified() {
    local url="$1" want="$2" dest="$3" tmp got

    [ -n "$want" ] || die "Für $url ist keine Prüfsumme hinterlegt."

    tmp="$(mktemp)"
    # Erst vollständig herunterladen, dann an den Zielort schieben: ein
    # abgebrochener Download darf kein halbes Binary hinterlassen.
    if ! curl -fsSL --retry 3 --retry-delay 2 "$url" -o "$tmp"; then
        rm -f "$tmp"
        die "Download fehlgeschlagen: $url"
    fi

    got="$(sha256sum "$tmp" | cut -d' ' -f1)"
    if [ "$want" != "$got" ]; then
        rm -f "$tmp"
        die "Prüfsumme von $url stimmt nicht (erwartet $want, bekommen $got)."
    fi

    install -m 0755 "$tmp" "$dest"
    rm -f "$tmp"
}

# Die Signatur ueber latest.json.
#
# Der Pruefsummenvergleich in download_verified allein schuetzt nichts: die
# Summe steht in derselben Datei, die von derselben Adresse kommt. Wer den
# Server oder die Leitung dorthin beherrscht, liefert ein anderes Binary und
# die passende Summe gleich mit — und das naechste volt-agent laeuft als root.
#
# Deshalb wird die Datei signiert, die die Summen traegt. Format wie bei
# `volt update`: `cosign sign-blob --key`, also eine base64-kodierte
# ECDSA-Signatur im DER-Format ueber den SHA-256 des Rumpfs. Das prueft
# openssl direkt.
verify_manifest() {
    local file="$1" sig_url="$2" keyfile sigfile

    if [ -z "$VOLT_RELEASE_KEY_PEM" ]; then
        if [ "$VOLT_ALLOW_UNSIGNED" != "1" ]; then
            die "Dieses Installationsskript traegt keinen Release-Schluessel; die Angaben unter $sig_url liessen sich damit nicht pruefen. Die veroeffentlichte Fassung unter https://get.voltpanel.dev/install.sh traegt ihn. Wer bewusst einen unsignierten Kanal betreibt, setzt VOLT_ALLOW_UNSIGNED=1."
        fi
        warn "Ohne Signaturpruefung installiert (VOLT_ALLOW_UNSIGNED=1)."
        return 0
    fi

    command -v openssl >/dev/null 2>&1 || die "openssl fehlt — ohne es laesst sich die Signatur nicht pruefen."

    keyfile="$(mktemp)"; sigfile="$(mktemp)"
    printf '%s\n' "$VOLT_RELEASE_KEY_PEM" > "$keyfile"

    if ! curl -fsSL --retry 3 --retry-delay 2 "$sig_url" -o "$sigfile.b64"; then
        rm -f "$keyfile" "$sigfile" "$sigfile.b64"
        die "Zu den Release-Angaben gibt es keine Signatur ($sig_url). Wer bewusst einen unsignierten Kanal betreibt, setzt VOLT_ALLOW_UNSIGNED=1."
    fi
    if ! base64 -d < "$sigfile.b64" > "$sigfile" 2>/dev/null; then
        rm -f "$keyfile" "$sigfile" "$sigfile.b64"
        die "Die Signatur unter $sig_url ist kein base64."
    fi

    if ! openssl dgst -sha256 -verify "$keyfile" -signature "$sigfile" "$file" >/dev/null 2>&1; then
        rm -f "$keyfile" "$sigfile" "$sigfile.b64"
        die "Die Signatur der Release-Angaben stimmt nicht. Hier wird nichts installiert."
    fi
    rm -f "$keyfile" "$sigfile" "$sigfile.b64"
    info "Signatur der Release-Angaben geprueft"
}

# Die jq-Filter unten stehen absichtlich in einfachen Anfuehrungszeichen: $k
# ist eine jq-Variable aus --arg, keine der Shell. Genau hier ist SC2016 nicht
# zutreffend — und nur hier.
# shellcheck disable=SC2016
if [ -n "$VOLT_LOCAL_DIR" ]; then
    for name in volt volt-agent; do
        [ -f "$VOLT_LOCAL_DIR/$name" ] || die "$VOLT_LOCAL_DIR/$name nicht gefunden."
        install -m 0755 "$VOLT_LOCAL_DIR/$name" "$VOLT_BIN_DIR/$name"
    done
    info "Binaries aus $VOLT_LOCAL_DIR übernommen"
else
    MANIFEST_URL="${VOLT_BASE_URL}/${VOLT_CHANNEL}/latest.json"
    MANIFEST_FILE="$(mktemp)"
    curl -fsSL --retry 3 --retry-delay 2 "$MANIFEST_URL" -o "$MANIFEST_FILE" \
        || die "Kanal $VOLT_CHANNEL nicht erreichbar: $MANIFEST_URL"

    verify_manifest "$MANIFEST_FILE" "${MANIFEST_URL}.sig"

    # Aus der Datei und nicht aus einer Variablen: geprueft wurden die Bytes
    # auf der Platte. Wer sie erst in eine Variable liest, prueft etwas
    # anderes als das, wonach er sich richtet — die Kommandosubstitution
    # schneidet Zeilenumbraeche am Ende ab.
    manifest_field() {
        jq -r --arg k "linux_${VOLT_ARCH}" "$1" "$MANIFEST_FILE" 2>/dev/null
    }

    REL_VERSION="$(manifest_field '.version // empty')"
    [ -n "$REL_VERSION" ] || die "Die Kanalbeschreibung unter $MANIFEST_URL ist unbrauchbar."

    VOLT_URL="$(manifest_field '.assets[$k].url // empty')"
    VOLT_SHA="$(manifest_field '.assets[$k].sha256 // empty')"
    AGENT_URL="$(manifest_field '.assets[$k].agent.url // empty')"
    AGENT_SHA="$(manifest_field '.assets[$k].agent.sha256 // empty')"

    [ -n "$VOLT_URL" ] || die "Version $REL_VERSION hat kein Paket für linux_${VOLT_ARCH}."
    # Ohne den Agent gäbe es ein Panel, das nichts ausführen kann.
    [ -n "$AGENT_URL" ] || die "Version $REL_VERSION enthält kein volt-agent für linux_${VOLT_ARCH}."

    info "Version $REL_VERSION aus Kanal $VOLT_CHANNEL"
    download_verified "$VOLT_URL"  "$VOLT_SHA"  "$VOLT_BIN_DIR/volt"
    download_verified "$AGENT_URL" "$AGENT_SHA" "$VOLT_BIN_DIR/volt-agent"
fi

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
# welches Zertifikat volt-web nach einem Zertifikatsbezug uebernimmt.
panel_domain: $VOLT_PANEL_DOMAIN

# Das Panel terminiert TLS selbst — es muss auch dann erreichbar sein, wenn
# nginx gerade nicht laeuft. Auf false nur, wenn ein eigener Proxy davor sitzt.
tls: true

data_dir: $VOLT_DATA_DIR
config_dir: $VOLT_CONFIG_DIR
log_dir: $VOLT_LOG_DIR
sites_dir: $VOLT_SITES_DIR

# Gruppe, unter der die nginx-Worker laufen. Das Wurzelverzeichnis jeder Site
# gehoert ihr, sonst kommt der Webserver nicht hinein.
web_group: www-data
backup_dir: $VOLT_BACKUP_DIR
db_path: $VOLT_DATA_DIR/volt.db
cert_dir: $VOLT_DATA_DIR/certs
socket_path: /run/volt/agent.sock
secret_key_path: $VOLT_CONFIG_DIR/keys/secret.key

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

# Bestehende Installationen: der Schluessel lag frueher direkt in /etc/volt,
# wo der Panel-Benutzer ihn nicht anlegen kann. Ohne diesen Schritt scheitert
# dort jede Ersteinrichtung mit "permission denied".
OLD_KEY="$VOLT_CONFIG_DIR/secret.key"
NEW_KEY="$VOLT_CONFIG_DIR/keys/secret.key"

if [ -f "$OLD_KEY" ] && [ ! -f "$NEW_KEY" ]; then
    mv "$OLD_KEY" "$NEW_KEY"
    info "Schluesseldatei nach keys/ verschoben"
fi
if [ -f "$NEW_KEY" ]; then
    chown "$VOLT_USER":"$VOLT_USER" "$NEW_KEY"
    chmod 0600 "$NEW_KEY"
fi
if grep -q "^secret_key_path: *$OLD_KEY *$" "$VOLT_CONFIG_DIR/config.yaml" 2>/dev/null; then
    sed -i "s|^secret_key_path: .*|secret_key_path: $NEW_KEY|" "$VOLT_CONFIG_DIR/config.yaml"
    info "secret_key_path auf keys/ umgestellt"
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
systemctl enable volt-agent.service volt-web.service >/dev/null 2>&1

# restart statt start: bei einem zweiten Durchlauf liegen neue Binaries oder
# eine reparierte Konfiguration bereit, und der laufende Prozess kennt beide
# nicht. Ein "start" auf einen aktiven Dienst taete schlicht nichts.
systemctl restart volt-agent.service
# Der Agent legt den Socket beim Start an; das Web darf erst danach starten.
for _ in $(seq 1 20); do
    [ -S /run/volt/agent.sock ] && break
    sleep 0.25
done
systemctl restart volt-web.service
systemctl enable --now volt-renew.timer volt-backup.timer >/dev/null 2>&1

# Nachsehen, statt es zu behaupten. Ein Dienst, der beim Start abbricht,
# wird von systemd neu gestartet — "enable --now" meldet trotzdem Erfolg,
# und die Installation sähe gelungen aus, obwohl nichts läuft.
sleep 1
for unit in volt-agent volt-web; do
    if ! systemctl is-active --quiet "$unit"; then
        printf '\n'
        warn "$unit läuft nicht. Die letzten Zeilen aus dem Journal:"
        journalctl -u "$unit" -n 15 --no-pager 2>/dev/null | sed 's/^/    /' >&2
        die "$unit startet nicht — die Installation ist unvollständig."
    fi
done

# Dass beide Prozesse laufen, heisst noch nicht, dass sie miteinander reden.
# Der Agent nimmt Verbindungen ueber einen Socket entgegen, den nur seine
# Gruppe oeffnen darf - stimmt dort etwas nicht, laeuft das Panel trotzdem
# und meldet den Fehler erst bei der ersten Operation.
HEALTH_URL="https://127.0.0.1:${VOLT_PORT}/healthz"
[ -n "${ACCESS_PATH:-}" ] && HEALTH_URL="https://127.0.0.1:${VOLT_PORT}/${ACCESS_PATH}/healthz"

# -k, weil das Panel bis zum ersten `volt cert issue` ein selbstsigniertes
# Zertifikat zeigt. Geprueft wird die Erreichbarkeit, nicht die Vertrauenskette.
HEALTH="$(curl -sk --max-time 5 "$HEALTH_URL" 2>/dev/null || true)"
case "$HEALTH" in
    *'"status":"ok"'*)
        info "volt-agent und volt-web laufen und erreichen einander"
        ;;
    *'"agent"'*)
        warn "Das Panel antwortet, erreicht aber den Agent nicht:"
        printf '%s\n' "$HEALTH" | sed 's/^/    /' >&2
        warn "Systemaktionen (Sites, Zertifikate) funktionieren so nicht."
        warn "Journal ansehen mit: journalctl -u volt-agent -n 30"
        ;;
    *)
        warn "Das Panel antwortet nicht unter $HEALTH_URL."
        warn "Journal ansehen mit: journalctl -u volt-web -n 30"
        ;;
esac
info "Timer für Erneuerung und Backup aktiv"

# --- Firewall --------------------------------------------------------------

step "Firewall"

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "^Status: active"; then
    ufw allow "$VOLT_PORT/tcp" >/dev/null 2>&1 || true
    ufw allow 80/tcp >/dev/null 2>&1 || true
    ufw allow 443/tcp >/dev/null 2>&1 || true
    info "ufw: Ports $VOLT_PORT, 80 und 443 freigegeben"
elif command -v nft >/dev/null 2>&1 \
     && nft list ruleset 2>/dev/null | grep -q "hook input.*policy drop"; then
    warn "nftables verwirft eingehend — Ports $VOLT_PORT, 80 und 443 bitte selbst freigeben."
else
    # Ein vorhandenes nft ohne Regeln filtert nichts. Davor zu warnen hiesse,
    # zu einer Aenderung zu raten, die nichts bewirkt.
    info "Keine lokale Paketfilterung aktiv"
fi

# --- Ersteinrichtung -------------------------------------------------------

step "Ersteinrichtung"

SETUP_OUTPUT=""
if ! sudo -u "$VOLT_USER" "$VOLT_BIN_DIR/volt" user list >/dev/null 2>&1 \
   || [ -z "$(sudo -u "$VOLT_USER" "$VOLT_BIN_DIR/volt" user list 2>/dev/null | sed -n '2p')" ]; then
    # Die angegebene ACME-Adresse ist die bessere Wahl als admin@<hostname>:
    # an sie kommt auch wirklich Post an.
    SETUP_EMAIL="$VOLT_ACME_EMAIL"
    [ -n "$SETUP_EMAIL" ] || SETUP_EMAIL="admin@$(hostname -f 2>/dev/null || hostname)"

    # Kein "|| true": ohne Administrator ist die Installation unbenutzbar.
    # Das darf nicht als Erfolg durchgehen.
    if ! SETUP_OUTPUT="$(sudo -u "$VOLT_USER" "$VOLT_BIN_DIR/volt" setup \
            --email "$SETUP_EMAIL" 2>&1)"; then
        printf '\n'
        printf '%s\n' "$SETUP_OUTPUT" | sed 's/^/    /' >&2
        die "Die Ersteinrichtung ist fehlgeschlagen — es gibt noch keinen Zugang."
    fi
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

  Sitzt eine Firewall beim Anbieter davor (Hetzner Cloud Firewall, AWS
  Security Group), braucht sie eigene Regeln für 80, 443 und $VOLT_PORT.

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
