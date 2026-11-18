#!/bin/bash
# THROWAWAY SPIKE S5 prototype. Not production code (production = signed .deb + typed Go agent operations).
#
# Idempotent first-time setup of a FRESH Debian 13 / Ubuntu 24.04 server for Hostashi.
#
#   install.sh [plan]                    read-only: preflight + which steps would change something
#   install.sh apply --yes [options]     apply; running it again must change nothing
#
# Options:
#   --binary PATH      install this hostashi binary (no release exists yet), enable and start the units
#   --no-quota         do not touch project quota (tests in containers; a real server should not use this)
#   --allow-untested   accept an OS release that is not on the supported list (Ubuntu 26.04)
#   --reboot           reboot at the end if the quota step needs it
#
# Exit codes: 0 ok (also "reboot required", see output), 2 refused by preflight (server left unchanged),
#             3 a step failed (re-running is safe, steps are idempotent), 64 usage.
#
# Test hooks (environment): HOSTASHI_OS_RELEASE (file), HOSTASHI_ARCH, HOSTASHI_STATE_DIR.
set -u
set -o pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
STATE_DIR=${HOSTASHI_STATE_DIR:-/var/lib/hostashi-installer}   # installer state and backups, root only
STATE_FILE=$STATE_DIR/state
LOG=${HOSTASHI_INSTALL_LOG:-/var/log/hostashi-install.log}
OS_RELEASE=${HOSTASHI_OS_RELEASE:-/etc/os-release}

# nginx.org signing keys. Every key in the downloaded file must be on this list [A: keys rotate, update here].
NGINX_KEY_URL=https://nginx.org/keys/nginx_signing.key
NGINX_KEY_FPRS="8540A6F18833A80E9C1653A42FD21310B49F6B46 573BFD6B3D8FBC641079A6ABABF5BD827BD9BF62 9E9BE90EACBCDE69FE9B204CBCDCD8A38D88A2B3"
NGINX_KEYRING=/usr/share/keyrings/nginx-archive-keyring.gpg

BASE_PKGS="ca-certificates curl gnupg e2fsprogs quota unattended-upgrades apt-transport-https"
PHP_PKGS="php-fpm php-cli php-mysql php-curl php-gd php-mbstring php-xml php-zip php-intl php-bcmath"
DB_PKGS="mariadb-server"

MODE=plan YES=no WANT_QUOTA=yes BINARY="" ALLOW_UNTESTED=no REBOOT=no
CHANGES=0 FAILED=0 NEED_REBOOT=no

log()   { echo "install: $*" >&2; }
note()  { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >>"$LOG" 2>/dev/null || true; }
refuse(){ log "REFUSED: $*"; log "server left unchanged"; exit 2; }

# ---------------------------------------------------------------- facts
detect() {
  # shellcheck disable=SC1090
  OS_ID=$(. "$OS_RELEASE"; echo "${ID:-unknown}")
  OS_VER=$(. "$OS_RELEASE"; echo "${VERSION_ID:-unknown}")
  OS_CODENAME=$(. "$OS_RELEASE"; echo "${VERSION_CODENAME:-}")
  RAW_ARCH=${HOSTASHI_ARCH:-$(dpkg --print-architecture 2>/dev/null || uname -m)}
  case "$RAW_ARCH" in x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) ARCH=$RAW_ARCH;; esac
}

pkg_installed() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q '^install ok installed$'; }
missing_pkgs() { local p out=""; for p in "$@"; do pkg_installed "$p" || out="$out $p"; done; echo "${out# }"; }
unit_state() { systemctl "$1" "$2" 2>/dev/null; }

