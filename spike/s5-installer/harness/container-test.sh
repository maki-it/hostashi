#!/bin/bash
# usage: container-test.sh <debian:trixie|ubuntu:24.04> <name> [platform: linux/amd64|linux/arm64]
# Starts a systemd container, runs the installer twice, compares fingerprints, then checks the result.
# NOTE: a container shares the host kernel, so quota and reboot are NOT covered here (see vm-test.sh).
set -u
IMG=$1; NAME=$2; PLAT=${3:-linux/amd64}; [ "$PLAT" = --start-only ] && PLAT=linux/amd64; ARCH=${PLAT#linux/}
HERE=$(cd "$(dirname "$0")/.." && pwd)
docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" --platform "$PLAT" --privileged --network host --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock "$IMG" \
  bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv iproute2 procps ca-certificates >/dev/null 2>&1; exec /lib/systemd/systemd' >/dev/null
for i in $(seq 1 600); do st=$(docker exec "$NAME" systemctl is-system-running 2>/dev/null); case "$st" in running|degraded) break;; esac; sleep 2; done
echo "container $NAME ($IMG $PLAT) systemd: $st"
docker exec "$NAME" mkdir -p /opt/s5
# Sandbox only: the build sandbox forces HTTPS through a local TLS-intercepting proxy (see /root/.ccr/README.md).
# Inside the container we trust its CA bundle and point apt and curl at the proxy. A real server needs none of this.
PX=""
if [ -n "${HTTPS_PROXY:-}" ] && [ -f /root/.ccr/ca-bundle.crt ]; then
  docker cp /root/.ccr/ca-bundle.crt "$NAME:/etc/ssl/certs/ca-certificates.crt"
  docker exec "$NAME" bash -c "echo 'Acquire::https::Proxy \"$HTTPS_PROXY\";' > /etc/apt/apt.conf.d/99sandbox-proxy"
  PX="-e HTTPS_PROXY=$HTTPS_PROXY -e CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt"
fi
if [ "$ARCH" != amd64 ]; then
  # Harness only: under qemu-user, systemd cannot set up mount namespaces (status=226/NAMESPACE), so MariaDB and the
  # sandboxed hostashi units would fail. This drop-in turns the sandboxing off for ALL services in the emulated container.
  docker exec "$NAME" bash -c 'mkdir -p /etc/systemd/system/service.d && printf "[Service]\nPrivateTmp=no\nProtectSystem=no\nProtectHome=no\nPrivateDevices=no\nProtectKernelTunables=no\nProtectKernelModules=no\nProtectControlGroups=no\nReadWritePaths=\nReadOnlyPaths=\nPrivateNetwork=no\nProtectHostname=no\nProtectKernelLogs=no\nProtectClock=no\nPrivateUsers=no\n" > /etc/systemd/system/service.d/zz-s5-emulation.conf && chmod 644 /etc/systemd/system/service.d/zz-s5-emulation.conf && systemctl daemon-reload'
fi
docker cp "$HERE/install.sh" "$NAME:/opt/s5/"; docker cp "$HERE/quota-setup.sh" "$NAME:/opt/s5/"; docker cp "$HERE/harness/snapshot.sh" "$NAME:/opt/s5/"
docker cp "$HERE/harness/hostashi-stub-$ARCH" "$NAME:/opt/s5/hostashi-stub"
x() { docker exec $PX "$NAME" "$@"; }
[ "${4:-}" = --start-only ] || [ "${3:-}" = --start-only ] && exit 0
echo "=== plan (fresh)"; x /opt/s5/install.sh plan --no-quota --binary /opt/s5/hostashi-stub; echo "exit=$?"
echo "=== apply #1"; t=$(date +%s); x /opt/s5/install.sh apply --yes --no-quota --binary /opt/s5/hostashi-stub; echo "exit=$? ($(( $(date +%s)-t )) s)"
x /opt/s5/snapshot.sh > /tmp/$NAME.snap1
echo "=== apply #2 (must change nothing)"; t=$(date +%s); x /opt/s5/install.sh apply --yes --no-quota --binary /opt/s5/hostashi-stub; echo "exit=$? ($(( $(date +%s)-t )) s)"
x /opt/s5/snapshot.sh > /tmp/$NAME.snap2
if diff /tmp/$NAME.snap1 /tmp/$NAME.snap2 >/tmp/$NAME.diff; then echo "FINGERPRINT: identical ($(wc -l </tmp/$NAME.snap1) lines)"; else echo "FINGERPRINT: DIFFERS"; cat /tmp/$NAME.diff | head -30; fi
echo "=== plan after apply"; x /opt/s5/install.sh plan --no-quota --binary /opt/s5/hostashi-stub; echo "exit=$?"
