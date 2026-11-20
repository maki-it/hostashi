#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests the firewall step of packaging/install.sh (ADR-0033) in a systemd
# container with a network namespace of its own, so the rules never touch
# the host (container-test.sh shares the host's network and passes
# --no-firewall). From the host it checks that SSH, the web and the panel
# answer and other ports do not, that every sshd port and the port of the
# installer's own SSH session stay open, that --firewall-allow opens a
# port, that the panel's rules file (ADR-0034) narrows SSH and the panel and
# opens ports, and a broken one changes nothing, that a fail2ban table survives every apply and restart, that a
# second apply changes nothing, that a flushed table comes back, and that
# --no-firewall removes everything.
#
#   firewall-test.sh IMAGE      e.g. debian:trixie
#
# HOSTASHI_TEST_CA (optional): as in container-test.sh. KEEP=1 leaves the
# container running.
set -euo pipefail

IMG=$1
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
NAME=hostashi-fw-test-$$
BASE=hostashi-fw-base-$$

cleanup() {
  [ -n "${KEEP:-}" ] && return
  docker rm -f "$NAME" "$NAME-prep" >/dev/null 2>&1 || true; docker rmi -f "$BASE" >/dev/null 2>&1 || true
}
trap cleanup EXIT

step() { echo "=== $*"; }
fail() { echo "FAIL: $*" >&2; docker exec "$NAME" sh -c 'nft list ruleset; tail -n 30 /tmp/fw.log' >&2 || true; exit 1; }
x() { docker exec "$NAME" "$@"; }