# write_file PATH MODE OWNER:GROUP  (content on stdin). Returns 0 and replaces the file only if it differs.
write_file() {
  local path=$1 mode=$2 owner=$3 tmp
  tmp=$(mktemp "$path.XXXXXX") || return 1
  cat >"$tmp"
  chmod "$mode" "$tmp"; chown "$owner" "$tmp"
  if [ -f "$path" ] && cmp -s "$tmp" "$path" && [ "$(stat -c %a "$path")" = "$(stat -c %a "$tmp")" ] && [ "$(stat -c %U:%G "$path")" = "$owner" ]; then
    rm -f "$tmp"; return 1
  fi
  mv "$tmp" "$path"; return 0
}
# file_matches PATH MODE OWNER:GROUP < expected content   (read-only twin of write_file)
file_matches() {
  local path=$1 mode=$2 owner=$3 tmp; tmp=$(mktemp) ; cat >"$tmp"
  [ -f "$path" ] && cmp -s "$tmp" "$path" && [ "$(stat -c %a "$path")" = "${mode#0}" ] && [ "$(stat -c %U:%G "$path")" = "$owner" ]
  local rc=$?; rm -f "$tmp"; return $rc
}

apt_install() {
  export DEBIAN_FRONTEND=noninteractive
  { apt-get update -qq && apt-get install -y -qq --no-install-recommends -o Dpkg::Options::=--force-confold "$@"; } >>"$LOG" 2>&1
}

# ---------------------------------------------------------------- preflight (changes nothing)
preflight() {
  local problems=0 warn=0
  fail() { log "preflight: FAIL: $*"; problems=$((problems+1)); }
  warnx() { log "preflight: warn: $*"; warn=$((warn+1)); }
  detect

  [ "$(id -u)" = 0 ] || { [ "$MODE" = plan ] && warnx "not root, some checks are skipped" || fail "must run as root"; }

  case "$OS_ID-$OS_VER" in
    debian-13) ;;                                   # first choice (ADR-0009)
    ubuntu-24.04) ;;                                # second choice
    ubuntu-26.04) [ "$ALLOW_UNTESTED" = yes ] || fail "Ubuntu 26.04 is not tested yet (use --allow-untested)";;
    *) fail "unsupported OS '$OS_ID $OS_VER' (supported: Debian 13, Ubuntu 24.04 LTS)";;
  esac
  case "$ARCH" in amd64|arm64) ;; *) fail "unsupported architecture '$RAW_ARCH' (amd64 and arm64 only)";; esac
  [ -d /run/systemd/system ] || fail "systemd is not PID 1"

  local mem_kb; mem_kb=$(awk '/^MemTotal:/{print $2}' /proc/meminfo)
  [ "${mem_kb:-0}" -ge 450000 ] || fail "only $((mem_kb/1024)) MiB RAM (minimum 512 MiB [E])"
  [ "${mem_kb:-0}" -ge 1800000 ] || warnx "$((mem_kb/1024)) MiB RAM is below the recommended 2 GiB [E]"
  local free_kb; free_kb=$(df -Pk / | awk 'NR==2{print $4}')
  [ "${free_kb:-0}" -ge 3000000 ] || fail "only $((free_kb/1024)) MiB free on / (minimum 3 GiB [E])"

  # "Fresh server only" (ADR-0009): refuse anything that looks like a grown system, unless this installer was here before.
  if [ ! -f "$STATE_FILE" ]; then
    local p found=""
    for p in apache2 nginx nginx-common nginx-core mariadb-server mysql-server mysql-community-server php-fpm php8.3-fpm php8.4-fpm caddy lighttpd; do
      pkg_installed "$p" && found="$found $p"
    done
    [ -z "$found" ] || fail "not a fresh server, found installed:$found (Hostashi installs on fresh servers only)"
    local port
    for port in 80 443 3306; do
      if command -v ss >/dev/null; then ss -Hltn "sport = :$port" 2>/dev/null | grep -q . && fail "port $port is already in use"
      else warnx "'ss' not found, cannot check port $port"; fi
    done
  fi

  # Quota must be possible, otherwise refuse BEFORE changing anything (S8 finding 1 and 8).
  if [ "$WANT_QUOTA" = yes ]; then
    local out rc
    out=$("$HERE/quota-setup.sh" plan 2>&1); rc=$?
    if [ $rc -ne 0 ]; then
      # Ubuntu generic kernels ship quota_v2 only in linux-modules-extra; the base step installs it, so this is not final.
      if [ "$OS_ID" = ubuntu ] && echo "$out" | grep -q 'linux-modules-extra' && apt-cache show "linux-modules-extra-$(uname -r)" >/dev/null 2>&1; then
        warnx "quota_v2 missing for $(uname -r), will install linux-modules-extra-$(uname -r) [U: not tested here]"
      else
        fail "project quota cannot be enabled safely: $(echo "$out" | grep 'NOT SAFE' | head -1) (override: --no-quota)"
      fi
    fi
  fi

  if [ -n "$BINARY" ]; then [ -x "$BINARY" ] || fail "--binary '$BINARY' is not an executable file"; fi
  [ $problems -eq 0 ] || refuse "$problems preflight problem(s)"
  log "preflight: ok ($OS_ID $OS_VER $ARCH, ${warn} warning(s))"
}

