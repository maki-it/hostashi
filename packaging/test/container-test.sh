#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Runs packaging/install.sh in a container with systemd as PID 1 and checks
# the result: preflight, first apply, a second apply that changes nothing,
# both units running, the panel's health check reaching the agent, file
# owners and modes, and the systemd exposure score of the panel unit.
#
#   container-test.sh IMAGE BINARY      e.g. debian:trixie ./hostashi
#
# The container shares the host kernel; quota and reboots are not covered.
# HOSTASHI_TEST_CA (optional): a CA bundle to trust inside the container,
# for build sandboxes behind a TLS-intercepting proxy. CI does not need it.
# KEEP=1 leaves the container running after the test, for debugging.
# PEBBLE_DIR (optional): a directory with the pebble and pebble-challtestsrv
# binaries (v2.10.3). With it, a site gets a certificate from Pebble, a
# local test CA; the test never talks to the real Let's Encrypt.
set -euo pipefail

IMG=$1
BINARY=$(realpath "$2")
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
NAME=hostashi-install-test-$$
BASE=hostashi-test-base-$$

cleanup() { [ -n "${KEEP:-}" ] && return;  docker rm -f "$NAME" "$NAME-prep" >/dev/null 2>&1 || true; docker rmi -f "$BASE" >/dev/null 2>&1 || true; }
trap cleanup EXIT

step() { echo "=== $*"; }
fail() { echo "FAIL: $*" >&2; docker exec "$NAME" journalctl --no-pager -u hostashi-agent -u hostashi -n 50 >&2 || true; exit 1; }

