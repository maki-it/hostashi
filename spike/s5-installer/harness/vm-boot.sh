#!/bin/bash
# usage: vm-boot.sh <image> <logfile> [boots=2]
# Boots the image N times under QEMU (TCG, no KVM in the sandbox). Kernel and initrd are re-read from the image's
# /boot before every boot, like GRUB would, so an initramfs rebuilt by the installer takes effect.
img=$1; log=$2; n=${3:-2}
for i in $(seq 1 $n); do
  m=$(mktemp -d); mount -o loop,ro "$img" $m; k=$(mktemp -d)
  cp $(ls $m/boot/vmlinuz-* | tail -1) $k/vmlinuz; cp $(ls $m/boot/initrd.img-* | tail -1) $k/initrd.img; umount $m; rmdir $m
  echo "##### boot $i $(date -u +%T)" >> "$log"
  timeout 5400 qemu-system-x86_64 -machine q35,accel=tcg -cpu max -m 2G -smp 4 -nographic -no-reboot \
    -kernel $k/vmlinuz -initrd $k/initrd.img -append "root=LABEL=rootfs rw console=ttyS0 systemd.show_status=0" \
    -drive file="$img",format=raw,if=virtio -netdev user,id=n0 -device virtio-net-pci,netdev=n0 \
    -serial file:"$log.boot$i" -monitor none -display none
  echo "##### qemu exit $? $(date -u +%T)" >> "$log"; rm -rf $k
done
