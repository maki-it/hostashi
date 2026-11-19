#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# The stubs are bash scripts in single quotes, expanded when they run, and
# LOG and NO_QUOTA are read by the sourced installer functions.
# shellcheck disable=SC2016,SC2034
#
# Tests the quota step of packaging/install.sh (ADR-0015) without touching
# the system: the filesystem, kernel, initramfs, GRUB, systemd and apt tools
# are stubs in a temporary directory, and /etc is a temporary directory too.
# What it cannot cover (a real e2fsck/tune2fs in the initramfs and the
# reboot) was tested by hand in spike S8.
#
#   sudo packaging/test/quota-test.sh
#
# Needs root only for chown root:root and mknod (a fake root block device).
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
[ "$(id -u)" = 0 ] || { echo "run as root (chown, mknod)" >&2; exit 64; }

T=$(mktemp -d)
SHELLCHECK=$(command -v shellcheck)
# The host's own tools without its initramfs generators: a CI runner may
# have dracut installed, which would win the detection over the stubs.
SYS=$T/sys
mkdir -p "$SYS"
for d in /bin /usr/bin /sbin /usr/sbin; do
  for f in "$d"/*; do
    case "${f##*/}" in dracut | lsinitrd | lsinitramfs | update-initramfs | mkinitramfs) continue;; esac
    [ -e "$SYS/${f##*/}" ] || ln -s "$f" "$SYS/${f##*/}"
  done
done
trap 'rm -rf "$T"' EXIT
FAKE=$T/bin
FAILS=0 PASSES=0

ok()   { PASSES=$((PASSES + 1)); echo "ok   $*"; }
bad()  { FAILS=$((FAILS + 1)); echo "FAIL $*" >&2; }
check() { local d=$1; shift; if "$@"; then ok "$d"; else bad "$d"; fi; }
fails() { ! "$@"; }

# ------------------------------------------------------------- stubs
stub() { printf '#!/bin/bash\n%s\n' "$2" >"$FAKE/$1"; chmod 0755 "$FAKE/$1"; }

make_stubs() {
  rm -rf "$FAKE"; mkdir -p "$FAKE"
  stub uname 'echo 6.12.101+deb13-cloud-amd64'
  stub findmnt '
case "$*" in
  "-no SOURCE /") echo "$Q/dev/sda1";;
  "-no FSTYPE /") cat "$Q/fstype";;
  "-no OPTIONS /") cat "$Q/mountopts";;
  --verify*) [ ! -e "$Q/fail-verify" ];;
esac'
  stub dumpe2fs 'echo "Filesystem features:      $(cat "$Q/features")"; echo "Inode size:	          $(cat "$Q/inode")"'
  stub modinfo '[ -e "$Q/kmod" ]'
  stub blkid 'echo 0b9a1c2e-1111-2222-3333-444455556666'
  stub update-initramfs 'echo "update-initramfs $*" >>"$Q/calls"; [ ! -e "$Q/fail-initramfs" ]'
  stub lsinitramfs '
[ -e "$Q/initrd-no-module" ] || echo usr/lib/modules/6.12/kernel/fs/quota/quota_v2.ko.xz
[ ! -f "$E/initramfs-tools/scripts/local-premount/hostashi-quota" ] || echo scripts/local-premount/hostashi-quota
[ ! -f "$E/initramfs-tools/hooks/hostashi-quota" ] || echo etc/hostashi-quota.conf'
  stub update-grub 'echo update-grub >>"$Q/calls"'
  stub systemctl '
case "$1" in
  is-enabled) [ -e "$Q/enabled-$2" ] && echo enabled || { echo disabled; exit 1; };;
  enable) touch "$Q/enabled-$2";;
  disable) rm -f "$Q/enabled-$2";;
  start) echo "start $2" >>"$Q/calls";;
  daemon-reload) ;;
esac'
  stub apt-get 'case "$*" in *install*) for p in "$@"; do touch "$Q/pkg-$p"; done;; esac'
  stub dpkg-query '[ -e "$Q/pkg-${!#}" ] && echo "install ok installed"'
}

