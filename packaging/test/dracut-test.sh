#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests the dracut module of the quota step (packaging/install.sh, ADR-0015)
# with the real dracut and kernel of Ubuntu 26.04, inside a throw-away
# privileged container:
#
#   docker run --rm --privileged -v "$PWD:/src:ro" ubuntu:26.04 /src/packaging/test/dracut-test.sh
#
# It builds an initramfs with the module, checks its content, runs the hook
# from the image against an ext4 file system on a loop device, and checks
# that a kernel without quota_v2 fails the build. The boot itself is not
# covered here.
set -uo pipefail

SRC=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
[ "$(id -u)" = 0 ] || { echo "run as root in a throw-away container" >&2; exit 64; }
[ -f /.dockerenv ] || [ -n "${container:-}" ] || { echo "refusing to run outside a container" >&2; exit 64; }

FAILS=0 PASSES=0
ok()   { PASSES=$((PASSES + 1)); echo "ok   $*"; }
bad()  { FAILS=$((FAILS + 1)); echo "FAIL $*" >&2; }
check() { local d=$1; shift; if "$@"; then ok "$d"; else bad "$d"; fi; }
fails() { ! "$@"; }

echo "--- install dracut and a kernel"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends dracut linux-image-virtual e2fsprogs zstd >/dev/null || exit 1
KVER=$(find /lib/modules -mindepth 1 -maxdepth 1 -printf '%f\n' | sort -V | tail -n 1)
echo "kernel $KVER, $(dracut --version)"
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

# The installer's functions, with the real paths of this container.
HOSTASHI_LIB=1
# shellcheck source=packaging/install.sh
. "$SRC/packaging/install.sh"
set +e
quota_initrd
check "dracut is the generator" [ "$Q_INITRD" = dracut ]

install -d -m 0755 "$QUOTA_DRACUT_DIR" "$(dirname "$QUOTA_DRACUT_CONF")"
quota_dracut_setup >"$QUOTA_DRACUT_SETUP"
quota_dracut_hook >"$QUOTA_DRACUT_HOOK"
chmod 0755 "$QUOTA_DRACUT_SETUP" "$QUOTA_DRACUT_HOOK"
quota_dracut_conf /dev/disk/by-uuid/0b9a1c2e-1111-2222-3333-444455556666 >"$QUOTA_DRACUT_CONF"

echo "--- build an initramfs with the module"
IMG=$T/initrd.img-$KVER
# --no-hostonly: the container's root is not a block device.
check "dracut builds the image" dracut --force --no-hostonly --kver "$KVER" "$IMG"
check "the module is in the image" bash -c "lsinitrd -m '$IMG' | grep -x hostashi-quota >/dev/null"
check "image passes the installer's check" quota_initrd_ok "$IMG"
conf=$(lsinitrd -f etc/hostashi-quota.conf "$IMG")
check "the device is in the image" [ "$conf" = "DEVICE=/dev/disk/by-uuid/0b9a1c2e-1111-2222-3333-444455556666" ]
hook=$(lsinitrd "$IMG" | grep -o 'var/lib/dracut/hooks/pre-mount/[0-9]*-hostashi-quota\.sh$')
check "the hook is a pre-mount hook" [ -n "$hook" ]
lsinitrd -f "$hook" "$IMG" >"$T/hook.sh"
check "the hook in the image is ours" cmp -s "$T/hook.sh" "$QUOTA_DRACUT_HOOK"
list=$(lsinitrd "$IMG")
for b in tune2fs e2fsck dumpe2fs sed modprobe; do
  check "$b is in the image" grep -Eq "s?bin/$b( -> .*)?\$" <<<"$list"
done

echo "--- without the configuration, dracut leaves the module out"
mv "$QUOTA_DRACUT_CONF" "$T/conf"
check "dracut builds the image" dracut --force --no-hostonly --kver "$KVER" "$T/plain.img"
check "the module is not in the image" bash -c "! lsinitrd -m '$T/plain.img' | grep -x hostashi-quota >/dev/null"
mv "$T/conf" "$QUOTA_DRACUT_CONF"

echo "--- the hook from the image sets the features on a real ext4"
truncate -s 64M "$T/fs.img"
mkfs.ext4 -q -F -I 256 "$T/fs.img"
LOOP=$(losetup --find --show "$T/fs.img") || { bad "losetup (needs --privileged)"; LOOP=""; }
if [ -n "$LOOP" ]; then
  # The container has no modules for the host kernel; the hook only needs
  # modprobe to succeed before tune2fs.
  mkdir -p "$T/bin"
  printf '#!/bin/sh\necho "modprobe $*" >>%s/calls\n' "$T" >"$T/bin/modprobe"
  chmod 0755 "$T/bin/modprobe"
  printf 'DEVICE=%s\n' "$LOOP" >/etc/hostashi-quota.conf
  : >"$T/kmsg"
  # dracut sources the hook in sh (dash here); /dev/kmsg and /dev/console are
  # files for the test.
  sed -e "s#/dev/kmsg#$T/kmsg#g" -e "s#/dev/console#$T/console#g" "$T/hook.sh" >"$T/hook-test.sh"
  check "the hook returns 0 when sourced" env PATH="$T/bin:$PATH" sh -c ". '$T/hook-test.sh'; echo after >'$T/after'"
  check "the sourcing shell goes on" [ -s "$T/after" ]
  feats=$(dumpe2fs -h "$LOOP" 2>/dev/null | sed -n 's/^Filesystem features: *//p')
  check "project feature set" grep -qw project <<<"$feats"
  check "quota feature set" grep -qw quota <<<"$feats"
  check "modprobe quota_v2 was asked first" grep -qx 'modprobe quota_v2' "$T/calls"
  check "the hook logged what it did" grep -q 'hostashi-quota: done' "$T/kmsg"
  : >"$T/kmsg"
  check "a second run changes nothing" env PATH="$T/bin:$PATH" sh -c ". '$T/hook-test.sh'"
  check "and logs nothing" [ ! -s "$T/kmsg" ]
  check "the file system is clean" e2fsck -fn "$LOOP"

  printf 'DEVICE=/dev/does-not-exist\n' >/etc/hostashi-quota.conf
  check "a missing device is ignored" sh -c ". '$T/hook-test.sh'"
  rm -f /etc/hostashi-quota.conf
  check "a missing configuration is ignored" sh -c ". '$T/hook-test.sh'"
  losetup -d "$LOOP"
fi

echo "--- a kernel without quota_v2 fails the build"
find "/lib/modules/$KVER" -name 'quota_v2.ko*' -delete
depmod "$KVER"
out=$(dracut --force --no-hostonly --kver "$KVER" "$T/broken.img" 2>&1); rc=$?
check "dracut fails" [ $rc != 0 ]
check "the error names quota_v2" grep -q "has no quota_v2 module" <<<"$out"

echo
echo "passed: $PASSES, failed: $FAILS"
[ "$FAILS" = 0 ]
