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
#   install.sh upgrade [--binary PATH]   apply again with the options of the last apply, installing
#                                        no packages (--no-apt); for the hostashi package (ADR-0050)
#
# Options:
#   --binary PATH        the hostashi binary to install (required for apply)
#   --panel-domain NAME  host name users open the panel at, e.g. panel.example.com (enables
#                        passkeys). nginx serves the panel there over HTTPS on the panel port with
#                        a Let's Encrypt certificate (ADR-0023, ADR-0042). Without it, the panel is
#                        reachable only through an SSH tunnel to --listen.
#   --panel-port PORT    HTTPS port of the panel, default 8443 (TCP and UDP must be open). 443
#                        shares the sites' port by host name and needs --panel-domain (ADR-0042)
#   --listen ADDR        panel listen address, default 127.0.0.1:8080 (loopback only: SSH tunnel)
#   --allow-untested     accept an OS release that is not on the supported list
#   --no-quota           do not enable disk quota (sites then get no disk limit)
#   --firewall-allow P   also open port P to the internet, as PORT/tcp or PORT/udp (repeatable)
#   --no-firewall        no Hostashi firewall (only behind a provider firewall); removes it if present
#   --no-db-tool         no web database tool (Adminer on port 8444, ADR-0038); removes it if present
#   --php-versions LIST  more PHP versions from sury.org next to the distribution's, comma-separated
#                        from 7.4, 8.1, 8.2, 8.3, 8.4, 8.5, 8.6 (7.4, 8.1 and 8.2 are legacy). Kept for later runs
#   --no-apt             install no packages; a step that needs a missing one fails and names it.
#                        For runs from the hostashi package, whose dependencies are installed (ADR-0050)
#
# nginx comes from the distribution with its brotli modules (ADR-0020) and
# runs on the configuration the agent renders. PHP is the distribution's
# (ADR-0019); each site gets its own FPM master, so the package's own FPM
# service stays off. MariaDB is the distribution's (ADR-0024); the agent
# talks to it as root over its Unix socket. Disk quota per site needs ext4
# project quota on the root filesystem (ADR-0015); the quota step prepares it
# and asks for ONE reboot, it never reboots by itself. fail2ban from the
# distribution guards SSH and the panel logins (ADR-0021). Site users log in
# over SSH and SFTP with keys only (ADR-0032). An nftables firewall lets in
# only SSH, the web and the panel, and security updates are installed
# every day without a reboot (ADR-0033). More PHP versions come from
# sury.org only with --php-versions, with the distribution's PHP pinned as
# the default (ADR-0019).
#
# The web database tool is Adminer, pinned by SHA-256 (packaging/adminer),
# in a PHP-FPM pool of its own under the system user hostashi-adminer;
# nginx serves it on port 8444 for the panel's host name (ADR-0038).
#
# Every successful apply saves its options (not --binary, --yes, --no-apt)
# to /etc/hostashi/install.options, one per line, which upgrade reads back.
# The file must be owned by root and writable by root only.
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
OPTIONS_FILE=${HOSTASHI_OPTIONS_FILE:-/etc/hostashi/install.options}
# The binary of the hostashi package, the default for upgrade.
PKG_BIN=${HOSTASHI_PKG_BIN:-/usr/bin/hostashi}

PHP_REQUEST=""
MODE=plan YES=no BINARY="" PANEL_DOMAIN="" PUBLIC_URL="" LISTEN=127.0.0.1:8080 PANEL_PORT=8443 ALLOW_UNTESTED=no NO_QUOTA=no NO_FIREWALL=no
FW_ALLOW=()
NO_DB_TOOL=no DB_TOOL_PORT=8444 NO_APT=no
PHP_REQUEST_SET=no
CHANGES=0 BINARY_CHANGED=no CONFIG_CHANGED=no

log()    { echo "install: $*" >&2; }
# apt_update is apt-get update with php-install's wait for sury.org's CDN
# (ADR-0019): once --php-versions added the source, every later step's
# update reads its indexes too.
apt_update() { HOSTASHI_LIB="" "$PHP_INSTALL_SRC" update; }
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