# fresh STATE: a stock Hetzner-like ext4 root without quota.
fresh() {
  rm -rf "${T:?}/q" "${T:?}/etc" "${T:?}/state" "${T:?}/boot" "${T:?}/dracut"
  mkdir -p "$T/q/dev" "$T/etc" "$T/state" "$T/boot" "$T/dracut"
  mknod "$T/q/dev/sda1" b 7 222
  echo ext4 >"$T/q/fstype"
  echo rw,relatime >"$T/q/mountopts"
  echo "has_journal ext_attr resize_inode dir_index filetype extent 64bit flex_bg sparse_super large_file huge_file dir_nlink extra_isize metadata_csum orphan_file" >"$T/q/features"
  echo 256 >"$T/q/inode"
  touch "$T/q/kmod"
  echo boot-1 >"$T/q/boot_id"
  touch "$T/boot/initrd.img-6.12.101+deb13-cloud-amd64"
  cat >"$T/etc/fstab" <<'F'
# /etc/fstab: static file system information.
UUID=0b9a1c2e-1111-2222-3333-444455556666 / ext4 discard,errors=remount-ro 0 1
UUID=AAAA-BBBB /boot/efi vfat umask=0077 0 1
F
  cp "$T/etc/fstab" "$T/fstab.orig"
  make_stubs
  load
}

# load sources the installer's functions with the test paths.
load() {
  export Q=$T/q E=$T/etc
  export PATH="$FAKE:$SYS"
  export HOSTASHI_ETC=$T/etc HOSTASHI_DRACUT_MODULES=$T/dracut HOSTASHI_STATE_DIR=$T/state HOSTASHI_BOOT_ID=$T/q/boot_id HOSTASHI_BOOT=$T/boot HOSTASHI_LIB=1
  # shellcheck source=packaging/install.sh
  . "$HERE/install.sh"
  set +e
  LOG=$T/log
  NO_QUOTA=no
}

reboot_into() { # features mountopts
  echo "$1" >"$T/q/features"; echo "$2" >"$T/q/mountopts"; echo "boot-$RANDOM" >"$T/q/boot_id"
}

no_quota_files() {
  [ ! -e "$T/etc/initramfs-tools/hooks/hostashi-quota" ] && [ ! -e "$T/etc/initramfs-tools/scripts/local-premount/hostashi-quota" ] &&
    [ ! -e "$T/etc/systemd/system/hostashi-quota-enforce.service" ] && [ ! -e "$T/etc/default/grub.d/hostashi-quota.cfg" ] &&
    [ ! -e "$T/dracut/90hostashi-quota" ] && [ ! -e "$T/etc/dracut.conf.d/90-hostashi-quota.conf" ] &&
    [ ! -e "$T/q/enabled-hostashi-quota-enforce.service" ] && ! grep -q quota_prepared_boot "$T/state/state" 2>/dev/null
}
fstab_unchanged() { cmp -s "$T/etc/fstab" "$T/fstab.orig"; }
mode_of() { stat -c '%a %U:%G' "$1"; }

# ------------------------------------------------------------- refusals
echo "--- unsupported systems change nothing"
fresh; echo overlay >"$T/q/fstype"; quota_detect
check "overlay root is unsupported" [ "$QUOTA" = unsupported ]
check "chk passes so the install goes on" chk_quota
check "reason names the filesystem" grep -q overlay <<<"$QUOTA_WHY"

fresh; rm "$T/q/kmod"; quota_detect
check "missing quota_v2 is unsupported" [ "$QUOTA" = unsupported ]
check "reason has the Ubuntu hint" grep -q linux-modules-extra-6.12.101+deb13-cloud-amd64 <<<"$QUOTA_WHY"

fresh; echo 128 >"$T/q/inode"; quota_detect
check "inode size 128 is unsupported" [ "$QUOTA" = unsupported ]

