#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Hostashi installer: sets up the panel itself (account, directories, key,
# binary, systemd units) on a FRESH Debian 13 or Ubuntu 26.04 server.
# It follows the step model of spike S5: every step has a read-only check
# and an apply, apply runs only if the check fails, and the check must pass
# afterwards. Running it twice changes nothing.
#
#   install.sh [plan] [options]          read-only: preflight and the steps that would change
#   install.sh apply --yes [options]     apply
#
# Options:
#   --binary PATH        the hostashi binary to install (required for apply)
#   --public-url URL     address users open the panel at, e.g. https://panel.example.com:8443
#                        (enables passkeys). With a host name, nginx serves the panel there over
#                        HTTPS with a Let's Encrypt certificate (ADR-0023); the port is the panel port.
#   --panel-port PORT    HTTPS port of the panel, default 8443 (TCP and UDP must be open)
#   --listen ADDR        panel listen address, default 127.0.0.1:8080 (loopback only: SSH tunnel)
#   --allow-untested     accept an OS release that is not on the supported list
#
# nginx comes from the distribution with its brotli modules (ADR-0020) and
# runs on the configuration the agent renders. PHP is the distribution's
# (ADR-0019); each site gets its own FPM master, so the package's own FPM
# service stays off. MariaDB is the distribution's (ADR-0024); the agent
# talks to it as root over its Unix socket. Not yet part of this installer:
# more PHP versions (sury.org), project quota (spike S5/S8) and a firewall.
#
# Exit codes: 0 ok, 2 refused by preflight (server unchanged), 3 a step
# failed (re-running is safe), 64 usage.
#
# Test hooks (environment): HOSTASHI_OS_RELEASE (file), HOSTASHI_ARCH.

# The chk_*/do_* step functions are called by name from run_steps.
# shellcheck disable=SC2329
set -u
set -o pipefail
umask 077

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
UNIT_DIR=${HOSTASHI_UNIT_SRC:-$HERE/systemd}
STATE_DIR=/var/lib/hostashi-installer
ACME_DIR=/var/lib/hostashi-acme
STATE_FILE=$STATE_DIR/state
LOG=/var/log/hostashi-install.log
OS_RELEASE=${HOSTASHI_OS_RELEASE:-/etc/os-release}
BIN=/usr/local/bin/hostashi

MODE=plan YES=no BINARY="" PUBLIC_URL="" LISTEN=127.0.0.1:8080 PANEL_PORT=8443 ALLOW_UNTESTED=no
CHANGES=0 BINARY_CHANGED=no CONFIG_CHANGED=no

log()    { echo "install: $*" >&2; }
note()   { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >>"$LOG" 2>/dev/null || true; }
refuse() { log "REFUSED: $*"; log "server left unchanged"; exit 2; }

detect() {
  # shellcheck source=/dev/null
  OS_ID=$(. "$OS_RELEASE" && echo "${ID:-}")
  # shellcheck source=/dev/null
  OS_VER=$(. "$OS_RELEASE" && echo "${VERSION_ID:-}")
  RAW_ARCH=${HOSTASHI_ARCH:-$(uname -m)}
  case "$RAW_ARCH" in x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) ARCH=unsupported;; esac
}

pkg_installed() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q '^install ok installed$'; }

# write_file PATH MODE OWNER:GROUP < content. Returns 0 if the file changed.
write_file() {
  local path=$1 mode=$2 owner=$3 tmp
  tmp=$(mktemp "$path.XXXXXX") || return 2
  cat >"$tmp"
  if ! chmod "$mode" "$tmp" || ! chown "$owner" "$tmp"; then rm -f "$tmp"; return 2; fi
  if [ -f "$path" ] && [ ! -L "$path" ] && cmp -s "$tmp" "$path" &&
     [ "$(stat -c '%a %U:%G' "$path")" = "${mode#0} $owner" ]; then
    rm -f "$tmp"; return 1
  fi
  mv -f "$tmp" "$path"
}

