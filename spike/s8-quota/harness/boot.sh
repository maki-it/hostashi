#!/bin/bash
# usage: boot.sh image logfile [extra kernel cmdline]   (kernel+initrd are taken from the image's /boot, like GRUB would)
img=$1; log=$2; extra=${3:-}
m=$(mktemp -d); mount -o loop,ro $img $m; k=$(mktemp -d)
cp $(ls $m/boot/vmlinuz-* | tail -1) $k/vmlinuz; cp $(ls $m/boot/initrd.img-* | tail -1) $k/initrd.img; umount $m; rmdir $m
timeout 1200 qemu-system-x86_64 -machine accel=tcg -cpu max -m 2G -smp 4 -nographic -no-reboot \
 -kernel $k/vmlinuz -initrd $k/initrd.img \
 -append "root=LABEL=rootfs rw console=ttyS0 systemd.show_status=0 $extra" \
 -drive file=$img,format=raw,if=virtio -serial file:$log -monitor none -display none
rm -rf $k
