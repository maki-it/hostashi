#!/bin/bash
exec 2>&1
echo "=== STAGE4 $(uname -r)"
rm -rf /srv/siteA/* /srv/siteB/*; setquota -P 101 0 20480 0 1000 /; setquota -P 102 0 20480 0 1000 /
DROP="setpriv --bounding-set=-all --inh-caps=-all --reuid=%U --regid=%U --clear-groups"
run() { u=$1; shift; setpriv --bounding-set=-all --inh-caps=-all --reuid=$u --regid=$u --clear-groups "$@"; }
run sa capsh --print 2>/dev/null | grep -i "current" || run sa grep -i cap /proc/self/status | head -3
echo "--- A: 40 MiB as unprivileged site user (limit 20 MiB)"; run sa dd if=/dev/zero of=/srv/siteA/big bs=1M count=40 conv=fsync 2>&1 | tail -2
ls -la /srv/siteA/big; repquota -Pn / | grep -E "^#10[12]"
echo "--- B unaffected"; run sb dd if=/dev/zero of=/srv/siteB/ok bs=1M count=5 conv=fsync 2>&1 | tail -1
echo "--- inode limit as sb (1000)"; run sb bash -c 'cd /srv/siteB; for i in $(seq 1 1100); do : > f$i 2>/dev/null || { echo "failed at file $i"; break; }; done; ls | wc -l'
echo "--- php-style write failing message"; run sa bash -c 'echo hi > /srv/siteA/small' 2>&1 | tail -1
echo "--- after rm, writes work again"; rm -f /srv/siteA/big; run sa dd if=/dev/zero of=/srv/siteA/again bs=1M count=5 conv=fsync 2>&1 | tail -1
echo "--- root bypass (CAP_SYS_RESOURCE) for the record"; dd if=/dev/zero of=/srv/siteA/rootbig bs=1M count=30 conv=fsync 2>&1 | tail -1; repquota -Pn / | grep -E "^#101"
echo "--- repquota read cost"; time repquota -Pn / >/dev/null
echo "--- quota survive: quotacheck/fsck state"; dmesg | grep -i quota | tail -3
echo "=== END"; systemctl poweroff