# ---------------------------------------------------------------- steps
# Every step has chk_<name> (0 = already satisfied, prints why not on stdout) and do_<name>.
# apply re-runs chk after do and fails the step if the postcondition does not hold.
STEPS="base_packages nginx_repo nginx php mariadb account dirs binary units updates quota"

chk_base_packages() {
  local m; m=$(missing_pkgs $BASE_PKGS)
  if [ "$WANT_QUOTA" = yes ] && [ "$OS_ID" = ubuntu ] && ! modinfo quota_v2 >/dev/null 2>&1 && ! grep -q quota_v2 "/lib/modules/$(uname -r)/modules.builtin" 2>/dev/null; then
    pkg_installed "linux-modules-extra-$(uname -r)" || m="$m linux-modules-extra-$(uname -r)"
  fi
  [ -z "${m# }" ] || { echo "missing:${m:+ $m}"; return 1; }
}
do_base_packages() {
  local extra=""
  if [ "$WANT_QUOTA" = yes ] && [ "$OS_ID" = ubuntu ] && ! modinfo quota_v2 >/dev/null 2>&1 && ! grep -q quota_v2 "/lib/modules/$(uname -r)/modules.builtin" 2>/dev/null; then
    extra="linux-modules-extra-$(uname -r)"
  fi
  # shellcheck disable=SC2086
  apt_install $BASE_PKGS $extra
}

nginx_list() { echo "deb [signed-by=$NGINX_KEYRING] https://nginx.org/packages/$OS_ID $OS_CODENAME nginx"; }
nginx_pin() { printf 'Package: nginx*\nPin: origin nginx.org\nPin-Priority: 900\n'; }
chk_nginx_repo() {
  [ -f "$NGINX_KEYRING" ] || { echo "keyring missing"; return 1; }
  local fprs; fprs=$(gpg --no-default-keyring --keyring "$NGINX_KEYRING" --list-keys --with-colons 2>/dev/null | awk -F: '$1=="fpr"{print $10}')
  [ -n "$fprs" ] || { echo "keyring empty"; return 1; }
  local f; for f in $fprs; do case " $NGINX_KEY_FPRS " in *" $f "*) ;; *) echo "keyring has unknown key $f"; return 1;; esac; done
  nginx_list | file_matches /etc/apt/sources.list.d/nginx.list 0644 root:root || { echo "sources entry differs"; return 1; }
  nginx_pin | file_matches /etc/apt/preferences.d/99-hostashi-nginx 0644 root:root || { echo "pin differs"; return 1; }
}
do_nginx_repo() {
  local tmp; tmp=$(mktemp -d)
  curl -fsSL --retry 3 --max-time 30 "$NGINX_KEY_URL" -o "$tmp/key" >>"$LOG" 2>&1 || { log "cannot download nginx signing key"; rm -rf "$tmp"; return 1; }
  local f
  for f in $(gpg --show-keys --with-colons "$tmp/key" 2>/dev/null | awk -F: '$1=="fpr"{print $10}'); do
    case " $NGINX_KEY_FPRS " in *" $f "*) ;; *) log "downloaded key $f is not on the pinned list, refusing"; rm -rf "$tmp"; return 1;; esac
  done
  gpg --dearmor <"$tmp/key" >"$tmp/keyring.gpg" 2>>"$LOG" || { rm -rf "$tmp"; return 1; }
  install -m 0644 -o root -g root "$tmp/keyring.gpg" "$NGINX_KEYRING"; rm -rf "$tmp"
  nginx_list | write_file /etc/apt/sources.list.d/nginx.list 0644 root:root || true
  nginx_pin | write_file /etc/apt/preferences.d/99-hostashi-nginx 0644 root:root || true
}