step "prepare $IMG with systemd, nftables and sshd"
docker run -d --name "$NAME-prep" "$IMG" sleep infinity >/dev/null
if [ -n "${HOSTASHI_TEST_CA:-}" ]; then
  docker exec "$NAME-prep" mkdir -p /etc/ssl/certs
  docker cp "$HOSTASHI_TEST_CA" "$NAME-prep:/etc/ssl/certs/ca-certificates.crt"
  docker exec -e HTTPS_PROXY="${HTTPS_PROXY:-}" "$NAME-prep" bash -c '
    printf "Acquire::https::Proxy \"%s\";\nAcquire::https::CAInfo \"/etc/ssl/certs/ca-certificates.crt\";\n" "$HTTPS_PROXY" >/etc/apt/apt.conf.d/99test-proxy
    sed -i "s#http://#https://#g" /etc/apt/sources.list.d/*.sources'
fi
docker exec "$NAME-prep" bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends systemd systemd-sysv nftables openssh-server python3 iproute2 >/dev/null'
docker commit "$NAME-prep" "$BASE" >/dev/null
docker rm -f "$NAME-prep" >/dev/null

if [ "$(stat -fc %T /sys/fs/cgroup)" = cgroup2fs ]; then
  cg=(--cgroupns=private)
else
  cg=(--cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw)
fi
# The default bridge network: the container has its own network namespace.
docker run -d --name "$NAME" --privileged "${cg[@]}" --tmpfs /run --tmpfs /run/lock "$BASE" /lib/systemd/systemd >/dev/null
state=""
for _ in $(seq 1 60); do
  state=$(x systemctl is-system-running 2>/dev/null || true)
  case "$state" in running | degraded) break;; esac
  sleep 2
done
echo "systemd: $state"
IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$NAME")
[ -n "$IP" ] || fail "container has no address"
echo "container address: $IP"

x mkdir -p /opt/hostashi
# The installer's dirs step creates them on a real server.
x install -d -m 0750 /etc/hostashi
x install -d -m 0700 /var/lib/hostashi-agent
docker cp "$HERE/." "$NAME:/opt/hostashi/"

# sshd on 22 and 2222; TCP listeners on the web, panel and two other ports.
# Ubuntu 26.04 starts sshd through ssh.socket, whose ports a generator
# takes from sshd_config at daemon-reload; Debian 13 runs ssh.service.
x sh -c 'printf "Port 22\nPort 2222\n" >/etc/ssh/sshd_config.d/ports.conf && mkdir -p /run/sshd && systemctl daemon-reload &&
  if systemctl is-enabled --quiet ssh.socket 2>/dev/null; then systemctl stop ssh.service; systemctl restart ssh.socket; else systemctl restart ssh.service; fi'
for p in 80 443 8443 3000 5000 6000 2200; do
  x systemd-run --quiet --unit="listen-$p" python3 -m http.server --bind 0.0.0.0 "$p"
done
sleep 2

# open PORT: true if a TCP connection from the host to the container succeeds.
open() { timeout 3 bash -c "exec 3<>/dev/tcp/$IP/$1" 2>/dev/null; }
must_open() { local p; for p in "$@"; do open "$p" || fail "port $p is closed"; done; }
must_closed() { local p; for p in "$@"; do if open "$p"; then fail "port $p is open"; fi; done; }

# fw ARGS...: run only the firewall step, with install.sh's variables set
# by ARGS (bash assignments). SSH_CONNECTION stands in for the admin's
# session on port 2200, which sshd -T does not name.
fw() {
  # shellcheck disable=SC2016 # expanded inside the container
  x env SSH_CONNECTION="198.51.100.7 50000 $IP 2200" bash -c '
    set -u
    HOSTASHI_LIB=1 . /opt/hostashi/install.sh
    LOG=/tmp/fw.log MODE=apply STEPS=firewall PANEL_PORT=8443
    eval "$1"
    run_steps' fw "${1:-}"
}

step "before the firewall every port answers"
must_open 22 2222 2200 80 443 8443 3000 5000

step "invalid --firewall-allow values are refused by preflight"
for bad in 0/tcp 65536/tcp 22/icmp 22 abc/udp; do
  if out=$(x /opt/hostashi/install.sh plan --firewall-allow "$bad" 2>&1); then fail "--firewall-allow $bad accepted"; fi
  grep -q "firewall-allow '$bad'" <<<"$out" || fail "no message for --firewall-allow $bad: $out"
done
if x /opt/hostashi/install.sh plan --no-firewall --firewall-allow 3000/tcp >/dev/null 2>&1; then fail "--no-firewall with --firewall-allow accepted"; fi

step "a fail2ban table is there before the firewall"
x nft add table inet f2b-table
x nft add chain inet f2b-table f2b-chain

step "apply"
out=$(fw) || fail "firewall step failed: $out"
echo "$out"
grep -q '\[apply\]   firewall' <<<"$out" || fail "step did not apply"
x systemctl is-enabled --quiet hostashi-firewall.service || fail "unit not enabled"
x systemctl is-active --quiet hostashi-firewall.service || fail "unit not active"
[ "$(x stat -c '%a %U:%G' /etc/hostashi/firewall.nft)" = "644 root:root" ] || fail "rules file has the wrong owner or mode"
x grep -q 'tcp dport { 22, 80, 443, 2200, 2222, 8443 } accept' /etc/hostashi/firewall.nft || fail "TCP ports: $(x grep 'tcp dport' /etc/hostashi/firewall.nft)"
x grep -q 'udp dport { 443, 8443 } accept' /etc/hostashi/firewall.nft || fail "UDP ports: $(x grep 'udp dport' /etc/hostashi/firewall.nft)"
must_open 22 2222 2200 80 443 8443
must_closed 3000 5000
x nft list table inet f2b-table >/dev/null || fail "fail2ban's table is gone"

step "second apply changes nothing"
out=$(fw) || fail "second run failed: $out"
grep -q '\[ok\]      firewall' <<<"$out" || fail "second run changed something: $out"

step "restart keeps the rules and the fail2ban table"
x systemctl restart hostashi-firewall.service
must_open 22 80
must_closed 3000
x nft list table inet f2b-table >/dev/null || fail "fail2ban's table is gone after a restart"

step "a flushed ruleset is repaired"
x nft delete table inet hostashi
must_open 3000
out=$(fw) || fail "repair failed: $out"
grep -q '\[apply\]   firewall: table inet hostashi not loaded' <<<"$out" || fail "missing table not noticed: $out"
must_closed 3000

step "the panel's rules: allowlists narrow SSH and the panel, opened ports add (ADR-0034)"
[ "$(x stat -c '%a %U:%G' /var/lib/hostashi-agent/firewall/panel.nft)" = "600 root:root" ] || fail "panel file has the wrong owner or mode"
x grep -q 'jump panel' /etc/hostashi/firewall.nft || fail "base file does not jump to the panel chain"
x grep -Eq 'elements = \{ 22, 2200, 2222 \}' /etc/hostashi/firewall.nft || fail "ssh_ports: $(x grep -A2 'set ssh_ports' /etc/hostashi/firewall.nft)"
# The agent renders this file (internal/firewall, TestRenderExample). The
# host (172.16.0.0/12) is outside the SSH allowlist and inside the panel's.
docker cp "$HERE/../internal/firewall/testdata/example.nft" "$NAME:/var/lib/hostashi-agent/firewall/panel.nft"
x systemctl reload hostashi-firewall.service || fail "panel rules do not load"
must_closed 22 2222 2200 5000
must_open 80 443 8443 3000 6000
x nft list table inet f2b-table >/dev/null || fail "fail2ban's table is gone after the panel's rules"
out=$(fw) || fail "installer run with panel rules failed: $out"
grep -q '\[ok\]      firewall' <<<"$out" || fail "installer run changed something with panel rules: $out"
x grep -q 'ssh_allow4' /var/lib/hostashi-agent/firewall/panel.nft || fail "the installer replaced the panel's rules"
must_closed 22

step "a panel file that does not load changes nothing"
x sh -c 'printf "table inet hostashi { chain panel { tcp dport 22 bogus } }\n" >/var/lib/hostashi-agent/firewall/panel.nft'
if x systemctl reload hostashi-firewall.service 2>/dev/null; then fail "a broken panel file loaded"; fi
must_closed 22 5000
must_open 80 8443 3000
x sh -c 'printf "# empty\n" >/var/lib/hostashi-agent/firewall/panel.nft'
x systemctl reload hostashi-firewall.service || fail "empty panel file does not load"
must_open 22 2222 2200
must_closed 3000 5000 6000

step "--firewall-allow opens a port, a run without it closes it again"
out=$(fw 'FW_ALLOW=(3000/tcp 5353/udp)') || fail "allow failed: $out"
x grep -q 'tcp dport { 22, 80, 443, 2200, 2222, 3000, 8443 } accept' /etc/hostashi/firewall.nft || fail "3000/tcp not in the rules"
x grep -q 'udp dport { 443, 5353, 8443 } accept' /etc/hostashi/firewall.nft || fail "5353/udp not in the rules"
must_open 3000
must_closed 5000
fw >/dev/null || fail "run without --firewall-allow failed"
must_closed 3000

step "without any SSH port the step refuses and changes nothing"
x sh -c 'mv /usr/sbin/sshd /usr/sbin/sshd.off'
# shellcheck disable=SC2016 # expanded inside the container
if out=$(x env -u SSH_CONNECTION bash -c 'HOSTASHI_LIB=1 . /opt/hostashi/install.sh; LOG=/tmp/fw.log MODE=apply STEPS=firewall PANEL_PORT=8443 FW_ALLOW=(3000/tcp); ss() { :; }; run_steps'); then
  fail "step ran without an SSH port: $out"
fi
x sh -c 'mv /usr/sbin/sshd.off /usr/sbin/sshd'
must_closed 3000
must_open 22

step "--no-firewall removes table, rules and unit"
out=$(fw 'NO_FIREWALL=yes') || fail "--no-firewall failed: $out"
x test ! -e /etc/hostashi/firewall.nft || fail "rules file left"
x test ! -e /etc/systemd/system/hostashi-firewall.service || fail "unit left"
if x nft list table inet hostashi >/dev/null 2>&1; then fail "table left"; fi
x nft list table inet f2b-table >/dev/null || fail "--no-firewall removed fail2ban's table"
must_open 3000 5000
out=$(fw 'NO_FIREWALL=yes') || fail "second --no-firewall run failed"
grep -q '\[ok\]      firewall' <<<"$out" || fail "second --no-firewall run changed something: $out"

echo "firewall test passed"