# file_matches PATH MODE OWNER:GROUP < expected content (read-only twin of write_file).
file_matches() {
  local path=$1 mode=$2 owner=$3 tmp rc
  tmp=$(mktemp) || return 1
  cat >"$tmp"
  [ -f "$path" ] && [ ! -L "$path" ] && cmp -s "$tmp" "$path" && [ "$(stat -c '%a %U:%G' "$path")" = "${mode#0} $owner" ]
  rc=$?
  rm -f "$tmp"
  return "$rc"
}

dir_ok() { [ -d "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%a %U:%G' "$1")" = "$2 $3" ]; }

# ------------------------------------------------------------- preflight
preflight() {
  local problems=0
  fail() { log "preflight: FAIL: $*"; problems=$((problems + 1)); }
  detect

  [ "$(id -u)" = 0 ] || fail "must run as root"

  case "$OS_ID-$OS_VER" in
    debian-13 | ubuntu-26.04) ;;   # ADR-0020
    *) [ "$ALLOW_UNTESTED" = yes ] || fail "unsupported OS '$OS_ID $OS_VER' (supported: Debian 13, Ubuntu 26.04; --allow-untested to try anyway)";;
  esac
  [ "$ARCH" != unsupported ] || fail "unsupported architecture '$RAW_ARCH' (amd64 and arm64 only)"
  [ -d /run/systemd/system ] || fail "systemd is not running as PID 1"
  [ -d "$UNIT_DIR" ] && [ -f "$UNIT_DIR/hostashi.service" ] && [ -f "$UNIT_DIR/hostashi-agent.service" ] &&
    [ -f "$UNIT_DIR/hostashi-php@.service" ] && [ -f "$UNIT_DIR/hostashi-panel.socket" ] ||
    fail "systemd units not found in $UNIT_DIR"

  case "$LISTEN" in
    127.0.0.1:[0-9]* | localhost:[0-9]* | \[::1\]:[0-9]*) ;;
    *) fail "--listen '$LISTEN' must be a loopback address (nginx serves the panel over HTTPS)";;
  esac
  case "$PANEL_PORT" in
    80 | 443) fail "--panel-port $PANEL_PORT is taken by the sites";;
    [1-9] | [1-9][0-9] | [1-9][0-9][0-9] | [1-9][0-9][0-9][0-9] | [1-9][0-9][0-9][0-9][0-9]) ;;
    *) fail "--panel-port '$PANEL_PORT' is not a port";;
  esac
  [ "$PANEL_PORT" -le 65535 ] || fail "--panel-port '$PANEL_PORT' is not a port"
  if [ -n "$PUBLIC_URL" ]; then
    printf '%s' "$PUBLIC_URL" | grep -Eq '^(https://[A-Za-z0-9.-]+(:[0-9]+)?|http://localhost(:[0-9]+)?)/?$' ||
      fail "--public-url '$PUBLIC_URL' must look like https://panel.example.com:$PANEL_PORT"
    PUBLIC_URL=${PUBLIC_URL%/}
    # A host name (not localhost, not an IP) is served by nginx on the panel port.
    local host=${PUBLIC_URL#https://} port=""
    host=${host%%:*}
    case "$PUBLIC_URL" in https://*:*) port=${PUBLIC_URL##*:};; esac
    if [ "${PUBLIC_URL#https://}" != "$PUBLIC_URL" ] && [ "$host" != localhost ] &&
      ! printf '%s' "$host" | grep -Eq '^[0-9.]+$'; then
      if [ -z "$port" ]; then
        PUBLIC_URL="$PUBLIC_URL:$PANEL_PORT"
        log "the panel is served on port $PANEL_PORT: public URL is $PUBLIC_URL"
      elif [ "$port" != "$PANEL_PORT" ]; then
        fail "--public-url port $port differs from --panel-port $PANEL_PORT"
      fi
    fi
  fi

  if [ "$MODE" = apply ]; then
    [ -n "$BINARY" ] || fail "--binary PATH is required for apply"
  fi
  if [ -n "$BINARY" ]; then
    if [ ! -f "$BINARY" ] || [ ! -x "$BINARY" ]; then
      fail "--binary '$BINARY' is not an executable file"
    elif ! "$BINARY" version >/dev/null 2>&1; then
      fail "--binary '$BINARY' does not run on this system (wrong architecture?)"
    fi
  fi

  # Fresh servers only, unless this installer was here before (re-run after a failure).
  if [ ! -f "$STATE_FILE" ]; then
    local p found=""
    for p in apache2 caddy lighttpd mysql-server; do pkg_installed "$p" && found="$found $p"; done
    [ -z "$found" ] || fail "not a fresh server, found installed:$found"
    if getent passwd hostashi >/dev/null && [ ! -x "$BIN" ]; then
      fail "a user 'hostashi' exists that this installer did not create"
    fi
  fi

  [ $problems -eq 0 ] || refuse "$problems preflight problem(s)"
  log "preflight: ok ($OS_ID $OS_VER $ARCH)"
}

