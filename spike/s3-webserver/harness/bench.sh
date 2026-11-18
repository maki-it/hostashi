#!/bin/sh
# Usage: bench.sh <label> <process-name>   (server must already be running on 18443)
# Prints RSS (sum over the server's processes) and ab throughput for a static
# file, a PHP page and the reverse proxy. ab is HTTP/1.1 keep-alive over TLS.
set -eu
L=$1; P=$2
rss() { ps -C "$P" -o rss= | awk '{s+=$1} END {printf "%.1f MiB (%d processes)", s/1024, NR}'; }
echo "[$L] RSS before load: $(rss)"
run() { # name host path
  ab -k -q -n 20000 -c 50 -H "Host: $2" "https://127.0.0.1:18443$3" 2>/dev/null |
    awk -v n="$1" '/Requests per second/ {r=$4} /Failed requests/ {f=$3} /Non-2xx/ {x=$3} /95%/ {p=$2} END {printf "[%s] %-7s %8s req/s  p95 %s ms  failed %s  non2xx %s\n", "'"$L"'", n, r, p, f, x+0}'
}
run static site04.test /hello.txt
run php    site01.test /index.php
run proxy  site03.test /
echo "[$L] RSS after load:  $(rss)"
