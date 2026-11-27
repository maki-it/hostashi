#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Spike S9 (ADR-0016): a second storage pool on its own ext4 volume (a loop
# device), in a Debian 13 container with systemd as PID 1. Throwaway: it
# prints findings as "RESULT key: value" lines and fails only when the setup
# itself breaks. Nothing outside the container and the loop device changes.
#
#   pool-test.sh BINARY        (as root on the host, with docker)
#
# HOSTASHI_TEST_CA (optional): a CA bundle to trust inside the container.
# KEEP=1 leaves the container and the loop device for debugging.
set -euo pipefail

BINARY=$(realpath "$1")
REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
IMG=${IMG:-debian:trixie}
NAME=hostashi-s9-$$
BASE=hostashi-s9-base-$$
IMGFILE=$(mktemp /var/tmp/s9-vol.XXXXXX)
LOOP=""

cleanup() {
  [ -n "${KEEP:-}" ] && { echo "kept: container $NAME, loop $LOOP"; return; }
  docker rm -f "$NAME" "$NAME-prep" >/dev/null 2>&1 || true
  docker rmi -f "$BASE" >/dev/null 2>&1 || true
  [ -n "$LOOP" ] && losetup -d "$LOOP" 2>/dev/null || true
  rm -f "$IMGFILE"
}
trap cleanup EXIT

step() { echo "=== $*"; }
res() { echo "RESULT $1: $2"; }
die() { echo "SETUP FAILED: $*" >&2; exit 1; }
x() { docker exec "$NAME" "$@"; }
xi() { docker exec -i "$NAME" "$@"; }

step "prepare $IMG with systemd"
docker run -d --name "$NAME-prep" --network host "$IMG" sleep infinity >/dev/null
if [ -n "${HOSTASHI_TEST_CA:-}" ]; then
  docker exec "$NAME-prep" mkdir -p /etc/ssl/certs
  docker cp "$HOSTASHI_TEST_CA" "$NAME-prep:/etc/ssl/certs/ca-certificates.crt"
  docker exec -e HTTPS_PROXY="${HTTPS_PROXY:-}" "$NAME-prep" bash -c '
    printf "Acquire::https::Proxy \"%s\";\nAcquire::https::CAInfo \"/etc/ssl/certs/ca-certificates.crt\";\n" "$HTTPS_PROXY" >/etc/apt/apt.conf.d/99test-proxy
    sed -i "s#http://#https://#g" /etc/apt/sources.list.d/*.sources'
fi
docker exec "$NAME-prep" bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv adduser curl e2fsprogs util-linux >/dev/null'
docker commit "$NAME-prep" "$BASE" >/dev/null
docker rm -f "$NAME-prep" >/dev/null
if [ "$(stat -fc %T /sys/fs/cgroup)" = cgroup2fs ]; then cg=(--cgroupns=private); else cg=(--cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw); fi
docker run -d --name "$NAME" --privileged --network host "${cg[@]}" \
  --tmpfs /run --tmpfs /run/lock "$BASE" /lib/systemd/systemd >/dev/null
for _ in $(seq 1 60); do
  case "$(x systemctl is-system-running 2>/dev/null || true)" in running | degraded) break;; esac
  sleep 2
done
x mkdir -p /opt/hostashi
docker cp "$REPO/packaging/." "$NAME:/opt/hostashi/"
docker cp "$BINARY" "$NAME:/opt/hostashi/hostashi"
x mkdir -p /etc/systemd/system/hostashi.service.d
xi sh -c 'cat >/etc/systemd/system/hostashi.service.d/pebble.conf' <<'UNIT'
[Service]
Environment=HOSTASHI_ACME_DIRECTORY=
UNIT

step "install"
x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --no-firewall --panel-domain panel.example.com >/dev/null ||
  { x tail -n 40 /var/log/hostashi-install.log >&2; die "install"; }

step "admin and login"
tok=$(x /usr/bin/hostashi setup-token -db /var/lib/hostashi/hostashi.db)
pw="test-$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"
cookie=""
api() {
  x curl -sS -o /tmp/api.out -w '%{http_code}' -X "$1" -H 'Content-Type: application/json' \
    ${cookie:+-H "Cookie: $cookie"} ${3:+--data "$3"} "http://127.0.0.1:8080$2"
}
[ "$(api POST /api/v1/setup "{\"token\":\"$tok\",\"username\":\"kim\",\"password\":\"$pw\"}")" = 201 ] || die "setup"
cookie=$(x curl -sS -o /dev/null -D - -H 'Content-Type: application/json' \
  --data "{\"username\":\"kim\",\"password\":\"$pw\"}" http://127.0.0.1:8080/api/v1/auth/login |
  tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \(hostashi_session=[^;]*\).*/\1/p')
