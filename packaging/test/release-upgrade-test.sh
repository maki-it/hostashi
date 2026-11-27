#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Upgrades a server from the latest stable release on get.hostashi.com to
# the package in DIST_DIR, in a container with systemd as PID 1: install
# with the published install script, set up an admin, a PHP site with a
# database, then install the new package as apt upgrade would. Checks that
# the panel comes back with the same login, the site still answers, the
# database keeps its rows and the new version runs. Unlike upgrade-test.sh
# (same package, higher number), this crosses real schema changes and
# needs the internet.
#
#   release-upgrade-test.sh IMAGE DIST_DIR      e.g. debian:trixie ./dist
#
# DIST_DIR holds hostashi_*_<arch>.deb (goreleaser). FROM=0.14.2 checks
# that the release installed is that version. KEEP=1 leaves the container
# running after the test, for debugging. EXTRA_CA=FILE adds a CA
# certificate to the container's trust store (a TLS-inspecting proxy).
# The single-quoted scripts expand inside the container.
# shellcheck disable=SC2016
set -euo pipefail

IMG=$1
DIST=$(realpath "$2")
URL=${HOSTASHI_GET_URL:-https://get.hostashi.com}
NAME=hostashi-release-upgrade-$$
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
new=$(dpkg-deb -f "${debs[0]}" Version 2>/dev/null || docker run --rm -v "$DIST:/d:ro" "$IMG" dpkg-deb -f "/d/$(basename "${debs[0]}")" Version)

step "start $IMG with systemd"
# As in upgrade-test.sh: cgroup v2 for systemd 258 and later.
if [ "$(stat -fc %T /sys/fs/cgroup)" = cgroup2fs ]; then
  cg=(--cgroupns=private)
else
  cg=(--cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw)
fi
docker run -d --name "$NAME" --privileged "${cg[@]}" --tmpfs /run --tmpfs /run/lock \
  "$IMG" sleep infinity >/dev/null
x bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv curl ca-certificates >/dev/null'
if [ -n "${EXTRA_CA:-}" ]; then
  docker cp "$EXTRA_CA" "$NAME:/usr/local/share/ca-certificates/extra-ca.crt"
  x update-ca-certificates >/dev/null
fi
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

health() {
  local _
  for _ in $(seq 1 60); do
    x curl -fsS -o /dev/null http://127.0.0.1:8080/api/v1/health 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

step "install the latest stable release from $URL"
x curl -fsSL -o /root/install.sh "$URL/install.sh" || fail "download install.sh"
x bash /root/install.sh --yes --no-firewall >/tmp/release-install.log 2>&1 || {
  tail -n 40 /tmp/release-install.log >&2
  x tail -n 60 /var/log/hostashi-install.log >&2 || true
  fail "install of the release"
}
old=$(x dpkg-query -W -f '${Version}' hostashi)
echo "installed: hostashi $old"
[ -z "${FROM:-}" ] || [ "$old" = "$FROM" ] || fail "installed $old, expected $FROM"
dpkg --compare-versions "$new" gt "$old" 2>/dev/null ||
  x dpkg --compare-versions "$new" gt "$old" || fail "$new is not newer than $old"
health || fail "panel not up after the install"

step "data on the old version: admin, PHP site, database"
api() {
  x curl -sS -o /tmp/api.out -w '%{http_code}' -X "$1" -H 'Content-Type: application/json' \
    ${cookie:+-H "Cookie: $cookie"} ${3:+--data "$3"} "http://127.0.0.1:8080$2"
}
login() {
  x curl -sS -o /dev/null -D - -H 'Content-Type: application/json' \
    --data "{\"username\":\"kim\",\"password\":\"$pw\"${1:+,\"totp\":\"$1\"}}" http://127.0.0.1:8080/api/v1/auth/login |
    tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \(hostashi_session=[^;]*\).*/\1/p'
}
totp() {
  python3 -c 'import base64,hashlib,hmac,struct,sys,time
s = sys.argv[1]
k = base64.b32decode(s + "=" * (-len(s) % 8))
h = hmac.new(k, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
o = h[-1] & 15
print("%06d" % ((struct.unpack(">I", h[o:o + 4])[0] & 0x7fffffff) % 1000000))' "$1"
}
bin=$(x bash -c 'command -v hostashi')
tok=$(x "$bin" setup-token -db /var/lib/hostashi/hostashi.db)
pw="test-$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"
cookie=""
[ "$(api POST /api/v1/setup "{\"token\":\"$tok\",\"username\":\"kim\",\"password\":\"$pw\"}")" = 201 ] || fail "setup: $(x cat /tmp/api.out)"
cookie=$(login)
[ -n "$cookie" ] || fail "login gave no session cookie"
[ "$(api POST /api/v1/auth/totp/enroll)" = 200 ] || fail "TOTP enroll: $(x cat /tmp/api.out)"
secret=$(x cat /tmp/api.out | sed -n 's/.*"secret":"\([A-Z2-7]*\)".*/\1/p')
[ "$(api POST /api/v1/auth/totp/confirm "{\"code\":\"$(totp "$secret")\"}")" = 200 ] || fail "TOTP confirm: $(x cat /tmp/api.out)"
step_used=$(( $(date +%s) / 30 ))
[ "$(api GET /api/v1/php/versions)" = 200 ] || fail "php versions: $(x cat /tmp/api.out)"
ver=$(x cat /tmp/api.out | sed -n 's/.*"installed":\["\([0-9.]*\)".*/\1/p')
[ -n "$ver" ] || fail "no PHP version installed: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/sites "{\"name\":\"blog\",\"php\":\"$ver\",\"domains\":[\"blog.example.com\"]}")" = 201 ] ||
  fail "create site: $(x cat /tmp/api.out)"
x sh -c 'echo "<?php echo \"php \" . PHP_MAJOR_VERSION . \" ok\";" >/home/blog/htdocs/index.php'
body=$(x curl -fsS -H 'Host: blog.example.com' http://127.0.0.1/) || fail "site does not answer"
[ "$body" = "php ${ver%%.*} ok" ] || fail "site answered '$body'"
[ "$(api POST /api/v1/sites/blog/databases '{"name":"blog_db","user":"blog_db"}')" = 201 ] ||
  fail "create database: $(x cat /tmp/api.out)"
dbpw=$(x cat /tmp/api.out | sed -n 's/.*"password":"\([A-Za-z0-9]*\)".*/\1/p')
x mariadb -u blog_db "-p$dbpw" blog_db -e 'CREATE TABLE t (v TEXT); INSERT INTO t VALUES ("before upgrade")' ||
  fail "write to the database"
[ "$(api GET /api/v1/sites)" = 200 ] || fail "list sites: $(x cat /tmp/api.out)"
sites_before=$(x cat /tmp/api.out | python3 -c 'import json,sys; d=json.load(sys.stdin); d=d.get("sites", d) if isinstance(d, dict) else d; print(sorted(s["name"] for s in d))')

step "upgrade to $new"
docker cp "${debs[0]}" "$NAME:/root/hostashi-new.deb"
out=$(x env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends /root/hostashi-new.deb 2>&1) ||
  { echo "$out" >&2; fail "upgrade"; }
[ "$(x dpkg-query -W -f '${Status} ${Version}' hostashi)" = "install ok installed $new" ] || fail "not configured at $new"
x grep -qF "hostashi $old -> " /var/log/hostashi-upgrade.log || fail "upgrade log"
x test -f "/var/lib/hostashi-installer/upgrade/hostashi-$old.db" || fail "no database copy of $old"
x systemctl is-active -q hostashi-agent hostashi || fail "services not active"
health || fail "panel not up after the upgrade"
# The binary may have moved (to /usr/bin with 0.15, PR 617): look again.
bin=$(x bash -c 'command -v hostashi')
got=$(x "$bin" version | head -n 1)
grep -qF "${new%%[~+-]*}" <<<"$got" || fail "running '$got', expected $new"

step "the data is still there on $new"
# A TOTP code works once: wait for the next step after the one used.
while [ $(( $(date +%s) / 30 )) -le "$step_used" ]; do sleep 1; done
cookie=$(login "$(totp "$secret")")
[ -n "$cookie" ] || fail "old password and TOTP do not log in after the upgrade: $(x curl -sS -H 'Content-Type: application/json' \
  --data "{\"username\":\"kim\",\"password\":\"$pw\"}" http://127.0.0.1:8080/api/v1/auth/login)"
[ "$(api GET /api/v1/sites)" = 200 ] || fail "list sites after the upgrade: $(x cat /tmp/api.out)"
sites_after=$(x cat /tmp/api.out | python3 -c 'import json,sys; d=json.load(sys.stdin); d=d.get("sites", d) if isinstance(d, dict) else d; print(sorted(s["name"] for s in d))')
[ "$sites_after" = "$sites_before" ] || fail "sites $sites_after, were $sites_before"
body=$(x curl -fsS -H 'Host: blog.example.com' http://127.0.0.1/) || fail "site does not answer after the upgrade"
[ "$body" = "php ${ver%%.*} ok" ] || fail "site answered '$body' after the upgrade"
[ "$(x mariadb -u blog_db "-p$dbpw" blog_db -Nse 'SELECT v FROM t')" = "before upgrade" ] || fail "database row lost"
[ "$(api GET /api/v1/sites/blog/databases)" = 200 ] || fail "list databases: $(x cat /tmp/api.out)"
x grep -q blog_db /tmp/api.out || fail "database gone from the panel: $(x cat /tmp/api.out)"
x /usr/share/hostashi/install.sh upgrade >/dev/null 2>&1 || fail "install.sh upgrade after the package upgrade"
health || fail "panel not up after install.sh upgrade"

echo "OK: upgrade from hostashi $old to $new on $(x bash -c '. /etc/os-release && echo "$PRETTY_NAME"') ($arch)"