fresh; echo garbage >"$T/q/inode"; quota_detect
check "unreadable inode size is unsupported" [ "$QUOTA" = unsupported ]

fresh; rm "$FAKE/update-initramfs"; quota_detect
check "no initramfs generator is unsupported" [ "$QUOTA" = unsupported ]
check "reason names both generators" grep -q "initramfs-tools nor dracut" <<<"$QUOTA_WHY"

fresh; echo "/dev/sdb1 / ext4 defaults 0 1" >>"$T/etc/fstab"; quota_detect
check "two root entries in fstab are unsupported" [ "$QUOTA" = unsupported ]

fresh; NO_QUOTA=yes; quota_detect
check "--no-quota switches the step off" [ "$QUOTA" = off ]
check "chk passes with --no-quota" chk_quota
check "nothing was written" no_quota_files
check "fstab untouched" fstab_unchanged

# ------------------------------------------------------------- prepare
echo "--- a stock ext4 root is prepared for one reboot"
fresh
why=$(chk_quota); rc=$?
check "chk fails on a stock root" [ $rc = 1 ]
check "plan says a reboot is needed" grep -q "ONE reboot" <<<"$why"
check "do_quota succeeds" do_quota
check "fstab: prjquota added to / only" grep -Eq $'^UUID=0b9a1c2e-1111-2222-3333-444455556666\t/\text4\tdiscard,errors=remount-ro,prjquota\t0\t1$' "$T/etc/fstab"
check "fstab: other entries unchanged" grep -qx 'UUID=AAAA-BBBB /boot/efi vfat umask=0077 0 1' "$T/etc/fstab"
check "fstab: comment kept" grep -qx '# /etc/fstab: static file system information.' "$T/etc/fstab"
check "fstab: backup is the original" cmp -s "$(find "$T/state" -name "fstab.*" | head -n 1)" "$T/fstab.orig"
check "fstab mode stays 644" [ "$(stat -c %a "$T/etc/fstab")" = 644 ]
check "hook installed 755 root" [ "$(mode_of "$T/etc/initramfs-tools/hooks/hostashi-quota")" = "755 root:root" ]
check "premount script installed 755 root" [ "$(mode_of "$T/etc/initramfs-tools/scripts/local-premount/hostashi-quota")" = "755 root:root" ]
check "enforcement unit installed 644 root" [ "$(mode_of "$T/etc/systemd/system/hostashi-quota-enforce.service")" = "644 root:root" ]
check "enforcement unit enabled" [ -e "$T/q/enabled-hostashi-quota-enforce.service" ]
check "hook points at the UUID" grep -q "DEVICE=/dev/disk/by-uuid/0b9a1c2e-1111-2222-3333-444455556666" "$T/etc/initramfs-tools/hooks/hostashi-quota"
check "GRUB drop-in adds rootflags" grep -q 'rootflags=prjquota' "$T/etc/default/grub.d/hostashi-quota.cfg"
check "GRUB updated" grep -qx update-grub "$T/q/calls"
check "every initramfs rebuilt" grep -qx 'update-initramfs -u -k all' "$T/q/calls"
check "quota package installed" [ -e "$T/q/pkg-quota" ]
check "prepared boot recorded" grep -qx 'quota_prepared_boot=boot-1' "$T/state/state"
check "chk passes while the reboot is pending" chk_quota
check "state is pending" [ "$QUOTA" = pending ]
check "enforcement not started before the reboot" bash -c "! grep -q '^start' '$T/q/calls'"
for f in hooks/hostashi-quota scripts/local-premount/hostashi-quota; do
  check "$f is valid sh" sh -n "$T/etc/initramfs-tools/$f"
done

echo "--- the reboot activates it, a re-run changes nothing"
cp "$T/etc/fstab" "$T/fstab.after"
reboot_into "has_journal ext_attr dir_index filetype extent 64bit flex_bg sparse_super metadata_csum orphan_file quota project" "rw,relatime,prjquota"
check "chk passes after the reboot" chk_quota
check "state is active" [ "$QUOTA" = active ]
check "fstab not touched again" cmp -s "$T/etc/fstab" "$T/fstab.after"

