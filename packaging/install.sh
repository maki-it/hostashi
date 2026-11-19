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
#   --no-quota           do not enable disk quota (sites then get no disk limit)
#
# nginx comes from the distribution with its brotli modules (ADR-0020) and
# runs on the configuration the agent renders. PHP is the distribution's
# (ADR-0019); each site gets its own FPM master, so the package's own FPM
# service stays off. MariaDB is the distribution's (ADR-0024); the agent
# talks to it as root over its Unix socket. Disk quota per site needs ext4
# project quota on the root filesystem (ADR-0015); the quota step prepares it
# and asks for ONE reboot, it never reboots by itself. fail2ban from the
# distribution guards SSH and the panel logins (ADR-0021). Not yet part of
# this installer: more PHP versions (sury.org) and a firewall.
#
# Exit codes: 0 ok, 2 refused by preflight (server unchanged), 3 a step
# failed (re-running is safe), 64 usage.
#
# Test hooks (environment): HOSTASHI_OS_RELEASE (file), HOSTASHI_ARCH,
# HOSTASHI_ETC (directory instead of /etc for the quota step),
# HOSTASHI_STATE_DIR, HOSTASHI_BOOT_ID (file), HOSTASHI_DRACUT_MODULES
# (directory instead of /usr/lib/dracut/modules.d), HOSTASHI_LIB=1 (define the
# functions only, for packaging/test/quota-test.sh).

# The chk_*/do_* step functions are called by name from run_steps.
# shellcheck disable=SC2329
set -u
set -o pipefail
umask 077

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
UNIT_DIR=${HOSTASHI_UNIT_SRC:-$HERE/systemd}
STATE_DIR=${HOSTASHI_STATE_DIR:-/var/lib/hostashi-installer}
ACME_DIR=/var/lib/hostashi-acme
STATE_FILE=$STATE_DIR/state
LOG=/var/log/hostashi-install.log
OS_RELEASE=${HOSTASHI_OS_RELEASE:-/etc/os-release}
BIN=/usr/local/bin/hostashi

MODE=plan YES=no BINARY="" PUBLIC_URL="" LISTEN=127.0.0.1:8080 PANEL_PORT=8443 ALLOW_UNTESTED=no NO_QUOTA=no
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
  quota_detect
  [ "$QUOTA" != unsupported ] || log "WARNING: disk quota is not available: $QUOTA_WHY. Sites get no disk limit."
}

# ------------------------------------------------------------- steps
# chk_<step> returns 0 if the step is satisfied and prints why not otherwise.
STEPS="account dirs key binary config nginx php mariadb backup units service fail2ban quota"

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

# fail2ban from the distribution, on by default (ADR-0021): jails sshd,
# hostashi-panel (the panel's auth.failed lines) and recidive, all reading
# the journal, banning through nftables. python3-systemd is its journal
# backend, nftables its ban action; neither is pulled in without recommends.
F2B_SRC=${HOSTASHI_F2B_SRC:-$HERE/fail2ban}
F2B_PKGS=(fail2ban python3-systemd nftables)
F2B_FILES=(
  "fail2ban-hostashi.conf:/etc/fail2ban/fail2ban.d/hostashi.conf"
  "hostashi.conf:/etc/fail2ban/jail.d/hostashi.conf"
  "hostashi-panel.conf:/etc/fail2ban/filter.d/hostashi-panel.conf"
)
F2B_JAILS=(sshd hostashi-panel recidive)
F2B_CHANGED=no

