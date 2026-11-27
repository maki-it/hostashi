#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Runs Hostashi on a server without IPv4. The installer runs in a systemd
# container on Docker's default network, which still has IPv4, because the
# package mirrors must be reachable from the CI runner (it has no IPv6 to
# the internet). Then the container leaves that network for one with IPv6
# only: no IPv4 address but loopback, no IPv4 route. A second container on
# that network is the client. From it the test checks that the panel and a
# site answer over IPv6, that the firewall lets the web, the panel and SSH
# in and nothing else, that a failed panel login is logged with the
# client's IPv6 address and fail2ban bans and the panel lifts the ban, that
# a site's IP rules, the real-IP setting and the API allowlist take IPv6
# prefixes, and that an external MariaDB server is reached by its IPv6
# address. A second apply with --no-apt must change nothing.
#
#   ipv6-test.sh IMAGE BINARY      e.g. debian:trixie ./hostashi
#
# Needs a Docker daemon that can create IPv6 networks (Docker 28 or later
# for --ipv4=false; older ones get the IPv4 address removed by hand).
# HOSTASHI_TEST_CA (optional): as in container-test.sh. KEEP=1 leaves the
# containers and the network.
set -euo pipefail

IMG=$1
BINARY=$(realpath "$2")
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
NAME=hostashi-v6-test-$$
BASE=hostashi-v6-base-$$
NET=hostashi-v6only-$$
SUBNET=fd42:7461:7368:$((($$ % 65000) + 1))::/64

