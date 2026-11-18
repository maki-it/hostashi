#!/bin/bash
# Runs the agent tests (incl. the real ext4 project quota test) inside a Debian 13 QEMU VM with the stock
# Debian kernel, because the usual sandbox kernels have no quota_v2. Needs root, qemu-system-x86,
# debootstrap and network access to deb.debian.org. Nothing outside $WORK is touched. Takes about 15 minutes
# for debootstrap plus about 1 minute emulated boot and test (TCG, no KVM).
set -euo pipefail
WORK=${WORK:-/tmp/s4vm}
HERE=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$WORK"; cd "$WORK"
if [ ! -d root/boot ]; then
  debootstrap --variant=minbase \
    --include=linux-image-amd64,quota,e2fsprogs,kmod,initramfs-tools,util-linux,mount,procps \
    trixie root http://deb.debian.org/debian
fi
(cd "$HERE" && CGO_ENABLED=0 go test -c -o "$WORK/agent.test" ./agent)
mkdir -p root/opt && cp agent.test root/opt/agent.test
cat > root/sbin/s4init <<'INIT'
#!/bin/bash
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
mount -t proc proc /proc; mount -t sysfs sys /sys; mount -t devtmpfs dev /dev; mount -t tmpfs tmp /tmp
exec >/dev/ttyS0 2>&1
echo "=== S4 VM: kernel $(uname -r)"
modprobe quota_v2 && echo "quota_v2 loaded"
S4_QUOTA_DEV=/dev/vdb /opt/agent.test -test.v -test.count=1 -test.run 'Ext4|OpenRoot|Unmounted|EndToEnd'
echo "=== S4 DONE rc=$?"
echo o > /proc/sysrq-trigger   # power off (no poweroff binary in minbase)
INIT
chmod +x root/sbin/s4init
rm -f rootfs.img disk2.img boot.log
truncate -s 1500M rootfs.img; mkfs.ext4 -q -L rootfs -d root rootfs.img; truncate -s 300M disk2.img
m=$(mktemp -d); mount -o loop,ro rootfs.img "$m"; mkdir -p k
cp "$(ls $m/boot/vmlinuz-* | tail -1)" k/vmlinuz; cp "$(ls $m/boot/initrd.img-* | tail -1)" k/initrd.img; umount "$m"
timeout 1500 qemu-system-x86_64 -machine accel=tcg -cpu max -m 2G -smp 4 -nographic -no-reboot \
  -kernel k/vmlinuz -initrd k/initrd.img \
  -append "root=LABEL=rootfs rw console=ttyS0 init=/sbin/s4init sysrq_always_enabled=1" \
  -drive file=rootfs.img,format=raw,if=virtio -drive file=disk2.img,format=raw,if=virtio \
  -serial file:boot.log -monitor none -display none || true
grep -E "^(=== |--- |PASS|FAIL|ok )|quota_test" boot.log
