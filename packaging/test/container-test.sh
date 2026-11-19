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
# STORAGEBOX_USER and STORAGEBOX_PASSWORD (optional): a Hetzner Storage Box
# (sub-)account to back up to instead of a local SFTP server. The host needs
# ssh, sftp and sshpass. The password installs the agent's key, and the test
# writes only to a new folder hostashi-ci-<pid>; on exit it removes that
# folder and puts the account's authorized_keys back as it was.
set -euo pipefail

IMG=$1
BINARY=$(realpath "$2")
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
NAME=hostashi-install-test-$$
BASE=hostashi-test-base-$$

SB_TMP=""
cleanup() {
  [ -n "$SB_TMP" ] && sb_cleanup
  [ -n "${KEEP:-}" ] && return
  docker rm -f "$NAME" "$NAME-prep" >/dev/null 2>&1 || true; docker rmi -f "$BASE" >/dev/null 2>&1 || true
}
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
x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --public-url https://panel.example.com || {
  x tail -n 60 /var/log/hostashi-install.log >&2 || true
  fail "first apply failed"
}

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
# Metrics (ADR-0022): off by default, then on with a metrics:read token.
# php_up comes from the unit's cgroup, which checks the slice path.
[ "$(x curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/metrics)" = 404 ] || fail "metrics on by default"
[ "$(api PUT /api/v1/settings/metrics '{"enabled":true}')" = 200 ] || fail "metrics on: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/tokens '{"name":"prometheus","scopes":["metrics:read"],"expiresInDays":1}')" = 201 ] || fail "metrics token: $(x cat /tmp/api.out)"
mtok=$(x sed -n 's/.*"token":"\([^"]*\)".*/\1/p' /tmp/api.out)
metrics=$(x curl -fsS -H "Authorization: Bearer $mtok" http://127.0.0.1:8080/metrics) || fail "scrape failed"
grep -qx "hostashi_site_php_up{php_version=\"$ver\",site=\"wp\"} 1" <<<"$metrics" ||
  fail "no php_up for wp: $(grep hostashi_site_ <<<"$metrics" || true)"
grep -q '^hostashi_agent_ops_total{op="web.apply",result="ok"}' <<<"$metrics" || fail "no agent op series"
grep '^hostashi_site_php_memory_bytes' <<<"$metrics" || echo "note: no PHP memory series (no memory controller here)"
[ "$(api PUT /api/v1/settings/metrics '{"enabled":false}')" = 200 ] || fail "metrics off: $(x cat /tmp/api.out)"
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

step "a database for the PHP site (MariaDB)"
[ "$(x systemctl is-active mariadb.service)" = active ] || fail "mariadb is not running"
[ "$(x mariadb -Nse 'SELECT @@bind_address')" = 127.0.0.1 ] || fail "mariadb listens on more than loopback"
x /usr/local/bin/hostashi db create wp wp_db
xi /usr/local/bin/hostashi db user create wp_db wp_user <<<"Test-db-pass_12345"
x /usr/local/bin/hostashi db list | grep '^wp_db	site=wp	users=wp_user$' >/dev/null || fail "db list: $(x /usr/local/bin/hostashi db list)"
expect_mode /var/lib/hostashi-agent/databases.json "600 root:root"
if xi /usr/local/bin/hostashi db create wp mysql </dev/null; then fail "system database accepted"; fi
# The site's PHP reaches its database over the socket, like WordPress with DB_HOST localhost.
xi runuser -u wp -- sh -c 'cat >/home/wp/htdocs/db.php' <<'PHP'
<?php
$db = new mysqli("localhost", "wp_user", "Test-db-pass_12345", "wp_db");
$db->query("CREATE TABLE IF NOT EXISTS t (i INT)");
$db->query("INSERT INTO t VALUES (42)");
echo "db ", $db->query("SELECT SUM(i) FROM t")->fetch_row()[0];
try { $db->query("SELECT 1 FROM mysql.user"); echo " open"; } catch (mysqli_sql_exception $e) { echo " jailed"; }
PHP
body=$(x curl -fsS -H 'Host: wp.example.com' http://127.0.0.1/db.php)
[ "$body" = "db 42 jailed" ] || fail "PHP with database answered '$body'"
# Export and import a dump in the site's home, as the site user.
x /usr/local/bin/hostashi db export wp_db backup.sql.gz
expect_mode /home/wp/backup.sql.gz "600 wp:wp"
x mariadb -e 'DELETE FROM wp_db.t'
x /usr/local/bin/hostashi db import wp_db backup.sql.gz
[ "$(x mariadb -Nse 'SELECT SUM(i) FROM wp_db.t')" = 42 ] || fail "import did not restore the table"
if x /usr/local/bin/hostashi db import wp_db ../other/backup.sql; then fail "path outside the home accepted"; fi
[ -z "$(x mariadb -Nse "SELECT 1 FROM mysql.user WHERE User LIKE 'hostashi%job%'")" ] || fail "job user left behind"
x rm -f /home/wp/backup.sql.gz
x /usr/local/bin/hostashi db delete wp_db
[ -z "$(x mariadb -Nse "SELECT 1 FROM mysql.user WHERE User='wp_user'")" ] || fail "database user still there"
# The same through the panel API: the generated password works once, and
# deleting the site drops its database and user.
[ "$(api POST /api/v1/sites/wp/databases '{"name":"wp_api","user":"wp_api"}')" = 201 ] ||
  fail "create database: $(x cat /tmp/api.out)"
dbpw=$(x cat /tmp/api.out | sed -n 's/.*"password":"\([A-Za-z0-9]*\)".*/\1/p')
[ ${#dbpw} = 32 ] || fail "no generated password: $(x cat /tmp/api.out)"
[ "$(x mariadb -u wp_api "-p$dbpw" -Nse 'SELECT DATABASE()' wp_api)" = wp_api ] || fail "generated password does not work"
[ "$(api GET /api/v1/sites/wp/databases)" = 200 ] || fail "list databases: $(x cat /tmp/api.out)"
! x grep -q "$dbpw" /tmp/api.out || fail "password returned by a read"
# Export and import through the API run as jobs; the client polls the job.
api_job() {
  local id state _
  id=$(x cat /tmp/api.out | sed -n 's/.*"id":"\([0-9a-f]*\)".*/\1/p')
  for _ in $(seq 30); do
    [ "$(api GET "/api/v1/databases/wp_api/jobs/$id")" = 200 ] || fail "job status: $(x cat /tmp/api.out)"
    state=$(x cat /tmp/api.out | sed -n 's/.*"state":"\([a-z]*\)".*/\1/p')
    [ "$state" = running ] || break
    sleep 1
  done
  [ "$state" = "done" ] || fail "job $id ended as $state: $(x cat /tmp/api.out)"
}
x mariadb -e 'CREATE TABLE wp_api.t (i INT); INSERT INTO wp_api.t VALUES (7)'
[ "$(api POST /api/v1/databases/wp_api/export '{"path":"api.sql"}')" = 202 ] || fail "export: $(x cat /tmp/api.out)"
api_job
[ "$(api POST /api/v1/databases/wp_api/export '{"path":"api.sql"}')" = 409 ] || fail "export over an existing file: $(x cat /tmp/api.out)"
x mariadb -e 'DROP TABLE wp_api.t'
[ "$(api POST /api/v1/databases/wp_api/import '{"path":"api.sql"}')" = 202 ] || fail "import: $(x cat /tmp/api.out)"
api_job
[ "$(x mariadb -Nse 'SELECT i FROM wp_api.t')" = 7 ] || fail "API import did not restore the table"
[ "$(api DELETE /api/v1/sites/wp)" = 204 ] || fail "delete PHP site: $(x cat /tmp/api.out)"
! x systemctl is-active --quiet "hostashi-php-$ver@wp.service" || fail "PHP unit still running after delete"
x test ! -e /home/wp || fail "PHP site home still there"
[ -z "$(x mariadb -Nse "SELECT 1 FROM mysql.user WHERE User='wp_api'")" ] || fail "database user of a deleted site still there"
[ -z "$(x mariadb -Nse "SHOW DATABASES LIKE 'wp\_api'")" ] || fail "database of a deleted site still there"

step "backups with restic (ADR-0008)"
[ "$(x restic version | cut -d' ' -f1)" = restic ] || fail "restic not installed"
[ "$(x systemctl is-enabled hostashi-backup.timer)" = disabled ] || fail "backup timer on without a target"
# Without a target the run fails and says why in the status file.
if x systemctl start hostashi-backup.service; then fail "backup without a target succeeded"; fi
x grep -q '"error": "no backup target is set up"' /var/lib/hostashi-agent/backup/status.json ||
  fail "status without a target: $(x cat /var/lib/hostashi-agent/backup/status.json)"
expect_mode /var/lib/hostashi-agent/backup "700 root:root"
if [ -n "${STORAGEBOX_USER:-}" ]; then
  # A real Storage Box. Only the test's own folder and the key it adds are
  # touched, and cleanup takes both away again.
  : "${STORAGEBOX_PASSWORD:?STORAGEBOX_PASSWORD is needed with STORAGEBOX_USER}"
  bk_user=$STORAGEBOX_USER bk_host=$STORAGEBOX_USER.your-storagebox.de bk_port=23 bk_path=hostashi-ci-$$
  SB_TMP=$(mktemp -d)
  # Commands come on stdin: sftp -b would turn password logins off.
  sb_sftp() { SSHPASS=$STORAGEBOX_PASSWORD sshpass -e sftp -q -P 23 -o PubkeyAuthentication=no -o PreferredAuthentications=password \
    -o StrictHostKeyChecking=accept-new -o "UserKnownHostsFile=$SB_TMP/known_hosts" "$bk_user@$bk_host"; }
  sb_ssh() { SSHPASS=$STORAGEBOX_PASSWORD sshpass -e ssh -p 23 -o PubkeyAuthentication=no -o PreferredAuthentications=password \
    -o "UserKnownHostsFile=$SB_TMP/known_hosts" "$bk_user@$bk_host" "$@"; }
  sb_cleanup() {
    echo "=== storage box cleanup"
    sb_ssh rm -r "$bk_path" || echo "WARN: could not remove $bk_path"
    if [ -s "$SB_TMP/authorized_keys" ]; then
      printf 'put %s .ssh/authorized_keys\n' "$SB_TMP/authorized_keys" | sb_sftp >/dev/null || echo "WARN: authorized_keys not restored"
    else
      printf -- '-rm .ssh/authorized_keys\n' | sb_sftp >/dev/null || echo "WARN: authorized_keys not removed"
    fi
    rm -rf "$SB_TMP"
    SB_TMP=""
  }
  printf -- '-get .ssh/authorized_keys %s\n' "$SB_TMP/authorized_keys" | sb_sftp >/dev/null || fail "storage box login"
  x sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends openssh-client >/dev/null'
else
  # A local SFTP server stands in for the Storage Box. sftp.test resolves to it.
  bk_user=u1 bk_host=sftp.test bk_port=2299 bk_path=hostashi
  x sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends openssh-server >/dev/null'
  x sh -c 'echo "127.0.0.1 sftp.test" >>/etc/hosts'
  x useradd -m -s /bin/sh u1
fi
# The agent creates the password and the SSH key with the target.
xi /usr/local/bin/hostashi backup set <<JSON >/tmp/bk.$$ || fail "backup set"
{"version":1,"target":{"user":"$bk_user","host":"$bk_host","port":$bk_port,"path":"$bk_path"},
 "retention":{"daily":7,"weekly":4,"monthly":6},"windowStart":2,"windowEnd":4,"readParts":12}
JSON
pub=$(sed -n 's/.*"publicKey": "\(ssh-ed25519 [^"]*\)".*/\1/p' /tmp/bk.$$)
grep -q '"ready": false' /tmp/bk.$$ || fail "ready without a host key: $(cat /tmp/bk.$$)"
[ -n "$pub" ] || fail "no public key: $(cat /tmp/bk.$$)"
expect_mode /etc/hostashi-agent/backup "700 root:root"
expect_mode /etc/hostashi-agent/backup/password "600 root:root"
expect_mode /etc/hostashi-agent/backup/id_ed25519 "600 root:root"
if xi /usr/local/bin/hostashi backup start </dev/null; then fail "run started without a host key"; fi
if [ -n "$SB_TMP" ]; then
  # What the administrator does in the Hetzner console or with install-ssh-key.
  sb_ssh install-ssh-key <<<"$pub" || fail "install-ssh-key"
else
  x install -d -m 0700 -o u1 -g u1 /home/u1/.ssh
  xi sh -c 'cat >/home/u1/.ssh/authorized_keys && chown u1:u1 /home/u1/.ssh/authorized_keys && chmod 600 /home/u1/.ssh/authorized_keys' <<<"$pub"
  x sh -c 'mkdir -p /run/sshd && /usr/sbin/sshd -p 2299 -o ListenAddress=127.0.0.1'
fi
# The panel reads the host key; the administrator compares the fingerprint
# (here with ssh-keyscan) and confirms it.
code=""
for _ in $(seq 1 20); do code=$(api POST /api/v1/backups/hostkey/scan) && [ "$code" = 200 ] && break; sleep 1; done
[ "$code" = 200 ] || fail "host key scan: $code $(x cat /tmp/api.out)"
hk=$(x sed -n 's/.*"key":"\([^"]*\)".*/\1/p' /tmp/api.out)
fp=$(x sed -n 's/.*"fingerprint":"\([^"]*\)".*/\1/p' /tmp/api.out)
want=$(x sh -c "ssh-keyscan -p $bk_port -t ed25519 $bk_host 2>/dev/null | grep -v '^#' | ssh-keygen -lf - | cut -d' ' -f2")
[ -n "$fp" ] && [ "$fp" = "$want" ] || fail "scanned fingerprint '$fp', ssh-keyscan '$want'"
[ "$(api PUT /api/v1/backups/hostkey "{\"key\":\"$hk\"}")" = 200 ] || fail "confirm host key: $(x cat /tmp/api.out)"
x grep -q '"ready":true' /tmp/api.out || fail "not ready after the host key: $(x cat /tmp/api.out)"
rm -f /tmp/bk.$$
[ "$(x systemctl is-enabled hostashi-backup.timer)" = enabled ] || fail "backup timer not on after setup"
x systemctl cat hostashi-backup.timer | grep -q '^OnCalendar=\*-\*-\* 02:00:00$' || fail "timer window not applied"
x /usr/local/bin/hostashi backup key show | grep '^restic password: ' >/dev/null || fail "backup key show"
r() { x env "RESTIC_REPOSITORY=sftp://$bk_user@$bk_host:$bk_port/$bk_path" RESTIC_PASSWORD_FILE=/etc/hostashi-agent/backup/password \
  restic -o 'sftp.args=-F /dev/null -i /etc/hostashi-agent/backup/id_ed25519 -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/etc/hostashi-agent/backup/known_hosts' --no-cache "$@"; }
# A site with a file and a database.
[ "$(api POST /api/v1/sites '{"name":"bk","domains":["bk.example.com"]}')" = 201 ] || fail "create site bk: $(x cat /tmp/api.out)"
xi runuser -u bk -- sh -c 'cat >/home/bk/htdocs/index.html' <<<"backed up"
# A symlink the site user planted: restored as a link, never followed.
x runuser -u bk -- ln -s /etc /home/bk/htdocs/etc-link
[ "$(api POST /api/v1/sites/bk/databases '{"name":"bk_db"}')" = 201 ] || fail "create bk_db: $(x cat /tmp/api.out)"
x mariadb -e "CREATE TABLE bk_db.t (v VARCHAR(20)); INSERT INTO bk_db.t VALUES ('from the dump');"
x systemctl start hostashi-backup.service || fail "backup run failed: $(x cat /var/lib/hostashi-agent/backup/status.json)"
x grep -q '"ok": true' /var/lib/hostashi-agent/backup/status.json || fail "status: $(x cat /var/lib/hostashi-agent/backup/status.json)"
x grep -q '"initialized": true' /var/lib/hostashi-agent/backup/status.json || fail "first run did not create the repository"
x /usr/local/bin/hostashi backup status | grep '"ok": true' >/dev/null || fail "backup status through the agent"
if [ "$(api GET /api/v1/sites/bk/backups)" != 200 ] || ! x grep -q '"kind":"db","database":"bk_db"' /tmp/api.out; then
  fail "site snapshots through the API: $(x cat /tmp/api.out)"
fi
# grep reads the whole listing: an early exit would end restic with SIGPIPE.
r ls latest --tag kind:panel | grep '^/var/lib/hostashi/backup/panel.db$' >/dev/null || fail "panel database copy not in the backup"
[ "$(r snapshots --tag site:bk,kind:files --json | grep -o '"short_id"' | wc -l)" = 1 ] || fail "no files snapshot of bk"
[ "$(r dump latest /home/bk/htdocs/index.html --tag site:bk,kind:files)" = "backed up" ] || fail "site file not in the backup"
r dump latest bk_db.sql --tag site:bk,kind:db,db:bk_db | grep "from the dump" >/dev/null || fail "database dump not in the backup"
r dump latest /etc/hostashi/hostashi.env --tag kind:panel | grep HOSTASHI_PUBLIC_URL >/dev/null || fail "panel state not in the backup"
if r ls latest --tag kind:panel | grep 'secret.key\|/etc/hostashi-agent' >/dev/null; then fail "secrets in the panel backup"; fi
# A run on demand through the agent returns at once.
before=$(x grep '"started"' /var/lib/hostashi-agent/backup/status.json)
[ "$(api POST /api/v1/backups/run)" = 202 ] || fail "backup run through the API: $(x cat /tmp/api.out)"
for _ in $(seq 1 90); do
  [ "$(x grep '"started"' /var/lib/hostashi-agent/backup/status.json)" != "$before" ] &&
    [ "$(x systemctl is-active hostashi-backup.service)" = inactive ] && break
  sleep 1
done
[ "$(x grep '"started"' /var/lib/hostashi-agent/backup/status.json)" != "$before" ] || fail "backup start did not run"
x grep -q '"ok": true' /var/lib/hostashi-agent/backup/status.json || fail "second run: $(x cat /var/lib/hostashi-agent/backup/status.json)"
# Retention keeps the newest snapshot per day, and restic also keeps the
# oldest one while fewer days than the policy exist: two snapshots now.
[ "$(r snapshots --tag site:bk,kind:files --json | grep -o '"short_id"' | wc -l)" = 2 ] || fail "second run made no snapshot"
# Restore into a new directory of the home, through the API.
restore() {
  [ "$(api POST "/api/v1/sites/bk/backups/$1/restore")" = 202 ] || fail "restore $1: $(x cat /tmp/api.out)"
  local id
  id=$(x grep -o '"id":"[0-9a-f]\{16\}"' /tmp/api.out | cut -d'"' -f4)
  for _ in $(seq 1 90); do
    api GET "/api/v1/backups/restores/$id" >/dev/null
    x grep -q '"state":"\(done\|failed\)"' /tmp/api.out && break
    sleep 1
  done
  x grep -q '"state":"done"' /tmp/api.out || fail "restore $1: $(x cat /tmp/api.out; x journalctl -u "hostashi-restore@$id.service" --no-pager | tail -20)"
  x grep -o '"dir":"restore-[0-9-]*"' /tmp/api.out | cut -d'"' -f4
}
[ "$(api GET /api/v1/sites/bk/backups)" = 200 ] || fail "snapshot list"
files_snap=$(x grep -o '"id":"[0-9a-f]\{64\}","time":"[^"]*","kind":"files"' /tmp/api.out | head -1 | cut -d'"' -f4)
db_snap=$(x grep -o '"id":"[0-9a-f]\{64\}","time":"[^"]*","kind":"db"' /tmp/api.out | head -1 | cut -d'"' -f4)
[ -n "$files_snap" ] && [ -n "$db_snap" ] || fail "no snapshot ids in $(x cat /tmp/api.out)"
dir=$(restore "$files_snap")
[ "$(x cat "/home/bk/$dir/htdocs/index.html")" = "backed up" ] || fail "restored file"
[ "$(x stat -c '%U %a' "/home/bk/$dir")" = "bk 700" ] || fail "restore directory: $(x stat -c '%U %a' "/home/bk/$dir")"
[ "$(x stat -c '%U' "/home/bk/$dir/htdocs/index.html")" = bk ] || fail "restored file owner"
[ "$(x readlink "/home/bk/$dir/htdocs/etc-link")" = /etc ] || fail "planted symlink not restored as a link"
[ "$(x cat /home/bk/htdocs/index.html)" = "backed up" ] || fail "live site changed"
sleep 1 # the next directory name has a new second
dir=$(restore "$db_snap")
x grep -q "from the dump" "/home/bk/$dir/bk_db.sql" || fail "restored dump"
restore_id=$(x grep -o '"id":"[0-9a-f]\{16\}"' /tmp/api.out | cut -d'"' -f4)
# Into the live database: refused. Into a new one of the site: imported.
[ "$(api POST "/api/v1/backups/restores/$restore_id/import" '{"database":"bk_db"}')" = 409 ] || fail "import over the live database: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/sites/bk/databases '{"name":"bk_restored"}')" = 201 ] || fail "create bk_restored: $(x cat /tmp/api.out)"
[ "$(api POST "/api/v1/backups/restores/$restore_id/import" '{"database":"bk_restored"}')" = 202 ] || fail "import: $(x cat /tmp/api.out)"
job=$(x grep -o '"id":"[0-9a-f]\{16\}"' /tmp/api.out | cut -d'"' -f4)
for _ in $(seq 1 60); do
  api GET "/api/v1/backups/imports/$job" >/dev/null
  x grep -q '"state":"\(done\|failed\)"' /tmp/api.out && break
  sleep 1
done
x grep -q '"state":"done"' /tmp/api.out || fail "import job: $(x cat /tmp/api.out)"
[ "$(x mariadb -N -e 'SELECT v FROM bk_restored.t')" = "from the dump" ] || fail "restored database content"
# The restore unit is no more exposed than the backup run.
x systemd-analyze security "hostashi-restore@0000000000000000.service" | tail -1
for d in bk_db bk_restored; do
  [ "$(api DELETE "/api/v1/databases/$d")" = 204 ] || fail "delete $d: $(x cat /tmp/api.out)"
done
[ "$(api DELETE /api/v1/sites/bk)" = 204 ] || fail "delete site bk: $(x cat /tmp/api.out)"

step "CloudPanel import over the locked SSH key (ADR-0026)"
# The container stands in for the old server too: a CloudPanel-like site
# user with a document root, read as root over a second sshd.
x sh -c 'command -v sshd >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends openssh-server >/dev/null'
x useradd -m -s /bin/sh oldsite
x install -d -o oldsite -g oldsite /home/oldsite/htdocs /home/oldsite/htdocs/example.com /home/oldsite/htdocs/example.com/wp-content
xi runuser -u oldsite -- sh -c 'cat >/home/oldsite/htdocs/example.com/index.php' <<<"<?php echo 'old';"
xi runuser -u oldsite -- sh -c 'cat >/home/oldsite/htdocs/example.com/wp-content/a.txt' <<<"upload"
x runuser -u oldsite -- sh -c 'chmod 4755 /home/oldsite/htdocs/example.com/wp-content/a.txt'
x runuser -u oldsite -- ln -s /etc/shadow /home/oldsite/htdocs/example.com/shadow-link
x sh -c 'echo secret >/home/oldsite/htdocs/example.com/root-file'
[ "$(api POST /api/v1/sites '{"name":"cpimp","domains":["cpimp.example.com"]}')" = 201 ] || fail "create site cpimp: $(x cat /tmp/api.out)"
if xi /usr/local/bin/hostashi cloudpanel import cpimp oldsite /home/oldsite/htdocs/example.com </dev/null >/dev/null 2>&1; then
  fail "import started without a migration source"
fi
[ "$(api PUT /api/v1/migrations/cloudpanel/source '{"host":"127.0.0.1","port":2298}')" = 200 ] || fail "set source: $(x cat /tmp/api.out)"
line=$(x sed -n 's/.*"authorizedKeysLine":"\(\([^"\\]\|\\.\)*\)".*/\1/p' /tmp/api.out | sed 's/\\"/"/g')
case "$line" in restrict,port-forwarding,*'command="/usr/lib/openssh/sftp-server -R" ssh-ed25519 '*) ;; *) fail "authorized_keys line: $line" ;; esac
# What the administrator does on the old server: one line for root.
x install -d -m 0700 /root/.ssh
xi sh -c 'cat >>/root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys' <<<"$line"
x sh -c 'mkdir -p /run/sshd && /usr/sbin/sshd -p 2298 -o ListenAddress=127.0.0.1'
code=""
for _ in $(seq 1 20); do code=$(api POST /api/v1/migrations/cloudpanel/source/hostkey/scan) && [ "$code" = 200 ] && break; sleep 1; done
[ "$code" = 200 ] || fail "source host key scan: $code $(x cat /tmp/api.out)"
hk=$(x sed -n 's/.*"key":"\([^"]*\)".*/\1/p' /tmp/api.out)
[ "$(api PUT /api/v1/migrations/cloudpanel/source/hostkey "{\"key\":\"$hk\"}")" = 200 ] || fail "confirm source host key: $(x cat /tmp/api.out)"
x grep -q '"ready":true' /tmp/api.out || fail "source not ready: $(x cat /tmp/api.out)"
expect_mode /var/lib/hostashi-agent/cloudpanel/id_ed25519 "600 root:root"
# Databases on the "old server" and the read-only dump user, created by
# the SQL that the panel shows. olddb2 has a view and a trigger; their
# DEFINER clauses name a user of the old server, which the import of
# ADR-0025 refuses, so only its dump is checked.
x mariadb -e "CREATE DATABASE olddb; CREATE TABLE olddb.t (v VARCHAR(20)); INSERT INTO olddb.t VALUES ('from web2');
  CREATE DATABASE olddb2; CREATE TABLE olddb2.t (v INT); CREATE VIEW olddb2.vt AS SELECT v FROM olddb2.t;
  CREATE TRIGGER olddb2.tg BEFORE INSERT ON olddb2.t FOR EACH ROW SET NEW.v = NEW.v + 1;"
for d in cpimp_db cpimp_db2; do
  [ "$(api POST /api/v1/sites/cpimp/databases "{\"name\":\"$d\"}")" = 201 ] || fail "create $d: $(x cat /tmp/api.out)"
done
if xi /usr/local/bin/hostashi cloudpanel import cpimp oldsite /home/oldsite/htdocs/example.com olddb:cpimp_db </dev/null >/dev/null 2>&1; then
  fail "database import started without the dump user"
fi
x sh -c '/usr/local/bin/hostashi cloudpanel database >/tmp/cpdb.sql' || fail "cloudpanel database"
x grep -q "^CREATE OR REPLACE USER 'hostashi_import'@'127.0.0.1' IDENTIFIED BY '[A-Za-z0-9]\{32\}';$" /tmp/cpdb.sql || fail "dump user SQL: $(x cat /tmp/cpdb.sql)"
x sh -c 'mariadb </tmp/cpdb.sql && rm /tmp/cpdb.sql' || fail "create the dump user"
expect_mode /var/lib/hostashi-agent/cloudpanel/db_password "600 root:root"
[ "$(api GET /api/v1/migrations/cloudpanel/source)" = 200 ] || fail "get source: $(x cat /tmp/api.out)"
x grep -q '"dbUser":"hostashi_import"' /tmp/api.out || fail "source without dump user: $(x cat /tmp/api.out)"
cp_import() {
  local id
  id=$(x /usr/local/bin/hostashi cloudpanel import cpimp oldsite /home/oldsite/htdocs/example.com "$@" | sed -n 's/.*"id": "\([0-9a-f]\{16\}\)".*/\1/p')
  [ -n "$id" ] || fail "import did not start"
  for _ in $(seq 1 90); do
    x /usr/local/bin/hostashi cloudpanel import-status "$id" >/tmp/cp.$$ 2>&1 || true
    grep -q '"state": "\(done\|failed\)"' /tmp/cp.$$ && break
    sleep 1
  done
  grep -q '"state": "done"' /tmp/cp.$$ || fail "import: $(cat /tmp/cp.$$; x journalctl -u "hostashi-import@$id.service" --no-pager | tail -20)"
  cat /tmp/cp.$$
  rm -f /tmp/cp.$$
}
out=$(cp_import olddb:cpimp_db olddb2:cpimp_db2)
grep -q '"files": 2' <<<"$out" || fail "first import: $out"
# The dump came through the tunnel and belongs to the site user; the hardened
# import of ADR-0025 loads it into the site's database.
dump=$(sed -n 's/.*"\(cloudpanel-import-db\/cpimp_db-[0-9-]*\.sql\)".*/\1/p' <<<"$out")
[ -n "$dump" ] || fail "no dump in $out"
expect_mode "/home/cpimp/$dump" "600 cpimp:cpimp"
expect_mode /home/cpimp/cloudpanel-import-db "700 cpimp:cpimp"
x /usr/local/bin/hostashi db import cpimp_db "$dump" || fail "db import of the dump"
[ "$(x mariadb -N -e 'SELECT v FROM cpimp_db.t')" = "from web2" ] || fail "imported content"
dump2=$(sed -n 's/.*"\(cloudpanel-import-db\/cpimp_db2-[0-9-]*\.sql\)".*/\1/p' <<<"$out")
x grep -q 'TRIGGER .tg.' "/home/cpimp/$dump2" || fail "trigger not dumped"
x grep -q 'VIEW .vt.' "/home/cpimp/$dump2" || fail "view not dumped"
# The dump user can read, not write.
pw=$(x cat /var/lib/hostashi-agent/cloudpanel/db_password)
if x mariadb -h 127.0.0.1 -u hostashi_import "-p$pw" -e "INSERT INTO olddb.t VALUES ('x')" 2>/dev/null; then fail "dump user could write"; fi
grep -q 'root-file: not owned by the site user' <<<"$out" || fail "root-owned file not skipped: $out"
dst=/home/cpimp/cloudpanel-import
[ "$(x cat "$dst/index.php")" = "<?php echo 'old';" ] || fail "imported file content"
expect_mode "$dst/wp-content/a.txt" "755 cpimp:cpimp"
expect_mode "$dst" "700 cpimp:cpimp"
[ "$(x readlink "$dst/shadow-link")" = /etc/shadow ] || fail "symlink not kept as a link"
x test ! -e "$dst/root-file" || fail "root-owned file copied"
# The final sync: a changed file is copied again, a removed one goes.
xi runuser -u oldsite -- sh -c 'cat >/home/oldsite/htdocs/example.com/index.php' <<<"<?php echo 'new!';"
x rm /home/oldsite/htdocs/example.com/wp-content/a.txt
out=$(cp_import)
grep -q '"files": 1' <<<"$out" || fail "final sync copied: $out"
grep -q '"deleted": 1' <<<"$out" || fail "final sync deleted: $out"
[ "$(x cat "$dst/index.php")" = "<?php echo 'new!';" ] || fail "synced file content"
# The old server was only read.
x test ! -e /home/oldsite/htdocs/example.com/cloudpanel-import || fail "wrote on the old side"
x systemd-analyze security "hostashi-import@0000000000000000.service" | tail -1
[ "$(api DELETE /api/v1/migrations/cloudpanel/source)" = 200 ] || fail "delete source: $(x cat /tmp/api.out)"
x test ! -e /var/lib/hostashi-agent/cloudpanel/id_ed25519 || fail "import key not removed"
x test ! -e /var/lib/hostashi-agent/cloudpanel/db_password || fail "dump user password not removed"
x mariadb -e "DROP DATABASE olddb; DROP DATABASE olddb2; DROP USER 'hostashi_import'@'127.0.0.1'"
for d in cpimp_db cpimp_db2; do
  [ "$(api DELETE "/api/v1/databases/$d")" = 204 ] || fail "delete $d: $(x cat /tmp/api.out)"
done
x pkill -f 'sshd -p 2298' || true
[ "$(api DELETE /api/v1/sites/cpimp)" = 204 ] || fail "delete site cpimp: $(x cat /tmp/api.out)"

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