chk_fail2ban() {
  local p f j jails
  for p in "${F2B_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
  for f in "${F2B_FILES[@]}"; do
    file_matches "${f#*:}" 0644 root:root <"$F2B_SRC/${f%%:*}" || { echo "${f#*:} differs"; return 1; }
  done
  [ "$F2B_CHANGED" = no ] || { echo "reload needed after a change"; return 1; }
  [ "$(systemctl is-enabled fail2ban.service 2>/dev/null)" = enabled ] || { echo "fail2ban not enabled"; return 1; }
  [ "$(systemctl is-active fail2ban.service 2>/dev/null)" = active ] || { echo "fail2ban not running"; return 1; }
  jails=$(fail2ban-client status 2>/dev/null | sed -n 's/.*Jail list:[[:space:]]*//p') || { echo "fail2ban does not answer"; return 1; }
  for j in "${F2B_JAILS[@]}"; do
    grep -qE "(^|[ ,])$j(,|$)" <<<"$jails" || { echo "jail $j not running"; return 1; }
  done
}
do_fail2ban() {
  local f rc
  if ! pkg_installed fail2ban || ! pkg_installed python3-systemd || ! pkg_installed nftables; then
    apt-get update -qq >>"$LOG" 2>&1 &&
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${F2B_PKGS[@]}" >>"$LOG" 2>&1 || return 1
  fi
  for f in "${F2B_FILES[@]}"; do
    write_file "${f#*:}" 0644 root:root <"$F2B_SRC/${f%%:*}"; rc=$?
    [ "$rc" -le 1 ] || return 1
    [ "$rc" = 0 ] && F2B_CHANGED=yes
  done
  # A broken file must not take down a running fail2ban.
  fail2ban-client -t >>"$LOG" 2>&1 || { log "fail2ban configuration test failed"; return 1; }
  systemctl enable fail2ban.service >>"$LOG" 2>&1 || return 1
  if [ "$F2B_CHANGED" = yes ] && [ "$(systemctl is-active fail2ban.service 2>/dev/null)" = active ]; then
    systemctl restart fail2ban.service >>"$LOG" 2>&1 || return 1
  else
    systemctl start fail2ban.service >>"$LOG" 2>&1 || return 1
  fi
  F2B_CHANGED=no
  # The server needs a moment before it answers on its socket.
  local _i
  for _i in $(seq 1 20); do fail2ban-client ping >/dev/null 2>&1 && break; sleep 0.5; done
}

php_unit() { sed "s/@VERSION@/$1/g" "$UNIT_DIR/hostashi-php@.service"; }