step "prepare $IMG with systemd"
docker run -d --name "$NAME-prep" --network host "$IMG" sleep infinity >/dev/null
if [ -n "${HOSTASHI_TEST_CA:-}" ]; then
  docker exec "$NAME-prep" mkdir -p /etc/ssl/certs
  docker cp "$HOSTASHI_TEST_CA" "$NAME-prep:/etc/ssl/certs/ca-certificates.crt"
  docker exec -e HTTPS_PROXY="${HTTPS_PROXY:-}" "$NAME-prep" bash -c '
    printf "Acquire::https::Proxy \"%s\";\nAcquire::https::CAInfo \"/etc/ssl/certs/ca-certificates.crt\";\n" "$HTTPS_PROXY" >/etc/apt/apt.conf.d/99test-proxy
    sed -i "s#http://#https://#g" /etc/apt/sources.list.d/*.sources'
fi
docker exec "$NAME-prep" bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv adduser curl >/dev/null'
docker commit "$NAME-prep" "$BASE" >/dev/null
docker rm -f "$NAME-prep" >/dev/null

# systemd 258 and later (Ubuntu 26.04) need cgroup v2. On a cgroup v1 host
# only older systemd (Debian 13) can boot, through the host's hierarchy.
if [ "$(stat -fc %T /sys/fs/cgroup)" = cgroup2fs ]; then
  cg=(--cgroupns=private)
else
  cg=(--cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw)
fi
docker run -d --name "$NAME" --privileged --network host "${cg[@]}" \
  --tmpfs /run --tmpfs /run/lock "$BASE" /lib/systemd/systemd >/dev/null
state=""
for _ in $(seq 1 60); do
  state=$(docker exec "$NAME" systemctl is-system-running 2>/dev/null || true)
  case "$state" in running | degraded) break;; esac
  sleep 2
done
echo "systemd: $state"

docker exec "$NAME" mkdir -p /opt/hostashi
docker cp "$HERE/." "$NAME:/opt/hostashi/"
docker cp "$BINARY" "$NAME:/opt/hostashi/hostashi"
x() { docker exec "$NAME" "$@"; }
xi() { docker exec -i "$NAME" "$@"; }

# The panel name would otherwise go to the real Let's Encrypt at the first
# start. The Pebble part below replaces this drop-in.
x mkdir -p /etc/systemd/system/hostashi.service.d
xi sh -c 'cat >/etc/systemd/system/hostashi.service.d/pebble.conf' <<'UNIT'
[Service]
Environment=HOSTASHI_ACME_DIRECTORY=
UNIT

step "apply without --yes is refused"
if x /opt/hostashi/install.sh apply --binary /opt/hostashi/hostashi; then fail "apply ran without --yes"; fi

step "plan"
x /opt/hostashi/install.sh plan --binary /opt/hostashi/hostashi

step "apply"
x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --public-url https://panel.example.com

step "second apply changes nothing"
out=$(x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --public-url https://panel.example.com)
echo "$out"
grep -q 'changes=0' <<<"$out" || fail "second run changed something"

step "services, health and permissions"
for u in hostashi-agent.service hostashi.service hostashi-panel.socket; do
  [ "$(x systemctl is-active $u)" = active ] || fail "$u is not running"
done
x grep -qx 'HOSTASHI_PUBLIC_URL=https://panel.example.com:8443' /etc/hostashi/hostashi.env || fail "public URL without the panel port"
health=$(x curl -fsS http://127.0.0.1:8080/api/v1/health)
echo "$health"
grep -q '"agent":"ok"' <<<"$health" || fail "panel cannot reach the agent"
x /usr/local/bin/hostashi ping

expect_mode() {
  local got
  got=$(x stat -c '%a %U:%G' "$1")
  [ "$got" = "$2" ] || fail "$1 is $got, want $2"
}
expect_mode /etc/hostashi "750 root:hostashi"
expect_mode /etc/hostashi/secret.key "400 hostashi:hostashi"
expect_mode /etc/hostashi/hostashi.env "640 root:hostashi"
expect_mode /var/lib/hostashi "750 hostashi:hostashi"
expect_mode /var/lib/hostashi/hostashi.db "600 hostashi:hostashi"
expect_mode /var/lib/hostashi-agent "700 root:root"
expect_mode /var/lib/hostashi-agent/sites.json "600 root:root"
expect_mode /run/hostashi "750 root:hostashi"
expect_mode /run/hostashi/agent.sock "660 root:hostashi"
expect_mode /run/hostashi-panel.sock "660 hostashi:www-data"
expect_mode /var/lib/hostashi-acme "755 root:root"
expect_mode /var/lib/hostashi-acme/.well-known/acme-challenge "2750 hostashi:www-data"

step "nginx runs on the agent's generation"
[ "$(x systemctl is-active nginx.service)" = active ] || fail "nginx is not running"
x test -L /var/lib/hostashi-agent/web/current || fail "no web generation"
code=$(x curl -s -o /dev/null -w '%{http_code}' -H 'Host: unknown.example.com' http://127.0.0.1/ || true)
[ "$code" = 000 ] || fail "unknown host answered $code, want no answer"

step "a site is served, with brotli"
x /usr/local/bin/hostashi site create blog
expect_mode /home/blog "750 blog:www-data"
expect_mode /home/blog/htdocs "755 blog:blog"
# shellcheck disable=SC2016 # expanded inside the container
x sh -c 'echo "hello from blog" >/home/blog/htdocs/index.html; for i in $(seq 1 200); do echo ".c$i { color: red; }"; done >/home/blog/htdocs/app.css; chown blog:blog /home/blog/htdocs/*'
web_apply() { docker exec -i "$NAME" /usr/local/bin/hostashi web apply <<<"$1"; }
web_apply '{"sites":[{"user":"blog","domains":["blog.example.com","www.blog.example.com"]}]}'
body=$(x curl -fsS -H 'Host: www.blog.example.com' http://127.0.0.1/)
[ "$body" = "hello from blog" ] || fail "site answered '$body'"
enc=$(x curl -fsS -o /dev/null -D - -H 'Accept-Encoding: br' -H 'Host: blog.example.com' http://127.0.0.1/app.css | tr -d '\r' | grep -i '^content-encoding:' || true)
[ "$enc" = "Content-Encoding: br" ] || fail "no brotli response ($enc)"
out=$(web_apply '{"sites":[{"user":"blog","domains":["blog.example.com","www.blog.example.com"]}]}')
grep -q 'changed: false' <<<"$out" || fail "same web state applied again: $out"
if x /usr/local/bin/hostashi site delete blog; then fail "site with a virtual host was deleted"; fi

step "systemd exposure"
x systemd-analyze security hostashi.service | tail -1
x systemd-analyze security hostashi-agent.service | tail -1
score=$(x systemd-analyze security hostashi.service | tail -1 | grep -Eo '[0-9]+\.[0-9]+' | head -1)
awk -v s="$score" 'BEGIN { exit !(s <= 2.0) }' || fail "panel unit exposure $score is above 2.0"

step "changed setting is applied and the panel restarts"
out=$(x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --public-url https://other.example.com)
echo "$out"
grep -q 'apply.*config' <<<"$out" || fail "config change not applied"
[ "$(x systemctl is-active hostashi.service)" = active ] || fail "panel did not come back after the change"
x grep -q 'HOSTASHI_PUBLIC_URL=https://other.example.com' /etc/hostashi/hostashi.env || fail "env file not updated"

body=$(x curl -fsS -H 'Host: blog.example.com' http://127.0.0.1/)
[ "$body" = "hello from blog" ] || fail "site gone after the agent restarted: '$body'"

step "remove the site"
web_apply '{"sites":[]}'
x /usr/local/bin/hostashi site delete blog
x test ! -e /home/blog || fail "site home still there"

step "a site through the panel API"
tok=$(x /usr/local/bin/hostashi setup-token -db /var/lib/hostashi/hostashi.db)
pw="test-$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"
# api METHOD PATH [BODY] prints the status code and leaves the body in /tmp/api.out (container).
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
[ "$(api POST /api/v1/sites '{"name":"shop","domains":["shop.example.com"]}')" = 201 ] || fail "create site: $(x cat /tmp/api.out)"
x sh -c 'echo "hello from shop" >/home/shop/htdocs/index.html'
body=$(x curl -fsS -H 'Host: shop.example.com' http://127.0.0.1/)
[ "$body" = "hello from shop" ] || fail "API site answered '$body'"
[ "$(api PUT /api/v1/sites/shop/domains '{"domains":["www.shop.example.com"]}')" = 200 ] || fail "set domains: $(x cat /tmp/api.out)"
body=$(x curl -fsS -H 'Host: www.shop.example.com' http://127.0.0.1/)
[ "$body" = "hello from shop" ] || fail "new domain answered '$body'"
[ "$(api DELETE /api/v1/sites/shop)" = 204 ] || fail "delete site: $(x cat /tmp/api.out)"
x test ! -e /home/shop || fail "API site home still there"

step "a PHP site through the panel API"
[ "$(api GET /api/v1/php/versions)" = 200 ] || fail "php versions: $(x cat /tmp/api.out)"
ver=$(x cat /tmp/api.out | sed -n 's/.*"installed":\["\([0-9.]*\)".*/\1/p')
[ -n "$ver" ] || fail "no PHP version installed: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/sites "{\"name\":\"wp\",\"php\":\"$ver\",\"domains\":[\"wp.example.com\"]}")" = 201 ] ||
  fail "create PHP site: $(x cat /tmp/api.out)"
