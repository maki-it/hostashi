#!/bin/bash
# Runs at boot inside the test VM. Stage 1: install (+ immediate re-run), reboot. Stage 2: re-run after reboot, quota test, power off.
exec > >(tee -a /var/log/s5-runner.log) 2>&1
S=/opt/s5; st=$(cat $S/stage); echo "=== S5 RUNNER stage $st kernel $(uname -r)"
run() { local t=$(date +%s); $S/install.sh apply --yes --binary $S/hostashi-stub; echo "exit=$? ($(( $(date +%s)-t )) s)"; }
case $st in
1) echo "=== stage1 apply #1"; run
   $S/snapshot.sh > $S/snap1a
   echo "=== stage1 apply #2 (same boot, before reboot)"; run
   $S/snapshot.sh > $S/snap1b; diff $S/snap1a $S/snap1b > $S/diff1 && echo "FINGERPRINT stage1: identical" || { echo "FINGERPRINT stage1: DIFFERS"; head -20 $S/diff1; }
   echo 2 > $S/stage; sync; echo "=== rebooting"; systemctl reboot ;;
2) echo "=== stage2 after reboot: findmnt $(findmnt -no OPTIONS /)"; dumpe2fs -h $(findmnt -no SOURCE /) 2>/dev/null | grep -i features
   echo "=== stage2 apply #3"; run
   $S/snapshot.sh > $S/snap2a
   echo "=== stage2 apply #4"; run
   $S/snapshot.sh > $S/snap2b; diff $S/snap2a $S/snap2b > $S/diff2 && echo "FINGERPRINT stage2: identical" || { echo "FINGERPRINT stage2: DIFFERS"; head -20 $S/diff2; }
   cat /var/lib/hostashi-installer/state
   echo "=== verify"; $S/verify.sh; echo "verify rc=$?"
   systemctl is-active hostashi-quota-enforce.service; quotaon -Ppv / 2>&1
   echo "=== quota enforcement test (site writer, 20 MiB limit)"
   useradd -r -M -s /usr/sbin/nologin s5site; mkdir -p /srv/s5site; chown s5site /srv/s5site
   chattr +P -p 4242 /srv/s5site; setquota -P 4242 0 20480 0 0 /
   su -s /bin/sh s5site -c 'dd if=/dev/zero of=/srv/s5site/f bs=1M count=40 2>&1 | tail -1'
   echo "written: $(du -m /srv/s5site/f | cut -f1) MiB"
   echo 3 > $S/stage; sync; echo "=== S5 RUNNER DONE"; systemctl poweroff ;;
*) echo "nothing to do"; systemctl poweroff ;;
esac