# apt_install PKG... installs the packages that are missing, without
# recommends, and does nothing if all are there. With --no-apt (a package
# upgrade runs the installer from dpkg, which holds the apt lock, ADR-0050)
# it installs nothing and fails with the names of the missing packages.
apt_install() {
  local p missing=()
  for p in "$@"; do pkg_installed "$p" || missing+=("$p"); done
  [ ${#missing[@]} -gt 0 ] || return 0
  if [ "$NO_APT" = yes ]; then
    log "missing package(s) with --no-apt: ${missing[*]} (install them with apt-get install ${missing[*]})"
    note "missing package(s) with --no-apt: ${missing[*]}"
    return 1
  fi
  apt_update >>"$LOG" 2>&1 &&
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${missing[@]}" >>"$LOG" 2>&1
}

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
# valid_domain NAME: a host name as agentapi.ValidateDomain accepts it, in
# lower case: at least two labels of letters, digits and inner hyphens,
# at most 253 characters, and a top-level label that is not all digits.
valid_domain() {
  local d=$1 l
  [ ${#d} -ge 3 ] && [ ${#d} -le 253 ] || return 1
  [[ "$d" == *.* ]] || return 1
  [[ "$d" =~ ^[a-z0-9.-]+$ ]] || return 1
  local IFS=.
  # shellcheck disable=SC2086 # split on dots on purpose
  set -- $d
  [ "${d: -1}" != . ] || return 1
  for l in "$@"; do
    [[ "$l" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || return 1
  done
  ! [[ "${!#}" =~ ^[0-9]+$ ]]
}

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
    80) fail "--panel-port 80 is taken by the sites";;
    [1-9] | [1-9][0-9] | [1-9][0-9][0-9] | [1-9][0-9][0-9][0-9] | [1-9][0-9][0-9][0-9][0-9]) ;;
    *) fail "--panel-port '$PANEL_PORT' is not a port";;
  esac
  [ "$PANEL_PORT" -le 65535 ] || fail "--panel-port '$PANEL_PORT' is not a port"
  [ "$NO_DB_TOOL" = yes ] || [ "$PANEL_PORT" != "$DB_TOOL_PORT" ] ||
    fail "--panel-port $PANEL_PORT is the port of the database tool; choose another or add --no-db-tool"
  if [ -n "$PANEL_DOMAIN" ]; then
    PANEL_DOMAIN=$(printf '%s' "$PANEL_DOMAIN" | tr '[:upper:]' '[:lower:]')
    valid_domain "$PANEL_DOMAIN" ||
      fail "--panel-domain '$PANEL_DOMAIN' must be a host name like panel.example.com (no scheme, port or path)"
    PUBLIC_URL=https://$PANEL_DOMAIN
    [ "$PANEL_PORT" = 443 ] || PUBLIC_URL=$PUBLIC_URL:$PANEL_PORT
    log "the panel is served at $PUBLIC_URL"
  elif [ "$PANEL_PORT" = 443 ]; then
    # nginx tells the panel from the sites on 443 by its name only.
    fail "--panel-port 443 needs --panel-domain"
  fi

  local a
  for a in "${FW_ALLOW[@]}"; do
    fw_valid "$a" || fail "--firewall-allow '$a' must be PORT/tcp or PORT/udp with a port from 1 to 65535"
  done
  [ "$NO_FIREWALL" = no ] || [ ${#FW_ALLOW[@]} -eq 0 ] || fail "--firewall-allow and --no-firewall exclude each other"

  if [ "$PHP_REQUEST_SET" = yes ]; then
    local v
    [ -n "$PHP_REQUEST" ] || fail "--php-versions needs a list such as 8.1,7.4"
    for v in $(tr ',' ' ' <<<"$PHP_REQUEST"); do
      php_allowed "$v" || fail "--php-versions: '$v' is not one of ${PHP_VERSIONS[*]}"
    done
    printf '%s' "$PHP_REQUEST" | grep -Eq '^[0-9.]+(,[0-9.]+)*$' || fail "--php-versions '$PHP_REQUEST' must be a comma-separated list such as 8.1,7.4"
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
STEPS="account dirs key binary config ssh harden sysctl nginx php phpversions pgclient valkey mariadb dbtool backup units service fail2ban firewall updates quota"

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
  echo "HOSTASHI_DB_TOOL_PORT=$(db_tool_port)"
}
chk_config() { env_file | file_matches /etc/hostashi/hostashi.env 0640 root:hostashi || { echo "settings differ"; return 1; }; }
do_config() { env_file | write_file /etc/hostashi/hostashi.env 0640 root:hostashi; [ $? -le 1 ] && CONFIG_CHANGED=yes; }

# SSH and SFTP per site (ADR-0032). Every site user is in the group
# hostashi-sites (internal/sysuser). One static Match block for that group
# allows only keys from the root-owned file the agent writes per site, no
# passwords and no forwarding. The block ends with its file, so root and the
# admin's own sshd settings stay as they are. sshd -t checks the result
# before sshd is reloaded; a file that fails the test is removed again, so
# a later reload cannot lock anyone out.
SSH_GROUP=hostashi-sites
SSH_KEYS_DIR=/etc/hostashi-ssh/keys
SSH_DROPIN=/etc/ssh/sshd_config.d/10-hostashi.conf
SSH_SRC=${HOSTASHI_SSH_SRC:-$HERE/ssh}
SSH_CHANGED=no

chk_ssh() {
  pkg_installed openssh-server || { echo "openssh-server not installed"; return 1; }
  getent group "$SSH_GROUP" >/dev/null || { echo "group $SSH_GROUP missing"; return 1; }
  dir_ok /etc/hostashi-ssh 755 root:root || { echo "/etc/hostashi-ssh"; return 1; }
  dir_ok "$SSH_KEYS_DIR" 755 root:root || { echo "$SSH_KEYS_DIR"; return 1; }
  file_matches "$SSH_DROPIN" 0644 root:root <"$SSH_SRC/10-hostashi.conf" || { echo "sshd drop-in differs"; return 1; }
  [ "$SSH_CHANGED" = no ] || { echo "sshd reload needed after a change"; return 1; }
}
do_ssh() {
  local rc
  apt_install openssh-server || return 1
  getent group "$SSH_GROUP" >/dev/null || groupadd --system "$SSH_GROUP" >>"$LOG" 2>&1 || return 1
  install -d -m 0755 -o root -g root /etc/hostashi-ssh "$SSH_KEYS_DIR" /etc/ssh/sshd_config.d || return 1
  write_file "$SSH_DROPIN" 0644 root:root <"$SSH_SRC/10-hostashi.conf"; rc=$?
  [ "$rc" -le 1 ] || return 1
  [ "$rc" = 0 ] && SSH_CHANGED=yes
  if ! /usr/sbin/sshd -t >>"$LOG" 2>&1; then
    rm -f "$SSH_DROPIN"
    log "sshd configuration test failed, the drop-in was removed"
    return 1
  fi
  if [ "$SSH_CHANGED" = yes ]; then
    systemctl try-reload-or-restart ssh.service >>"$LOG" 2>&1 || return 1
  fi
  SSH_CHANGED=no
}

# Two leaks that SSH logins make easier (ADR-0032), closed without a
# chroot. The pool of homes is root 0711: a site user reaches its own home
# by path but cannot list the others. /proc is mounted with
# hidepid=invisible: every user sees only their own processes; root keeps
# the full view, and so do the members of the group hostashi-proc (empty,
# for a monitoring agent that needs all processes). The /proc line in fstab
# makes systemd-remount-fs apply the options at every boot; the step also
# remounts /proc now.
PROC_GROUP=hostashi-proc
POOL_DIRS=(/home)

proc_opts() { echo "hidepid=invisible,gid=$(getent group "$PROC_GROUP" | cut -d: -f3)"; }
proc_fstab_line() { printf 'proc\t/proc\tproc\tdefaults,%s\t0\t0\n' "$(proc_opts)"; }
proc_mounted_ok() {
  local opts
  opts=$(findmnt -no OPTIONS /proc 2>/dev/null | tr , '\n')
  grep -qx 'hidepid=invisible' <<<"$opts" && grep -qx "gid=$(getent group "$PROC_GROUP" | cut -d: -f3)" <<<"$opts"
}

chk_harden() {
  local d
  for d in "${POOL_DIRS[@]}"; do dir_ok "$d" 711 root:root || { echo "$d is not root 0711"; return 1; }; done
  getent group "$PROC_GROUP" >/dev/null || { echo "group $PROC_GROUP missing"; return 1; }
  grep -qxF "$(proc_fstab_line)" "$FSTAB" 2>/dev/null || { echo "/proc line in $FSTAB differs"; return 1; }
  proc_mounted_ok || { echo "/proc is not mounted with hidepid"; return 1; }
}
do_harden() {
  local d backup
  for d in "${POOL_DIRS[@]}"; do chown root:root "$d" && chmod 0711 "$d" || return 1; done
  getent group "$PROC_GROUP" >/dev/null || groupadd --system "$PROC_GROUP" >>"$LOG" 2>&1 || return 1
  if ! grep -qxF "$(proc_fstab_line)" "$FSTAB" 2>/dev/null; then
    [ -e "$FSTAB" ] || : >"$FSTAB"
    backup="$STATE_DIR/fstab.$(date -u +%Y%m%dT%H%M%SZ)"
    cp -p "$FSTAB" "$backup" || { log "harden: cannot back up $FSTAB"; return 1; }
    note "harden: fstab backup $backup"
    # Any other /proc line goes; the new one is appended.
    if ! { awk '!($2 == "/proc" && $1 !~ /^#/)' "$FSTAB" >"$FSTAB.hostashi-new" &&
      proc_fstab_line >>"$FSTAB.hostashi-new" &&
      chmod 0644 "$FSTAB.hostashi-new" && mv -f "$FSTAB.hostashi-new" "$FSTAB"; }; then
      rm -f "$FSTAB.hostashi-new"; return 1
    fi
    if ! findmnt --verify --tab-file "$FSTAB" >>"$LOG" 2>&1; then
      log "harden: findmnt --verify rejects the new $FSTAB, restoring it"
      cp -p "$backup" "$FSTAB"; return 1
    fi
  fi
  proc_mounted_ok || mount -o "remount,$(proc_opts)" /proc >>"$LOG" 2>&1
}

# Kernel settings of the hardening baseline (ADR-0033): see
# packaging/sysctl. systemd-sysctl loads the file at every boot; the step
# also applies it now. A key this kernel does not have (no Yama, no IPv6)
# is skipped, here and in hostashi doctor.
SYSCTL_SRC=${HOSTASHI_SYSCTL_SRC:-$HERE/sysctl}
SYSCTL_FILE=/etc/sysctl.d/90-hostashi.conf
SYSCTL_PROC=${HOSTASHI_SYSCTL_PROC:-/proc/sys}

# sysctl_each CMD runs CMD KEY VALUE FILE for every key of the file that
# this kernel has (FILE is its path under /proc/sys).
sysctl_each() {
  local key val f
  while IFS='=' read -r key val; do
    key=$(echo "$key" | tr -d '[:space:]'); val=$(echo "$val" | xargs)
    case "$key" in ''|'#'*) continue;; esac
    f="$SYSCTL_PROC/${key//./\/}"
    [ -e "$f" ] || continue
    "$@" "$key" "$val" "$f" || return 1
  done <"$SYSCTL_SRC/90-hostashi.conf"
}
sysctl_diff() { local live; live=$(xargs <"$3" 2>/dev/null); [ "$live" = "$2" ] || echo "$1: $live, want $2"; }
sysctl_set() { echo "$2" >"$3" 2>>"$LOG" || { log "sysctl: cannot set $1"; return 1; }; }
# sysctl_drift prints "key: live value, want value" for every key whose
# live value differs from the file.
sysctl_drift() { sysctl_each sysctl_diff; }

chk_sysctl() {
  local drift
  file_matches "$SYSCTL_FILE" 0644 root:root <"$SYSCTL_SRC/90-hostashi.conf" || { echo "$SYSCTL_FILE differs"; return 1; }
  drift=$(sysctl_drift)
  [ -z "$drift" ] || { echo "live values differ: ${drift//$'\n'/; }"; return 1; }
}
do_sysctl() {
  local drift
  install -d -m 0755 -o root -g root "${SYSCTL_FILE%/*}" || return 1
  write_file "$SYSCTL_FILE" 0644 root:root <"$SYSCTL_SRC/90-hostashi.conf"
  [ $? -le 1 ] || return 1
  # Written straight to /proc/sys: the sysctl tool (procps) may be missing.
  sysctl_each sysctl_set || return 1
  drift=$(sysctl_drift)
  [ -z "$drift" ] || { log "sysctl: values did not take: $drift"; return 1; }
}

# nginx and its brotli modules from the distribution (ADR-0020). The agent
# renders the whole config; the package's default site is never used.
NGINX_PKGS=(nginx libnginx-mod-http-brotli-filter libnginx-mod-http-brotli-static)
NGINX_DROPIN=/etc/systemd/system/nginx.service.d/hostashi.conf

chk_nginx() {
  local p
  for p in "${NGINX_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
}
do_nginx() { apt_install "${NGINX_PKGS[@]}"; }

# PHP from the distribution (Debian 13: 8.4, Ubuntu 26.04: 8.5) with the
# extensions of spike S5, and phpredis for the object cache (ADR-0045). The
# agent runs one FPM master per site.
PHP_PKGS=(php-fpm php-cli php-mysql php-curl php-gd php-mbstring php-xml php-zip php-intl php-bcmath)
# phpredis by its versioned name: sury.org's php<version>-redis provide
# php-redis and make apt remove the distribution's php-redis, while
# php<distro version>-redis stays (ADR-0036).
distro_php_redis() {
  local dep
  dep=$(dpkg-query -W -f='${Depends}' php-fpm 2>/dev/null) || return 1
  case "$dep" in php[0-9].[0-9]-fpm) dep=${dep#php}; echo "php${dep%-fpm}-redis";; *) return 1;; esac
}
# Every version the agent may run a site on (agentapi.PHPVersions); a unit
# template is installed for each, whether the version is installed or not.
PHP_VERSIONS=(7.4 8.1 8.2 8.3 8.4 8.5 8.6)

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
  p=$(distro_php_redis) || { echo "cannot tell the PHP version of php-fpm"; return 1; }
  pkg_installed "$p" || { echo "$p not installed"; return 1; }
  for u in $(distro_fpm_units); do
    [ "$(systemctl is-enabled "$u" 2>/dev/null)" != enabled ] || { echo "$u is enabled"; return 1; }
    [ "$(systemctl is-active "$u" 2>/dev/null)" != active ] || { echo "$u is running"; return 1; }
  done
}
do_php() {
  apt_install "${PHP_PKGS[@]}" || return 1
  local u r
  r=$(distro_php_redis) || return 1
  apt_install "$r" || return 1
  for u in $(distro_fpm_units); do
    systemctl disable --now "$u" >>"$LOG" 2>&1 || return 1
  done
}

# More PHP versions from sury.org (ADR-0019, ADR-0036). The step always
# installs the script packaging/php/php-install and its unit
# hostashi-php-install@.service, which the panel starts to install a
# version; with --php-versions it also runs the script for those versions.
# The script keeps the requested versions, so a later run without the
# option keeps them, and pins the distribution's PHP as the default.
PHP_INSTALL_SRC=${HOSTASHI_PHP_SRC:-$HERE/php}/php-install
PHP_INSTALL_BIN=/usr/local/lib/hostashi/php-install
PHP_INSTALL_UNIT=hostashi-php-install@.service
PHP_INSTALL_LOGS=/var/lib/hostashi-agent/php-install

php_allowed() { local v; for v in "${PHP_VERSIONS[@]}"; do [ "$v" = "$1" ] && return 0; done; return 1; }
php_requested() { tr ',' ' ' <<<"$PHP_REQUEST"; }

chk_phpversions() {
  local why d=${PHP_INSTALL_BIN%/*}
  dir_ok "$d" 755 root:root || { echo "$d"; return 1; }
  dir_ok "$PHP_INSTALL_LOGS" 700 root:root || { echo "$PHP_INSTALL_LOGS"; return 1; }
  file_matches "$PHP_INSTALL_BIN" 0755 root:root <"$PHP_INSTALL_SRC" || { echo "php-install differs"; return 1; }
  file_matches "/etc/systemd/system/$PHP_INSTALL_UNIT" 0644 root:root <"$UNIT_DIR/$PHP_INSTALL_UNIT" || { echo "$PHP_INSTALL_UNIT differs"; return 1; }
  # shellcheck disable=SC2046 # one version per word
  why=$(HOSTASHI_STATE_DIR=$STATE_DIR HOSTASHI_OS_RELEASE=$OS_RELEASE "$PHP_INSTALL_SRC" check $(php_requested)) || { echo "$why"; return 1; }
}
do_phpversions() {
  local rc
  install -d -m 0755 -o root -g root "${PHP_INSTALL_BIN%/*}" || return 1
  install -d -m 0700 -o root -g root "$PHP_INSTALL_LOGS" || return 1
  write_file "$PHP_INSTALL_BIN" 0755 root:root <"$PHP_INSTALL_SRC"; [ $? -le 1 ] || return 1
  write_file "/etc/systemd/system/$PHP_INSTALL_UNIT" 0644 root:root <"$UNIT_DIR/$PHP_INSTALL_UNIT"; rc=$?
  [ "$rc" -le 1 ] || return 1
  if [ "$rc" = 0 ]; then systemctl daemon-reload >>"$LOG" 2>&1 || return 1; fi
  # shellcheck disable=SC2046 # one version per word
  if ! HOSTASHI_STATE_DIR=$STATE_DIR HOSTASHI_OS_RELEASE=$OS_RELEASE "$PHP_INSTALL_BIN" check $(php_requested) >/dev/null; then
    [ -n "$PHP_REQUEST" ] || set -- $(cat "$STATE_DIR/php-versions" 2>/dev/null)
    # shellcheck disable=SC2046 # one version per word
    HOSTASHI_STATE_DIR=$STATE_DIR HOSTASHI_OS_RELEASE=$OS_RELEASE "$PHP_INSTALL_BIN" install $(php_requested) "$@" 2>&1 | tee -a "$LOG" | grep -E 'WARNING|REFUSED|cannot' >&2
    [ "${PIPESTATUS[0]}" = 0 ] || return 1
  fi
}

# PostgreSQL client tools from apt.postgresql.org for external PostgreSQL
# servers (ADR-0051). The step installs only the script
# packaging/postgresql/pg-client-install and its unit
# hostashi-pg-client-install@.service; the panel starts the unit when the
# first PostgreSQL server is added. A major the script installed earlier is
# checked (and installed again if missing) on every run.
PG_CLIENT_SRC=${HOSTASHI_PG_SRC:-$HERE/postgresql}/pg-client-install
PG_CLIENT_BIN=/usr/local/lib/hostashi/pg-client-install
PG_CLIENT_UNIT=hostashi-pg-client-install@.service
PG_CLIENT_LOGS=/var/lib/hostashi-agent/pg-client-install

pg_client() { HOSTASHI_PHP_LIB=$PHP_INSTALL_SRC HOSTASHI_STATE_DIR=$STATE_DIR HOSTASHI_OS_RELEASE=$OS_RELEASE "$@"; }

chk_pgclient() {
  local why
  dir_ok "$PG_CLIENT_LOGS" 700 root:root || { echo "$PG_CLIENT_LOGS"; return 1; }
  file_matches "$PG_CLIENT_BIN" 0755 root:root <"$PG_CLIENT_SRC" || { echo "pg-client-install differs"; return 1; }
  file_matches "/etc/systemd/system/$PG_CLIENT_UNIT" 0644 root:root <"$UNIT_DIR/$PG_CLIENT_UNIT" || { echo "$PG_CLIENT_UNIT differs"; return 1; }
  why=$(pg_client "$PG_CLIENT_SRC" check) || { echo "$why"; return 1; }
}
do_pgclient() {
  local rc
  install -d -m 0755 -o root -g root "${PG_CLIENT_BIN%/*}" || return 1
  install -d -m 0700 -o root -g root "$PG_CLIENT_LOGS" || return 1
  write_file "$PG_CLIENT_BIN" 0755 root:root <"$PG_CLIENT_SRC"; [ $? -le 1 ] || return 1
  write_file "/etc/systemd/system/$PG_CLIENT_UNIT" 0644 root:root <"$UNIT_DIR/$PG_CLIENT_UNIT"; rc=$?
  [ "$rc" -le 1 ] || return 1
  if [ "$rc" = 0 ]; then systemctl daemon-reload >>"$LOG" 2>&1 || return 1; fi
  if ! pg_client "$PG_CLIENT_BIN" check >/dev/null; then
    # shellcheck disable=SC2046 # one major per word
    set -- $(cat "$STATE_DIR/pg-client-majors" 2>/dev/null)
    [ $# -gt 0 ] || return 0
    pg_client "$PG_CLIENT_BIN" install "$@" 2>&1 | tee -a "$LOG" | grep -E 'REFUSED|cannot' >&2
    [ "${PIPESTATUS[0]}" = 0 ] || return 1
  fi
}

# Valkey from the distribution for the sites' object caches (ADR-0045). The
# agent runs one instance per site that turns the cache on, through the
# template hostashi-valkey@.service (step units). The package's own service
# would listen on 127.0.0.1:6379 for everyone: it is stopped and masked, so
# a package update cannot start it again.
VALKEY_PKGS=(valkey-server)
VALKEY_DEFAULT_UNIT=valkey-server.service

chk_valkey() {
  local p
  for p in "${VALKEY_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
  [ "$(systemctl is-enabled "$VALKEY_DEFAULT_UNIT" 2>/dev/null)" = masked ] || { echo "$VALKEY_DEFAULT_UNIT is not masked"; return 1; }
  [ "$(systemctl is-active "$VALKEY_DEFAULT_UNIT" 2>/dev/null)" != active ] || { echo "$VALKEY_DEFAULT_UNIT is running"; return 1; }
}
do_valkey() {
  apt_install "${VALKEY_PKGS[@]}" || return 1
  systemctl disable --now "$VALKEY_DEFAULT_UNIT" >>"$LOG" 2>&1 || return 1
  systemctl mask "$VALKEY_DEFAULT_UNIT" >>"$LOG" 2>&1
}

# MariaDB from the distribution (ADR-0024). The package defaults stay: it
# listens on 127.0.0.1 only and root logs in only through the Unix socket
# (unix_socket), which is how the agent connects. No password is set. One
# file of ours turns LOAD DATA LOCAL off (ADR-0033); a change to it
# restarts MariaDB.
MARIADB_PKGS=(mariadb-server)
MARIADB_SRC=${HOSTASHI_MARIADB_SRC:-$HERE/mariadb}
MARIADB_CNF=/etc/mysql/mariadb.conf.d/90-hostashi.cnf

chk_mariadb() {
  local p
  for p in "${MARIADB_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
  file_matches "$MARIADB_CNF" 0644 root:root <"$MARIADB_SRC/90-hostashi.cnf" || { echo "$MARIADB_CNF differs"; return 1; }
  [ "$(systemctl is-enabled mariadb.service 2>/dev/null)" = enabled ] || { echo "mariadb not enabled"; return 1; }
  [ "$(systemctl is-active mariadb.service 2>/dev/null)" = active ] || { echo "mariadb not running"; return 1; }
}
do_mariadb() {
  local rc
  apt_install "${MARIADB_PKGS[@]}" || return 1
  install -d -m 0755 -o root -g root "${MARIADB_CNF%/*}" || return 1
  write_file "$MARIADB_CNF" 0644 root:root <"$MARIADB_SRC/90-hostashi.cnf"
  rc=$?
  [ "$rc" -le 1 ] || return 1
  systemctl enable mariadb.service >>"$LOG" 2>&1 || return 1
  # write_file returns 0 when it changed the file.
  if [ "$rc" = 0 ]; then
    systemctl restart mariadb.service >>"$LOG" 2>&1
  else
    systemctl start mariadb.service >>"$LOG" 2>&1
  fi
}

# The web database tool (ADR-0038): Adminer's single MySQL-only file,
# pinned by its SHA-256 in packaging/adminer/SHA256SUMS, with Hostashi's
# index.php and tool.php that allow only one-time ticket logins, and
# adminer.css with the panel's fonts for the panel's look. Its own
# PHP-FPM runs the distribution's PHP under hostashi-adminer, a system user
# without home and shell; the agent writes the tickets into a directory
# that systemd-tmpfiles creates at boot. nginx serves it on DB_TOOL_PORT.
DBTOOL_SRC=${HOSTASHI_DBTOOL_SRC:-$HERE/adminer}
DBTOOL_USER=hostashi-adminer
DBTOOL_DIR=/usr/share/hostashi/adminer
DBTOOL_FILES=(adminer.php index.php tool.php adminer.css
  fonts/sora-latin-600-normal.woff2 fonts/sora-latin-700-normal.woff2
  fonts/ibm-plex-sans-latin-400-normal.woff2 fonts/ibm-plex-sans-latin-500-normal.woff2
  fonts/ibm-plex-sans-latin-600-normal.woff2 fonts/ibm-plex-mono-latin-400-normal.woff2
  fonts/ibm-plex-mono-latin-500-normal.woff2)
DBTOOL_CONF=/etc/hostashi-adminer/php-fpm.conf
DBTOOL_TMPFILES=/etc/tmpfiles.d/hostashi-adminer.conf
DBTOOL_TICKETS=/run/hostashi-adminer/tickets
DBTOOL_SESSIONS=/run/hostashi-adminer/sessions
DBTOOL_UNIT=hostashi-adminer.service
DBTOOL_CHANGED=no

db_tool_port() { if [ "$NO_DB_TOOL" = yes ]; then echo 0; else echo "$DB_TOOL_PORT"; fi; }

# dbtool_php prints the distribution's PHP version, from the php-fpm
# package's dependency (php8.4-fpm on Debian 13, php8.5-fpm on Ubuntu 26.04).
dbtool_php() {
  dpkg-query -W -f='${Depends}' php-fpm 2>/dev/null | grep -oE 'php[0-9]+\.[0-9]+-fpm' | head -n1 | sed -e 's/^php//' -e 's/-fpm$//'
}
dbtool_unit() {
  local v
  v=$(dbtool_php)
  [ -n "$v" ] || { echo "no PHP-FPM version found (php-fpm package)" >&2; return 1; }
  sed "s/@VERSION@/$v/g" "$DBTOOL_SRC/$DBTOOL_UNIT"
}
dbtool_pinned() { (cd "$DBTOOL_SRC" && sha256sum --quiet --strict -c SHA256SUMS) >/dev/null 2>&1; }

chk_dbtool() {
  local f unit
  if [ "$NO_DB_TOOL" = yes ]; then
    [ ! -e "/etc/systemd/system/$DBTOOL_UNIT" ] || { echo "$DBTOOL_UNIT installed, --no-db-tool given"; return 1; }
    [ ! -e "$DBTOOL_DIR" ] || { echo "$DBTOOL_DIR present, --no-db-tool given"; return 1; }
    return 0
  fi
  dbtool_pinned || { echo "adminer.php does not match its pinned SHA-256"; return 1; }
  getent passwd "$DBTOOL_USER" >/dev/null || { echo "user $DBTOOL_USER missing"; return 1; }
  [ "$(getent passwd "$DBTOOL_USER" | cut -d: -f7)" = /usr/sbin/nologin ] || { echo "$DBTOOL_USER has a login shell"; return 1; }
  dir_ok /usr/share/hostashi 755 root:root || { echo "/usr/share/hostashi"; return 1; }
  dir_ok "$DBTOOL_DIR" 755 root:root || { echo "$DBTOOL_DIR"; return 1; }
  dir_ok "$DBTOOL_DIR/fonts" 755 root:root || { echo "$DBTOOL_DIR/fonts"; return 1; }
  for f in "${DBTOOL_FILES[@]}"; do
    file_matches "$DBTOOL_DIR/$f" 0644 root:root <"$DBTOOL_SRC/$f" || { echo "$DBTOOL_DIR/$f differs"; return 1; }
  done
  dir_ok "${DBTOOL_CONF%/*}" 755 root:root || { echo "${DBTOOL_CONF%/*}"; return 1; }
  file_matches "$DBTOOL_CONF" 0644 root:root <"$DBTOOL_SRC/php-fpm.conf" || { echo "$DBTOOL_CONF differs"; return 1; }
  file_matches "$DBTOOL_TMPFILES" 0644 root:root <"$DBTOOL_SRC/hostashi-adminer.tmpfiles" || { echo "$DBTOOL_TMPFILES differs"; return 1; }
  dir_ok "$DBTOOL_TICKETS" 700 "$DBTOOL_USER:$DBTOOL_USER" || { echo "$DBTOOL_TICKETS"; return 1; }
  dir_ok "$DBTOOL_SESSIONS" 750 "root:$DBTOOL_USER" || { echo "$DBTOOL_SESSIONS"; return 1; }
  unit=$(dbtool_unit 2>&1) || { echo "$unit"; return 1; }
  printf '%s\n' "$unit" | file_matches "/etc/systemd/system/$DBTOOL_UNIT" 0644 root:root || { echo "$DBTOOL_UNIT differs"; return 1; }
  [ "$DBTOOL_CHANGED" = no ] || { echo "restart needed after a change"; return 1; }
  [ "$(systemctl is-enabled "$DBTOOL_UNIT" 2>/dev/null)" = enabled ] || { echo "$DBTOOL_UNIT not enabled"; return 1; }
  [ "$(systemctl is-active "$DBTOOL_UNIT" 2>/dev/null)" = active ] || { echo "$DBTOOL_UNIT not running"; return 1; }
}
do_dbtool() {
  local f unit rc
  if [ "$NO_DB_TOOL" = yes ]; then
    systemctl disable --now "$DBTOOL_UNIT" >>"$LOG" 2>&1
    rm -f "/etc/systemd/system/$DBTOOL_UNIT" "$DBTOOL_TMPFILES" "$DBTOOL_CONF" || return 1
    # The agent's files for external database servers (ADR-0041 addendum 1).
    rm -rf "$DBTOOL_DIR" /run/hostashi-adminer "${DBTOOL_CONF%/*}/servers" "/etc/systemd/system/$DBTOOL_UNIT.d" || return 1
    rmdir "${DBTOOL_CONF%/*}" 2>/dev/null
    systemctl daemon-reload >>"$LOG" 2>&1
    return 0
  fi
  dbtool_pinned || { log "dbtool: $DBTOOL_SRC/adminer.php does not match SHA256SUMS, not installing it"; return 1; }
  unit=$(dbtool_unit) || return 1
  if ! getent passwd "$DBTOOL_USER" >/dev/null; then
    adduser --system --group --no-create-home --home /nonexistent --shell /usr/sbin/nologin "$DBTOOL_USER" >>"$LOG" 2>&1 || return 1
  fi
  install -d -m 0755 -o root -g root /usr/share/hostashi "$DBTOOL_DIR" "$DBTOOL_DIR/fonts" "${DBTOOL_CONF%/*}" || return 1
  for f in "${DBTOOL_FILES[@]}"; do
    write_file "$DBTOOL_DIR/$f" 0644 root:root <"$DBTOOL_SRC/$f"; rc=$?
    [ "$rc" -le 1 ] || return 1
    [ "$rc" = 1 ] || DBTOOL_CHANGED=yes
  done
  write_file "$DBTOOL_CONF" 0644 root:root <"$DBTOOL_SRC/php-fpm.conf"; rc=$?
  [ "$rc" -le 1 ] || return 1
  [ "$rc" = 1 ] || DBTOOL_CHANGED=yes
  write_file "$DBTOOL_TMPFILES" 0644 root:root <"$DBTOOL_SRC/hostashi-adminer.tmpfiles"
  [ $? -le 1 ] || return 1
  systemd-tmpfiles --create "$DBTOOL_TMPFILES" >>"$LOG" 2>&1 || return 1
  printf '%s\n' "$unit" | write_file "/etc/systemd/system/$DBTOOL_UNIT" 0644 root:root; rc=$?
  [ "$rc" -le 1 ] || return 1
  [ "$rc" = 1 ] || DBTOOL_CHANGED=yes
  systemctl daemon-reload >>"$LOG" 2>&1 || return 1
  systemctl enable "$DBTOOL_UNIT" >>"$LOG" 2>&1 || return 1
  if [ "$DBTOOL_CHANGED" = yes ]; then
    systemctl restart "$DBTOOL_UNIT" >>"$LOG" 2>&1 || return 1
  else
    systemctl start "$DBTOOL_UNIT" >>"$LOG" 2>&1 || return 1
  fi
  DBTOOL_CHANGED=no
}

# restic from the distribution for the backups (ADR-0008), and the ssh
# client its SFTP backend starts. The backup timer is installed with the
# units but stays off until a backup target is set up.
BACKUP_PKGS=(restic openssh-client)

chk_backup() {
  local p
  for p in "${BACKUP_PKGS[@]}"; do pkg_installed "$p" || { echo "$p not installed"; return 1; }; done
}
do_backup() { apt_install "${BACKUP_PKGS[@]}"; }

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
  apt_install "${F2B_PKGS[@]}" || return 1
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

# nftables firewall, on by default (ADR-0033). Hostashi owns one table,
# inet hostashi, and touches nothing else (see packaging/firewall). Inbound
# is dropped except established traffic, loopback, the ICMP that IPv4 and
# IPv6 need, DHCP replies, every SSH port, the web (TCP 80, TCP and UDP 443)
# and the panel port (TCP and UDP for HTTP/3). Outbound stays open.
#
# Lockout protection: the SSH ports are the ones `sshd -T` names, plus the
# local port of every SSH session that is open right now (ss) and of the
# session running this installer (SSH_CONNECTION), so the admin's own
# session is never cut off. Without any SSH port the step refuses.
# --firewall-allow opens more ports. Like the other options it has to be
# given on every run; a run without it closes the port again.
# --no-firewall removes the table, the rules file and the unit.
FW_SRC=${HOSTASHI_FW_SRC:-$HERE/firewall}
FW_FILE=/etc/hostashi/firewall.nft
FW_UNIT=hostashi-firewall.service
FW_CHANGED=no
# The panel's rules (ADR-0034): the agent owns this file, the installer only
# creates it empty once, so a re-run keeps the panel's rules.
FW_PANEL_DIR=${HOSTASHI_FW_PANEL_DIR:-/var/lib/hostashi-agent/firewall}
FW_PANEL_FILE=$FW_PANEL_DIR/panel.nft

# fw_valid PORT/PROTO: a port from 1 to 65535 and tcp or udp.
fw_valid() {
  [[ "$1" =~ ^([1-9][0-9]{0,4})/(tcp|udp)$ ]] && [ "${BASH_REMATCH[1]}" -le 65535 ]
}

# fw_ssh_ports prints every port SSH must stay reachable on, one per line.
fw_ssh_ports() {
  /usr/sbin/sshd -T 2>/dev/null | awk '$1 == "port" { print $2 }'
  # Local address of each established connection of an sshd process
  # (sshd-session since OpenSSH 10), e.g. 192.0.2.1:22 or [2001:db8::1]:22.
  ss -Htnp state established 2>/dev/null | awk '/"sshd/ { n = split($3, a, ":"); print a[n] }'
  if [ -n "${SSH_CONNECTION:-}" ]; then
    # shellcheck disable=SC2086 # four fields: client ip, client port, server ip, server port
    set -- $SSH_CONNECTION
    [ $# -eq 4 ] && echo "$4"
  fi
}

# fw_ports PROTO prints the open ports of PROTO, sorted and comma-separated.
fw_ports() {
  local a
  {
    if [ "$1" = tcp ]; then fw_ssh_ports; echo 80; fi
    echo 443
    echo "$PANEL_PORT"
    if [ "$1" = tcp ] && [ "$NO_DB_TOOL" = no ]; then echo "$DB_TOOL_PORT"; fi
    for a in "${FW_ALLOW[@]}"; do [ "${a#*/}" = "$1" ] && echo "${a%/*}"; done
  } | grep -E '^[0-9]+$' | sort -nu | paste -sd, - | sed 's/,/, /g'
}

# fw_panel_ports prints the ports of the panel allowlist: the panel and the
# database tool, which is served for the same users (ADR-0038).
fw_panel_ports() {
  {
    [ "$PANEL_PORT" = 443 ] || echo "$PANEL_PORT"
    [ "$NO_DB_TOOL" = yes ] || echo "$DB_TOOL_PORT"
  } | paste -sd, - | sed 's/,/, /g'
}

# fw_render prints the rules file; it fails without an SSH port.
fw_render() {
  local ssh
  ssh=$(fw_ssh_ports | grep -E '^[0-9]+$' | sort -nu | paste -sd, - | sed 's/,/, /g')
  [ -n "$ssh" ] || { echo "no SSH port found (sshd -T), refusing to lock SSH out" >&2; return 1; }
  # On 443 the panel shares the sites' port: the packet filter cannot tell
  # them apart, so panel_ports stays empty and nginx enforces the panel
  # allowlist (ADR-0042). The database tool keeps its own port there.
  local ports panel
  ports=$(fw_panel_ports)
  panel=(-e "s/@PANEL@/$ports/")
  [ -n "$ports" ] || panel=(-e "/@PANEL@/d")
  sed -e "s/@TCP@/$(fw_ports tcp)/" -e "s/@UDP@/$(fw_ports udp)/" -e "s/@SSH@/$ssh/" \
    "${panel[@]}" -e "s#@PANEL_FILE@#$FW_PANEL_FILE#" "$FW_SRC/hostashi.nft"
}

# fw_panel_file creates the panel's rules file if it is missing.
fw_panel_file() {
  install -d -m 0700 -o root -g root "$FW_PANEL_DIR" || return 1
  [ -f "$FW_PANEL_FILE" ] && return 0
  printf '# Written by the Hostashi agent from the panel (ADR-0034).\n' >"$FW_PANEL_FILE.new" &&
    chmod 0600 "$FW_PANEL_FILE.new" && mv -f "$FW_PANEL_FILE.new" "$FW_PANEL_FILE"
}

chk_firewall() {
  local rules
  if [ "$NO_FIREWALL" = yes ]; then
    [ ! -e "/etc/systemd/system/$FW_UNIT" ] || { echo "firewall unit installed, --no-firewall given"; return 1; }
    [ ! -e "$FW_FILE" ] || { echo "firewall rules present, --no-firewall given"; return 1; }
    ! nft list table inet hostashi >/dev/null 2>&1 || { echo "table inet hostashi loaded, --no-firewall given"; return 1; }
    return 0
  fi
  pkg_installed nftables || { echo "nftables not installed"; return 1; }
  rules=$(fw_render 2>&1) || { echo "$rules"; return 1; }
  [ -f "$FW_PANEL_FILE" ] || { echo "$FW_PANEL_FILE missing"; return 1; }
  file_matches "/etc/systemd/system/$FW_UNIT" 0644 root:root <"$FW_SRC/$FW_UNIT" || { echo "firewall unit differs"; return 1; }
  printf '%s\n' "$rules" | file_matches "$FW_FILE" 0644 root:root || { echo "firewall rules differ"; return 1; }
  [ "$FW_CHANGED" = no ] || { echo "reload needed after a change"; return 1; }
  [ "$(systemctl is-enabled "$FW_UNIT" 2>/dev/null)" = enabled ] || { echo "firewall not enabled"; return 1; }
  [ "$(systemctl is-active "$FW_UNIT" 2>/dev/null)" = active ] || { echo "firewall not running"; return 1; }
  nft list table inet hostashi >/dev/null 2>&1 || { echo "table inet hostashi not loaded"; return 1; }
}
do_firewall() {
  local rules rc tmp
  if [ "$NO_FIREWALL" = yes ]; then
    # Stopping the unit deletes the table (ExecStop).
    systemctl disable --now "$FW_UNIT" >>"$LOG" 2>&1
    rm -f "/etc/systemd/system/$FW_UNIT" "$FW_FILE" || return 1
    systemctl daemon-reload >>"$LOG" 2>&1 || return 1
    if nft list table inet hostashi >/dev/null 2>&1; then nft delete table inet hostashi >>"$LOG" 2>&1 || return 1; fi
    return 0
  fi
  apt_install nftables || return 1
  rules=$(fw_render 2>>"$LOG") || { log "no SSH port found, the firewall was not set up"; return 1; }
  fw_panel_file || return 1
  # nft checks the rules before anything is written or loaded.
  tmp=$(mktemp) || return 1
  printf '%s\n' "$rules" >"$tmp"
  if ! nft -c -f "$tmp" >>"$LOG" 2>&1; then rm -f "$tmp"; log "firewall rules do not load"; return 1; fi
  rm -f "$tmp"
  write_file "/etc/systemd/system/$FW_UNIT" 0644 root:root <"$FW_SRC/$FW_UNIT"; rc=$?
  [ "$rc" -le 1 ] || return 1
  if [ "$rc" = 0 ]; then FW_CHANGED=yes; systemctl daemon-reload >>"$LOG" 2>&1 || return 1; fi
  printf '%s\n' "$rules" | write_file "$FW_FILE" 0644 root:root; rc=$?
  [ "$rc" -le 1 ] || return 1
  [ "$rc" = 0 ] && FW_CHANGED=yes
  systemctl enable "$FW_UNIT" >>"$LOG" 2>&1 || return 1
  if [ "$FW_CHANGED" = yes ] && [ "$(systemctl is-active "$FW_UNIT" 2>/dev/null)" = active ]; then
    systemctl reload "$FW_UNIT" >>"$LOG" 2>&1 || return 1
  else
    systemctl start "$FW_UNIT" >>"$LOG" 2>&1 || return 1
  fi
  # A flushed ruleset leaves the unit active without the table.
  nft list table inet hostashi >/dev/null 2>&1 || systemctl restart "$FW_UNIT" >>"$LOG" 2>&1 || return 1
  FW_CHANGED=no
}

# Security updates, on by default (ADR-0033): unattended-upgrades from the
# distribution installs updates from the security archive only, every day,
# and never reboots. The package's own files stay; ours sorts after them and
# replaces the origin list (see packaging/apt). The per-site PHP masters
# are not restarted by the PHP package, so a dpkg hook restarts the ones
# that still run a replaced binary or library.
APT_SRC=${HOSTASHI_APT_SRC:-$HERE/apt}
UU_FILE=/etc/apt/apt.conf.d/52hostashi-unattended-upgrades
PHP_HOOK_FILE=/etc/apt/apt.conf.d/80hostashi-php-restart
PHP_HOOK_BIN=/usr/local/lib/hostashi/php-restart
UU_TIMERS=(apt-daily.timer apt-daily-upgrade.timer)

# uu_origins prints the security origins of the running distribution.
# shellcheck disable=SC2016 # ${distro_codename} is for unattended-upgrades
uu_origins() {
  case "$OS_ID" in
    debian) printf '\t"origin=Debian,codename=${distro_codename}-security,label=Debian-Security";\n';;
    ubuntu) printf '\t"origin=Ubuntu,archive=${distro_codename}-security";\n'
            printf '\t"origin=UbuntuESMApps,archive=${distro_codename}-apps-security";\n'
            printf '\t"origin=UbuntuESM,archive=${distro_codename}-infra-security";\n';;
    *) return 1;;
  esac
  # More PHP versions come from sury.org (ADR-0019), which has no separate
  # security archive: every update there is a fix of an installed version.
  # The pin keeps the default PHP on the distribution, also for
  # unattended-upgrades. Without the source this origin matches nothing.
  printf '\t"origin=deb.sury.org,archive=${distro_codename}";\n'
  # The PostgreSQL client tools from apt.postgresql.org (ADR-0051): the same,
  # and the pin keeps everything but the client packages away.
  printf '\t"origin=apt.postgresql.org,archive=${distro_codename}-pgdg";\n'
}
uu_render() {
  local origins
  origins=$(uu_origins) || return 1
  awk -v o="$origins" '$0 == "@ORIGINS@" { print o; next } { print }' "$APT_SRC/52hostashi-unattended-upgrades"
}

chk_updates() {
  local t
  pkg_installed unattended-upgrades || { echo "unattended-upgrades not installed"; return 1; }
  uu_render | file_matches "$UU_FILE" 0644 root:root || { echo "unattended-upgrades settings differ"; return 1; }
  file_matches "$PHP_HOOK_FILE" 0644 root:root <"$APT_SRC/80hostashi-php-restart" || { echo "PHP restart hook differs"; return 1; }
  local d=${PHP_HOOK_BIN%/*}
  dir_ok "$d" 755 root:root || { echo "$d"; return 1; }
  file_matches "$PHP_HOOK_BIN" 0755 root:root <"$APT_SRC/php-restart" || { echo "PHP restart script differs"; return 1; }
  for t in "${UU_TIMERS[@]}"; do
    [ "$(systemctl is-enabled "$t" 2>/dev/null)" = enabled ] || { echo "$t not enabled"; return 1; }
  done
}
do_updates() {
  local t
  apt_install unattended-upgrades || return 1
  uu_render | write_file "$UU_FILE" 0644 root:root
  [ $? -le 1 ] || return 1
  install -d -m 0755 -o root -g root "${PHP_HOOK_BIN%/*}" || return 1
  write_file "$PHP_HOOK_BIN" 0755 root:root <"$APT_SRC/php-restart"
  [ $? -le 1 ] || return 1
  write_file "$PHP_HOOK_FILE" 0644 root:root <"$APT_SRC/80hostashi-php-restart"
  [ $? -le 1 ] || return 1
  for t in "${UU_TIMERS[@]}"; do systemctl enable --now "$t" >>"$LOG" 2>&1 || return 1; done
}

php_unit() { sed "s/@VERSION@/$1/g" "$UNIT_DIR/hostashi-php@.service"; }

chk_units() {
  local u
  for u in hostashi-agent.service hostashi.service hostashi-panel.socket hostashi-backup.service hostashi-backup.timer hostashi-restore@.service hostashi-import@.service hostashi-dbjob@.service hostashi-valkey@.service hostashi-files@.service hostashi-files@.socket; do
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
  for u in hostashi-agent.service hostashi.service hostashi-panel.socket hostashi-backup.service hostashi-backup.timer hostashi-restore@.service hostashi-import@.service hostashi-dbjob@.service hostashi-valkey@.service hostashi-files@.service hostashi-files@.socket; do
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
  apt_install "${QUOTA_PKGS[@]}" || return 1

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

# parse_args sets the option variables. Mode words only come from the
# command line; the options file holds options only.
parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      plan) MODE=plan;;
      apply) MODE=apply;;
      upgrade) MODE=upgrade;;
      --yes) YES=yes;;
      --allow-untested) ALLOW_UNTESTED=yes;;
      --binary) shift; BINARY=${1:-};;
      --panel-domain) shift; PANEL_DOMAIN=${1:-};;
      --listen) shift; LISTEN=${1:-};;
      --panel-port) shift; PANEL_PORT=${1:-};;
      --no-quota) NO_QUOTA=yes;;
      --no-firewall) NO_FIREWALL=yes;;
      --firewall-allow) shift; FW_ALLOW+=("${1:-}");;
      --no-db-tool) NO_DB_TOOL=yes;;
      --no-apt) NO_APT=yes;;
      --php-versions) shift; PHP_REQUEST=${1:-}; PHP_REQUEST_SET=yes;;
      *) echo "usage: $0 [plan | apply --yes | upgrade] --binary PATH [--no-apt] [--panel-domain NAME] [--panel-port PORT] [--listen ADDR] [--allow-untested] [--no-quota] [--firewall-allow PORT/PROTO]... [--no-firewall] [--no-db-tool] [--php-versions LIST]" >&2; exit 64;;
    esac
    shift
  done
}

# saved_options prints the options of this run for OPTIONS_FILE: one
# option per line, a value after one space. Values never hold spaces
# (preflight checks host names, ports, addresses and lists).
saved_options() {
  local a
  echo "# Written by the Hostashi installer after each successful apply (ADR-0050)."
  echo "# 'install.sh upgrade' reads it back. Re-run the installer to change it."
  echo "--listen $LISTEN"
  echo "--panel-port $PANEL_PORT"
  [ -z "$PANEL_DOMAIN" ] || echo "--panel-domain $PANEL_DOMAIN"
  [ "$ALLOW_UNTESTED" = no ] || echo "--allow-untested"
  [ "$NO_QUOTA" = no ] || echo "--no-quota"
  [ "$NO_FIREWALL" = no ] || echo "--no-firewall"
  for a in "${FW_ALLOW[@]}"; do echo "--firewall-allow $a"; done
  [ "$NO_DB_TOOL" = no ] || echo "--no-db-tool"
  [ "$PHP_REQUEST_SET" = no ] || echo "--php-versions $PHP_REQUEST"
}

# load_options reads OPTIONS_FILE into SAVED_ARGS. It refuses a file that
# someone other than root could have written, and any line that is not a
# known option: upgrade runs as root from the package's maintainer scripts.
SAVED_ARGS=()
load_options() {
  local line opt val
  [ -f "$OPTIONS_FILE" ] && [ ! -L "$OPTIONS_FILE" ] ||
    { log "REFUSED: $OPTIONS_FILE is missing: set the server up with 'install.sh apply --yes' first"; exit 2; }
  case "$(stat -c '%U:%G %a' "$OPTIONS_FILE")" in
    root:root\ 600|root:root\ 640|root:root\ 644) ;;
    *) log "REFUSED: $OPTIONS_FILE must be owned by root:root and writable by root only"; exit 2;;
  esac
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in ''|'#'*) continue;; esac
    opt=${line%% *}
    val=""
    [ "$opt" = "$line" ] || val=${line#* }
    case "$opt" in
      --allow-untested|--no-quota|--no-firewall|--no-db-tool)
        [ -z "$val" ] || { log "REFUSED: $OPTIONS_FILE: '$opt' takes no value"; exit 2; }
        SAVED_ARGS+=("$opt");;
      --listen|--panel-port|--panel-domain|--firewall-allow|--php-versions)
        [ -n "$val" ] && [ "${val#* }" = "$val" ] || { log "REFUSED: $OPTIONS_FILE: '$opt' needs one value"; exit 2; }
        SAVED_ARGS+=("$opt" "$val");;
      *) log "REFUSED: $OPTIONS_FILE: unknown line '$line'"; exit 2;;
    esac
  done <"$OPTIONS_FILE"
}

main() {
  if [ "${1:-}" = upgrade ]; then
    shift
    load_options
    parse_args "${SAVED_ARGS[@]}"
    parse_args "$@"
    MODE=apply YES=yes NO_APT=yes
    [ -n "$BINARY" ] || BINARY=$PKG_BIN
  else
    parse_args "$@"
    [ "$MODE" != upgrade ] || { echo "usage: upgrade comes first: $0 upgrade [--binary PATH]" >&2; exit 64; }
  fi
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
  # The directory exists after the dirs step; write_file leaves its mode alone.
  saved_options | write_file "$OPTIONS_FILE" 0600 root:root
  [ $? -le 1 ] || log "WARNING: could not save the options to $OPTIONS_FILE; 'install.sh upgrade' needs them"

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