# ------------------------------------------------------------- steps
# chk_<step> returns 0 if the step is satisfied and prints why not otherwise.
STEPS="account dirs key binary config nginx php mariadb backup units service"

chk_account() {
  getent passwd hostashi >/dev/null || { echo "user hostashi missing"; return 1; }
  [ "$(getent passwd hostashi | cut -d: -f7)" = /usr/sbin/nologin ] || { echo "hostashi has a login shell"; return 1; }
  [ "$(id -u hostashi)" != 0 ] || { echo "hostashi has uid 0"; return 1; }
}
do_account() {
  adduser --system --group --no-create-home --home /var/lib/hostashi --shell /usr/sbin/nologin hostashi >>"$LOG" 2>&1
}

chk_dirs() {
  dir_ok /etc/hostashi 750 root:hostashi || { echo "/etc/hostashi"; return 1; }
  dir_ok /var/lib/hostashi 750 hostashi:hostashi || { echo "/var/lib/hostashi"; return 1; }
  dir_ok /var/lib/hostashi-agent 700 root:root || { echo "/var/lib/hostashi-agent"; return 1; }
  dir_ok "$STATE_DIR" 700 root:root || { echo "$STATE_DIR"; return 1; }
  dir_ok "$ACME_DIR" 755 root:root || { echo "$ACME_DIR"; return 1; }
  dir_ok "$ACME_DIR/.well-known" 755 root:root || { echo "$ACME_DIR/.well-known"; return 1; }
  dir_ok "$ACME_DIR/.well-known/acme-challenge" 2750 hostashi:www-data || { echo "$ACME_DIR/.well-known/acme-challenge"; return 1; }
}
# HTTP-01 challenge files: the panel writes them, nginx (www-data) serves
# them; they are public anyway. The setgid bit gives every file the group
# www-data.
do_dirs() {
  install -d -m 0750 -o root -g hostashi /etc/hostashi &&
  install -d -m 0750 -o hostashi -g hostashi /var/lib/hostashi &&
  install -d -m 0700 -o root -g root /var/lib/hostashi-agent "$STATE_DIR" &&
  install -d -m 0755 -o root -g root "$ACME_DIR" "$ACME_DIR/.well-known" &&
  install -d -m 2750 -o hostashi -g www-data "$ACME_DIR/.well-known/acme-challenge"
}

# The encryption key for stored secrets (TOTP seeds): 32 random bytes, read
# only by the panel. It is never replaced, because losing it makes every
# stored secret unreadable. Keep it out of the database backup.
chk_key() {
  [ -f /etc/hostashi/secret.key ] && [ ! -L /etc/hostashi/secret.key ] || { echo "key missing"; return 1; }
  [ "$(stat -c '%a %U:%G %s' /etc/hostashi/secret.key)" = "400 hostashi:hostashi 32" ] || { echo "key has wrong owner, mode or size"; return 1; }
}
do_key() {
  if [ -e /etc/hostashi/secret.key ]; then
    # Never overwrite an existing key; only fix owner and mode of a proper one.
    [ "$(stat -c %s /etc/hostashi/secret.key)" = 32 ] || { log "existing /etc/hostashi/secret.key has the wrong size, not touching it"; return 1; }
    chown hostashi:hostashi /etc/hostashi/secret.key && chmod 0400 /etc/hostashi/secret.key
    return
  fi
  local tmp
  tmp=$(mktemp /etc/hostashi/.secret.key.XXXXXX) || return 1
  head -c 32 /dev/urandom >"$tmp" && chown hostashi:hostashi "$tmp" && chmod 0400 "$tmp" && mv -n "$tmp" /etc/hostashi/secret.key
}