chk_units() {
  local u
  for u in hostashi-agent.service hostashi.service hostashi-panel.socket hostashi-backup.service hostashi-backup.timer hostashi-restore@.service hostashi-import@.service; do
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
  for u in hostashi-agent.service hostashi.service hostashi-panel.socket hostashi-backup.service hostashi-backup.timer hostashi-restore@.service hostashi-import@.service; do
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

# ------------------------------------------------------------- quota
# Project quota on the root filesystem (ADR-0015, spike S8). A stock ext4
# root has neither the "project" nor the "quota" feature, and both can only
# be set while it is unmounted. So this step prepares the next boot: an
# initramfs hook runs e2fsck and tune2fs before the root is mounted, fstab and
# the kernel command line mount it with prjquota, and a unit switches on
# enforcement after boot (mounting alone may only count usage, S8 finding 3).
# One reboot; the installer never reboots by itself. Where this is not
# possible (another filesystem, no quota_v2 module, no known initramfs
# generator) the step changes nothing and warns: sites then get no disk limit.
#
# Two initramfs generators are supported: initramfs-tools (Debian 13) gets a
# hook and a local-premount script, dracut (the default since Ubuntu 25.10)
# gets a module whose pre-mount hook runs the same script. The script runs
# before the root is mounted and never blocks the boot.
#
# The initramfs must contain quota_v2 once the feature is set, otherwise the
# root mount fails (S8 finding 2). Both add it for every kernel and fail the
# initramfs build of a kernel that lacks it, so a kernel update stops with an
# error instead of producing an unbootable server.
ETC=${HOSTASHI_ETC:-/etc}
FSTAB=$ETC/fstab
QUOTA_HOOK=$ETC/initramfs-tools/hooks/hostashi-quota
QUOTA_PREMOUNT=$ETC/initramfs-tools/scripts/local-premount/hostashi-quota
QUOTA_UNIT=$ETC/systemd/system/hostashi-quota-enforce.service
QUOTA_GRUB=$ETC/default/grub.d/hostashi-quota.cfg
# dracut reads modules only from its own directory; the device to change is
# in the configuration under /etc, so the module itself never changes.
QUOTA_DRACUT_DIR=${HOSTASHI_DRACUT_MODULES:-/usr/lib/dracut/modules.d}/90hostashi-quota
QUOTA_DRACUT_SETUP=$QUOTA_DRACUT_DIR/module-setup.sh
QUOTA_DRACUT_HOOK=$QUOTA_DRACUT_DIR/hostashi-quota.sh
QUOTA_DRACUT_CONF=$ETC/dracut.conf.d/90-hostashi-quota.conf
BOOT_ID_FILE=${HOSTASHI_BOOT_ID:-/proc/sys/kernel/random/boot_id}
BOOT_DIR=${HOSTASHI_BOOT:-/boot}
QUOTA_PKGS=(quota)
# QUOTA: active, pending (prepared, reboot needed), needed, failed (prepared
# but not active after a reboot), unsupported, off (--no-quota).
# Q_INITRD: the initramfs generator, initramfs-tools or dracut ("" if none).
QUOTA="" QUOTA_WHY="" Q_DEV="" Q_FEATURES=no Q_MOUNTED=no Q_FSTAB=no Q_INITRD=""

has_quota_v2() {
  modinfo -k "$1" quota_v2 >/dev/null 2>&1 || grep -qw quota_v2 "/lib/modules/$1/modules.builtin" 2>/dev/null
}

# dracut conflicts with initramfs-tools, so at most one of them is installed.
# Ubuntu's dracut package ships an update-initramfs wrapper, which is why
# initramfs-tools is recognized by lsinitramfs as well.
quota_initrd() {
  if command -v dracut >/dev/null && command -v lsinitrd >/dev/null; then
    Q_INITRD=dracut
  elif command -v update-initramfs >/dev/null && command -v lsinitramfs >/dev/null; then
    Q_INITRD=initramfs-tools
  else
    Q_INITRD=""
  fi
}

# quota_boot_files: the files that make the initramfs set the features.
quota_boot_files() {
  case "$Q_INITRD" in
    dracut) echo "$QUOTA_DRACUT_SETUP" "$QUOTA_DRACUT_HOOK" "$QUOTA_DRACUT_CONF";;
    initramfs-tools) echo "$QUOTA_HOOK" "$QUOTA_PREMOUNT";;
  esac
}

quota_boot_files_present() {
  local f n=0
  for f in $(quota_boot_files); do [ -f "$f" ] || return 1; n=$((n + 1)); done
  [ "$n" -gt 0 ]
}