x systemctl is-active --quiet "hostashi-php-$ver@wp.service" || fail "PHP unit of the site not running"
x systemctl is-enabled --quiet "hostashi-php-$ver@wp.service" || fail "PHP unit of the site not enabled"
[ "$(x stat -c '%a %U:%G' /run/hostashi-php/wp/php-fpm.sock)" = "660 wp:www-data" ] || fail "PHP socket has the wrong owner or mode"
# The worker runs as the site user and cannot read outside its home and /tmp.
xi runuser -u wp -- sh -c 'cat >/home/wp/htdocs/index.php' <<'PHP'
<?php
echo "php ", PHP_MAJOR_VERSION, ".", PHP_MINOR_VERSION, " user ", posix_getpwuid(posix_geteuid())["name"];
echo @file_get_contents("/etc/hostname") === false ? " jailed" : " open";
PHP
body=$(x curl -fsS -H 'Host: wp.example.com' http://127.0.0.1/some/pretty/url)
[ "$body" = "php $ver user wp jailed" ] || fail "PHP site answered '$body'"
# PHP off: the pool goes away and PHP source is never sent.
[ "$(api PUT /api/v1/sites/wp/php '{"php":""}')" = 200 ] || fail "PHP off: $(x cat /tmp/api.out)"
! x systemctl is-active --quiet "hostashi-php-$ver@wp.service" || fail "PHP unit still running after PHP off"
code=$(x curl -sS -o /dev/null -w '%{http_code}' -H 'Host: wp.example.com' http://127.0.0.1/index.php)
[ "$code" = 403 ] || fail "index.php without PHP answered $code"
[ "$(api PUT /api/v1/sites/wp/php "{\"php\":\"$ver\"}")" = 200 ] || fail "PHP on again: $(x cat /tmp/api.out)"
body=$(x curl -fsS -H 'Host: wp.example.com' http://127.0.0.1/)
[ "$body" = "php $ver user wp jailed" ] || fail "PHP site answered '$body' after PHP on"
# The distribution's own FPM service stays off.
! x systemctl is-active --quiet "php$ver-fpm.service" || fail "distribution php$ver-fpm is running"
[ "$(api DELETE /api/v1/sites/wp)" = 204 ] || fail "delete PHP site: $(x cat /tmp/api.out)"
! x systemctl is-active --quiet "hostashi-php-$ver@wp.service" || fail "PHP unit still running after delete"
x test ! -e /home/wp || fail "PHP site home still there"

if [ -z "${PEBBLE_DIR:-}" ]; then
  echo "PEBBLE_DIR not set, skipping the certificate test"
else
  step "a certificate from a test CA (Pebble)"
  x mkdir -p /opt/pebble
  docker cp "$PEBBLE_DIR/pebble" "$NAME:/opt/pebble/pebble"
  docker cp "$PEBBLE_DIR/pebble-challtestsrv" "$NAME:/opt/pebble/pebble-challtestsrv"
  # Pebble's own HTTPS listener: a self-signed certificate for localhost,
  # which the panel trusts through HOSTASHI_ACME_CA.
  tmp=$(mktemp -d)
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj /CN=localhost \
    -addext subjectAltName=DNS:localhost -addext basicConstraints=critical,CA:TRUE \
    -keyout "$tmp/pebble.key" -out "$tmp/pebble.pem" 2>/dev/null
  docker cp "$tmp/pebble.key" "$NAME:/opt/pebble/key.pem"
  docker cp "$tmp/pebble.pem" "$NAME:/opt/pebble/cert.pem"
  rm -rf "$tmp"
  xi sh -c 'cat >/opt/pebble/config.json' <<'JSON'
{"pebble": {"listenAddress": "127.0.0.1:14000", "managementListenAddress": "127.0.0.1:15000",
  "certificate": "/opt/pebble/cert.pem", "privateKey": "/opt/pebble/key.pem",
  "httpPort": 80, "tlsPort": 443, "externalAccountBindingRequired": false}}
JSON
  # DNS: every name resolves to 127.0.0.1, where nginx answers the challenge.
  x systemd-run --unit pebble-dns /opt/pebble/pebble-challtestsrv -defaultIPv4 127.0.0.1 -defaultIPv6 "" \
    -dnsserver 127.0.0.1:8053 -doh "" -http01 "" -https01 "" -tlsalpn01 "" -management 127.0.0.1:8055 >/dev/null
  x systemd-run --unit pebble -E PEBBLE_VA_NOSLEEP=1 -E PEBBLE_WFE_NONCEREJECT=0 \
    /opt/pebble/pebble -config /opt/pebble/config.json -dnsserver 127.0.0.1:8053 >/dev/null
  x install -m 0640 -o root -g hostashi /opt/pebble/cert.pem /etc/hostashi/pebble-ca.pem
  x mkdir -p /etc/systemd/system/hostashi.service.d
  xi sh -c 'cat >/etc/systemd/system/hostashi.service.d/pebble.conf' <<'UNIT'
[Service]
Environment=HOSTASHI_ACME_DIRECTORY=https://localhost:14000/dir HOSTASHI_ACME_CA=/etc/hostashi/pebble-ca.pem
UNIT
  for _ in $(seq 1 30); do x curl -sfk -o /dev/null https://localhost:14000/dir && break; sleep 1; done
  x systemctl daemon-reload
  x systemctl restart hostashi.service
  for _ in $(seq 1 30); do x curl -sf -o /dev/null http://127.0.0.1:8080/api/v1/health && break; sleep 1; done

  [ "$(api POST /api/v1/sites '{"name":"secure","domains":["secure.example.com","www.secure.example.com"]}')" = 201 ] ||
    fail "create site: $(x cat /tmp/api.out)"
  x sh -c 'echo "hello over https" >/home/secure/htdocs/index.html'
  [ "$(api PUT /api/v1/sites/secure/tls '{"enabled":true}')" = 202 ] || fail "TLS on: $(x cat /tmp/api.out)"
  active=""
  for _ in $(seq 1 60); do
    [ "$(api GET /api/v1/sites/secure)" = 200 ] || fail "get site: $(x cat /tmp/api.out)"
    if x grep -q '"active":true' /tmp/api.out; then active=1; break; fi
    if x grep -q '"error":' /tmp/api.out; then fail "certificate failed: $(x cat /tmp/api.out)"; fi
    sleep 2
  done
  [ -n "$active" ] || fail "TLS not active after two minutes: $(x cat /tmp/api.out)"
  body=$(x curl -fsSk --resolve www.secure.example.com:443:127.0.0.1 https://www.secure.example.com/)
  [ "$body" = "hello over https" ] || fail "HTTPS answered '$body'"
  hdr() { x curl -fsSk -o /dev/null -D - --resolve secure.example.com:443:127.0.0.1 https://secure.example.com/ | tr -d '\r' | grep -i "^$1:" || true; }
  [ -z "$(hdr strict-transport-security)" ] || fail "HSTS sent without the setting"
  [ "$(api PUT /api/v1/sites/secure/hsts '{"enabled":true}')" = 200 ] || fail "HSTS on: $(x cat /tmp/api.out)"
  [ "$(hdr strict-transport-security)" = "strict-transport-security: max-age=31536000" ] ||
    fail "HSTS header: '$(hdr strict-transport-security)'"
  if x sh -c 'nginx -V 2>&1' | grep -q -- --with-http_v3_module; then
    [ "$(hdr alt-svc)" = 'alt-svc: h3=":443"; ma=86400' ] || fail "Alt-Svc header: '$(hdr alt-svc)'"
    if x curl -V | grep -q 'Features:.* HTTP3'; then
      out=$(x curl -fsSk --http3-only -o /tmp/h3.out -w '%{http_version}' --resolve www.secure.example.com:443:127.0.0.1 https://www.secure.example.com/)
      [ "$out" = 3 ] && [ "$(x cat /tmp/h3.out)" = "hello over https" ] || fail "HTTP/3 answered '$out' '$(x cat /tmp/h3.out)'"
      # Unknown names over QUIC get no certificate either.
      if x curl -sSk --http3-only -o /dev/null --resolve other.example.com:443:127.0.0.1 https://other.example.com/ 2>/dev/null; then
        fail "QUIC handshake for an unknown name succeeded"
      fi
    else
      echo "curl without HTTP/3, skipping the QUIC request"
    fi
  else
    fail "nginx on $IMG has no HTTP/3"
  fi
  loc=$(x curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' -H 'Host: secure.example.com' http://127.0.0.1/a?b=1)
  [ "$loc" = "301 https://secure.example.com/a?b=1" ] || fail "HTTP did not redirect: '$loc'"
  # The key never leaves root; the panel cannot read it.
  expect_mode /var/lib/hostashi-agent/tls/secure/key.pem "600 root:root"
  if x runuser -u hostashi -- cat /var/lib/hostashi-agent/tls/secure/key.pem >/dev/null 2>&1; then fail "panel user can read the site key"; fi
  [ "$(api DELETE /api/v1/sites/secure)" = 204 ] || fail "delete TLS site: $(x cat /tmp/api.out)"
  x test ! -e /var/lib/hostashi-agent/tls/secure || fail "certificate of a deleted site still there"

  step "the panel over HTTPS (ADR-0023)"
  # The public URL is https://other.example.com:8443 since the settings change.
  p8443() { x curl -sSk --resolve other.example.com:8443:127.0.0.1 "$@"; }
  for _ in $(seq 1 60); do
    p8443 -f -o /dev/null https://other.example.com:8443/api/v1/health 2>/dev/null && break
    sleep 2
  done
  health=$(p8443 -f https://other.example.com:8443/api/v1/health) || fail "panel not served on 8443"
  grep -q '"agent":"ok"' <<<"$health" || fail "panel over HTTPS: $health"
  hsts=$(p8443 -f -o /dev/null -D - https://other.example.com:8443/ | tr -d '\r' | grep -i '^strict-transport-security:' || true)
  [ "$hsts" = "strict-transport-security: max-age=31536000" ] || fail "panel HSTS: '$hsts'"
  loc=$(x curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' -H 'Host: other.example.com' http://127.0.0.1/login)
  [ "$loc" = "301 https://other.example.com:8443/login" ] || fail "panel HTTP did not redirect: '$loc'"
  if x curl -sSk -o /dev/null --resolve unknown.example.com:8443:127.0.0.1 https://unknown.example.com:8443/ 2>/dev/null; then
    fail "panel port answered an unknown name"
  fi
  expect_mode /var/lib/hostashi-agent/tls/_panel/key.pem "600 root:root"
  # The panel takes the client address from nginx, never from the client.
  ip=$(x hostname -I | awk '{print $1}')
  [ "$(api POST /api/v1/tokens '{"name":"probe","scopes":["sites:read"],"expiresInDays":1}')" = 201 ] || fail "token: $(x cat /tmp/api.out)"
  bearer=$(x sed -n 's/.*"token":"\([^"]*\)".*/\1/p' /tmp/api.out)
  code=$(x curl -sSk -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $bearer" -H 'X-Real-IP: 203.0.113.9' \
    -H 'X-Forwarded-For: 203.0.113.9' --resolve "other.example.com:8443:$ip" https://other.example.com:8443/api/v1/sites)
  [ "$code" = 200 ] || fail "token over HTTPS: $code"
  [ "$(api GET /api/v1/tokens)" = 200 ] || fail "list tokens: $(x cat /tmp/api.out)"
  x grep -q "\"lastUsedIp\":\"$ip\"" /tmp/api.out || fail "panel did not see the client address $ip: $(x cat /tmp/api.out)"
  # Only nginx and the panel may use the socket.
  if x runuser -u nobody -- test -w /run/hostashi-panel.sock; then fail "another user can write the panel socket"; fi
fi

echo "PASS: installer test on $IMG"
