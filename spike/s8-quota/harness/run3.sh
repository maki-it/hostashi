#!/bin/bash
exec 2>&1
S=/var/lib/s8; mkdir -p $S; stage=$(cat $S/stage 2>/dev/null || echo 0)
Q=/opt/s8/quota-setup.sh
echo "=== STAGE $stage kernel $(uname -r)"
case $stage in
0)
  $Q plan; $Q apply; echo "rc(no --yes)=$?"
  $Q apply --yes; echo rc=$?; cat /etc/fstab
  echo 1 > $S/stage; sync; systemctl reboot ;;
1)
  echo "--- root opts"; findmnt -no OPTIONS /; dumpe2fs -h /dev/vda 2>/dev/null | grep -iE "features|Project quota"
  dmesg | grep -iE "quota|hostashi" | head -5
  echo "--- idempotent"; T0=$(date +%s); $Q plan | tail -2; $Q apply --yes; echo rc=$? ; echo "(took $(( $(date +%s)-T0 ))s)"
  echo "--- enforcement"
  mkdir -p /srv/siteA /srv/siteB; useradd -M -s /bin/false sa; useradd -M -s /bin/false sb
  chown sa /srv/siteA; chown sb /srv/siteB
  chattr -p 101 +P /srv/siteA; chattr -p 102 +P /srv/siteB; lsattr -dp /srv/siteA /srv/siteB
  setquota -P 101 0 20480 0 1000 /; setquota -P 102 0 20480 0 1000 /
  echo "block hard=20MiB inode hard=1000"
  setpriv --reuid=sa --regid=sa --clear-groups dd if=/dev/zero of=/srv/siteA/big bs=1M count=40 2>&1 | tail -2
  echo "siteB still writable:"; setpriv --reuid=sb --regid=sb --clear-groups dd if=/dev/zero of=/srv/siteB/ok bs=1M count=5 2>&1 | tail -1
  echo "root-owned file in siteA counts?"; dd if=/dev/zero of=/srv/siteA/rootfile bs=1M count=1 2>&1 | tail -1
  echo "inode test:"; cd /srv/siteB; for i in $(seq 1 1100); do : > f$i 2>/dev/null || { echo "failed at file $i"; break; }; done; ls | wc -l; cd /
  echo "--- other owner (www-data style uid) in siteA dir"; chown 12345 /srv/siteA/rootfile; repquota -Pv / 2>&1 | head -12
  echo "--- cross-project hardlink/rename"; ln /srv/siteA/rootfile /srv/siteB/link 2>&1; mv /srv/siteA/rootfile /srv/siteB/moved 2>&1; ls -lp /srv/siteB/moved 2>&1; lsattr -p /srv/siteB/moved 2>&1
  echo "--- usage read (agent op)"; time quotactl_dummy=1 repquota -Pn / 2>&1 | head -5
  echo 2 > $S/stage; systemctl poweroff ;;
esac
