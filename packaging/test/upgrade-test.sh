#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Upgrades a set-up server through the .deb (ADR-0050) in a container with
# systemd as PID 1: install the package, set the server up with the
# installer, then install a newer version of the package. Checks that the
# first install leaves the server alone, that the upgrade keeps a copy of
# the panel database and brings agent and panel back, that broken saved
# options stop the upgrade until they are fixed, that only the newest
# copies are kept, that a downgrade goes through with the same database
# schema and stops otherwise, and that hostashi-rollback goes back.
# The server comes from a local signed repository, as from get.hostashi.com,
# but was set up without the suite runtimes (before ADR-0077): the upgrade
# adds it and hostashi-runtimes arrives right after dpkg is done, and
# 'install.sh upgrade' brings it back when it is missing.
#
#   upgrade-test.sh IMAGE DIST_DIR      e.g. debian:trixie ./dist
#
# DIST_DIR holds hostashi_*_<arch>.deb and runtimes/hostashi-runtimes_*.deb.
# The newer versions are the same package with a higher version number,
# carrying the test repository's key for the suite runtimes.
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
rdebs=("$DIST"/runtimes/hostashi-runtimes_*_all.deb)
[ ${#rdebs[@]} -eq 1 ] && [ -f "${rdebs[0]}" ] || { echo "expected one runtimes/hostashi-runtimes_*_all.deb in $DIST" >&2; exit 1; }

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

# A local repository with the suites stable and runtimes, signed by a
# throwaway key (packaging/apt-repo/build.sh), at file:/repo.
x mkdir -p /pkg/rt /pkg/tools
docker cp "${debs[0]}" "$NAME:/pkg/hostashi.deb"
docker cp "${rdebs[0]}" "$NAME:/pkg/rt/"
docker cp "$(dirname "$0")/../apt-repo/build.sh" "$NAME:/pkg/tools/build.sh"
x bash -c '
  set -e
  export DEBIAN_FRONTEND=noninteractive
  apt-get install -y -qq --no-install-recommends apt-utils gnupg xz-utils >/dev/null
  mkdir -m 0700 /pkg/gpg
  mkdir /pkg/stable
  cp /pkg/hostashi.deb /pkg/stable/
  GNUPGHOME=/pkg/gpg gpg --batch --quiet --passphrase "" --quick-gen-key "Hostashi test <test@example.invalid>" ed25519 sign 1d 2>/dev/null
  GNUPGHOME=/pkg/gpg /pkg/tools/build.sh /pkg/stable /pkg/repo-s >/dev/null
  GNUPGHOME=/pkg/gpg SUITE=runtimes PACKAGE=hostashi-runtimes /pkg/tools/build.sh /pkg/rt /pkg/repo-r >/dev/null
  mkdir -m 0755 /repo
  cp -a /pkg/repo-s/. /pkg/repo-r/. /repo/
  chmod -R a+rX /repo'

# The same package as version V.1, V.2, ...: only the number changes.
x bash -c '
  set -e
  v=$(dpkg-deb -f /pkg/hostashi.deb Version)
  dpkg-deb -R /pkg/hostashi.deb /pkg/tree
  cp /repo/hostashi-runtimes.asc /pkg/tree/usr/share/keyrings/hostashi-runtimes-keyring.asc
  for n in 1 2 3 4; do
    sed -i "s/^Version: .*/Version: $v.$n/" /pkg/tree/DEBIAN/control
    dpkg-deb -b /pkg/tree "/pkg/hostashi-$n.deb" >/dev/null
  done'
v=$(x dpkg-deb -f /pkg/hostashi.deb Version)
apt() { x env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "$@"; }
# health waits up to 30 s for the panel to answer: a start that the
# package scripts trigger without waiting takes a moment.
health() {
  local _
  for _ in $(seq 1 30); do
    x bash -c 'exec 3<>/dev/tcp/127.0.0.1/8080 && printf "GET /api/v1/health HTTP/1.0\r\n\r\n" >&3 && head -n 1 <&3' 2>/dev/null | grep ' 200 ' >/dev/null && return 0
    sleep 1
  done
  return 1
}

step "first install changes nothing and says how to set up"
out=$(apt /pkg/hostashi.deb 2>&1) || { echo "$out" >&2; fail "apt install"; }
grep -q 'Set the server up with' <<<"$out" || fail "no setup hint: $out"
x test ! -e /etc/hostashi/install.options || fail "options file before setup"
x test ! -e /etc/systemd/system/hostashi.service || fail "units installed before setup"

step "set up with the installer"
x /usr/share/hostashi/install.sh apply --yes --binary /usr/bin/hostashi --no-firewall >/dev/null || {
  x tail -n 60 /var/log/hostashi-install.log >&2 || true
  fail "apply"
}
x test -f /etc/hostashi/install.options || fail "no options saved"
x test -f /var/lib/hostashi/hostashi.db || fail "no panel database"
health || fail "panel not up after setup"

# As packaging/get/install.sh leaves it; the setup above did not know the
# suite runtimes yet, like an installer before ADR-0077.
x bash -c 'install -m 0644 /repo/hostashi.asc /usr/share/keyrings/hostashi-test.asc
  printf "Types: deb\nURIs: file:/repo\nSuites: stable\nComponents: main\nSigned-By: /usr/share/keyrings/hostashi-test.asc\n" >/etc/apt/sources.list.d/hostashi.sources'
x test ! -e /etc/apt/sources.list.d/hostashi-runtimes.sources || fail "suite runtimes before the upgrade"

# agent_pid prints the main PID of hostashi-agent.
agent_pid() { x systemctl show -p MainPID --value hostashi-agent.service; }
step "setup starts the package's binary"
[ "$(x readlink "/proc/$(agent_pid)/exe")" = /usr/bin/hostashi ] || fail "agent does not run /usr/bin/hostashi"
x test ! -e /usr/local/bin/hostashi || fail "a copy in /usr/local/bin after setup"
[ "$(x cat /var/lib/hostashi-installer/binary.sha256)" = "$(x sh -c 'sha256sum </usr/bin/hostashi | cut -d" " -f1')" ] || fail "binary checksum not noted"

# A server set up by 0.14 or older: the installer copied the binary to
# /usr/local/bin, the units started that copy and no checksum was kept.
step "a server with the binary in /usr/local/bin (before 1.0)"
x bash -c 'set -e
  install -m 0755 /usr/bin/hostashi /usr/local/bin/hostashi
  sed -i "s#/usr/bin/hostashi#/usr/local/bin/hostashi#g" /etc/systemd/system/hostashi*.service
  rm -f /var/lib/hostashi-installer/binary.sha256
  systemctl daemon-reload
  systemctl restart hostashi-agent.service hostashi.service'
health || fail "panel not up on the old layout"
[ "$(x readlink "/proc/$(agent_pid)/exe")" = /usr/local/bin/hostashi ] || fail "old layout not set up"

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

step "the upgrade moves the server off /usr/local/bin"
x test ! -e /usr/local/bin/hostashi || fail "/usr/local/bin/hostashi still there"
if x grep -l /usr/local/bin/hostashi -r /etc/systemd/system; then fail "a unit still starts /usr/local/bin/hostashi"; fi
for u in hostashi-agent hostashi; do
  pid=$(x systemctl show -p MainPID --value "$u.service")
  [ "$(x readlink "/proc/$pid/exe")" = /usr/bin/hostashi ] || fail "$u does not run /usr/bin/hostashi"
done
for u in hostashi-backup.service hostashi-dbjob@.service hostashi-restore@.service hostashi-files@.service hostashi-wpcli-install.service; do
  x systemctl cat "$u" | grep '^ExecStart=/usr/bin/hostashi ' >/dev/null || fail "$u does not start /usr/bin/hostashi"
done
[ "$(x bash -lc 'command -v hostashi')" = /usr/bin/hostashi ] || fail "hostashi in PATH is not /usr/bin/hostashi"
x bash -c 'source /usr/local/share/bash-completion/completions/hostashi && COMP_WORDS=(hostashi site li) COMP_CWORD=2 && _hostashi && [ "${COMPREPLY[*]}" = "list limits" ]' ||
  fail "bash completion after the move"
x test -S /run/hostashi/agent.sock || fail "agent socket missing after the move"

# runtimes_installed waits up to 120 s for hostashi-runtimes: the unit the
# upgrade started waits for dpkg's lock.
runtimes_installed() {
  local _
  for _ in $(seq 1 60); do
    [ "$(x dpkg-query -W -f '${Status}' hostashi-runtimes 2>/dev/null)" = "install ok installed" ] && return 0
    sleep 2
  done
  x journalctl --no-pager -u hostashi-runtimes-install -n 30 >&2 || true
  return 1
}
step "the upgrade adds the suite runtimes and installs hostashi-runtimes after dpkg"
x test -f /etc/apt/sources.list.d/hostashi-runtimes.sources || fail "no source for the suite runtimes"
x systemctl is-enabled -q hostashi-runtimes-install.timer || fail "timer not enabled"
x grep -q 'hostashi-runtimes: installing it in the background' /var/log/hostashi-upgrade.log || fail "the upgrade did not start the install"
runtimes_installed || fail "hostashi-runtimes not installed after the upgrade"
x grep -q '"schema": 1' /usr/share/hostashi/runtimes.json || fail "runtimes.json content"
health || fail "panel not up after hostashi-runtimes"

step "install.sh upgrade brings a missing hostashi-runtimes back"
x env DEBIAN_FRONTEND=noninteractive apt-get remove -y -qq hostashi-runtimes >/dev/null 2>&1 || fail "remove hostashi-runtimes"
out=$(x /usr/share/hostashi/install.sh upgrade 2>&1) || { echo "$out" >&2; fail "install.sh upgrade"; }
grep -q 'installing it in the background' <<<"$out" || fail "install.sh upgrade did not start the install: $out"
runtimes_installed || fail "hostashi-runtimes not installed after install.sh upgrade"
health || fail "panel not up after install.sh upgrade"

step "install.sh upgrade restarts agent and panel for a binary dpkg replaced"
pid=$(agent_pid)
x sh -c 'echo 0000 >/var/lib/hostashi-installer/binary.sha256'
out=$(x /usr/share/hostashi/install.sh upgrade 2>&1) || { echo "$out" >&2; fail "install.sh upgrade"; }
grep -q 'binary: new binary, services not restarted for it yet' <<<"$out" || fail "changed binary not seen: $out"
[ "$(agent_pid)" != "$pid" ] || fail "agent not restarted for the new binary"
health || fail "panel not up after the restart"
out=$(x /usr/share/hostashi/install.sh upgrade 2>&1) || { echo "$out" >&2; fail "second install.sh upgrade"; }
grep -q 'changes=0' <<<"$out" || fail "second upgrade changed something: $out"

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
health || fail "panel not up after the upgrades"
schema=$(x /usr/bin/hostashi version -schema)
[ "$(x cat "/var/lib/hostashi-installer/upgrade/hostashi-$v.3.schema")" = "$schema" ] || fail "schema of $v.3 not noted"

step "a downgrade with the same schema goes through"
apt --allow-downgrades /pkg/hostashi-3.deb >/dev/null 2>&1 || fail "downgrade to .3"
[ "$(x dpkg-query -W -f '${Status} ${Version}' hostashi)" = "install ok installed $v.3" ] || fail "not at .3"
health || fail "panel not up after the downgrade"

step "a downgrade to another schema stops and names hostashi-rollback"
x bash -c "echo 1 >/var/lib/hostashi-installer/upgrade/hostashi-$v.2.schema"
if out=$(apt --allow-downgrades /pkg/hostashi-2.deb 2>&1); then fail "downgrade across schemas went through"; fi
grep -qF "hostashi-rollback --yes $v.2" <<<"$out" || fail "no hint at hostashi-rollback: $out"
[ "$(x dpkg-query -W -f '${Status} ${Version}' hostashi)" = "install ok installed $v.3" ] || fail "package changed by the refused downgrade"
health || fail "panel not up after the refused downgrade"

step "hostashi-rollback restores the database copy and installs the version"
db=/var/lib/hostashi/hostashi.db
x env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sqlite3 >/dev/null
owner=$(x stat -c %U "$db")
perm=$(x stat -c '%a %U:%G' "$db")
sql() { x runuser -u "$owner" -- sqlite3 "$1" "$2"; }
# A table the copy of .2 does not have, written while the panel is down.
x systemctl stop hostashi-panel.socket hostashi.service
sql "$db" 'CREATE TABLE rollback_marker (x)'
x systemctl start hostashi-panel.socket hostashi.service
health || fail "panel not up after the marker"
out=$(x hostashi-rollback --deb /pkg/hostashi-2.deb "$v.2")
grep -q 'Run again with --yes' <<<"$out" || fail "rollback without --yes: $out"
[ "$(sql "$db" "SELECT count(*) FROM sqlite_master WHERE name = 'rollback_marker'")" = 1 ] || fail "rollback without --yes changed the database"
for bad in "$v.4" "$v.0" 'x;y'; do
  if x hostashi-rollback --yes --deb /pkg/hostashi-4.deb "$bad" >/dev/null 2>&1; then fail "rollback to '$bad' accepted"; fi
done
if x hostashi-rollback --yes --deb /pkg/hostashi-1.deb "$v.2" >/dev/null 2>&1; then fail "rollback with the wrong .deb accepted"; fi
# Without a version: the newest copy older than .3, which is .2.
out=$(x hostashi-rollback --deb /pkg/hostashi-2.deb)
grep -qF "from $v.3 to $v.2" <<<"$out" || fail "rollback picked another version: $out"
x hostashi-rollback --yes --deb /pkg/hostashi-2.deb "$v.2" || fail "hostashi-rollback"
[ "$(x dpkg-query -W -f '${Status} ${Version}' hostashi)" = "install ok installed $v.2" ] || fail "not at .2 after the rollback"
health || fail "panel not up after the rollback"
[ "$(sql "$db" "SELECT count(*) FROM sqlite_master WHERE name = 'rollback_marker'")" = 0 ] || fail "database copy not restored"
[ "$(x sqlite3 -readonly "/var/lib/hostashi-installer/upgrade/before-rollback-$v.3.db" "SELECT count(*) FROM sqlite_master WHERE name = 'rollback_marker'")" = 1 ] ||
  fail "database before the rollback not kept"
[ "$(x stat -c '%a %U:%G' "$db")" = "$perm" ] || fail "database owner or mode changed: $(x stat -c '%a %U:%G' "$db"), was $perm"
[ "$(x stat -c '%a %U' "/var/lib/hostashi-installer/upgrade/before-rollback-$v.3.db")" = "600 root" ] || fail "kept database owner or mode"

echo "OK: upgrade of hostashi $v on $(x bash -c '. /etc/os-release && echo "$PRETTY_NAME"') ($arch)"