quota_detect() {
  QUOTA="" QUOTA_WHY="" Q_DEV="" Q_FEATURES=no Q_MOUNTED=no Q_FSTAB=no
  quota_initrd
  if [ "$NO_QUOTA" = yes ]; then QUOTA=off QUOTA_WHY="switched off with --no-quota"; return 0; fi
  local src fstype opts head feats inode kver prepared rootopts
  src=$(findmnt -no SOURCE / 2>/dev/null) fstype=$(findmnt -no FSTYPE / 2>/dev/null) opts=$(findmnt -no OPTIONS / 2>/dev/null)
  if [ "$fstype" != ext4 ]; then
    QUOTA=unsupported QUOTA_WHY="the root filesystem is '${fstype:-unknown}', only ext4 is supported"; return 0
  fi
  Q_DEV=$(readlink -f "$src" 2>/dev/null || echo "$src")
  if [ ! -b "$Q_DEV" ]; then QUOTA=unsupported QUOTA_WHY="the root device '$src' is not a block device"; return 0; fi
  head=$(dumpe2fs -h "$Q_DEV" 2>/dev/null)
  feats=$(printf '%s\n' "$head" | sed -n 's/^Filesystem features: *//p')
  inode=$(printf '%s\n' "$head" | sed -n 's/^Inode size: *//p')
  case " $feats " in *" project "*) case " $feats " in *" quota "*) Q_FEATURES=yes;; esac;; esac
  case ",$opts," in *,prjquota,*) Q_MOUNTED=yes;; esac
  rootopts=$(awk '$2 == "/" && $1 !~ /^#/ {print $4}' "$FSTAB" 2>/dev/null)
  case ",$rootopts," in *,prjquota,*) Q_FSTAB=yes;; esac

  if [ "$Q_FEATURES" = yes ] && [ "$Q_MOUNTED" = yes ] && [ "$Q_FSTAB" = yes ] &&
    quota_boot_files_present && [ -f "$QUOTA_UNIT" ] && pkg_installed quota &&
    [ "$(systemctl is-enabled hostashi-quota-enforce.service 2>/dev/null)" = enabled ]; then
    QUOTA=active; return 0
  fi
  prepared=$(sed -n 's/^quota_prepared_boot=//p' "$STATE_FILE" 2>/dev/null | tail -n 1)
  if [ -n "$prepared" ]; then
    if [ "$prepared" = "$(cat "$BOOT_ID_FILE" 2>/dev/null)" ]; then
      QUOTA=pending QUOTA_WHY="prepared, a reboot activates it"; return 0
    fi
    if [ "$Q_FEATURES" = no ] || [ "$Q_MOUNTED" = no ]; then
      QUOTA=failed QUOTA_WHY="project quota was prepared but is not active after the reboot (features set: $Q_FEATURES, mounted with prjquota: $Q_MOUNTED). Check the boot messages of hostashi-quota (journalctl -b -k), or re-run with --no-quota"
      return 0
    fi
  fi

  kver=$(uname -r)
  if ! [ "${inode:-0}" -ge 256 ] 2>/dev/null; then
    QUOTA=unsupported QUOTA_WHY="the inode size of $Q_DEV is ${inode:-unknown}, project quota needs 256"; return 0
  fi
  if ! has_quota_v2 "$kver"; then
    QUOTA=unsupported QUOTA_WHY="kernel $kver has no quota_v2 module (Ubuntu: apt install linux-modules-extra-$kver)"; return 0
  fi
  if [ -z "$Q_INITRD" ]; then
    QUOTA=unsupported QUOTA_WHY="neither initramfs-tools nor dracut is installed"; return 0
  fi
  if [ "$(awk '$2 == "/" && $1 !~ /^#/' "$FSTAB" 2>/dev/null | wc -l)" != 1 ]; then
    QUOTA=unsupported QUOTA_WHY="expected exactly one entry for / in $FSTAB"; return 0
  fi
  QUOTA=needed
  if [ "$Q_FEATURES" = yes ] && [ "$Q_MOUNTED" = yes ]; then
    QUOTA_WHY="project quota is on, enforcement and the initramfs hook are not set up"
  else
    QUOTA_WHY="enable ext4 project quota on $Q_DEV (needs ONE reboot afterwards)"
  fi
}

quota_hook() {
  cat <<'H'
#!/bin/sh
# Managed by the Hostashi installer (ADR-0015). The root filesystem uses the
# ext4 quota feature and cannot be mounted without quota_v2.
case "$1" in prereqs) echo ""; exit 0;; esac
if ! modinfo -k "$version" quota_v2 >/dev/null 2>&1 &&
  ! grep -qw quota_v2 "/lib/modules/$version/modules.builtin" 2>/dev/null; then
  echo "hostashi-quota: kernel $version has no quota_v2 module, the root filesystem needs it (Ubuntu: linux-modules-extra-$version)" >&2
  exit 1
fi
. /usr/share/initramfs-tools/hook-functions
manual_add_modules quota_v2
copy_exec /sbin/tune2fs
copy_exec /sbin/e2fsck
copy_exec /sbin/dumpe2fs
mkdir -p "$DESTDIR/etc"
H
  printf "echo 'DEVICE=%s' >\"\$DESTDIR/etc/hostashi-quota.conf\"\n" "$1"
}

