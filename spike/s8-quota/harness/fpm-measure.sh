#!/bin/bash
# usage: measure.sh <N> <mode: per-site|shared> <pm>
N=$1; MODE=$2; PM=$3; D=/tmp/s8/fpm/run-$MODE-$PM; rm -rf $D; mkdir -p $D
pool() { cat <<P
[site$1]
user = nobody
group = nogroup
listen = $D/site$1.sock
listen.owner = root
listen.mode = 0666
pm = $PM
pm.max_children = 5
pm.start_servers = 1
pm.min_spare_servers = 1
pm.max_spare_servers = 2
pm.process_idle_timeout = 10s
P
}
glob() { printf '[global]\npid = %s/%s.pid\nerror_log = %s/%s.log\ndaemonize = yes\n' $D $1 $D $1; }
pids=()
if [ $MODE = per-site ]; then
  for i in $(seq 1 $N); do { glob m$i; pool $i; } > $D/m$i.conf; php-fpm8.3 -y $D/m$i.conf -c /etc/php/8.3/fpm/php.ini >/dev/null 2>&1; done
else { glob shared; for i in $(seq 1 $N); do pool $i; done; } > $D/shared.conf; php-fpm8.3 -y $D/shared.conf -c /etc/php/8.3/fpm/php.ini >/dev/null 2>&1; fi
sleep 3
sum() { # args: pid list ; prints count, sum PSS KiB, sum RSS KiB
  local c=0 p=0 r=0; for pid in "$@"; do [ -r /proc/$pid/smaps_rollup ] || continue; c=$((c+1)); p=$((p+$(awk '/^Pss:/{s+=$2} END{print s+0}' /proc/$pid/smaps_rollup))); r=$((r+$(awk '/^Rss:/{s+=$2} END{print s+0}' /proc/$pid/smaps_rollup))); done; echo "procs=$c PSS_KiB=$p RSS_KiB=$r"; }
allp() { pgrep -f "php-fpm: (master|pool)" ; }
echo "[$MODE pm=$PM N=$N] idle: $(sum $(allp))"
echo "  masters only: $(sum $(pgrep -f 'php-fpm: master'))"
# one request to site1 (cold) and a second (warm)
req() { SCRIPT_NAME=/t.php SCRIPT_FILENAME=/tmp/s8/fpm/t.php REQUEST_METHOD=GET cgi-fcgi -bind -connect $D/site1.sock >/dev/null; }
t0=$(date +%s%N); req; t1=$(date +%s%N); req; t2=$(date +%s%N)
echo "  request latency: first=$(( (t1-t0)/1000 )) us, second=$(( (t2-t1)/1000 )) us"
echo "  after request: $(sum $(allp))"
pkill -f "php-fpm: master"; sleep 1
