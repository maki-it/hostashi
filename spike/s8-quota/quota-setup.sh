#!/bin/bash
# THROWAWAY SPIKE S8 prototype. Not production code (production = typed Go agent operation).
# Enables ext4 project quota on the root filesystem of a fresh Debian/Ubuntu server.
#   quota-setup.sh detect          machine-readable facts, changes nothing
#   quota-setup.sh plan            human-readable preview, changes nothing
#   quota-setup.sh apply --yes     applies the plan (needs a reboot afterwards)
# Exit codes: 0 ok / nothing to do, 2 unsafe or unsupported (server left unchanged), 3 failed and rolled back.
set -u
STATE=${HOSTASHI_STATE:-/var/lib/hostashi}
FSTAB=${HOSTASHI_FSTAB:-/etc/fstab}
HOOK_DIR=/etc/initramfs-tools
log() { echo "quota-setup: $*" >&2; }
die_unsafe() { log "NOT SAFE: $*"; log "server left unchanged"; exit 2; }

detect() {
  SRC=$(findmnt -no SOURCE /) ; FSTYPE=$(findmnt -no FSTYPE /); OPTS=$(findmnt -no OPTIONS /)
  DEV=$(readlink -f "$SRC" 2>/dev/null || echo "$SRC")
  FEATURES=""; INODE=0
  if [ "$FSTYPE" = ext4 ] && [ -b "$DEV" ]; then
    FEATURES=$(dumpe2fs -h "$DEV" 2>/dev/null | sed -n 's/^Filesystem features: *//p')
    INODE=$(dumpe2fs -h "$DEV" 2>/dev/null | sed -n 's/^Inode size: *//p')
  fi
  HAS_FEATURES=no; case " $FEATURES " in *" project "*) case " $FEATURES " in *" quota "*) HAS_FEATURES=yes;; esac;; esac
  MOUNT_ACTIVE=no; case ",$OPTS," in *,prjquota,*) MOUNT_ACTIVE=yes;; esac
  KMOD=no
  if grep -q 'quota_v2' "/lib/modules/$(uname -r)/modules.builtin" 2>/dev/null || modinfo quota_v2 >/dev/null 2>&1; then KMOD=yes; fi
  FSTAB_HAS=no
  awk '$2=="/" && $1 !~ /^#/ {print $4}' "$FSTAB" | grep -q '\(^\|,\)prjquota\(,\|$\)' && FSTAB_HAS=yes
  DISTRO=$(. /etc/os-release; echo "$ID-$VERSION_ID")
}

report() { cat <<R
distro=$DISTRO kernel=$(uname -r)
root_source=$SRC device=$DEV fstype=$FSTYPE inode_size=$INODE
quota_v2_kernel_support=$KMOD
fs_features_project_and_quota=$HAS_FEATURES
mounted_with_prjquota=$MOUNT_ACTIVE fstab_has_prjquota=$FSTAB_HAS
R
}

check_safe() {
  [ "$(id -u)" = 0 ] || die_unsafe "must run as root"
  [ "$FSTYPE" = ext4 ] || die_unsafe "root filesystem is '$FSTYPE', only ext4 is implemented in this spike"
  [ -b "$DEV" ] || die_unsafe "cannot resolve root device from '$SRC'"
  [ "${INODE:-0}" -ge 256 ] 2>/dev/null || die_unsafe "inode size $INODE < 256, project quota impossible without mkfs"
  [ "$KMOD" = yes ] || die_unsafe "kernel $(uname -r) has no quota_v2 (Ubuntu: apt install linux-modules-extra-$(uname -r))"
  command -v tune2fs >/dev/null && command -v e2fsck >/dev/null || die_unsafe "e2fsprogs missing"
  command -v update-initramfs >/dev/null || die_unsafe "no initramfs-tools (not Debian/Ubuntu?)"
  [ "$(awk '$2=="/" && $1 !~ /^#/' "$FSTAB" | wc -l)" = 1 ] || die_unsafe "expected exactly one root entry in $FSTAB"
}

plan() {
  if [ "$HAS_FEATURES" = yes ] && [ "$MOUNT_ACTIVE" = yes ]; then echo "Nothing to do: project quota already active on $DEV."; return 0; fi
  echo "Plan for $DEV (ext4):"
  [ "$HAS_FEATURES" = yes ] && echo " - fs features project+quota already set (skip)" || echo " - at next boot, BEFORE root is mounted: e2fsck -fp + tune2fs -O project,quota (initramfs hook)"
  [ "$FSTAB_HAS" = yes ] && echo " - $FSTAB already has prjquota on / (skip)" || echo " - back up $FSTAB, add 'prjquota' to the options of /"
  echo " - add kernel option rootflags=prjquota via GRUB drop-in (only if update-grub exists)"
  echo " - install + enable hostashi-quota-enforce.service (quotaon -P /; after boot ext4 only counts usage until this runs)"
  echo " - rebuild initramfs"
  echo " - REBOOT REQUIRED (one reboot, a few seconds longer because of fsck)"
}