chk_nginx() {
  pkg_installed nginx || { echo "nginx not installed"; return 1; }
  local v; v=$(dpkg-query -W -f='${Version}' nginx)
  case "$v" in *"~$OS_CODENAME") ;; *) echo "nginx $v is not from nginx.org"; return 1;; esac
  [ "$(unit_state is-enabled nginx.service)" = enabled ] || { echo "nginx not enabled"; return 1; }
  [ "$(unit_state is-active nginx.service)" = active ] || { echo "nginx not running"; return 1; }
}
do_nginx() { apt_install nginx && systemctl enable --now nginx.service >>"$LOG" 2>&1; }

php_fpm_units() { systemctl list-unit-files --no-legend 'php*-fpm.service' 2>/dev/null | awk '{print $1}'; }
chk_php() {
  local m; m=$(missing_pkgs $PHP_PKGS); [ -z "$m" ] || { echo "missing: $m"; return 1; }
  local u
  # The default shared FPM master is not used: ADR-0015 wants one master per site, started by Hostashi.
  for u in $(php_fpm_units); do
    [ "$(unit_state is-enabled "$u")" != enabled ] || { echo "$u still enabled"; return 1; }
    [ "$(unit_state is-active "$u")" != active ] || { echo "$u still running"; return 1; }
  done
}
do_php() {
  # shellcheck disable=SC2086
  apt_install $PHP_PKGS || return 1
  local u; for u in $(php_fpm_units); do systemctl disable --now "$u" >>"$LOG" 2>&1 || return 1; done
}

mariadb_dropin() {
  printf '# Managed by the Hostashi installer.\n[mysqld]\nbind-address = 127.0.0.1\nskip-name-resolve\nlocal-infile = 0\n'
}
MARIADB_DROPIN=/etc/mysql/mariadb.conf.d/99-hostashi.cnf
chk_mariadb() {
  pkg_installed mariadb-server || { echo "mariadb-server not installed"; return 1; }
  mariadb_dropin | file_matches $MARIADB_DROPIN 0644 root:root || { echo "drop-in differs"; return 1; }
  [ "$(unit_state is-enabled mariadb.service)" = enabled ] || { echo "mariadb not enabled"; return 1; }
  [ "$(unit_state is-active mariadb.service)" = active ] || { echo "mariadb not running"; return 1; }
  # applied config must already be in effect (a restart is only needed when the drop-in was just written)
  [ "$(mariadb -NBe 'select @@bind_address' 2>/dev/null)" = 127.0.0.1 ] || { echo "bind-address not in effect"; return 1; }
}
do_mariadb() {
  apt_install $DB_PKGS || return 1
  mkdir -p "$(dirname $MARIADB_DROPIN)"
  mariadb_dropin | write_file $MARIADB_DROPIN 0644 root:root || true
  systemctl enable mariadb.service >>"$LOG" 2>&1 && systemctl restart mariadb.service >>"$LOG" 2>&1
}

chk_account() {
  getent passwd hostashi >/dev/null || { echo "user hostashi missing"; return 1; }
  [ "$(getent passwd hostashi | cut -d: -f7)" = /usr/sbin/nologin ] || { echo "hostashi has a login shell"; return 1; }
}
do_account() { adduser --system --group --no-create-home --home /var/lib/hostashi --shell /usr/sbin/nologin hostashi >>"$LOG" 2>&1; }