echo "--- a reboot that did not set the features is reported, not retried"
fresh; do_quota >/dev/null 2>&1
reboot_into "has_journal ext_attr dir_index filetype extent 64bit" "rw,relatime"
why=$(chk_quota); rc=$?
check "chk fails" [ $rc = 1 ]
check "reason says prepared but not active" grep -q "prepared but is not active" <<<"$why"
: >"$T/q/calls"
check "do_quota refuses" fails do_quota
check "nothing rebuilt on the refusal" [ ! -s "$T/q/calls" ]
NO_QUOTA=yes
check "--no-quota gets past it" chk_quota
NO_QUOTA=no

echo "--- a root created with quota only gets the hook and enforcement"
fresh
echo "has_journal extent 64bit metadata_csum quota project" >"$T/q/features"
echo "rw,relatime,prjquota" >"$T/q/mountopts"
sed -i 's/errors=remount-ro/errors=remount-ro,prjquota/' "$T/etc/fstab"
why=$(chk_quota)
check "needed with a reason about enforcement" grep -q enforcement <<<"$why"
check "do_quota succeeds" do_quota
check "enforcement started now" grep -qx 'start hostashi-quota-enforce.service' "$T/q/calls"
check "no reboot recorded" bash -c "! grep -q quota_prepared_boot '$T/state/state' 2>/dev/null"
check "active right away" chk_quota
check "state is active" [ "$QUOTA" = active ]

# ------------------------------------------------------------- rollback
echo "--- failures roll back"
fresh; touch "$T/q/fail-initramfs"
check "do_quota fails when update-initramfs fails" fails do_quota
check "fstab restored" fstab_unchanged
check "no quota files left" no_quota_files

fresh; touch "$T/q/initrd-no-module"
check "do_quota fails when the initramfs lacks quota_v2" fails do_quota
check "fstab restored" fstab_unchanged
check "no quota files left" no_quota_files

fresh; touch "$T/q/fail-verify"
check "do_quota fails when findmnt rejects the new fstab" fails do_quota
check "fstab restored" fstab_unchanged
check "no quota files left" no_quota_files

fresh; rm "$T/boot/initrd.img-6.12.101+deb13-cloud-amd64"
check "do_quota fails without any initramfs" fails do_quota
check "fstab restored" fstab_unchanged

# ------------------------------------------------------------- dracut
# Ubuntu 26.04: dracut instead of initramfs-tools. Ubuntu's dracut package
# keeps an update-initramfs wrapper but has no lsinitramfs.
use_dracut() {
  rm -f "$FAKE/lsinitramfs"
  stub dracut 'echo "dracut $*" >>"$Q/calls"; [ ! -e "$Q/fail-initramfs" ]'
  stub lsinitrd '
[ -e "$Q/initrd-no-module" ] || echo "-rw-r--r--   1 root root  9000 Nov 19 12:00 usr/lib/modules/7.0.0-44-generic/kernel/fs/quota/quota_v2.ko.zst"
grep -qs "add_dracutmodules.*hostashi-quota" "$E/dracut.conf.d/90-hostashi-quota.conf" && [ -f "$HOSTASHI_DRACUT_MODULES/90hostashi-quota/module-setup.sh" ] || exit 0
echo "-rwxr--r--   1 root root   900 Nov 19 12:00 var/lib/dracut/hooks/pre-mount/10-hostashi-quota.sh"
echo "-rw-r--r--   1 root root    60 Nov 19 12:00 etc/hostashi-quota.conf"'
}