apply() {
  [ "$HAS_FEATURES" = yes ] && [ "$MOUNT_ACTIVE" = yes ] && [ -e /etc/systemd/system/hostashi-quota-enforce.service ] && { echo "Nothing to do."; return 0; }
  ts=$(date +%Y%m%d-%H%M%S); mkdir -p "$STATE/backup"
  cp -p "$FSTAB" "$STATE/backup/fstab.$ts" || die_unsafe "cannot back up fstab"
  rollback() { log "failure, rolling back"; cp -p "$STATE/backup/fstab.$ts" "$FSTAB"; systemctl disable hostashi-quota-enforce.service >/dev/null 2>&1; rm -f /etc/systemd/system/hostashi-quota-enforce.service $HOOK_DIR/hooks/hostashi-quota $HOOK_DIR/scripts/local-premount/hostashi-quota /etc/default/grub.d/hostashi-quota.cfg; update-initramfs -u >/dev/null 2>&1; exit 3; }
  if [ "$FSTAB_HAS" = no ]; then
    awk -v OFS='\t' '$2=="/" && $1 !~ /^#/ { $4=$4",prjquota" } {print}' "$FSTAB" > "$FSTAB.new" || rollback
    mv "$FSTAB.new" "$FSTAB"
  fi
  findmnt --verify --fstab >/dev/null 2>&1 || { log "findmnt --verify failed"; rollback; }
  if [ "$HAS_FEATURES" = no ]; then
    DEVREF=$(blkid -s UUID -o value "$DEV" | sed 's|^|/dev/disk/by-uuid/|'); [ "$DEVREF" != /dev/disk/by-uuid/ ] || DEVREF=$DEV
    mkdir -p $HOOK_DIR/hooks $HOOK_DIR/scripts/local-premount
    cat > $HOOK_DIR/hooks/hostashi-quota <<H
#!/bin/sh
[ "\$1" = prereqs ] && { echo; exit 0; }
. /usr/share/initramfs-tools/hook-functions
copy_exec /sbin/tune2fs; copy_exec /sbin/e2fsck; copy_exec /sbin/dumpe2fs
# Without this the root mount FAILS once the quota feature is set (verified, Debian 13 initramfs lacks quota_v2).
manual_add_modules quota_v2
mkdir -p "\$DESTDIR/etc"; echo 'DEVICE=$DEVREF' > "\$DESTDIR/etc/hostashi-quota.conf"
H
    cat > $HOOK_DIR/scripts/local-premount/hostashi-quota <<'S'
#!/bin/sh
[ "$1" = prereqs ] && { echo udev; exit 0; }
. /etc/hostashi-quota.conf
# Never block boot: every failure path exits 0 and leaves the fs as it was.
[ -b "$DEVICE" ] || exit 0
case "$(dumpe2fs -h "$DEVICE" 2>/dev/null)" in *"Filesystem features:"*" project"*) exit 0;; esac
# Safety order: the module must load BEFORE the feature is set, else the root mount fails.
modprobe quota_v2 2>/dev/null || { echo "hostashi-quota: quota_v2 not loadable, leaving fs unchanged" >/dev/console; exit 0; }
echo "hostashi-quota: enabling project quota on $DEVICE" >/dev/console
e2fsck -fp "$DEVICE" >/dev/console 2>&1
rc=$?; [ $rc -le 1 ] || { echo "hostashi-quota: e2fsck rc=$rc, skipping" >/dev/console; exit 0; }
tune2fs -O project,quota "$DEVICE" >/dev/console 2>&1 && echo "hostashi-quota: done" >/dev/console
exit 0
S
    chmod +x $HOOK_DIR/hooks/hostashi-quota $HOOK_DIR/scripts/local-premount/hostashi-quota
  fi
  cat > /etc/systemd/system/hostashi-quota-enforce.service <<'U'
[Unit]
Description=Hostashi: enable project quota limit enforcement on /
ConditionPathExists=/sbin/quotaon
After=local-fs.target
Before=multi-user.target
[Service]
Type=oneshot
RemainAfterExit=yes
# '-': ignore a non-zero exit (seen on Hetzner Debian 13 cloud kernel, reason not yet read from the journal)
ExecStart=-/usr/sbin/quotaon -P /
[Install]
WantedBy=multi-user.target
U
  systemctl enable hostashi-quota-enforce.service >/dev/null 2>&1 || { log "systemctl enable failed"; rollback; }
  if command -v update-grub >/dev/null; then
    mkdir -p /etc/default/grub.d
    echo 'GRUB_CMDLINE_LINUX="$GRUB_CMDLINE_LINUX rootflags=prjquota"' > /etc/default/grub.d/hostashi-quota.cfg
    update-grub >/dev/null 2>&1 || { log "update-grub failed"; rollback; }
  fi
  update-initramfs -u >/dev/null 2>&1 || { log "update-initramfs failed"; rollback; }
  # Verify BEFORE asking for a reboot: the new initramfs must contain the module and the hook.
  initrd=/boot/initrd.img-$(uname -r)
  lsinitramfs "$initrd" | grep -q 'quota_v2' && lsinitramfs "$initrd" | grep -q 'local-premount/hostashi-quota' || { log "initramfs verification failed"; rollback; }
  echo "Prepared. Reboot to activate (fstab backup: $STATE/backup/fstab.$ts)."
}

main() {
  detect
  case "${1:-}" in
    detect) report ;;
    plan) report; check_safe; plan ;;
    apply) [ "${2:-}" = --yes ] || { log "refusing to change anything without --yes (run 'plan' first)"; exit 2; }
           check_safe; apply ;;
    *) echo "usage: $0 detect|plan|apply --yes" >&2; exit 64 ;;
  esac
}
main "$@"