dir_ok() { [ -d "$1" ] && [ "$(stat -c '%a %U:%G' "$1")" = "$2 $3" ]; }
chk_dirs() {
  dir_ok /etc/hostashi 750 root:hostashi || { echo "/etc/hostashi"; return 1; }
  dir_ok /var/lib/hostashi 750 hostashi:hostashi || { echo "/var/lib/hostashi"; return 1; }
  dir_ok /var/log/hostashi 750 hostashi:hostashi || { echo "/var/log/hostashi"; return 1; }
  dir_ok "$STATE_DIR" 700 root:root || { echo "$STATE_DIR"; return 1; }
}
do_dirs() {
  install -d -m 0750 -o root -g hostashi /etc/hostashi
  install -d -m 0750 -o hostashi -g hostashi /var/lib/hostashi /var/log/hostashi
  install -d -m 0700 -o root -g root "$STATE_DIR"
}

chk_binary() {
  [ -n "$BINARY" ] || return 0   # no release exists yet, nothing to install
  [ -x /usr/local/bin/hostashi ] && cmp -s "$BINARY" /usr/local/bin/hostashi || { echo "binary differs or missing"; return 1; }
}
do_binary() { install -m 0755 -o root -g root "$BINARY" /usr/local/bin/hostashi.new && mv /usr/local/bin/hostashi.new /usr/local/bin/hostashi; }

unit_agent() { cat <<'U'
[Unit]
Description=Hostashi agent (privileged, typed operations only)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/hostashi agent
RuntimeDirectory=hostashi
RuntimeDirectoryMode=0755
Restart=on-failure
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictRealtime=yes

[Install]
WantedBy=multi-user.target
U
}
unit_panel() { cat <<'U'
[Unit]
Description=Hostashi panel (web UI and API, no root)
After=network.target hostashi-agent.service
Wants=hostashi-agent.service

[Service]
Type=simple
User=hostashi
Group=hostashi
ExecStart=/usr/local/bin/hostashi serve
Restart=on-failure
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictRealtime=yes
RestrictSUIDSGID=yes
SystemCallArchitectures=native
ReadWritePaths=/var/lib/hostashi /var/log/hostashi
ReadOnlyPaths=/etc/hostashi

[Install]
WantedBy=multi-user.target
U
}
chk_units() {
  unit_agent | file_matches /etc/systemd/system/hostashi-agent.service 0644 root:root || { echo "agent unit differs"; return 1; }
  unit_panel | file_matches /etc/systemd/system/hostashi.service 0644 root:root || { echo "panel unit differs"; return 1; }
  if [ -x /usr/local/bin/hostashi ]; then
    local u; for u in hostashi-agent hostashi; do
      [ "$(unit_state is-enabled $u.service)" = enabled ] || { echo "$u not enabled"; return 1; }
      [ "$(unit_state is-active $u.service)" = active ] || { echo "$u not running"; return 1; }
    done
  fi
}
do_units() {
  local ch=0
  unit_agent | write_file /etc/systemd/system/hostashi-agent.service 0644 root:root && ch=1
  unit_panel | write_file /etc/systemd/system/hostashi.service 0644 root:root && ch=1
  [ $ch = 0 ] || systemctl daemon-reload >>"$LOG" 2>&1 || return 1
  if [ -x /usr/local/bin/hostashi ]; then
    systemctl enable hostashi-agent.service hostashi.service >>"$LOG" 2>&1 || return 1
    # restart only when the binary or a unit changed in this run, otherwise just make sure they run
    if [ "$BINARY_CHANGED" = yes ] || [ $ch = 1 ]; then systemctl restart hostashi-agent.service hostashi.service >>"$LOG" 2>&1
    else systemctl start hostashi-agent.service hostashi.service >>"$LOG" 2>&1; fi
  fi
}

updates_conf() { cat <<'C'
// Managed by the Hostashi installer. Security updates from the distribution plus the nginx.org stable branch.
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
Unattended-Upgrade::Origins-Pattern {
	"origin=nginx,label=nginx";
};
C
}
chk_updates() { updates_conf | file_matches /etc/apt/apt.conf.d/52hostashi-unattended 0644 root:root || { echo "config differs"; return 1; }; }
do_updates() { updates_conf | write_file /etc/apt/apt.conf.d/52hostashi-unattended 0644 root:root; return 0; }

