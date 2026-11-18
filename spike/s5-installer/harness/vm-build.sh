#!/bin/bash
# usage: vm-build.sh <workdir>   -> <workdir>/debian13.img (8 GiB raw, ext4 root WITHOUT quota features, like a cloud image)
# Builds a minimal Debian 13 amd64 system with debootstrap and installs the S5 test runner as a boot service.
set -eu
W=$1; HERE=$(cd "$(dirname "$0")/.." && pwd); mkdir -p "$W"; cd "$W"
[ -d root ] || debootstrap --variant=minbase --include=linux-image-amd64,systemd-sysv,udev,iproute2,ca-certificates,e2fsprogs,initramfs-tools,kmod,procps,curl,netbase,dbus trixie root http://deb.debian.org/debian
R=$W/root
echo s5-vm > $R/etc/hostname
printf 'LABEL=rootfs / ext4 errors=remount-ro 0 1\n' > $R/etc/fstab
mkdir -p $R/etc/systemd/network
printf '[Match]\nName=en*\n[Network]\nDHCP=yes\n' > $R/etc/systemd/network/50-dhcp.network
chroot $R systemctl enable systemd-networkd systemd-resolved >/dev/null 2>&1 || chroot $R systemctl enable systemd-networkd
echo 'nameserver 10.0.2.3' > $R/etc/resolv.conf
# Sandbox only (TLS-intercepting egress proxy reachable at the QEMU host address 10.0.2.2). A real server needs none of this.
if [ -n "${HTTPS_PROXY:-}" ]; then
  PORT=${HTTPS_PROXY##*:}; echo "Acquire::https::Proxy \"http://10.0.2.2:$PORT\";" > $R/etc/apt/apt.conf.d/99sandbox-proxy
  echo "HTTPS_PROXY=http://10.0.2.2:$PORT" > $R/etc/s5-proxy.env
  cp /root/.ccr/agent-proxy-ca.crt $R/usr/local/share/ca-certificates/sandbox-proxy.crt; chroot $R update-ca-certificates >/dev/null
else : > $R/etc/s5-proxy.env; fi
mkdir -p $R/opt/s5; cp "$HERE/install.sh" "$HERE/quota-setup.sh" "$HERE/harness/snapshot.sh" "$HERE/harness/verify.sh" "$HERE/harness/vm-runner.sh" $R/opt/s5/
cp "$HERE/harness/hostashi-stub-amd64" $R/opt/s5/hostashi-stub; chmod +x $R/opt/s5/*
echo 1 > $R/opt/s5/stage
cat > $R/etc/systemd/system/s5.service <<'U'
[Unit]
Description=S5 test runner
After=multi-user.target network-online.target
Wants=network-online.target
[Service]
Type=oneshot
EnvironmentFile=/etc/s5-proxy.env
ExecStart=/opt/s5/vm-runner.sh
StandardOutput=journal+console
StandardError=journal+console
TimeoutStartSec=0
[Install]
WantedBy=multi-user.target
U
chroot $R systemctl enable s5.service systemd-networkd-wait-online.service >/dev/null 2>&1
rm -f debian13.img; truncate -s 8G debian13.img
mkfs.ext4 -q -L rootfs -d $R debian13.img    # plain mkfs: no quota/project features, as on a stock cloud image
echo "built $W/debian13.img"
