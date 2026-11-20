#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Runs packaging/install.sh in a container with systemd as PID 1 and checks
# the result: preflight, first apply, a second apply that changes nothing,
# both units running, the panel's health check reaching the agent, file
# owners and modes, and the systemd exposure scores of the Hostashi units.
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
x /opt/hostashi/install.sh plan --binary /opt/hostashi/hostashi --no-firewall

# The container shares the host's network (--network host), so every run
# passes --no-firewall: the rules would land on the host. The firewall
# step is tested in a container of its own (packaging/test/firewall-test.sh).
step "apply"
x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --no-firewall --public-url https://panel.example.com || {
  x tail -n 60 /var/log/hostashi-install.log >&2 || true
  fail "first apply failed"
}

step "second apply changes nothing"
out=$(x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --no-firewall --public-url https://panel.example.com)
echo "$out"
grep -q 'changes=0' <<<"$out" || fail "second run changed something"
# The container's root is an overlay: the quota step must skip with a
# warning and leave the system alone (packaging/test/quota-test.sh covers ext4).
grep -q 'WARNING: disk quota is not available' <<<"$out" || fail "no warning about missing disk quota"
x test ! -e /etc/systemd/system/hostashi-quota-enforce.service || fail "quota unit installed on an overlay root"

step "security updates only, never a reboot"
# shellcheck disable=SC2016 # expanded inside the container
codename=$(x sh -c '. /etc/os-release && echo "$VERSION_CODENAME"')
origins=$(x unattended-upgrade --dry-run -d 2>&1 | sed -n 's/^Allowed origins are: //p' | head -1)
echo "allowed origins: $origins"
case "$IMG" in
  debian*) want="origin=Debian,codename=$codename-security,label=Debian-Security";;
  *) want="origin=Ubuntu,archive=$codename-security";;
esac
grep -qF "$want" <<<"$origins" || fail "security origin missing: $origins"
for o in $origins; do
  # sury.org (more PHP versions, ADR-0019) has no security archive of its own.
  case "$o" in *-security* | "origin=deb.sury.org,archive=$codename") ;; *) fail "unattended-upgrades allows a non-security origin: $o";; esac
done
[ "$(x apt-config dump --format '%v%n' Unattended-Upgrade::Automatic-Reboot)" = false ] || fail "automatic reboot is on"
for t in apt-daily.timer apt-daily-upgrade.timer; do
  x systemctl is-enabled --quiet "$t" || fail "$t not enabled"
done

step "kernel settings and no core dumps"
expect_file_mode() { [ "$(x stat -c '%a %U:%G' "$1")" = "$2" ] || fail "$1 is not $2"; }
expect_file_mode /etc/sysctl.d/90-hostashi.conf "644 root:root"
for kv in fs.suid_dumpable=0 fs.protected_regular=2 kernel.kptr_restrict=2 net.ipv4.conf.all.accept_redirects=0 net.ipv4.tcp_syncookies=1; do
  k=${kv%%=*}; got=$(x cat "/proc/sys/${k//.//}")
  [ "$got" = "${kv#*=}" ] || fail "$k is $got, want ${kv#*=}"
done
# A template has no properties of its own; an instance shows them.
for u in hostashi-agent.service hostashi.service hostashi-backup.service hostashi-restore@probe.service hostashi-import@probe.service; do
  [ "$(x systemctl show -p LimitCORE --value "$u")" = 0 ] || fail "$u allows core dumps"
done

step "services, health and permissions"
if x nft list table inet hostashi >/dev/null 2>&1; then fail "firewall table loaded despite --no-firewall"; fi
x test ! -e /etc/systemd/system/hostashi-firewall.service || fail "firewall unit installed despite --no-firewall"
for u in hostashi-agent.service hostashi.service hostashi-panel.socket; do
  [ "$(x systemctl is-active $u)" = active ] || fail "$u is not running"