[ -n "$cookie" ] || die "login"
# An admin needs a second factor first (ADR-0035); the code comes from the host.
totp() {
  python3 -c 'import base64,hashlib,hmac,struct,sys,time
s = sys.argv[1]
k = base64.b32decode(s + "=" * (-len(s) % 8))
h = hmac.new(k, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
o = h[-1] & 15
print("%06d" % ((struct.unpack(">I", h[o:o + 4])[0] & 0x7fffffff) % 1000000))' "$1"
}
[ "$(api POST /api/v1/auth/totp/enroll)" = 200 ] || die "TOTP enroll"
secret=$(x sed -n 's/.*"secret":"\([A-Z2-7]*\)".*/\1/p' /tmp/api.out)
[ "$(api POST /api/v1/auth/totp/confirm "{\"code\":\"$(totp "$secret")\"}")" = 200 ] || die "TOTP confirm"
ver=$( [ "$(api GET /api/v1/php/versions)" = 200 ] && x sed -n 's/.*"installed":\["\([0-9.]*\)".*/\1/p' /tmp/api.out)
[ -n "$ver" ] || die "no PHP"

step "second volume: ext4 on a loop device, mounted at /srv/vol1"
truncate -s 512M "$IMGFILE"
LOOP=$(losetup -f --show "$IMGFILE")
x mkfs.ext4 -q "$LOOP"
# Quota on a fresh, unmounted volume: features can be set offline, no reboot.
if x tune2fs -O project,quota -Q prjquota "$LOOP" >/dev/null 2>&1; then res quota.offline-enable "tune2fs set project,quota on the unmounted volume"; else res quota.offline-enable "tune2fs failed"; fi
x mkdir -p /srv/vol1
if x mount -o nosuid,nodev,prjquota "$LOOP" /srv/vol1 2>/dev/null; then
  res quota.mount "mounted with prjquota"
else
  res quota.mount "kernel refuses prjquota (no CONFIG_QUOTA here), mounted without"
  x tune2fs -O ^quota,^project "$LOOP" >/dev/null 2>&1 || true
  x mount -o nosuid,nodev "$LOOP" /srv/vol1 || die "mount"
fi
x chmod 0711 /srv/vol1
id=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')
xi sh -c 'umask 077; cat >/srv/vol1/.hostashi-pool; chmod 0644 /srv/vol1/.hostashi-pool' <<<"$id"
x systemctl stop hostashi-agent.service
# The registry file is the only way to add a pool today (no op, no CLI).
reg=$(x cat /var/lib/hostashi-agent/sites.json)
echo "registry before: $reg"
new=$(sed "s#\"pools\": *\[#\"pools\":[{\"name\":\"vol1\",\"path\":\"/srv/vol1\",\"id\":\"$id\",\"requireMount\":true},#" <<<"$reg")
xi sh -c 'cat >/var/lib/hostashi-agent/sites.json.new && chmod 600 /var/lib/hostashi-agent/sites.json.new && mv /var/lib/hostashi-agent/sites.json.new /var/lib/hostashi-agent/sites.json' <<<"$new"
x systemctl start hostashi-agent.service
sleep 2
x systemctl is-active --quiet hostashi-agent.service || { x journalctl -u hostashi-agent -n 20 --no-pager; die "agent with second pool"; }
res registry.manual "agent starts with a hand-edited second pool: $(x cat /var/lib/hostashi-agent/sites.json | tr -d '\n' | head -c 300)"

step "a PHP site on vol1 and one on default"
code=$(api POST /api/v1/sites "{\"name\":\"big\",\"pool\":\"vol1\",\"php\":\"$ver\",\"domains\":[\"big.example.com\"]}")
res site.create.vol1 "HTTP $code $(x cat /tmp/api.out | head -c 200)"
[ "$code" = 201 ] || die "create on vol1"
[ "$(api POST /api/v1/sites "{\"name\":\"small\",\"php\":\"$ver\",\"domains\":[\"small.example.com\"]}")" = 201 ] || die "create small"
res site.home "$(x stat -c '%n %a %U:%G' /srv/vol1/big) on $(x findmnt -no SOURCE -T /srv/vol1/big)"
res site.passwd "$(x getent passwd big | cut -d: -f6)"
xi runuser -u big -- sh -c 'cat >/srv/vol1/big/htdocs/index.php' <<<'<?php echo "big:", __DIR__;'
res site.http "$(x curl -sS -H 'Host: big.example.com' http://127.0.0.1/ || echo FAIL)"
res pool.mode "$(x stat -c '%a %U:%G' /srv/vol1)"

step "isolation: what PHP masters see of the other pool"
bigpid=$(x systemctl show -p MainPID --value "hostashi-php-$ver@big.service")
smallpid=$(x systemctl show -p MainPID --value "hostashi-php-$ver@small.service")
res iso.big.sees.home "$(x ls -A "/proc/$bigpid/root/home" | tr '\n' ' ')"
res iso.big.sees.vol1 "$(x ls -A "/proc/$bigpid/root/srv/vol1" 2>&1 | tr '\n' ' ')"
res iso.small.sees.vol1 "$(x ls -A "/proc/$smallpid/root/srv/vol1" 2>&1 | tr '\n' ' ')"
res iso.small.reads.big "$(x sh -c "ls /proc/$smallpid/root/srv/vol1/big/htdocs" 2>&1 | tr '\n' ' ')"
res iso.small.user.lists.vol1 "$(x runuser -u small -- ls /srv/vol1 2>&1 | tr '\n' ' ')"
res iso.small.user.lists.big "$(x runuser -u small -- ls /srv/vol1/big 2>&1 | tr '\n' ' ')"
res unit.requiresmounts "$(x systemctl show -p RequiresMountsFor --value "hostashi-php-$ver@big.service")"
res unit.after "$(x systemctl show -p After --value "hostashi-php-$ver@big.service" | tr ' ' '\n' | grep -E 'srv|vol1|mount' | tr '\n' ' ')"
res unit.dropin "$(x sh -c "cat /etc/systemd/system/hostashi-php-$ver@big.service.d/*.conf" | tr '\n' ' ')"

step "cron, file manager and page cache on vol1"
[ "$(api POST /api/v1/sites/big/cron '{"schedule":"*/5 * * * *","command":"pwd > cron.out"}')" = 201 ] && res cron.create ok || res cron.create "$(x cat /tmp/api.out)"
cunit=$(x sh -c 'systemctl list-unit-files "hostashi-cron-*" --no-legend | awk "{print \$1}" | grep -m1 timer' || true)
[ -n "$cunit" ] && { x systemctl start "${cunit%.timer}.service" || true; sleep 2; res cron.run "$(x cat /srv/vol1/big/cron.out 2>&1)"; }
code=$(api PUT /api/v1/sites/big/page-cache '{"enabled":true,"preset":"php","minutes":10}')
res pagecache.on "HTTP $code, dir: $(x stat -c '%n %a %U:%G' /srv/vol1/.hostashi-cache 2>&1)"
code=$(api GET "/api/v1/sites/big/files?path=htdocs")
res files.list "HTTP $code $(x head -c 150 /tmp/api.out)"
code=$(api GET /api/v1/sites/big/limits)
res quota.api "HTTP $code $(x head -c 200 /tmp/api.out)"

step "doctor with two pools"
res doctor "$(x /usr/bin/hostashi doctor 2>&1 | grep -E 'pool|quota|disk|vol1' | tr '\n' '|')"

step "the volume goes away (lazy unmount, like a detached volume)"
x umount -l /srv/vol1
res unmounted.dir "$(x ls -A /srv/vol1 | tr '\n' ' ')"
code=$(api POST /api/v1/sites "{\"name\":\"big2\",\"pool\":\"vol1\",\"domains\":[\"big2.example.com\"]}")
res unmounted.create "HTTP $code $(x head -c 200 /tmp/api.out)"
res unmounted.create.leftover "$(x ls -A /srv/vol1 | tr '\n' ' ') user:$(x getent passwd big2 || echo none)"
x systemctl restart "hostashi-php-$ver@big.service" 2>/dev/null && res unmounted.php.restart "started (!)" || res unmounted.php.restart "refused: $(x systemctl status "hostashi-php-$ver@big.service" --no-pager 2>&1 | grep -m1 -iE 'error|fail|bind' || true)"
res unmounted.http "$(x curl -sS -o /dev/null -w '%{http_code}' -H 'Host: big.example.com' http://127.0.0.1/)"
[ -n "$cunit" ] && { x systemctl start "${cunit%.timer}.service" 2>/dev/null && res unmounted.cron "ran (!) $(x ls -A /srv/vol1)" || res unmounted.cron "refused"; }
code=$(api POST /api/v1/sites/big/page-cache/purge)
res unmounted.pagecache.purge "HTTP $code, /srv/vol1 now: $(x ls -A /srv/vol1 | tr '\n' ' ')"
code=$(api PUT /api/v1/sites/big/page-cache '{"enabled":false,"preset":"php","minutes":10}')
code2=$(api PUT /api/v1/sites/big/page-cache '{"enabled":true,"preset":"php","minutes":10}')
res unmounted.pagecache.toggle "HTTP $code/$code2, /srv/vol1 now: $(x ls -A /srv/vol1 | tr '\n' ' ')"
code=$(api PUT "/api/v1/sites/big/files/content?path=htdocs/x.txt" 'hello')
res unmounted.files.write "HTTP $code $(x head -c 150 /tmp/api.out), /srv/vol1 now: $(x ls -A /srv/vol1 | tr '\n' ' ')"
code=$(api GET /api/v1/sites/big/limits)
res unmounted.quota "HTTP $code $(x head -c 150 /tmp/api.out)"
code=$(api DELETE /api/v1/sites/big)
res unmounted.delete "HTTP $code $(x head -c 200 /tmp/api.out) user:$(x getent passwd big | cut -d: -f1)"
res unmounted.doctor "$(x /usr/bin/hostashi doctor 2>&1 | grep -E 'pool|quota|disk|vol1' | tr '\n' '|')"
code=$(api PUT /api/v1/sites/small/domains '{"domains":["small.example.com","www.small.example.com"]}')
res unmounted.other.site.change "HTTP $code $(x head -c 150 /tmp/api.out)"
res unmounted.agent.restart "$(x systemctl restart hostashi-agent.service && sleep 2 && x systemctl is-active hostashi-agent.service)"

step "the volume is back"
x mount -o nosuid,nodev "$LOOP" /srv/vol1
x systemctl restart "hostashi-php-$ver@big.service" && sleep 1
res remounted.http "$(x curl -sS -H 'Host: big.example.com' http://127.0.0.1/ || echo FAIL)"
res remounted.leftover.under.mountpoint "$(x sh -c 'mkdir -p /tmp/rootview && mount --bind / /tmp/rootview && ls -A /tmp/rootview/srv/vol1; umount /tmp/rootview' | tr '\n' ' ')"
code=$(api DELETE /api/v1/sites/big)
res remounted.delete "HTTP $code, home: $(x ls -A /srv/vol1 | tr '\n' ' ')"

step "path tricks: a symlinked pool and a wrong marker"
x systemctl stop hostashi-agent.service
x ln -s /srv/vol1 /srv/link
reg=$(x cat /var/lib/hostashi-agent/sites.json)
new=$(sed "s#\"pools\": *\[#\"pools\":[{\"name\":\"link\",\"path\":\"/srv/link\",\"id\":\"$id\",\"requireMount\":true},{\"name\":\"nomark\",\"path\":\"/srv/vol1\",\"id\":\"wrong\",\"requireMount\":true},{\"name\":\"plain\",\"path\":\"/srv/plain\",\"id\":\"$id\",\"requireMount\":true},#" <<<"$reg")
x install -d -m 0711 /srv/plain
xi sh -c "cat >/srv/plain/.hostashi-pool" <<<"$id"
xi sh -c 'cat >/var/lib/hostashi-agent/sites.json.new && chmod 600 /var/lib/hostashi-agent/sites.json.new && mv /var/lib/hostashi-agent/sites.json.new /var/lib/hostashi-agent/sites.json' <<<"$new"
x systemctl start hostashi-agent.service; sleep 2
res registry.samepath "agent with two pools on one path: $(x systemctl is-active hostashi-agent.service)"
for p in link nomark plain; do
  code=$(api POST /api/v1/sites "{\"name\":\"t$p\",\"pool\":\"$p\",\"domains\":[\"t$p.example.com\"]}")
  res trick.$p "HTTP $code $(x head -c 200 /tmp/api.out)"
done
echo "done"