# quota_premount_body defines hostashi_quota, the part that runs before the
# root filesystem is mounted. It sets the ext4 project and quota features and
# never blocks the boot: every problem leaves the filesystem as it was. It
# returns instead of exiting, because dracut sources its hooks.
quota_premount_body() {
  cat <<'S'
hostashi_quota() {
  local DEVICE features rc
  [ -f /etc/hostashi-quota.conf ] || return 0
  # shellcheck disable=SC1091 # written into the initramfs by the hook or module
  . /etc/hostashi-quota.conf
  [ -b "$DEVICE" ] || return 0
  features=$(dumpe2fs -h "$DEVICE" 2>/dev/null | sed -n 's/^Filesystem features: *//p')
  case " $features " in *" project "*) case " $features " in *" quota "*) return 0;; esac;; esac
  # The module must load before the feature is set, or the root mount fails.
  modprobe quota_v2 2>/dev/null || { echo "hostashi-quota: quota_v2 not loadable, filesystem unchanged" >/dev/kmsg; return 0; }
  echo "hostashi-quota: enabling project quota on $DEVICE" >/dev/kmsg
  e2fsck -fp "$DEVICE" >/dev/console 2>&1
  rc=$?
  [ "$rc" -le 1 ] || { echo "hostashi-quota: e2fsck exit $rc, filesystem unchanged" >/dev/kmsg; return 0; }
  if tune2fs -O project,quota "$DEVICE" >/dev/console 2>&1; then
    echo "hostashi-quota: done" >/dev/kmsg
  else
    echo "hostashi-quota: tune2fs failed" >/dev/kmsg
  fi
  return 0
}
S
}

# quota_premount: the initramfs-tools local-premount script.
quota_premount() {
  cat <<'S'
#!/bin/sh
# Managed by the Hostashi installer (ADR-0015). Sets the ext4 project and
# quota features before the root filesystem is mounted.
case "$1" in prereqs) echo udev; exit 0;; esac
S
  quota_premount_body
  echo 'hostashi_quota'
  echo 'exit 0'
}

# quota_dracut_hook: the dracut pre-mount hook. dracut sources it, so it
# must not exit and must not leave anything behind.
quota_dracut_hook() {
  cat <<'S'
#!/bin/sh
# Managed by the Hostashi installer (ADR-0015). Sets the ext4 project and
# quota features before the root filesystem is mounted.
S
  quota_premount_body
  echo 'hostashi_quota'
  echo 'unset -f hostashi_quota'
}

# quota_dracut_setup: the dracut module. check returns 255, so dracut only
# includes it when the configuration adds it. installkernel fails the build
# of a kernel without quota_v2, which dracut treats as fatal.
quota_dracut_setup() {
  cat <<'M'
#!/bin/bash
# Managed by the Hostashi installer (ADR-0015). The root filesystem uses the
# ext4 quota feature and cannot be mounted without quota_v2.
# shellcheck disable=SC2154 # kernel, moddir and initdir come from dracut

check() {
    return 255
}

depends() {
    return 0
}

installkernel() {
    # Not host-only: quota_v2 is not loaded before the first reboot.
    hostonly='' instmods -c quota_v2 && return 0
    dfatal "hostashi-quota: kernel $kernel has no quota_v2 module, the root filesystem needs it (Ubuntu: linux-modules-extra-$kernel)"
    return 1
}

install() {
    if [[ -z ${hostashi_quota_device-} ]]; then
        dfatal "hostashi-quota: hostashi_quota_device is not set in /etc/dracut.conf.d"
        return 1
    fi
    inst_multiple tune2fs e2fsck dumpe2fs sed modprobe
    inst_hook pre-mount 10 "$moddir/hostashi-quota.sh"
    mkdir -p "$initdir/etc"
    printf 'DEVICE=%s\n' "$hostashi_quota_device" > "$initdir/etc/hostashi-quota.conf"
}
M
}

# quota_dracut_conf: adds the module to every initramfs dracut builds.
quota_dracut_conf() {
  echo '# Managed by the Hostashi installer (ADR-0015).'
  echo 'add_dracutmodules+=" hostashi-quota "'
  printf 'hostashi_quota_device="%s"\n' "$1"
}