chk_binary() {
  [ -x "$BIN" ] || { echo "binary missing"; return 1; }
  [ -z "$BINARY" ] || cmp -s "$BINARY" "$BIN" || { echo "binary differs"; return 1; }
  [ "$(stat -c '%a %U:%G' "$BIN")" = "755 root:root" ] || { echo "binary has wrong owner or mode"; return 1; }
}
do_binary() {
  install -m 0755 -o root -g root "$BINARY" "$BIN.new" && mv -f "$BIN.new" "$BIN" && BINARY_CHANGED=yes
}

env_file() {
  echo "# Managed by the Hostashi installer. Re-run it with other options to change these."
  echo "HOSTASHI_LISTEN=$LISTEN"
  echo "HOSTASHI_PUBLIC_URL=$PUBLIC_URL"
  echo "HOSTASHI_PANEL_PORT=$PANEL_PORT"
}
chk_config() { env_file | file_matches /etc/hostashi/hostashi.env 0640 root:hostashi || { echo "settings differ"; return 1; }; }
do_config() { env_file | write_file /etc/hostashi/hostashi.env 0640 root:hostashi; [ $? -le 1 ] && CONFIG_CHANGED=yes; }

# nginx and its brotli modules from the distribution (ADR-0020). The agent
# renders the whole config; the package's default site is never used.
NGINX_PKGS=(nginx libnginx-mod-http-brotli-filter libnginx-mod-http-brotli-static)
NGINX_DROPIN=/etc/systemd/system/nginx.service.d/hostashi.conf

chk_nginx() {
  local p
  for p in "${NGINX_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
}
do_nginx() {
  apt-get update -qq >>"$LOG" 2>&1 &&
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${NGINX_PKGS[@]}" >>"$LOG" 2>&1
}

# PHP from the distribution (Debian 13: 8.4, Ubuntu 26.04: 8.5) with the
# extensions of spike S5. The agent runs one FPM master per site.
PHP_PKGS=(php-fpm php-cli php-mysql php-curl php-gd php-mbstring php-xml php-zip php-intl php-bcmath)
# Every version the agent may run a site on (agentapi.PHPVersions); a unit
# template is installed for each, whether the version is installed or not.
PHP_VERSIONS=(7.4 8.1 8.2 8.3 8.4 8.5)

# distro_fpm_units prints the package FPM units (php8.4-fpm.service, ...).
distro_fpm_units() {
  local v
  for v in "${PHP_VERSIONS[@]}"; do
    [ -e "/usr/lib/systemd/system/php$v-fpm.service" ] && echo "php$v-fpm.service"
  done
  return 0
}

chk_php() {
  local p u
  for p in "${PHP_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
  for u in $(distro_fpm_units); do
    [ "$(systemctl is-enabled "$u" 2>/dev/null)" != enabled ] || { echo "$u is enabled"; return 1; }
    [ "$(systemctl is-active "$u" 2>/dev/null)" != active ] || { echo "$u is running"; return 1; }
  done
}
do_php() {
  apt-get update -qq >>"$LOG" 2>&1 &&
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${PHP_PKGS[@]}" >>"$LOG" 2>&1 || return 1
  local u
  for u in $(distro_fpm_units); do
    systemctl disable --now "$u" >>"$LOG" 2>&1 || return 1
  done
}

