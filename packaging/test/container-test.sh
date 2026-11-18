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
set -euo pipefail

IMG=$1
BINARY=$(realpath "$2")
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
NAME=hostashi-install-test-$$
BASE=hostashi-test-base-$$

cleanup() { docker rm -f "$NAME" "$NAME-prep" >/dev/null 2>&1 || true; docker rmi -f "$BASE" >/dev/null 2>&1 || true; }
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
for u in hostashi-agent hostashi; do
  [ "$(x systemctl is-active $u.service)" = active ] || fail "$u is not running"
done
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

echo "PASS: installer test on $IMG"