cleanup() {
  [ -n "${KEEP:-}" ] && return
  docker rm -f "$NAME" "$NAME-prep" "$NAME-client" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  docker rmi -f "$BASE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

step() { echo "=== $*"; }
fail() {
  echo "FAIL: $*" >&2
  docker exec "$NAME" sh -c 'ip addr; ip -6 route; nft list ruleset; journalctl --no-pager -u hostashi-agent -u hostashi -n 40' >&2 || true
  exit 1
}
x() { docker exec "$NAME" "$@"; }
xi() { docker exec -i "$NAME" "$@"; }
c() { docker exec "$NAME-client" "$@"; }
# drop4 CONTAINER removes every IPv4 address but loopback and every IPv4 route.
drop4() {
  # shellcheck disable=SC2016 # expands in the container's shell
  docker exec "$1" sh -c 'for d in $(ls /sys/class/net); do [ "$d" = lo ] || ip -4 addr flush dev "$d"; done; ip -4 route flush table main'
}
# Pipes into grep use grep >/dev/null, not grep -q (see firewall-test.sh).

step "prepare $IMG with systemd"
docker run -d --name "$NAME-prep" "$IMG" sleep infinity >/dev/null
if [ -n "${HOSTASHI_TEST_CA:-}" ]; then
  docker exec "$NAME-prep" mkdir -p /etc/ssl/certs
  docker cp "$HOSTASHI_TEST_CA" "$NAME-prep:/etc/ssl/certs/ca-certificates.crt"
  docker exec -e HTTPS_PROXY="${HTTPS_PROXY:-}" "$NAME-prep" bash -c '
    printf "Acquire::https::Proxy \"%s\";\nAcquire::https::CAInfo \"/etc/ssl/certs/ca-certificates.crt\";\n" "$HTTPS_PROXY" >/etc/apt/apt.conf.d/99test-proxy
    sed -i "s#http://#https://#g" /etc/apt/sources.list.d/*.sources'
fi
docker exec "$NAME-prep" bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus adduser curl ca-certificates e2fsprogs iproute2 nftables openssh-server python3 >/dev/null'
docker commit "$NAME-prep" "$BASE" >/dev/null
docker rm -f "$NAME-prep" >/dev/null

step "an IPv6-only network ($SUBNET)"
if docker network create --ipv4=false --ipv6 --subnet "$SUBNET" "$NET" >/dev/null 2>&1; then
  V4OFF=docker
else
  # Docker before 28 always gives a network IPv4; it is removed below.
  docker network create --ipv6 --subnet "$SUBNET" "$NET" >/dev/null
  V4OFF=manual
fi
echo "IPv4 switched off by: $V4OFF"

if [ "$(stat -fc %T /sys/fs/cgroup)" = cgroup2fs ]; then
  cg=(--cgroupns=private)
else
  cg=(--cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw)
fi
# The default bridge network for the install: a network namespace of its
# own (the firewall step runs for real), and IPv4 to the mirrors.
docker run -d --name "$NAME" --privileged "${cg[@]}" --tmpfs /run --tmpfs /run/lock "$BASE" /lib/systemd/systemd >/dev/null
state=""
for _ in $(seq 1 60); do
  state=$(x systemctl is-system-running 2>/dev/null || true)
  case "$state" in running | degraded) break;; esac
  sleep 2
done
echo "systemd: $state"
x mount --make-rshared /
x mkdir -p /opt/hostashi
docker cp "$HERE/." "$NAME:/opt/hostashi/"
docker cp "$BINARY" "$NAME:/opt/hostashi/hostashi"
# No ACME: the panel name would go to the real Let's Encrypt.
x mkdir -p /etc/systemd/system/hostashi.service.d
xi sh -c 'cat >/etc/systemd/system/hostashi.service.d/no-acme.conf' <<'UNIT'
[Service]
Environment=HOSTASHI_ACME_DIRECTORY=
UNIT

step "install (with the firewall)"
x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --panel-domain panel.example.com >/dev/null || {
  x tail -n 60 /var/log/hostashi-install.log >&2 || true
  fail "apply failed"
}

step "move the server to the IPv6-only network"
docker network connect "$NET" "$NAME"
docker network disconnect bridge "$NAME"
if [ "$V4OFF" = manual ]; then
  drop4 "$NAME"
fi
S6=$(docker inspect -f "{{(index .NetworkSettings.Networks \"$NET\").GlobalIPv6Address}}" "$NAME")
[ -n "$S6" ] || fail "server has no IPv6 address"
[ -z "$(x ip -4 -o addr show scope global)" ] || fail "server still has IPv4: $(x ip -4 -o addr show scope global)"
[ -z "$(x ip -4 route show)" ] || fail "server still has an IPv4 route: $(x ip -4 route show)"
x ip -6 route show default | grep . >/dev/null || fail "no IPv6 default route"
echo "server: $S6"

docker run -d --name "$NAME-client" --network "$NET" "$BASE" sleep infinity >/dev/null
if [ "$V4OFF" = manual ]; then
  # Without NET_ADMIN the client keeps its IPv4 address; the server has
  # none, so the client cannot use it either.
  drop4 "$NAME-client" 2>/dev/null || true
fi
C6=$(docker inspect -f "{{(index .NetworkSettings.Networks \"$NET\").GlobalIPv6Address}}" "$NAME-client")
[ -n "$C6" ] || fail "client has no IPv6 address"
C64=$(python3 -c 'import ipaddress,sys; print(ipaddress.ip_network(sys.argv[1] + "/64", strict=False))' "$C6")
echo "client: $C6 ($C64)"

step "the services come back on IPv6 only"
x systemctl restart hostashi-agent.service hostashi.service
for _ in $(seq 60); do x curl -fsS -o /dev/null http://127.0.0.1:8080/api/v1/health 2>/dev/null && break; sleep 1; done
x curl -fsS http://127.0.0.1:8080/api/v1/health | grep '"agent":"ok"' >/dev/null || fail "panel cannot reach the agent"
out=$(x /opt/hostashi/install.sh apply --yes --no-apt --binary /opt/hostashi/hostashi --panel-domain panel.example.com 2>&1) || {
  echo "$out" >&2; fail "apply --no-apt failed without IPv4"
}
grep 'changes=0' <<<"$out" >/dev/null || fail "apply --no-apt without IPv4 changed something: $out"
listen=$(x ss -ltnH)
for p in 80 443 8443 22; do
  grep -E "(\[::\]|\*):$p " <<<"$listen" >/dev/null || fail "nothing listens on [::]:$p: $listen"
done
for u in nginx hostashi-agent hostashi mariadb fail2ban hostashi-firewall; do
  [ "$(x systemctl is-active "$u.service")" = active ] || fail "$u is not running"
done

step "the panel answers the client over IPv6"
# panel URL CURL-ARGS...: a request from the client to the panel on 8443.
panel() {
  local path=$1; shift
  c curl -sSk -m 10 --connect-to "panel.example.com:8443:[$S6]:8443" "$@" "https://panel.example.com:8443$path"
}
for _ in $(seq 30); do panel /api/v1/health -f -o /dev/null 2>/dev/null && break; sleep 1; done
panel /api/v1/health -f | grep '"agent":"ok"' >/dev/null || fail "panel not reachable over IPv6"

step "setup, second factor and an API token"
tok=$(x /usr/local/bin/hostashi setup-token -db /var/lib/hostashi/hostashi.db)
pw="test-$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"
api() {
  x curl -sS -o /tmp/api.out -w '%{http_code}' -X "$1" -H 'Content-Type: application/json' \
    ${cookie:+-H "Cookie: $cookie"} ${3:+--data "$3"} "http://127.0.0.1:8080$2"
}
cookie=""
[ "$(api POST /api/v1/setup "{\"token\":\"$tok\",\"username\":\"kim\",\"password\":\"$pw\"}")" = 201 ] || fail "setup: $(x cat /tmp/api.out)"
cookie=$(x curl -sS -o /dev/null -D - -H 'Content-Type: application/json' \
  --data "{\"username\":\"kim\",\"password\":\"$pw\"}" http://127.0.0.1:8080/api/v1/auth/login |
  tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \(hostashi_session=[^;]*\).*/\1/p')
[ -n "$cookie" ] || fail "login gave no session cookie"
totp() {
  python3 -c 'import base64,hashlib,hmac,struct,sys,time
s = sys.argv[1]
k = base64.b32decode(s + "=" * (-len(s) % 8))
h = hmac.new(k, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
o = h[-1] & 15
print("%06d" % ((struct.unpack(">I", h[o:o + 4])[0] & 0x7fffffff) % 1000000))' "$1"
}
[ "$(api POST /api/v1/auth/totp/enroll)" = 200 ] || fail "TOTP enroll: $(x cat /tmp/api.out)"
secret=$(x cat /tmp/api.out | sed -n 's/.*"secret":"\([A-Z2-7]*\)".*/\1/p')
[ "$(api POST /api/v1/auth/totp/confirm "{\"code\":\"$(totp "$secret")\"}")" = 200 ] || fail "TOTP confirm: $(x cat /tmp/api.out)"
[ "$(api GET /api/v1/scopes)" = 200 ] || fail "scopes: $(x cat /tmp/api.out)"
scopes=$(x cat /tmp/api.out | python3 -c 'import json,sys; print(json.dumps([s["name"] for s in json.load(sys.stdin)["scopes"]]))')
[ "$(api POST /api/v1/tokens "{\"name\":\"ipv6 test\",\"scopes\":$scopes,\"expiresInDays\":1}")" = 201 ] || fail "create token: $(x cat /tmp/api.out)"
token=$(x cat /tmp/api.out | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
code=$(panel /api/v1/sites -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token")
[ "$code" = 200 ] || fail "token over IPv6 answered $code"
xi /usr/local/bin/hostashi login -token http://127.0.0.1:8080 <<<"$token" >/dev/null || fail "hostashi login -token"

step "the API allowlist takes IPv6 prefixes"
[ "$(api PUT /api/v1/settings/api-ip-allowlist "{\"entries\":[\"$C64\"]}")" = 200 ] || fail "set allowlist: $(x cat /tmp/api.out)"
code=$(panel /api/v1/sites -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token")
[ "$code" = 200 ] || fail "token from an allowed /64 answered $code"
[ "$(api PUT /api/v1/settings/api-ip-allowlist '{"entries":["2001:db8:dead::/48"]}')" = 200 ] || fail "set allowlist: $(x cat /tmp/api.out)"
code=$(panel /api/v1/sites -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token")
[ "$code" = 403 ] || fail "token from outside the allowlist answered $code"
[ "$(api PUT /api/v1/settings/api-ip-allowlist '{"entries":[]}')" = 200 ] || fail "clear allowlist: $(x cat /tmp/api.out)"

step "a site answers over IPv6 and logs the client's address"
[ "$(api POST /api/v1/sites '{"name":"blog","domains":["blog.example.com"]}')" = 201 ] || fail "create site: $(x cat /tmp/api.out)"
x sh -c 'echo "hello over IPv6" >/home/blog/htdocs/index.html'
site() { c curl -sS -m 10 --connect-to "blog.example.com:80:[$S6]:80" "$@" http://blog.example.com/; }
body=$(site -f)
[ "$body" = "hello over IPv6" ] || fail "site answered '$body'"
sleep 1
x grep -F "$C6" /var/log/nginx/sites/blog/access.log >/dev/null || fail "client's address not in the access log: $(x tail -n 3 /var/log/nginx/sites/blog/access.log)"

step "a site's IP rules take IPv6 addresses and prefixes (ADR-0066)"
code_after() { # BODY: set the access rules, then the client's status code
  [ "$(api PUT /api/v1/sites/blog/access "$1")" = 200 ] || fail "set access $1: $(x cat /tmp/api.out)"
  sleep 1
  site -o /dev/null -w '%{http_code}' || true
}
[ "$(code_after "{\"ipMode\":\"allow\",\"ips\":[\"$C6\"]}")" = 200 ] || fail "allowed address refused"
[ "$(code_after "{\"ipMode\":\"allow\",\"ips\":[\"2001:db8::/32\"]}")" = 403 ] || fail "address outside the allow rule let in"
[ "$(code_after "{\"ipMode\":\"deny\",\"ips\":[\"$C64\"]}")" = 403 ] || fail "denied /64 let in"
[ "$(code_after '{}')" = 200 ] || fail "site closed after the rules were removed"

step "real IP behind a proxy with an IPv6 address (ADR-0059)"
[ "$(api PUT /api/v1/sites/blog/real-ip "{\"header\":\"X-Forwarded-For\",\"ranges\":[\"$C64\"]}")" = 200 ] || fail "set real IP: $(x cat /tmp/api.out)"
sleep 1
site -f -o /dev/null -H 'X-Forwarded-For: 2001:db8::77' || fail "site behind the proxy"
sleep 1
x tail -n 1 /var/log/nginx/sites/blog/access.log | grep -F '2001:db8::77' >/dev/null || fail "real IP not taken from the header: $(x tail -n 1 /var/log/nginx/sites/blog/access.log)"
[ "$(api PUT /api/v1/sites/blog/real-ip '{}')" = 200 ] || fail "clear real IP: $(x cat /tmp/api.out)"

step "the firewall lets the web, the panel and SSH in over IPv6, nothing else"
x nft list table inet hostashi >/dev/null || fail "firewall table not loaded"
port_open() { c timeout 5 bash -c "exec 3<>/dev/tcp/$S6/$1" 2>/dev/null; }
for p in 22 80 443 8443; do port_open "$p" || fail "port $p closed over IPv6"; done
x sh -c 'nohup python3 -m http.server --bind :: 9999 >/dev/null 2>&1 &'
for _ in $(seq 20); do x ss -ltnH | grep ':9999 ' >/dev/null && break; sleep 0.5; done
x curl -fsS -o /dev/null -g "http://[::1]:9999/" || fail "test listener not up"
if port_open 9999; then fail "a port the firewall should drop answered over IPv6"; fi
x pkill -f 'http.server --bind :: 9999' || true

step "a failed panel login is logged with the IPv6 address, fail2ban bans it, the panel lifts the ban"
x fail2ban-client set hostashi-panel bantime 120 >/dev/null
for _ in $(seq 12); do
  panel /api/v1/auth/login -o /dev/null -H 'Content-Type: application/json' \
    --data '{"username":"kim","password":"not-the-password-1"}' 2>/dev/null || true
done
x sh -c 'journalctl -u hostashi.service -o cat --no-pager | grep auth.failed' | grep -F "\"ip\":\"$C6\"" >/dev/null ||
  fail "auth.failed without the client's IPv6 address: $(x sh -c 'journalctl -u hostashi.service -o cat --no-pager | grep auth.failed | tail -n 2')"
for _ in $(seq 30); do x fail2ban-client status hostashi-panel | grep -F "$C6" >/dev/null && break; sleep 1; done
x fail2ban-client status hostashi-panel | grep -F "$C6" >/dev/null || fail "fail2ban did not ban $C6: $(x fail2ban-client status hostashi-panel)"
x nft list ruleset | grep -F "$C6" >/dev/null || fail "ban not in nftables"
if port_open 8443; then fail "banned client still reaches the panel"; fi
[ "$(api GET /api/v1/firewall/bans)" = 200 ] || fail "bans through the panel: $(x cat /tmp/api.out)"
x grep -F "\"$C6\"" /tmp/api.out >/dev/null || fail "panel does not show the IPv6 ban: $(x cat /tmp/api.out)"
[ "$(api DELETE "/api/v1/firewall/bans/$C6")" = 200 ] || fail "unblock through the panel: $(x cat /tmp/api.out)"
port_open 8443 || fail "client still blocked after the unblock"
x fail2ban-client set hostashi-panel bantime 3600 >/dev/null

step "the firewall's panel allowlist takes an IPv6 prefix (ADR-0034)"
[ "$(api PUT /api/v1/firewall "{\"state\":{\"panelAllow\":[\"2001:db8:dead::/48\"],\"sshAllow\":[],\"open\":[]}}")" = 200 ] ||
  fail "set panel allowlist: $(x cat /tmp/api.out)"
sleep 2
if port_open 8443; then fail "panel reachable from outside its IPv6 allowlist"; fi
port_open 80 || fail "the web closed by the panel allowlist"
[ "$(api PUT /api/v1/firewall "{\"state\":{\"panelAllow\":[\"$C64\"],\"sshAllow\":[],\"open\":[]}}")" = 200 ] ||
  fail "set panel allowlist: $(x cat /tmp/api.out)"
sleep 2
port_open 8443 || fail "panel closed for an allowed /64"
[ "$(api PUT /api/v1/firewall '{"state":{"panelAllow":[],"sshAllow":[],"open":[]}}')" = 200 ] || fail "clear firewall rules: $(x cat /tmp/api.out)"

step "an external MariaDB server by its IPv6 address (ADR-0041)"
# The local MariaDB stands in for the external server, as in
# container-test.sh, but on the server's IPv6 address.
printf '[mariadbd]\nbind-address = 127.0.0.1,%s\n' "$S6" | xi sh -c 'cat >/etc/mysql/mariadb.conf.d/95-test-external.cnf'
x systemctl restart mariadb.service
x mariadb -e "CREATE USER 'extadmin'@'$S6' IDENTIFIED BY 'Ext-admin-pw-1234'; GRANT ALL ON *.* TO 'extadmin'@'$S6' WITH GRANT OPTION"
[ "$(api POST /api/v1/db-servers "{\"name\":\"ext\",\"host\":\"$S6\",\"adminUser\":\"extadmin\",\"adminPassword\":\"Ext-admin-pw-1234\",\"tlsMode\":\"none\"}")" = 201 ] ||
  fail "add external server by IPv6 address: $(x cat /tmp/api.out)"
[ "$(api GET /api/v1/php/versions)" = 200 ] || fail "php versions: $(x cat /tmp/api.out)"
ver=$(x cat /tmp/api.out | sed -n 's/.*"installed":\["\([0-9.]*\)".*/\1/p')
[ "$(api POST /api/v1/sites "{\"name\":\"wp\",\"php\":\"$ver\",\"domains\":[\"wp.example.com\"]}")" = 201 ] || fail "create PHP site: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/sites/wp/databases '{"name":"wp_ext","server":"ext"}')" = 201 ] || fail "create external database: $(x cat /tmp/api.out)"
x mariadb -e 'CREATE TABLE wp_ext.t (i INT); INSERT INTO wp_ext.t VALUES (11)'
[ "$(api POST /api/v1/databases/wp_ext/export '{"path":"ext.sql.gz"}')" = 202 ] || fail "external export: $(x cat /tmp/api.out)"
id=$(x cat /tmp/api.out | sed -n 's/.*"id":"\([0-9a-f]*\)".*/\1/p')
state=""
for _ in $(seq 60); do
  [ "$(api GET "/api/v1/databases/wp_ext/jobs/$id")" = 200 ] || fail "job status: $(x cat /tmp/api.out)"
  state=$(x cat /tmp/api.out | sed -n 's/.*"state":"\([a-z]*\)".*/\1/p')
  [ "$state" = running ] || break
  sleep 1
done
[ "$state" = "done" ] || fail "external export ended as $state: $(x cat /tmp/api.out)"
x sh -c 'zcat /home/wp/ext.sql.gz | grep "(11)" >/dev/null' || fail "external export lacks the row"
x sh -c "grep -h '^IPAddressAllow=' /etc/systemd/system/hostashi-dbjob@*.service.d/job.conf | grep -x 'IPAddressAllow=$S6' >/dev/null" ||
  fail "job drop-in: $(x sh -c 'cat /etc/systemd/system/hostashi-dbjob@*.service.d/job.conf')"

step "hostashi doctor without IPv4"
rc=0
doc=$(x /usr/local/bin/hostashi doctor) || rc=$?
echo "$doc"
for id in system.os service.panel service.nginx service.mariadb service.fail2ban system.firewall; do
  grep -Eq "^  ok    $id " <<<"$doc" || fail "doctor: $id is not ok"
done
[ "$rc" = 0 ] || fail "doctor exited $rc"

echo "PASS: Hostashi works on an IPv6-only network ($IMG)"