chk_quota() {
  [ "$WANT_QUOTA" = yes ] || return 0
  # satisfied = prepared (fstab+initramfs, waits for reboot) or active
  local out; out=$("$HERE/quota-setup.sh" plan 2>&1)
  case "$out" in *"Nothing to do"*) return 0;; esac
  if [ -f "$STATE_DIR/quota-prepared" ] && [ "$(cat /proc/sys/kernel/random/boot_id)" = "$(cat "$STATE_DIR/quota-prepared")" ]; then
    NEED_REBOOT=yes; return 0   # prepared in this boot, reboot still pending
  fi
  echo "quota not active"; return 1
}
do_quota() {
  HOSTASHI_STATE=$STATE_DIR "$HERE/quota-setup.sh" apply --yes >>"$LOG" 2>&1 || return 1
  cat /proc/sys/kernel/random/boot_id >"$STATE_DIR/quota-prepared"
  NEED_REBOOT=yes
}

# ---------------------------------------------------------------- runner
BINARY_CHANGED=no
run_steps() {
  local s why
  for s in $STEPS; do
    why=$(chk_$s 2>&1); if [ $? -eq 0 ]; then printf '  [ok]      %s\n' "$s"; continue; fi
    if [ "$MODE" = plan ]; then printf '  [change]  %s: %s\n' "$s" "$why"; CHANGES=$((CHANGES+1)); continue; fi
    printf '  [apply]   %s: %s\n' "$s" "$why"; note "step $s: $why"
    [ "$s" = binary ] && BINARY_CHANGED=yes
    if ! do_$s || ! why=$(chk_$s 2>&1); then
      log "step '$s' FAILED${why:+ (postcondition: $why)}, see $LOG. Re-running the installer is safe."; FAILED=1; return 3
    fi
    CHANGES=$((CHANGES+1))
  done
}

main() {
  while [ $# -gt 0 ]; do case "$1" in
    plan) MODE=plan;; apply) MODE=apply;; --yes) YES=yes;;
    --no-quota) WANT_QUOTA=no;; --allow-untested) ALLOW_UNTESTED=yes;; --reboot) REBOOT=yes;;
    --binary) shift; BINARY=${1:-};;
    *) echo "usage: $0 [plan | apply --yes] [--binary PATH] [--no-quota] [--allow-untested] [--reboot]" >&2; exit 64;;
  esac; shift; done
  if [ "$MODE" = apply ] && [ "$YES" != yes ]; then log "refusing to change anything without --yes (run 'plan' first)"; exit 2; fi
  [ "$MODE" = apply ] && { mkdir -p "$(dirname "$LOG")"; touch "$LOG"; chmod 600 "$LOG"; }
  preflight
  if [ "$MODE" = apply ] && [ ! -f "$STATE_FILE" ]; then
    mkdir -p "$STATE_DIR"; chmod 700 "$STATE_DIR"
    printf 'started=%s\nos=%s-%s\narch=%s\n' "$(date -u +%FT%TZ)" "$OS_ID" "$OS_VER" "$ARCH" >"$STATE_FILE"
    CHANGES=$((CHANGES+1))
  fi
  run_steps || exit $?
  if [ "$MODE" = plan ]; then
    [ $CHANGES -eq 0 ] && echo "Nothing to do." || echo "$CHANGES step(s) would change something. Apply with: $0 apply --yes"
    exit 0
  fi
  if [ $CHANGES -eq 0 ]; then echo "Nothing to do. (changes=0)"; else echo "Done. changes=$CHANGES"; fi
  if [ "$NEED_REBOOT" = yes ]; then
    echo "REBOOT REQUIRED to activate project quota. Run the installer again after the reboot to confirm."
    [ "$REBOOT" = yes ] && { echo "rebooting in 5 s"; sleep 5; systemctl reboot; }
  elif ! grep -q '^completed=' "$STATE_FILE" 2>/dev/null; then
    printf 'completed=%s\n' "$(date -u +%FT%TZ)" >>"$STATE_FILE"
  fi
  exit 0
}
main "$@"