quota_unit() {
  cat <<'U'
# Managed by the Hostashi installer (ADR-0015).
[Unit]
Description=Hostashi: enforce project quota limits on /
ConditionPathExists=/usr/sbin/quotaon
After=local-fs.target
Before=hostashi-agent.service multi-user.target

[Service]
Type=oneshot
RemainAfterExit=yes
# "-": a non-zero exit with "File exists" means enforcement is already on
# (seen on the Hetzner Debian 13 cloud kernel, spike S8).
ExecStart=-/usr/sbin/quotaon -P /

[Install]
WantedBy=multi-user.target
U
}

chk_quota() {
  quota_detect
  case "$QUOTA" in
    active | pending | off | unsupported) return 0;;
    *) echo "$QUOTA_WHY"; return 1;;
  esac
}

quota_rebuild_initrd() {
  case "$Q_INITRD" in
    dracut) dracut --force --regenerate-all;;
    initramfs-tools) update-initramfs -u -k all;;
    *) return 1;;
  esac
}

# quota_initrd_ok checks that an initramfs carries quota_v2 and the hook.
quota_initrd_ok() {
  local list hook
  case "$Q_INITRD" in
    dracut) list=$(lsinitrd "$1" 2>/dev/null) hook='hooks/pre-mount/[0-9]*-hostashi-quota\.sh$';;
    *) list=$(lsinitramfs "$1" 2>/dev/null) hook='scripts/local-premount/hostashi-quota$';;
  esac
  # A built-in quota_v2 (modules.builtin) has no file in the initramfs.
  { grep -q '/quota_v2\.ko' <<<"$list" || grep -qw quota_v2 "/lib/modules/${1##*/initrd.img-}/modules.builtin" 2>/dev/null; } &&
    grep -q "$hook" <<<"$list" && grep -q 'etc/hostashi-quota\.conf$' <<<"$list"
}