echo "--- dracut (Ubuntu 26.04) gets a module instead of initramfs-tools files"
fresh; use_dracut
check "detected as dracut" bash -c '. "$0"; quota_detect; [ "$Q_INITRD" = dracut ] && [ "$QUOTA" = needed ]' "$HERE/install.sh"
quota_detect
check "a stock root needs quota" [ "$QUOTA" = needed ]
check "do_quota succeeds" do_quota
D=$T/dracut/90hostashi-quota
check "module-setup installed 755 root" [ "$(mode_of "$D/module-setup.sh")" = "755 root:root" ]
check "hook installed 755 root" [ "$(mode_of "$D/hostashi-quota.sh")" = "755 root:root" ]
check "config installed 644 root" [ "$(mode_of "$T/etc/dracut.conf.d/90-hostashi-quota.conf")" = "644 root:root" ]
check "config adds the module" grep -qx 'add_dracutmodules+=" hostashi-quota "' "$T/etc/dracut.conf.d/90-hostashi-quota.conf"
check "config names the UUID" grep -qx 'hostashi_quota_device="/dev/disk/by-uuid/0b9a1c2e-1111-2222-3333-444455556666"' "$T/etc/dracut.conf.d/90-hostashi-quota.conf"
check "no initramfs-tools files" [ ! -e "$T/etc/initramfs-tools" ]
check "every initramfs rebuilt by dracut" grep -qx 'dracut --force --regenerate-all' "$T/q/calls"
check "update-initramfs not used" bash -c "! grep -q '^update-initramfs' '$T/q/calls'"
check "fstab: prjquota added" grep -q 'errors=remount-ro,prjquota' "$T/etc/fstab"
check "GRUB drop-in adds rootflags" grep -q 'rootflags=prjquota' "$T/etc/default/grub.d/hostashi-quota.cfg"
check "state is pending" chk_quota
check "module-setup is valid bash" bash -n "$D/module-setup.sh"
check "hook is valid sh" sh -n "$D/hostashi-quota.sh"
check "hook never exits (dracut sources it)" bash -c "! grep -Eq '(^|[;{|&] *)exit( |\$)' '$D/hostashi-quota.sh'"
if [ -n "$SHELLCHECK" ]; then
  check "module-setup passes shellcheck" "$SHELLCHECK" -s bash "$D/module-setup.sh"
  check "hook passes shellcheck" "$SHELLCHECK" -s dash "$D/hostashi-quota.sh"
  quota_premount >"$T/premount"
  check "premount script passes shellcheck" "$SHELLCHECK" -s dash "$T/premount"
fi

reboot_into "has_journal ext_attr dir_index filetype extent 64bit flex_bg sparse_super metadata_csum orphan_file quota project" "rw,relatime,prjquota"
check "active after the reboot" chk_quota
check "state is active" [ "$QUOTA" = active ]
rm "$D/module-setup.sh"
check "a missing module is not active" fails chk_quota

echo "--- dracut failures roll back"
fresh; use_dracut; touch "$T/q/fail-initramfs"
check "do_quota fails when dracut fails" fails do_quota
check "fstab restored" fstab_unchanged
check "no quota files left" no_quota_files

fresh; use_dracut; touch "$T/q/initrd-no-module"
check "do_quota fails when the initramfs lacks quota_v2" fails do_quota
check "no quota files left" no_quota_files
check "rollback rebuilt the initramfs" [ "$(grep -c '^dracut --force --regenerate-all$' "$T/q/calls")" = 2 ]

# ------------------------------------------------------------- hook guard
echo "--- the hook stops a kernel update without quota_v2"
fresh; do_quota >/dev/null 2>&1
rm "$T/q/kmod"
out=$(version=6.99.0-test sh "$T/etc/initramfs-tools/hooks/hostashi-quota" 2>&1); rc=$?
check "hook exits 1" [ $rc = 1 ]
check "hook names the kernel" grep -q "6.99.0-test" <<<"$out"
out=$(sh "$T/etc/initramfs-tools/hooks/hostashi-quota" prereqs); rc=$?
check "hook answers prereqs" [ $rc = 0 ]

echo
echo "passed: $PASSES, failed: $FAILS"
[ "$FAILS" = 0 ]
