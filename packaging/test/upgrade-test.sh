#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Upgrades a set-up server through the .deb (ADR-0050) in a container with
# systemd as PID 1: install the package, set the server up with the
# installer, then install a newer version of the package. Checks that the
# first install leaves the server alone, that the upgrade keeps a copy of
# the panel database and brings agent and panel back, that broken saved
# options stop the upgrade until they are fixed, and that only the newest
# copies are kept.
#
#   upgrade-test.sh IMAGE DIST_DIR      e.g. debian:trixie ./dist
#
# The newer versions are the same package with a higher version number.
# KEEP=1 leaves the container running after the test, for debugging.
# The single-quoted scripts and formats expand inside the container.
# shellcheck disable=SC2016
set -euo pipefail

IMG=$1
DIST=$(realpath "$2")
NAME=hostashi-upgrade-test-$$
cleanup() {
  [ -n "${KEEP:-}" ] && return
  docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

step() { echo "=== $*"; }
x() { docker exec "$NAME" "$@"; }
fail() {
  echo "FAIL: $*" >&2
  x tail -n 40 /var/log/hostashi-upgrade.log >&2 || true
  x journalctl --no-pager -u hostashi-agent -u hostashi -n 30 >&2 || true
  exit 1
}

arch=$(docker run --rm "$IMG" dpkg --print-architecture)
debs=("$DIST"/hostashi_*_"$arch".deb)
[ ${#debs[@]} -eq 1 ] && [ -f "${debs[0]}" ] || { echo "expected one hostashi_*_$arch.deb in $DIST" >&2; exit 1; }

step "start $IMG with systemd"
# systemd 258 and later (Ubuntu 26.04) need cgroup v2; on a cgroup v1 host
# only older systemd (Debian 13) boots, through the host's hierarchy. The
# container has its own network, so the panel's port is its own.
if [ "$(stat -fc %T /sys/fs/cgroup)" = cgroup2fs ]; then
  cg=(--cgroupns=private)
else
  cg=(--cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw)
fi
docker run -d --name "$NAME" --privileged "${cg[@]}" --tmpfs /run --tmpfs /run/lock \
  "$IMG" sleep infinity >/dev/null
x bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv >/dev/null'
docker commit "$NAME" "$NAME-img" >/dev/null
docker rm -f "$NAME" >/dev/null
docker run -d --name "$NAME" --privileged "${cg[@]}" --tmpfs /run --tmpfs /run/lock \
  "$NAME-img" /lib/systemd/systemd >/dev/null
docker rmi "$NAME-img" >/dev/null 2>&1 || true
state=""
for _ in $(seq 1 60); do
  state=$(x systemctl is-system-running 2>/dev/null || true)
  case "$state" in running | degraded) break;; esac
  sleep 2
done
echo "systemd: $state"

# The same package as version V.1, V.2, ...: only the number changes.
x mkdir -p /pkg
docker cp "${debs[0]}" "$NAME:/pkg/hostashi.deb"
x bash -c '
  set -e
  v=$(dpkg-deb -f /pkg/hostashi.deb Version)
  dpkg-deb -R /pkg/hostashi.deb /pkg/tree
  for n in 1 2 3 4; do
    sed -i "s/^Version: .*/Version: $v.$n/" /pkg/tree/DEBIAN/control
    dpkg-deb -b /pkg/tree "/pkg/hostashi-$n.deb" >/dev/null
  done'
v=$(x dpkg-deb -f /pkg/hostashi.deb Version)
apt() { x env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "$@"; }
health() {
  x bash -c 'exec 3<>/dev/tcp/127.0.0.1/8080 && printf "GET /api/v1/health HTTP/1.0\r\n\r\n" >&3 && head -n 1 <&3' | grep -q ' 200 '
}

step "first install changes nothing and says how to set up"
out=$(apt /pkg/hostashi.deb 2>&1) || { echo "$out" >&2; fail "apt install"; }
grep -q 'Set the server up with' <<<"$out" || fail "no setup hint: $out"
x test ! -e /etc/hostashi/install.options || fail "options file before setup"
x test ! -e /usr/local/bin/hostashi || fail "binary installed before setup"

step "set up with the installer"
x /usr/share/hostashi/install.sh apply --yes --binary /usr/bin/hostashi --no-firewall >/dev/null || {
  x tail -n 60 /var/log/hostashi-install.log >&2 || true
  fail "apply"
}
x test -f /etc/hostashi/install.options || fail "no options saved"
x test -f /var/lib/hostashi/hostashi.db || fail "no panel database"
health || fail "panel not up after setup"

step "upgrade keeps a database copy and restarts the panel"
out=$(apt /pkg/hostashi-1.deb 2>&1) || { echo "$out" >&2; fail "upgrade"; }
grep -q "saved to /var/lib/hostashi-installer/upgrade/hostashi-$v.db" <<<"$out" || fail "no database copy reported: $out"
[ "$(x stat -c '%a %U' "/var/lib/hostashi-installer/upgrade/hostashi-$v.db")" = "600 root" ] || fail "copy owner or mode"
[ "$(x stat -c '%a' /var/lib/hostashi-installer/upgrade)" = 700 ] || fail "copy directory mode"
x grep -q "hostashi $v -> " /var/log/hostashi-upgrade.log || fail "upgrade log"
x grep -q ' is up$' /var/log/hostashi-upgrade.log || fail "postinst did not see the panel up"
[ "$(x dpkg-query -W -f '${Status} ${Version}' hostashi)" = "install ok installed $v.1" ] || fail "not configured"
x systemctl is-active -q hostashi-agent hostashi || fail "services not active"
health || fail "panel not up after upgrade"

step "broken saved options stop the upgrade until fixed"
x chmod 0666 /etc/hostashi/install.options
if out=$(apt /pkg/hostashi-2.deb 2>&1); then fail "upgrade went through with broken options"; fi
grep -q 'dpkg --configure -a' <<<"$out" || fail "no hint how to retry: $out"
[ "$(x dpkg-query -W -f '${Status}' hostashi)" != "install ok installed" ] || fail "package configured anyway"
health || fail "panel not started again after the failed upgrade"
x chmod 0600 /etc/hostashi/install.options
x dpkg --configure -a >/dev/null || fail "dpkg --configure -a after the fix"
[ "$(x dpkg-query -W -f '${Status} ${Version}' hostashi)" = "install ok installed $v.2" ] || fail "not configured after the fix"
health || fail "panel not up after the fix"

step "only the newest three copies stay"
apt /pkg/hostashi-3.deb >/dev/null 2>&1 || fail "upgrade to .3"
apt /pkg/hostashi-4.deb >/dev/null 2>&1 || fail "upgrade to .4"
got=$(x bash -c 'cd /var/lib/hostashi-installer/upgrade && ls -1 hostashi-*.db' | sort)
want=$(printf 'hostashi-%s.db\n' "$v.1" "$v.2" "$v.3" | sort)
[ "$got" = "$want" ] || fail "kept copies: $got"
health || fail "panel not up at the end"

echo "OK: upgrade of hostashi $v on $(x bash -c '. /etc/os-release && echo "$PRETTY_NAME"') ($arch)"