do_quota() {
  quota_detect
  [ "$QUOTA" = needed ] || { log "quota: $QUOTA_WHY"; return 1; }
  local backup created=() devref f k ok list
  quota_rollback() {
    log "quota: step failed, rolling back"
    [ -z "${backup:-}" ] || cp -p "$backup" "$FSTAB"
    systemctl disable hostashi-quota-enforce.service >>"$LOG" 2>&1
    for f in "${created[@]}"; do rm -f "$f"; done
    rmdir "$QUOTA_DRACUT_DIR" 2>/dev/null
    systemctl daemon-reload >>"$LOG" 2>&1
    [ ! -e "$QUOTA_GRUB" ] && command -v update-grub >/dev/null && update-grub >>"$LOG" 2>&1
    quota_rebuild_initrd >>"$LOG" 2>&1
    return 1
  }
  apt-get update -qq >>"$LOG" 2>&1 &&
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${QUOTA_PKGS[@]}" >>"$LOG" 2>&1 || return 1

  backup="$STATE_DIR/fstab.$(date -u +%Y%m%dT%H%M%SZ)"
  cp -p "$FSTAB" "$backup" || { log "quota: cannot back up $FSTAB"; return 1; }
  note "quota: fstab backup $backup"
  if [ "$Q_FSTAB" = no ]; then
    if ! { awk -v OFS='\t' '$2 == "/" && $1 !~ /^#/ { $4 = $4 ",prjquota" } { print }' "$FSTAB" >"$FSTAB.hostashi-new" &&
      chmod 0644 "$FSTAB.hostashi-new" && mv -f "$FSTAB.hostashi-new" "$FSTAB"; }; then
      rm -f "$FSTAB.hostashi-new"; quota_rollback; return 1
    fi
    findmnt --verify --tab-file "$FSTAB" >>"$LOG" 2>&1 || { log "quota: findmnt --verify rejects the new $FSTAB"; quota_rollback; return 1; }
  fi

  devref=$(blkid -s UUID -o value "$Q_DEV" 2>/dev/null)
  if [ -n "$devref" ]; then devref=/dev/disk/by-uuid/$devref; else devref=$Q_DEV; fi
  for f in $(quota_boot_files) "$QUOTA_UNIT"; do
    install -d -m 0755 "$(dirname "$f")" || { quota_rollback; return 1; }
    [ -e "$f" ] || created+=("$f")
  done
  if [ "$Q_INITRD" = dracut ]; then
    quota_dracut_setup | write_file "$QUOTA_DRACUT_SETUP" 0755 root:root; [ $? -le 1 ] || { quota_rollback; return 1; }
    quota_dracut_hook | write_file "$QUOTA_DRACUT_HOOK" 0755 root:root; [ $? -le 1 ] || { quota_rollback; return 1; }
    quota_dracut_conf "$devref" | write_file "$QUOTA_DRACUT_CONF" 0644 root:root; [ $? -le 1 ] || { quota_rollback; return 1; }
  else
    quota_hook "$devref" | write_file "$QUOTA_HOOK" 0755 root:root; [ $? -le 1 ] || { quota_rollback; return 1; }
    quota_premount | write_file "$QUOTA_PREMOUNT" 0755 root:root; [ $? -le 1 ] || { quota_rollback; return 1; }
  fi
  quota_unit | write_file "$QUOTA_UNIT" 0644 root:root; [ $? -le 1 ] || { quota_rollback; return 1; }
  if ! { systemctl daemon-reload >>"$LOG" 2>&1 && systemctl enable hostashi-quota-enforce.service >>"$LOG" 2>&1; }; then
    quota_rollback; return 1
  fi

  if command -v update-grub >/dev/null; then
    install -d -m 0755 "$(dirname "$QUOTA_GRUB")" || { quota_rollback; return 1; }
    [ -e "$QUOTA_GRUB" ] || created+=("$QUOTA_GRUB")
    # shellcheck disable=SC2016 # expanded by GRUB, not here
    echo 'GRUB_CMDLINE_LINUX="$GRUB_CMDLINE_LINUX rootflags=prjquota"' | write_file "$QUOTA_GRUB" 0644 root:root
    [ $? -le 1 ] || { quota_rollback; return 1; }
    update-grub >>"$LOG" 2>&1 || { log "quota: update-grub failed"; quota_rollback; return 1; }
  fi

  quota_rebuild_initrd >>"$LOG" 2>&1 || { log "quota: rebuilding the initramfs ($Q_INITRD) failed"; quota_rollback; return 1; }
  # Every initramfs GRUB may boot must carry the module and the hook before
  # anyone reboots.
  ok=0
  for k in "$BOOT_DIR"/initrd.img-*; do
    [ -e "$k" ] || continue
    if ! quota_initrd_ok "$k"; then
      log "quota: $k lacks quota_v2 or the hook"; quota_rollback; return 1
    fi
    ok=$((ok + 1))
  done
  [ "$ok" -gt 0 ] || { log "quota: no initramfs found in $BOOT_DIR"; quota_rollback; return 1; }

  if [ "$Q_FEATURES" = yes ] && [ "$Q_MOUNTED" = yes ]; then
    systemctl start hostashi-quota-enforce.service >>"$LOG" 2>&1 || { quota_rollback; return 1; }
  else
    printf 'quota_prepared_boot=%s\n' "$(cat "$BOOT_ID_FILE")" >>"$STATE_FILE" || { quota_rollback; return 1; }
  fi
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
      --no-quota) NO_QUOTA=yes;;
      *) echo "usage: $0 [plan | apply --yes] --binary PATH [--public-url URL] [--panel-port PORT] [--listen ADDR] [--allow-untested] [--no-quota]" >&2; exit 64;;
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
    [ "$QUOTA" != pending ] || echo "Disk quota is prepared and waits for a reboot."
    if [ $CHANGES -eq 0 ]; then echo "Nothing to do."; else echo "$CHANGES step(s) would change something. Apply with: $0 apply --yes --binary PATH"; fi
    exit 0
  fi

  if [ $CHANGES -eq 0 ]; then echo "Nothing to do. (changes=0)"; else echo "Done. changes=$CHANGES"; fi
  quota_detect
  case "$QUOTA" in
    pending) echo "REBOOT REQUIRED: disk quota is prepared and becomes active after one reboot. Re-run the installer afterwards to check it.";;
    unsupported) echo "WARNING: disk quota is not available ($QUOTA_WHY). Sites get no disk limit.";;
  esac
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

[ "${HOSTASHI_LIB:-}" = 1 ] || main "$@"
