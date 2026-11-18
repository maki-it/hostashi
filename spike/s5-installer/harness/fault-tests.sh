#!/bin/bash
# usage: fault-tests.sh <debian:trixie|ubuntu:24.04>
# Refusal and recovery cases. Each case starts from a fresh systemd container. Expected exit codes in brackets.
set -u
IMG=$1; HERE=$(cd "$(dirname "$0")" && pwd); N=t-s5-fault; PASS=0; FAILN=0
fresh() { "$HERE/container-test.sh" "$IMG" $N --start-only >/dev/null 2>&1; }
PX="-e HTTPS_PROXY=${HTTPS_PROXY:-} -e CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt"
x() { docker exec $PX $N "$@"; }
expect() { local want=$1 name=$2; shift 2; out=$(x "$@" 2>&1); rc=$?
  if [ $rc = $want ]; then PASS=$((PASS+1)); echo "PASS [$want] $name"; else FAILN=$((FAILN+1)); echo "FAIL [want $want got $rc] $name"; echo "$out" | tail -5; fi
  echo "$out" | grep -E 'REFUSED|FAIL|FAILED|changes=|Nothing' | head -3 | sed 's/^/       /'; }
unchanged() { x test ! -e /var/lib/hostashi-installer/state && x test ! -e /etc/apt/sources.list.d/nginx.list && echo "       ok: no state, no repo (server unchanged)" || { echo "       FAIL: server was changed"; FAILN=$((FAILN+1)); }; }
I=/opt/s5/install.sh; B="--no-quota --binary /opt/s5/hostashi-stub"

fresh
x bash -c 'printf "ID=debian\nVERSION_ID=12\nVERSION_CODENAME=bookworm\n" > /tmp/osr12; printf "ID=ubuntu\nVERSION_ID=26.04\nVERSION_CODENAME=resolute\n" > /tmp/osr26'
expect 2  "apply without --yes" $I apply $B; unchanged
expect 2  "Debian 12 refused" env HOSTASHI_OS_RELEASE=/tmp/osr12 $I apply --yes $B; unchanged
expect 2  "Ubuntu 26.04 refused without --allow-untested" env HOSTASHI_OS_RELEASE=/tmp/osr26 $I apply --yes $B; unchanged
expect 0  "Ubuntu 26.04 plan accepted with --allow-untested (plan only)" env HOSTASHI_OS_RELEASE=/tmp/osr26 $I plan --allow-untested $B
expect 2  "riscv64 refused" env HOSTASHI_ARCH=riscv64 $I apply --yes $B; unchanged
expect 2  "quota impossible on this root (container overlay) refused" $I apply --yes --binary /opt/s5/hostashi-stub; unchanged
expect 2  "missing --binary file refused" $I apply --yes --no-quota --binary /nonexistent; unchanged
x bash -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3-minimal >/dev/null 2>&1; (setsid nohup python3 -m http.server 80 >/dev/null 2>&1 &); sleep 3'
x ss -Hltn 'sport = :80' | grep -q . && echo "       setup: something listens on :80" || echo "       setup FAILED: nothing on :80"
expect 2  "port 80 in use refused" $I apply --yes $B; unchanged
x pkill -f http.server
x bash -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y -qq apache2 >/dev/null 2>&1; systemctl stop apache2'
expect 2  "not a fresh server (apache2 installed) refused" $I apply --yes $B; unchanged

fresh
expect 3  "nginx.org unreachable -> step fails" env HTTPS_PROXY=http://127.0.0.1:9 $I apply --yes $B
expect 0  "re-run after failure completes" $I apply --yes $B
expect 0  "re-run is a no-op" $I apply --yes $B
x bash -c 'echo "[mysqld]\nbind-address=0.0.0.0" > /etc/mysql/mariadb.conf.d/99-hostashi.cnf; systemctl restart mariadb'
expect 0  "tampered MariaDB drop-in is restored" $I apply --yes $B
x mariadb -NBe 'select @@bind_address' | sed 's/^/       bind_address now: /'
x bash -c 'gpg --batch --quick-gen-key --passphrase "" "evil <e@x>" >/dev/null 2>&1; gpg --export "evil" > /usr/share/keyrings/nginx-archive-keyring.gpg'
expect 0  "foreign key in nginx keyring is detected and replaced" $I apply --yes $B
x systemctl enable --now $(x bash -c "systemctl list-unit-files --no-legend 'php*-fpm.service' | awk '{print \$1}'") >/dev/null 2>&1
expect 0  "re-enabled distro php-fpm is disabled again" $I apply --yes $B
expect 0  "final run is a no-op" $I apply --yes $B
x $I apply --yes $B | tail -1 | sed 's/^/       /'
docker rm -f $N >/dev/null
echo "fault tests on $IMG: $PASS passed, $FAILN failed"; [ $FAILN = 0 ]
