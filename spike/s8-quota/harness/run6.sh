#!/bin/bash
exec 2>&1
echo "=== STAGE6"; quotaon -Pp /; systemctl is-active hostashi-quota-enforce
run() { u=$1; shift; setpriv --bounding-set=-all --inh-caps=-all --reuid=$u --regid=$u --clear-groups "$@"; }
rm -f /srv/siteA/*; setquota -P 101 0 20480 0 1000 /
run sa dd if=/dev/zero of=/srv/siteA/big bs=1M count=40 conv=fsync 2>&1 | tail -3
repquota -Pn / | grep "^#101"
run sa bash -c 'echo test > /srv/siteA/small' 2>&1 | tail -1
echo "--- inode limit as sb"; setquota -P 102 0 0 0 100 /; rm -rf /srv/siteB/*; run sb bash -c 'cd /srv/siteB; for i in $(seq 1 200); do : > f$i 2>/dev/null || { echo "failed at file $i"; break; }; done; ls | wc -l'
echo "=== END"; systemctl poweroff