# MariaDB from the distribution (ADR-0024). The package defaults stay: it
# listens on 127.0.0.1 only and root logs in only through the Unix socket
# (unix_socket), which is how the agent connects. No password is set.
MARIADB_PKGS=(mariadb-server)

chk_mariadb() {
  local p
  for p in "${MARIADB_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
  [ "$(systemctl is-enabled mariadb.service 2>/dev/null)" = enabled ] || { echo "mariadb not enabled"; return 1; }
  [ "$(systemctl is-active mariadb.service 2>/dev/null)" = active ] || { echo "mariadb not running"; return 1; }
}
do_mariadb() {
  apt-get update -qq >>"$LOG" 2>&1 &&
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${MARIADB_PKGS[@]}" >>"$LOG" 2>&1 &&
  systemctl enable --now mariadb.service >>"$LOG" 2>&1
}

# restic from the distribution for the backups (ADR-0008), and the ssh
# client its SFTP backend starts. The backup timer is installed with the
# units but stays off until a backup target is set up.
BACKUP_PKGS=(restic openssh-client)

chk_backup() {
  local p
  for p in "${BACKUP_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
}
do_backup() {
  apt-get update -qq >>"$LOG" 2>&1 &&
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${BACKUP_PKGS[@]}" >>"$LOG" 2>&1
}

php_unit() { sed "s/@VERSION@/$1/g" "$UNIT_DIR/hostashi-php@.service"; }

chk_units() {
  local u
  for u in hostashi-agent.service hostashi.service hostashi-panel.socket hostashi-backup.service hostashi-backup.timer hostashi-restore@.service; do
    file_matches "/etc/systemd/system/$u" 0644 root:root <"$UNIT_DIR/$u" || { echo "$u differs"; return 1; }
  done
  dir_ok "$(dirname "$NGINX_DROPIN")" 755 root:root || { echo "nginx drop-in directory"; return 1; }
  file_matches "$NGINX_DROPIN" 0644 root:root <"$UNIT_DIR/nginx-hostashi.conf" || { echo "nginx drop-in differs"; return 1; }
  local v
  for v in "${PHP_VERSIONS[@]}"; do
    php_unit "$v" | file_matches "/etc/systemd/system/hostashi-php-$v@.service" 0644 root:root || { echo "PHP $v unit differs"; return 1; }
  done
}
do_units() {
  local u
  for u in hostashi-agent.service hostashi.service hostashi-panel.socket hostashi-backup.service hostashi-backup.timer hostashi-restore@.service; do
    write_file "/etc/systemd/system/$u" 0644 root:root <"$UNIT_DIR/$u"
    [ $? -le 1 ] || return 1
  done
  install -d -m 0755 -o root -g root "$(dirname "$NGINX_DROPIN")" || return 1
  write_file "$NGINX_DROPIN" 0644 root:root <"$UNIT_DIR/nginx-hostashi.conf"
  [ $? -le 1 ] || return 1
  local v
  for v in "${PHP_VERSIONS[@]}"; do
    php_unit "$v" | write_file "/etc/systemd/system/hostashi-php-$v@.service" 0644 root:root
    [ $? -le 1 ] || return 1
  done
  CONFIG_CHANGED=yes
  systemctl daemon-reload >>"$LOG" 2>&1
}