done
x grep -qx 'HOSTASHI_PUBLIC_URL=https://panel.example.com:8443' /etc/hostashi/hostashi.env || fail "public URL without the panel port"
health=$(x curl -fsS http://127.0.0.1:8080/api/v1/health)
echo "$health"
grep -q '"agent":"ok"' <<<"$health" || fail "panel cannot reach the agent"
x /usr/local/bin/hostashi ping

step "hostashi doctor"
rc=0
doc=$(x /usr/local/bin/hostashi doctor) || rc=$?
echo "$doc"
for id in system.os system.pool service.panel service.nginx service.mariadb service.fail2ban; do
  grep -Eq "^  ok    $id " <<<"$doc" || fail "doctor: $id is not ok"
done
grep -Eq "^  WARN  system.quota " <<<"$doc" || fail "doctor: no quota warning on an overlay root"
for id in system.updates system.reboot system.php system.sysctl; do
  grep -Eq "^  ok    $id " <<<"$doc" || fail "doctor: $id is not ok"
done
# This test passes --no-firewall (the container shares the host's network).
grep -Eq "^  WARN  system.firewall .*--no-firewall" <<<"$doc" || fail "doctor: no warning for --no-firewall"
grep -Eq "^  (ok  |WARN)  system.ssh " <<<"$doc" || fail "doctor: no SSH check"
[ "$rc" = 0 ] || fail "doctor exited $rc"
x /usr/local/bin/hostashi doctor -json | grep -q '"id": "service.nginx"' || fail "doctor -json"

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

step "site users log in over SSH with keys from the agent's file only"
x id -nG blog | grep -qw hostashi-sites || fail "site user is not in hostashi-sites"
expect_mode /etc/hostashi-ssh/keys "755 root:root"
expect_mode /etc/ssh/sshd_config.d/10-hostashi.conf "644 root:root"
site_sshd=$(x /usr/sbin/sshd -T -C user=blog,host=client,addr=127.0.0.1)
root_sshd=$(x /usr/sbin/sshd -T -C user=root,host=client,addr=127.0.0.1)
grep -qx 'authorizedkeysfile /etc/hostashi-ssh/keys/%u' <<<"$site_sshd" || fail "site user's key file is not the agent's"
grep -qx 'passwordauthentication no' <<<"$site_sshd" || fail "site user may use a password"
grep -qx 'allowtcpforwarding no' <<<"$site_sshd" || fail "site user may forward ports"
grep -q '^authorizedkeysfile .ssh/authorized_keys' <<<"$root_sshd" || fail "the drop-in changed root's settings"
# A second sshd on its own port reads the same configuration. The key file
# here is written by hand in the format of internal/sshkeys.
x sh -c 'mkdir -p /run/sshd /root/t && rm -f /root/t/k /root/t/r && ssh-keygen -q -t ed25519 -N "" -f /root/t/k && ssh-keygen -q -t ed25519 -N "" -f /root/t/r'
# shellcheck disable=SC2016 # expanded inside the container
x sh -c 'printf "restrict,command=\"internal-sftp\" %s test\n" "$(cut -d" " -f1,2 /root/t/k.pub)" >/etc/hostashi-ssh/keys/blog && chmod 644 /etc/hostashi-ssh/keys/blog'
# A key the site user adds to its own home must not work.
x sh -c 'install -d -o blog -g blog -m 700 /home/blog/.ssh && install -o blog -g blog -m 600 /root/t/r.pub /home/blog/.ssh/authorized_keys'
x /usr/sbin/sshd -p 2297 -o ListenAddress=127.0.0.1
sshopt=(-o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)
x sh -c 'echo pwd >/root/t/batch'
x sftp "${sshopt[@]}" -P 2297 -i /root/t/k -b /root/t/batch blog@127.0.0.1 | grep -q '/home/blog' || fail "SFTP with the site's key failed"
if x ssh "${sshopt[@]}" -p 2297 -i /root/t/k blog@127.0.0.1 id 2>/dev/null | grep -q uid=; then fail "an SFTP-only key ran a command"; fi
if x sftp "${sshopt[@]}" -P 2297 -i /root/t/r -b /root/t/batch blog@127.0.0.1 >/dev/null 2>&1; then fail "a key from the site's home was accepted"; fi
# Hardening that comes with SSH (ADR-0032): the pool cannot be listed, and
# /proc shows a site user only its own processes.
expect_mode /home "711 root:root"
x findmnt -no OPTIONS /proc | tr , '\n' | grep -qx hidepid=invisible || fail "/proc without hidepid: $(x findmnt -no OPTIONS /proc)"
x grep -qE '^proc[[:space:]]+/proc[[:space:]]+proc[[:space:]]+defaults,hidepid=invisible,gid=[0-9]+' /etc/fstab || fail "no /proc line in fstab"
x runuser -u blog -- ls /home >/dev/null 2>&1 && fail "a site user can list /home"
x runuser -u blog -- test -d /home/blog/htdocs || fail "a site user cannot reach its own home"
others=$(x runuser -u blog -- ps -eo user= | sort -u | tr '\n' ' ')
[ "$others" = "blog " ] || fail "a site user sees processes of: $others"
x systemctl is-system-running --quiet || x systemctl --failed --no-pager
x pkill -f 'sshd -p 2297' || true
# pam_systemd started a user manager for the logins; it would block the
# site's deletion later, as any process of the site user does.
x loginctl terminate-user blog 2>/dev/null || true
for _ in $(seq 1 30); do x pgrep -u blog >/dev/null || break; sleep 1; done
x pgrep -u blog >/dev/null && fail "processes of blog left after the SSH logins"
x rm -rf /etc/hostashi-ssh/keys/blog /home/blog/.ssh /root/t
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
# exposure UNIT MAX fails when the unit's exposure score rises above MAX
# (ADR-0033): a change that weakens the sandbox shows up here.
exposure() {
  local score
  # "→ Overall exposure level for hostashi-php-8.4@wp.service: 2.4 OK :-)"
  score=$(x systemd-analyze security "$1" | tail -1 | sed -E 's/.*: ([0-9]+\.[0-9]+) .*/\1/')
  echo "$1: exposure $score"
  awk -v s="$score" -v m="$2" 'BEGIN { exit !(s <= m) }' || fail "$1: exposure $score is above $2"
}
exposure hostashi.service 2.0
exposure hostashi-agent.service 4.5

step "changed setting is applied and the panel restarts"
out=$(x /opt/hostashi/install.sh apply --yes --binary /opt/hostashi/hostashi --no-firewall --public-url https://other.example.com)
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
# An admin needs a second factor before anything else (ADR-0035). The
# code comes from the host, which has python3.
totp() {
  python3 -c 'import base64,hashlib,hmac,struct,sys,time
s = sys.argv[1]
k = base64.b32decode(s + "=" * (-len(s) % 8))
h = hmac.new(k, struct.pack(">Q", int(time.time()) // 30), hashlib.sha1).digest()
o = h[-1] & 15
print("%06d" % ((struct.unpack(">I", h[o:o + 4])[0] & 0x7fffffff) % 1000000))' "$1"
}
[ "$(api POST /api/v1/sites '{"name":"early","domains":["early.example.com"]}')" = 403 ] || fail "admin without a second factor: $(x cat /tmp/api.out)"
x grep -q second_factor_required /tmp/api.out || fail "no second_factor_required: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/auth/totp/enroll)" = 200 ] || fail "TOTP enroll: $(x cat /tmp/api.out)"
secret=$(x cat /tmp/api.out | sed -n 's/.*"secret":"\([A-Z2-7]*\)".*/\1/p')
[ -n "$secret" ] || fail "no TOTP secret: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/auth/totp/confirm "{\"code\":\"$(totp "$secret")\"}")" = 200 ] || fail "TOTP confirm: $(x cat /tmp/api.out)"

step "fail2ban reads the panel's failed logins"
for j in sshd hostashi-panel recidive; do
  x fail2ban-client status "$j" >/dev/null || fail "fail2ban jail $j is not running"
done
expect_mode /etc/fail2ban/jail.d/hostashi.conf "644 root:root"
expect_mode /etc/fail2ban/filter.d/hostashi-panel.conf "644 root:root"
expect_mode /etc/fail2ban/fail2ban.d/hostashi.conf "644 root:root"
[ "$(api POST /api/v1/auth/login '{"username":"kim","password":"not-the-password-1"}')" = 401 ] || fail "wrong password: $(x cat /tmp/api.out)"
sleep 1
x sh -c 'journalctl -u hostashi.service -o cat --no-pager | grep auth.failed' || fail "no auth.failed line in the journal"
# The filter must match exactly that line; loopback is matched here and only
# ignored by the jail (ignoreip), so the regex test sees it.
f2b=$(x fail2ban-regex systemd-journal hostashi-panel)
grep -E '^Lines: ' <<<"$f2b"
grep -Eq '^Lines: [0-9]+ lines, 0 ignored, [1-9][0-9]* matched' <<<"$f2b" || fail "fail2ban filter does not match the panel's line"
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
# The master sees only its own site's home (ADR-0033); another home on the
# server stays hidden.
x install -d -m 0750 /home/probe-other
homes=$(x sh -c "ls -A /proc/\$(systemctl show -p MainPID --value hostashi-php-$ver@wp.service)/root/home")
x rmdir /home/probe-other
[ "$homes" = wp ] || fail "PHP master of wp sees these homes: $homes"
exposure "hostashi-php-$ver@wp.service" 3.0
# The worker runs as the site user and cannot read outside its home and /tmp.
xi runuser -u wp -- sh -c 'cat >/home/wp/htdocs/index.php' <<'PHP'
<?php
echo "php ", PHP_MAJOR_VERSION, ".", PHP_MINOR_VERSION, " user ", posix_getpwuid(posix_geteuid())["name"];
echo @file_get_contents("/etc/hostname") === false ? " jailed" : " open";
PHP
body=$(x curl -fsS -H 'Host: wp.example.com' http://127.0.0.1/some/pretty/url)
[ "$body" = "php $ver user wp jailed" ] || fail "PHP site answered '$body'"
# A PHP package update replaces the binary under the running master; the
# dpkg hook (ADR-0033) restarts the site's unit, the package's own unit
# stays off.
pid=$(x systemctl show -p MainPID --value "hostashi-php-$ver@wp.service")
x env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --reinstall "php$ver-fpm" >/dev/null || fail "reinstall of php$ver-fpm failed"
new=$pid
for _ in $(seq 1 30); do
  new=$(x systemctl show -p MainPID --value "hostashi-php-$ver@wp.service")
  [ "$new" != "$pid" ] && [ "$new" != 0 ] && x systemctl is-active --quiet "hostashi-php-$ver@wp.service" && break
  sleep 1
done
[ "$new" != "$pid" ] || fail "PHP master of wp not restarted after the update (pid $pid)"
if x grep -Eq ' /usr/[^ ]+ \(deleted\)$' "/proc/$new/maps"; then fail "restarted PHP master still maps a replaced file"; fi
if x systemctl is-active --quiet "php$ver-fpm.service"; then fail "package unit php$ver-fpm started by the update"; fi
body=$(x curl -fsS -H 'Host: wp.example.com' http://127.0.0.1/some/pretty/url)
[ "$body" = "php $ver user wp jailed" ] || fail "PHP site answered '$body' after the update"
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
# The site's PHP runs in its slice, and the slice takes the limits (ADR-0015).
phpcg=$(x systemctl show -p ControlGroup --value "hostashi-php-$ver@wp.service")
[ "$phpcg" = "/hostashi.slice/hostashi-site.slice/hostashi-site-wp.slice/hostashi-php-$ver@wp.service" ] || fail "PHP unit not in the site slice: $phpcg"
x /usr/local/bin/hostashi site limits wp memory=268435456 tasks=64 cpu-weight=200
x /usr/local/bin/hostashi site limits wp | grep -qx 'memory=268435456 tasks=64 cpu-weight=200 cpu-quota=0' || fail "limits not read back"
for p in MemoryMax=268435456 MemoryHigh=241591905 TasksMax=64 CPUWeight=200; do
  [ "$(x systemctl show -p "${p%%=*}" --value hostashi-site-wp.slice)" = "${p#*=}" ] || fail "slice ${p%%=*} not ${p#*=}"
done
sl=/sys/fs/cgroup/hostashi.slice/hostashi-site.slice/hostashi-site-wp.slice
for f in memory.max=268435456 pids.max=64 cpu.weight=200; do
  if x test -e "$sl/${f%%=*}"; then
    [ "$(x cat "$sl/${f%%=*}")" = "${f#*=}" ] || fail "$sl/${f%%=*} is $(x cat "$sl/${f%%=*}"), want ${f#*=}"
  else
    echo "note: no ${f%%=*} in the container's cgroup (controller not delegated)"
  fi
done
# PHP of a new site cannot start programs (ADR-0033); the switch per site
# allows it and takes it back.
xi runuser -u wp -- sh -c 'cat >/home/wp/htdocs/prog.php' <<'PHP'
<?php
echo function_exists("exec") ? "exec on" : "exec off";
PHP
prog() { x curl -fsS -H 'Host: wp.example.com' http://127.0.0.1/prog.php; }
# prog_is WANT: the reload of the pool takes a moment to reach new workers.
prog_is() { local _i; for _i in $(seq 1 10); do [ "$(prog)" = "$1" ] && return 0; sleep 0.5; done; return 1; }
prog_is "exec off" || fail "new site may start programs: $(prog)"
[ "$(api PUT /api/v1/sites/wp/php-programs '{"enabled":true}')" = 200 ] || fail "allow programs: $(x cat /tmp/api.out)"
prog_is "exec on" || fail "programs still off after allowing them: $(prog)"
[ "$(api PUT /api/v1/sites/wp/php-programs '{"enabled":false}')" = 200 ] || fail "forbid programs: $(x cat /tmp/api.out)"
prog_is "exec off" || fail "programs still on after forbidding them: $(prog)"
# The panel gave the new site the default preset; custom values through
# the API reach the slice and the PHP pool.
[ "$(api GET /api/v1/sites/wp/limits)" = 200 ] || fail "site limits: $(x cat /tmp/api.out)"
x grep -q '"set":true,"preset":"small"' /tmp/api.out || fail "new site has no default preset: $(x cat /tmp/api.out)"
x grep -q '"applyError":""' /tmp/api.out || fail "default limits not applied: $(x cat /tmp/api.out)"
x grep -qx 'pm.max_children = 5' /var/lib/hostashi-agent/php/wp.conf || fail "default workers not in the pool"
[ "$(api PUT /api/v1/sites/wp/limits '{"custom":{"diskBytes":1073741824,"diskInodes":10000,"memoryBytes":536870912,"phpWorkers":7,"tasks":96,"cpuPriority":"low"}}')" = 200 ] ||
  fail "custom limits: $(x cat /tmp/api.out)"
x grep -qx 'pm.max_children = 7' /var/lib/hostashi-agent/php/wp.conf || fail "custom workers not in the pool"
x systemctl is-active --quiet "hostashi-php-$ver@wp.service" || fail "PHP unit not running after the worker change"
for p in MemoryMax=536870912 TasksMax=96 CPUWeight=50; do
  [ "$(x systemctl show -p "${p%%=*}" --value hostashi-site-wp.slice)" = "${p#*=}" ] || fail "slice ${p%%=*} not ${p#*=} after the API change"
done
# SSH sessions of the site user run in its user slice, which gets the same
# limits through a drop-in (ADR-0015, ADR-0032).
wpuid=$(x id -u wp)
expect_mode "/etc/systemd/system/user-$wpuid.slice.d/10-hostashi.conf" "644 root:root"
for p in MemoryMax=536870912 TasksMax=96 CPUWeight=50; do
  [ "$(x systemctl show -p "${p%%=*}" --value "user-$wpuid.slice")" = "${p#*=}" ] || fail "user slice ${p%%=*} not ${p#*=}"
done
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

step "SSH keys through the panel API (ADR-0032)"
x sh -c 'mkdir -p /run/sshd /root/t && rm -f /root/t/k && ssh-keygen -q -t ed25519 -N "" -C kim@laptop -f /root/t/k'
pub=$(x cat /root/t/k.pub)
[ "$(api POST /api/v1/sites/wp/ssh-keys "{\"label\":\"laptop\",\"access\":\"shell\",\"public\":\"$pub\"}")" = 201 ] || fail "add SSH key: $(x cat /tmp/api.out)"
keyid=$(x sed -n 's/^{"id":\([0-9]*\),.*/\1/p' /tmp/api.out)
expect_mode /etc/hostashi-ssh/keys/wp "644 root:root"
x grep -q "^restrict,pty $(cut -d' ' -f1,2 <<<"$pub") laptop\$" /etc/hostashi-ssh/keys/wp || fail "key file: $(x cat /etc/hostashi-ssh/keys/wp)"
[ "$(x getent passwd wp | cut -d: -f7)" = /bin/bash ] || fail "shell key did not give wp a login shell"
[ "$(api GET /api/v1/ssh/host-keys)" = 200 ] || fail "host keys: $(x cat /tmp/api.out)"
x grep -q '"type":"ssh-ed25519","fingerprint":"SHA256:' /tmp/api.out || fail "no ed25519 host key: $(x cat /tmp/api.out)"
x /usr/sbin/sshd -p 2297 -o ListenAddress=127.0.0.1
sshopt=(-o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)
out=$(x ssh "${sshopt[@]}" -p 2297 -i /root/t/k wp@127.0.0.1 'id -un; cat /proc/self/cgroup') || fail "shell login with the panel's key failed"
grep -qx wp <<<"$out" || fail "shell login ran as someone else: $out"
grep -q "/user.slice/user-$wpuid.slice/" <<<"$out" || fail "SSH session not in user-$wpuid.slice: $out"
# Back to SFTP only: no shell, and commands are refused.
[ "$(api PUT "/api/v1/sites/wp/ssh-keys/$keyid" '{"label":"laptop","access":"sftp"}')" = 200 ] || fail "change SSH key: $(x cat /tmp/api.out)"
[ "$(x getent passwd wp | cut -d: -f7)" = /usr/sbin/nologin ] || fail "wp kept its login shell without a shell key"
if x ssh "${sshopt[@]}" -p 2297 -i /root/t/k wp@127.0.0.1 id 2>/dev/null | grep -q uid=; then fail "an SFTP-only key ran a command"; fi
[ "$(api DELETE "/api/v1/sites/wp/ssh-keys/$keyid")" = 204 ] || fail "delete SSH key: $(x cat /tmp/api.out)"
x grep -q '^restrict' /etc/hostashi-ssh/keys/wp && fail "deleted key still in the key file"
if x sftp "${sshopt[@]}" -P 2297 -i /root/t/k -b /dev/null wp@127.0.0.1 >/dev/null 2>&1; then fail "a deleted key still logs in"; fi
x pkill -f 'sshd -p 2297' || true
x loginctl terminate-user wp 2>/dev/null || true
for _ in $(seq 1 30); do x pgrep -u wp >/dev/null || break; sleep 1; done
x pgrep -u wp >/dev/null && fail "processes of wp left after the SSH logins"
x rm -rf /root/t

step "a database for the PHP site (MariaDB)"
[ "$(x systemctl is-active mariadb.service)" = active ] || fail "mariadb is not running"
[ "$(x mariadb -Nse 'SELECT @@bind_address')" = 127.0.0.1 ] || fail "mariadb listens on more than loopback"
[ "$(x mariadb -Nse 'SELECT @@local_infile')" = 0 ] || fail "mariadb allows LOAD DATA LOCAL"
expect_mode /etc/mysql/mariadb.conf.d/90-hostashi.cnf "644 root:root"
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

step "two sites stay apart (ADR-0033 H8, 05-security 5.5)"
# A second site with PHP and a database. Everything below runs as wp, the
# way a site's shell login or its PHP code would, and must not reach shop.
[ "$(api POST /api/v1/sites "{\"name\":\"shop\",\"php\":\"$ver\",\"domains\":[\"shop.example.com\"]}")" = 201 ] ||
  fail "create site shop: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/sites/shop/databases '{"name":"shop_db","user":"shop_db"}')" = 201 ] ||
  fail "create database of shop: $(x cat /tmp/api.out)"
xi runuser -u shop -- sh -c 'cat >/home/shop/htdocs/index.php' <<'PHP'
<?php echo "shop secret";
PHP
[ "$(x curl -fsS -H 'Host: shop.example.com' http://127.0.0.1/index.php)" = "shop secret" ] || fail "shop does not answer"
x runuser -u shop -- sh -c 'exec sleep 300' &
for _ in $(seq 10); do x pgrep -u shop -x sleep >/dev/null && break; sleep 1; done
as_wp() { x runuser -u wp -- "$@"; }
# Files: no read of shop's home. The web server's log holds every site's
# requests, and the journal holds shop's PHP messages: wp reads and writes
# neither.
if as_wp cat /home/shop/htdocs/index.php 2>/dev/null; then fail "wp reads shop's files"; fi
if as_wp ls /home/shop >/dev/null 2>&1; then fail "wp lists shop's home"; fi
for log in /var/log/nginx/access.log /var/log/nginx/error.log; do
  x test -f "$log" || fail "no $log"
  if as_wp cat "$log" >/dev/null 2>&1; then fail "wp reads $log"; fi
  if as_wp sh -c "echo x >>$log" 2>/dev/null; then fail "wp writes $log"; fi
done
[ -z "$(as_wp journalctl -q --no-pager -t hostashi-php-shop 2>/dev/null)" ] || fail "wp reads shop's PHP messages in the journal"
# Sockets: curl exits with 7 when it cannot connect.
for sock in /run/hostashi-php/shop/php-fpm.sock /run/hostashi-panel.sock /run/hostashi/agent.sock; do
  rc=0
  as_wp curl -sS -m 5 -o /dev/null --unix-socket "$sock" http://x/ 2>/dev/null || rc=$?
  [ "$rc" = 7 ] || fail "wp connects to $sock (curl exit $rc)"
done
# Processes: hidepid hides shop's processes from wp.
pid=$(x pgrep -u shop -x sleep | head -1)
[ -n "$pid" ] || fail "no process of shop to look for"
if as_wp pgrep -u shop >/dev/null; then fail "wp sees shop's processes"; fi
if as_wp test -e "/proc/$pid"; then fail "wp sees /proc/$pid of shop"; fi
# Databases: wp's own login reaches only its own database, and the socket
# does not let the system user wp in.
if x mariadb -u wp_api "-p$dbpw" -Nse 'SELECT 1' shop_db 2>/dev/null; then fail "wp's database user opens shop_db"; fi
x mariadb -u wp_api "-p$dbpw" -Nse 'SHOW DATABASES' | grep -qx shop_db && fail "wp's database user lists shop_db"
if as_wp mariadb -Nse 'SELECT 1' 2>/dev/null; then fail "system user wp logs in to MariaDB over the socket"; fi
# The same from wp's PHP code.
xi runuser -u wp -- sh -c 'cat >/home/wp/htdocs/iso.php' <<'PHP'
<?php
$out = [];
$out[] = @file_get_contents("/home/shop/htdocs/index.php") === false ? "file:no" : "file:yes";
foreach (["php" => "/run/hostashi-php/shop/php-fpm.sock", "panel" => "/run/hostashi-panel.sock", "agent" => "/run/hostashi/agent.sock"] as $k => $s) {
  $c = @stream_socket_client("unix://" . $s, $errno, $errstr, 2);
  $out[] = $c === false ? "$k:no" : "$k:yes";
}
echo implode(" ", $out);
PHP
body=$(x curl -fsS -H 'Host: wp.example.com' http://127.0.0.1/iso.php)
[ "$body" = "file:no php:no panel:no agent:no" ] || fail "wp's PHP reaches shop or the services: $body"
x rm -f /home/wp/htdocs/iso.php
x pkill -u shop -x sleep || true
[ "$(api DELETE /api/v1/sites/shop)" = 204 ] || fail "delete site shop: $(x cat /tmp/api.out)"

step "cron jobs of a site (ADR-0028)"
# The job writes who and where it runs, and whether other homes and /etc
# are hidden or read-only for it.
[ "$(api POST /api/v1/sites/wp/cron '{"schedule":"*/5 * * * *","command":"{ id -un; pwd; ls /home; touch /etc/x 2>/dev/null && echo etc-writable; echo 100%; } > cron.out 2>&1"}')" = 201 ] ||
  fail "create cron job: $(x cat /tmp/api.out)"
cid=$(x cat /tmp/api.out | sed -n 's/^{"id":\([0-9]*\),.*/\1/p')
[ -n "$cid" ] || fail "no cron job id: $(x cat /tmp/api.out)"
cunit="hostashi-cron-wp-$cid"
x systemctl is-active --quiet "$cunit.timer" || fail "cron timer not running"
x systemctl is-enabled --quiet "$cunit.timer" || fail "cron timer not enabled"
[ "$(x stat -c '%a %U:%G' "/var/lib/hostashi-cron/wp/$cid.sh")" = "640 root:wp" ] || fail "cron script has the wrong owner or mode"
[ "$(x systemctl show -P Nice "$cunit.service")" = 10 ] || fail "cron job does not run with Nice=10"
[ "$(x systemctl show -P Slice "$cunit.service")" = hostashi-site-wp.slice ] || fail "cron job is not in the site's slice"
[ "$(api POST "/api/v1/sites/wp/cron/$cid/run")" = 202 ] || fail "run cron job: $(x cat /tmp/api.out)"
for _ in $(seq 1 30); do x test -s /home/wp/cron.out && ! x systemctl is-active --quiet "$cunit.service" && break; sleep 1; done
out=$(x cat /home/wp/cron.out)
[ "$out" = "$(printf 'wp\n/home/wp\nwp\n100%%')" ] || fail "cron job wrote '$out'"
if [ "$(api GET /api/v1/sites/wp/cron)" != 200 ] || ! x grep -q '"result":"success"' /tmp/api.out || ! x grep -q '"exitStatus":0' /tmp/api.out; then
  fail "cron status: $(x cat /tmp/api.out)"
fi
# Pause, then a disabled site keeps the timer stopped until it is enabled.
[ "$(api PUT "/api/v1/sites/wp/cron/$cid" '{"schedule":"*/5 * * * *","command":"true","paused":true}')" = 200 ] || fail "pause: $(x cat /tmp/api.out)"
! x systemctl is-active --quiet "$cunit.timer" || fail "paused cron timer still running"
[ "$(api PUT "/api/v1/sites/wp/cron/$cid" '{"schedule":"*/5 * * * *","command":"true"}')" = 200 ] || fail "resume: $(x cat /tmp/api.out)"
x systemctl is-active --quiet "$cunit.timer" || fail "resumed cron timer not running"
[ "$(api PUT /api/v1/sites/wp/disabled '{"disabled":true}')" = 200 ] || fail "disable site: $(x cat /tmp/api.out)"
! x systemctl is-active --quiet "$cunit.timer" || fail "cron timer of a disabled site still running"
[ "$(api POST "/api/v1/sites/wp/cron/$cid/run")" = 409 ] || fail "run on a disabled site: $(x cat /tmp/api.out)"
[ "$(api PUT /api/v1/sites/wp/disabled '{"disabled":false}')" = 200 ] || fail "enable site: $(x cat /tmp/api.out)"
x systemctl is-active --quiet "$cunit.timer" || fail "cron timer not back after enabling the site"
[ "$(api DELETE /api/v1/sites/wp)" = 204 ] || fail "delete PHP site: $(x cat /tmp/api.out)"
x test ! -e "/etc/systemd/system/$cunit.timer" || fail "cron units of a deleted site still there"
x test ! -e /var/lib/hostashi-cron/wp || fail "cron scripts of a deleted site still there"
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
# DEFINER clauses name a user of the old server, so the panel loads the
# dumps as the databases' owner account (ADR-0030). The trigger is created
# without a database name: mariadb-dump keeps a name written there, and a
# trigger on another database's table is refused at the load, as it should.
x mariadb -e "CREATE DATABASE olddb; CREATE TABLE olddb.t (v VARCHAR(20)); INSERT INTO olddb.t VALUES ('from web2');
  CREATE DATABASE olddb2; CREATE TABLE olddb2.t (v INT); CREATE VIEW olddb2.vt AS SELECT v FROM olddb2.t;
  USE olddb2; CREATE TRIGGER tg BEFORE INSERT ON t FOR EACH ROW SET NEW.v = NEW.v + 1;"
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
x grep -q 'TRIGGER.*tg' "/home/cpimp/$dump2" || fail "trigger not dumped: $(x grep -i 'create\|trigger' "/home/cpimp/$dump2")"
x grep -q 'VIEW.*vt' "/home/cpimp/$dump2" || fail "view not dumped: $(x grep -i 'create\|view' "/home/cpimp/$dump2")"
# The panel loads both dumps as the databases' owners; the view and the
# trigger work afterwards, and the owner account stays locked.
imp=$(sed -n 's/.*"id": "\([0-9a-f]\{16\}\)".*/\1/p' <<<"$out" | head -1)
[ "$(api POST "/api/v1/migrations/cloudpanel/imports/$imp/load")" = 202 ] || fail "load dumps: $(x cat /tmp/api.out)"
for _ in $(seq 1 60); do
  api GET "/api/v1/migrations/cloudpanel/imports/$imp/loads" >/dev/null
  [ "$(x grep -o '"state":"done"' /tmp/api.out | wc -l)" = 2 ] && break
  x grep -q '"state":"failed"\|"error":' /tmp/api.out && break
  sleep 1
done
[ "$(x grep -o '"state":"done"' /tmp/api.out | wc -l)" = 2 ] || fail "loads: $(x cat /tmp/api.out; x journalctl -u hostashi-agent --no-pager | tail -20)"
x mariadb -e "INSERT INTO cpimp_db2.t VALUES (41)"
[ "$(x mariadb -N -e 'SELECT v FROM cpimp_db2.vt')" = 42 ] || fail "view or trigger after the load"
owner=$(x mariadb -N -e "SELECT DEFINER FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA='cpimp_db2'")
grep -q '^hostashi_own_[0-9a-f]\{16\}@localhost$' <<<"$owner" || fail "trigger definer: $owner"
locked=$(x mariadb -N -e "SELECT JSON_VALUE(Priv, '\$.account_locked') FROM mysql.global_priv WHERE User='${owner%@localhost}'")
[ "$locked" = 1 ] || [ "$locked" = true ] || fail "owner not locked: $locked"
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
[ "$(api GET '/api/v1/migrations/cloudpanel/imports?site=cpimp')" = 200 ] || fail "list imports: $(x cat /tmp/api.out)"
[ "$(x grep -o '"state":"done"' /tmp/api.out | wc -l)" = 2 ] || fail "imports of cpimp: $(x cat /tmp/api.out)"
[ "$(x cat "$dst/index.php")" = "<?php echo 'new!';" ] || fail "synced file content"
# Activate: the imported files become the web root, the old one is kept.
x runuser -u cpimp -- sh -c 'echo before >/home/cpimp/htdocs/before.txt'
[ "$(api POST /api/v1/migrations/cloudpanel/sites/cpimp/activate)" = 200 ] || fail "activate: $(x cat /tmp/api.out)"
prev=$(x sed -n 's/.*"previous":"\(htdocs-before-import-[0-9-]*\)".*/\1/p' /tmp/api.out)
[ -n "$prev" ] || fail "no previous web root in $(x cat /tmp/api.out)"
[ "$(x cat /home/cpimp/htdocs/index.php)" = "<?php echo 'new!';" ] || fail "web root not swapped"
[ "$(x cat "/home/cpimp/$prev/before.txt")" = before ] || fail "previous web root not kept"
expect_mode /home/cpimp/htdocs "755 cpimp:cpimp"
x test ! -e "$dst" || fail "import directory still there after activate"
[ "$(api POST /api/v1/migrations/cloudpanel/sites/cpimp/activate)" = 409 ] || fail "second activate: $(x cat /tmp/api.out)"
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

step "more PHP versions from sury.org (ADR-0019)"
inst() { x /opt/hostashi/install.sh "$@" --binary /opt/hostashi/hostashi --no-firewall --public-url https://other.example.com; }
if inst plan --php-versions 8.0 2>/dev/null; then fail "PHP 8.0 accepted"; fi
if inst plan --php-versions '8.1;7.4' 2>/dev/null; then fail "a list with ';' accepted"; fi
# shellcheck disable=SC2016 # a dpkg-query format, not shell
def=$(x dpkg-query -W -f='${Depends}' php-fpm | sed 's/^php\([0-9.]*\)-fpm.*/\1/')
out=$(inst apply --yes --php-versions "8.1,7.4,$def") || { x tail -n 60 /var/log/hostashi-install.log >&2 || true; fail "apply with --php-versions failed"; }
echo "$out"
grep -q 'apply.*phpversions' <<<"$out" || fail "phpversions step not applied"
expect_mode /usr/share/keyrings/hostashi-sury-php.gpg "644 root:root"
expect_mode /etc/apt/preferences.d/hostashi-sury-php "644 root:root"
# The default version stays on the distribution, the extra ones and
# php-common come from sury.org (Spike S10).
origin() { x apt-cache policy "$1" | awk '/\*\*\*/ { getline; print; exit }'; }
case "$(origin "php$def-fpm")" in *packages.sury.org*) fail "php$def-fpm comes from sury.org";; esac
for p in php-common php8.1-fpm php7.4-fpm; do
  case "$(origin "$p")" in *packages.sury.org*) ;; *) fail "$p does not come from sury.org: $(origin "$p")";; esac
done
pending=$(x apt-get -s upgrade | grep -c '^Inst php' || true)
[ "$pending" = 0 ] || fail "$pending PHP packages would be upgraded: $(x apt-get -s upgrade | grep '^Inst php')"
for v in 8.1 7.4; do
  ! x systemctl is-enabled --quiet "php$v-fpm.service" || fail "package unit php$v-fpm is enabled"
  ! x systemctl is-active --quiet "php$v-fpm.service" || fail "package unit php$v-fpm is running"
done
origins=$(x unattended-upgrade --dry-run -d 2>&1 | sed -n 's/^Allowed origins are: //p' | head -1)
grep -qF "origin=deb.sury.org,archive=$codename" <<<"$origins" || fail "sury.org not in unattended-upgrades: $origins"
out=$(inst apply --yes)
grep -q 'changes=0' <<<"$out" || { echo "$out"; fail "a run without --php-versions changed something"; }
# The unit the panel starts (ADR-0036): the script refuses what is not on
# its list, and installs what is.
expect_mode /usr/local/lib/hostashi/php-install "755 root:root"
if x systemctl start hostashi-php-install@8.0.service 2>/dev/null; then fail "the unit installed PHP 8.0"; fi
x systemctl start hostashi-php-install@8.2.service || { x journalctl -u hostashi-php-install@8.2.service --no-pager -n 30 >&2; fail "the unit did not install PHP 8.2"; }
x /usr/local/lib/hostashi/php-install check 8.2 || fail "PHP 8.2 not set up after the unit ran"
# The agent reads this log for the panel (ADR-0036).
expect_mode /var/lib/hostashi-agent/php-install "700 root:root"
x tail -n 1 /var/lib/hostashi-agent/php-install/8.2.log | grep -q '^php-install: done:' || fail "no final line in the unit's log: $(x tail -n 5 /var/lib/hostashi-agent/php-install/8.2.log)"
x grep -qx 'Package: php-common php7.4\* php8.1\* php8.2\*' /etc/apt/preferences.d/hostashi-sury-php || fail "pin does not cover 8.2: $(x cat /etc/apt/preferences.d/hostashi-sury-php)"
out=$(inst apply --yes)
grep -q 'changes=0' <<<"$out" || { echo "$out"; fail "a run after the panel's install changed something"; }
[ "$(api GET /api/v1/php/versions)" = 200 ] || fail "php versions: $(x cat /tmp/api.out)"
for v in 7.4 8.1 8.2 "$def"; do x grep -q "\"installed\":\\[[^]]*\"$v\"" /tmp/api.out || fail "PHP $v not listed as installed: $(x cat /tmp/api.out)"; done
# A site on each extra version, then switched between them.
[ "$(api POST /api/v1/sites '{"name":"legacy","php":"7.4","domains":["legacy.example.com"]}')" = 201 ] || fail "create 7.4 site: $(x cat /tmp/api.out)"
xi runuser -u legacy -- sh -c 'cat >/home/legacy/htdocs/index.php' <<'PHP'
<?php
echo "php ", PHP_MAJOR_VERSION, ".", PHP_MINOR_VERSION, " ", json_encode(["ok" => extension_loaded("mysqli") && extension_loaded("gd") && extension_loaded("intl")]);
PHP
for v in 7.4 8.1 "$def"; do
  [ "$(api PUT /api/v1/sites/legacy/php "{\"php\":\"$v\"}")" = 200 ] || fail "switch to $v: $(x cat /tmp/api.out)"
  x systemctl is-active --quiet "hostashi-php-$v@legacy.service" || fail "PHP $v unit of the site not running"
  exposure "hostashi-php-$v@legacy.service" 3.0
  body=$(x curl -fsS -H 'Host: legacy.example.com' http://127.0.0.1/index.php)
  [ "$body" = "php $v {\"ok\":true}" ] || fail "PHP $v site answered '$body'"
done
[ "$(api DELETE /api/v1/sites/legacy)" = 204 ] || fail "delete legacy site: $(x cat /tmp/api.out)"
# Installing a version from the panel (ADR-0036): the API starts the unit,
# the status follows it to the script's final line.
[ "$(api POST /api/v1/php/install '{"version":"8.0"}')" = 422 ] || fail "PHP 8.0 install: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/php/install '{"version":"8.6"}')" = 202 ] || fail "PHP 8.6 install: $(x cat /tmp/api.out)"
state=""
for _ in $(seq 1 150); do
  [ "$(api GET /api/v1/php/install)" = 200 ] || fail "install status: $(x cat /tmp/api.out)"
  state=$(x sed -n 's/.*"state":"\([a-z]*\)".*/\1/p' /tmp/api.out)
  [ "$state" = running ] || break
  sleep 2
done
[ "$state" = succeeded ] || fail "PHP 8.6 from the panel: $(x cat /tmp/api.out)"
x grep -q '"repository":true' /tmp/api.out || fail "sury.org not reported: $(x cat /tmp/api.out)"
[ "$(api GET /api/v1/php/versions)" = 200 ] || fail "php versions: $(x cat /tmp/api.out)"
x grep -q '"installed":\[[^]]*"8.6"' /tmp/api.out || fail "PHP 8.6 not installed: $(x cat /tmp/api.out)"
[ "$(api POST /api/v1/php/install '{"version":"8.6"}')" = 409 ] || fail "second install of 8.6: $(x cat /tmp/api.out)"
# 8.6, the newest version, runs a site with the same extensions.
[ "$(api POST /api/v1/sites '{"name":"next","php":"8.6","domains":["next.example.com"]}')" = 201 ] || fail "create 8.6 site: $(x cat /tmp/api.out)"
xi runuser -u next -- sh -c 'cat >/home/next/htdocs/index.php' <<'PHP'
<?php
echo "php ", PHP_MAJOR_VERSION, ".", PHP_MINOR_VERSION, " ", json_encode(["ok" => extension_loaded("mysqli") && extension_loaded("gd") && extension_loaded("intl")]);
PHP
x systemctl is-active --quiet hostashi-php-8.6@next.service || fail "PHP 8.6 unit of the site not running"
exposure hostashi-php-8.6@next.service 3.0
body=$(x curl -fsS -H 'Host: next.example.com' http://127.0.0.1/index.php)
[ "$body" = 'php 8.6 {"ok":true}' ] || fail "PHP 8.6 site answered '$body'"
[ "$(api DELETE /api/v1/sites/next)" = 204 ] || fail "delete 8.6 site: $(x cat /tmp/api.out)"

step "root gets an account back (ADR-0035)"
out=$(x /usr/local/bin/hostashi user recover kim) || fail "user recover: $out"
echo "$out"
inv=$(sed -n 's|^  https://[^/]*/#/invite/\([A-Za-z0-9_-]*\)$|\1|p' <<<"$out")
[ -n "$inv" ] || fail "no invitation link with the panel address"
# The command ran as the database's owner: nothing of the panel's is root's now.
owners=$(x sh -c 'stat -c %U /var/lib/hostashi/hostashi.db*' | sort -u)
[ "$owners" = hostashi ] || fail "database files owned by: $owners"
[ "$(api GET /api/v1/auth/me)" = 401 ] || fail "the old session still works: $(x cat /tmp/api.out)"
cookie=""
[ "$(api POST /api/v1/invitations/accept "{\"token\":\"$inv\",\"password\":\"$pw-new\"}")" = 200 ] || fail "accept: $(x cat /tmp/api.out)"
x grep -q '"secondFactorRequired":true' /tmp/api.out || fail "recovered admin without the second factor demand: $(x cat /tmp/api.out)"

echo "PASS: installer test on $IMG"
