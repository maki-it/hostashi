#!/bin/bash
# THROWAWAY: runs the whole S8 flow on a fresh Debian/Ubuntu test server (needs root, ext4 root fs).
#   ./server-test.sh start    plan + apply, installs a verify unit, reboots
#   ./server-test.sh show     prints the result written after the reboot
# Result file: /root/s8-result.txt (contains only kernel/mount/quota facts, no secrets).
set -u
HERE=$(cd "$(dirname "$0")" && pwd); Q=$HERE/../quota-setup.sh; [ -x "$Q" ] || Q=$HERE/quota-setup.sh; OUT=/root/s8-result.txt
[ -x "$Q" ] || { echo "quota-setup.sh not found next to this script (or one directory up), copy both files into the same directory and chmod +x" >&2; exit 1; }
case "${1:-}" in
start)
  apt-get update -qq && apt-get install -y -qq quota e2fsprogs initramfs-tools >/dev/null || exit 1
  { echo "== before"; cat /etc/os-release | head -2; uname -r; findmnt -no SOURCE,FSTYPE,OPTIONS /
    dumpe2fs -h "$(findmnt -no SOURCE /)" 2>/dev/null | grep -i "features"; modinfo quota_v2 2>&1 | head -2
    echo "== plan"; "$Q" plan; echo "plan rc=$?"
    echo "== apply"; "$Q" apply --yes; echo "apply rc=$?"; } > $OUT 2>&1
  cat $OUT
  cat > /usr/local/sbin/s8-verify.sh <<V
#!/bin/bash
run() { u=\$1; shift; setpriv --bounding-set=-all --inh-caps=-all --reuid=\$u --regid=\$u --clear-groups "\$@"; }
{ echo "== after reboot"; findmnt -no OPTIONS /; quotaon -Pp /; systemctl is-active hostashi-quota-enforce
  dumpe2fs -h "\$(findmnt -no SOURCE /)" 2>/dev/null | grep -iE "features|Project quota"
  $Q apply --yes; echo "rerun rc=\$?"
  mkdir -p /srv/siteA /srv/siteB; id sa >/dev/null 2>&1 || useradd -M -s /usr/sbin/nologin sa; id sb >/dev/null 2>&1 || useradd -M -s /usr/sbin/nologin sb
  chown sa /srv/siteA; chown sb /srv/siteB; chattr -p 101 +P /srv/siteA; chattr -p 102 +P /srv/siteB
  setquota -P 101 0 20480 0 1000 /; setquota -P 102 0 20480 0 1000 /
  run sa dd if=/dev/zero of=/srv/siteA/big bs=1M count=40 conv=fsync 2>&1 | tail -2
  run sb dd if=/dev/zero of=/srv/siteB/ok bs=1M count=5 conv=fsync 2>&1 | tail -1
  repquota -Pn / | grep '^#10'; echo "== done"; } >> $OUT 2>&1
systemctl disable s8-verify.service
V
  chmod +x /usr/local/sbin/s8-verify.sh
  printf '[Unit]\nDescription=S8 verify\nAfter=multi-user.target hostashi-quota-enforce.service\n[Service]\nType=oneshot\nExecStart=/usr/local/sbin/s8-verify.sh\n[Install]\nWantedBy=multi-user.target\n' > /etc/systemd/system/s8-verify.service
  systemctl enable s8-verify.service >/dev/null 2>&1
  grep -q "apply rc=0" $OUT && { echo "rebooting in 5 s"; sleep 5; systemctl reboot; } || echo "apply did not succeed, NOT rebooting" ;;
show) cat $OUT ;;
*) echo "usage: $0 start|show" >&2; exit 64 ;;
esac