chk_service() {
  local u
  for u in hostashi-agent.service hostashi.service nginx.service hostashi-panel.socket; do
    [ "$(systemctl is-enabled "$u" 2>/dev/null)" = enabled ] || { echo "$u not enabled"; return 1; }
    [ "$(systemctl is-active "$u" 2>/dev/null)" = active ] || { echo "$u not running"; return 1; }
  done
  # The agent puts nginx on its own config at start (first generation).
  [ -L /var/lib/hostashi-agent/web/current ] || { echo "nginx not on a Hostashi generation yet"; return 1; }
  [ "$BINARY_CHANGED" = no ] && [ "$CONFIG_CHANGED" = no ] || { echo "restart needed after a change"; return 1; }
}
do_service() {
  systemctl enable nginx.service hostashi-agent.service hostashi-panel.socket hostashi.service >>"$LOG" 2>&1 || return 1
  if [ "$BINARY_CHANGED" = yes ] || [ "$CONFIG_CHANGED" = yes ]; then
    systemctl stop hostashi.service >>"$LOG" 2>&1
    systemctl restart hostashi-panel.socket hostashi-agent.service >>"$LOG" 2>&1 || return 1
    systemctl start hostashi.service >>"$LOG" 2>&1 || return 1
  else
    systemctl start hostashi-panel.socket hostashi-agent.service hostashi.service >>"$LOG" 2>&1 || return 1
  fi
  BINARY_CHANGED=no CONFIG_CHANGED=no
  # Give the units a moment (the agent's first generation restarts nginx),
  # then the check verifies they stayed up.
  local _i
  for _i in 1 2 3 4 5 6 7 8 9 10; do
    [ -L /var/lib/hostashi-agent/web/current ] && break
    sleep 1
  done
  sleep 2
}

run_steps() {
  local s why
  for s in $STEPS; do
    if why=$(chk_"$s" 2>&1); then
      printf '  [ok]      %s\n' "$s"
      continue
    fi
    if [ "$MODE" = plan ]; then
      printf '  [change]  %s: %s\n' "$s" "$why"
      CHANGES=$((CHANGES + 1))
      continue
    fi
    printf '  [apply]   %s: %s\n' "$s" "$why"
    note "step $s: $why"
    if ! do_"$s" || ! why=$(chk_"$s" 2>&1); then
      log "step '$s' FAILED${why:+ ($why)}, see $LOG. Re-running the installer is safe."
      return 3
    fi
    CHANGES=$((CHANGES + 1))
  done
}

main() {
  while [ $# -gt 0 ]; do
    case "$1" in
      plan) MODE=plan;;
      apply) MODE=apply;;
      --yes) YES=yes;;
      --allow-untested) ALLOW_UNTESTED=yes;;
      --binary) shift; BINARY=${1:-};;
      --public-url) shift; PUBLIC_URL=${1:-};;
      --listen) shift; LISTEN=${1:-};;
      --panel-port) shift; PANEL_PORT=${1:-};;
      *) echo "usage: $0 [plan | apply --yes] --binary PATH [--public-url URL] [--panel-port PORT] [--listen ADDR] [--allow-untested]" >&2; exit 64;;
    esac
    shift
  done
  if [ "$MODE" = apply ] && [ "$YES" != yes ]; then
    log "refusing to change anything without --yes (run 'plan' first)"
    exit 2
  fi

  preflight

  if [ "$MODE" = apply ]; then
    touch "$LOG" && chmod 600 "$LOG"
    if [ ! -f "$STATE_FILE" ]; then
      install -d -m 0700 -o root -g root "$STATE_DIR"
      printf 'started=%s\nos=%s-%s\narch=%s\n' "$(date -u +%FT%TZ)" "$OS_ID" "$OS_VER" "$ARCH" >"$STATE_FILE"
    fi
  fi

  run_steps || exit $?

  if [ "$MODE" = plan ]; then
    if [ $CHANGES -eq 0 ]; then echo "Nothing to do."; else echo "$CHANGES step(s) would change something. Apply with: $0 apply --yes --binary PATH"; fi
    exit 0
  fi

  if [ $CHANGES -eq 0 ]; then echo "Nothing to do. (changes=0)"; else echo "Done. changes=$CHANGES"; fi
  grep -q '^completed=' "$STATE_FILE" 2>/dev/null || printf 'completed=%s\n' "$(date -u +%FT%TZ)" >>"$STATE_FILE"

  # The panel writes the setup token on its first start; wait briefly for it.
  local tok=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    tok=$("$BIN" setup-token -db /var/lib/hostashi/hostashi.db 2>/dev/null) && [ -n "$tok" ] && break
    tok=""
    sleep 1
  done
  if [ -n "$tok" ]; then
    echo "First login: open the panel and use this one-time setup token (valid 24 hours):"
    echo "  $tok"
  fi
  exit 0
}

main "$@"
