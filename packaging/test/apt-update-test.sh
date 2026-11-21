#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests php-install's apt_update with a stubbed apt-get: it waits out a
# package mirror whose indexes do not match (sury.org's CDN, ADR-0019),
# switching between xz and gz indexes, and fails at once on other errors.
# install.sh's apt_update uses it. Needs no root and no network.
set -u
set -o pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

mkdir -p "$T/bin"
# The stub answers each call with the next line of $T/answers ("ok",
# "sync" or "other") and logs the compression type it was given.
cat >"$T/bin/apt-get" <<'EOF'
#!/bin/bash
conf=""
while [ $# -gt 0 ]; do
  case "$1" in -c) conf=$2; shift 2;; *) shift;; esac
done
sed -n 's/^Acquire::CompressionTypes::Order:: "\(.*\)";$/\1/p' "$conf" >>"$T/calls"
grep -q '^#clear Acquire::CompressionTypes::Order;$' "$conf" || echo "no-clear" >>"$T/calls"
n=$(wc -l <"$T/calls")
case "$(sed -n "${n}p" "$T/answers")" in
  ok) exit 0;;
  sync)
    echo "E: Failed to fetch https://packages.sury.org/php/dists/trixie/main/binary-amd64/Packages.xz  File has unexpected size (1 != 2). Mirror sync in progress? [IP: 192.0.2.1 443]"
    echo "E: Some index files failed to download. They have been ignored, or old ones used instead."
    exit 100;;
  *) echo "E: Could not resolve 'packages.sury.org'"; exit 100;;
esac
EOF
chmod +x "$T/bin/apt-get"
export T PATH="$T/bin:$PATH" HOSTASHI_APT_SYNC_PAUSE=0

run() { # run ANSWERS...: apt_update with these answers; prints the types used
  printf '%s\n' "$@" >"$T/answers"
  : >"$T/calls"
  "$HERE/php/php-install" update >"$T/out" 2>&1
}
calls() { tr '\n' ' ' <"$T/calls"; }

run ok || fail "a working update failed: $(cat "$T/out")"
[ "$(calls)" = "xz " ] || fail "first try: $(calls)"

run sync sync ok || fail "an update that recovers failed: $(cat "$T/out")"
[ "$(calls)" = "xz gz xz " ] || fail "xz and gz not alternated: $(calls)"
grep -q 'out of sync, trying again' "$T/out" || fail "no note about the retry: $(cat "$T/out")"

if run other ok; then fail "another error was retried"; fi
[ "$(calls)" = "xz " ] || fail "another error was retried: $(calls)"
grep -q "Could not resolve" "$T/out" || fail "apt's error not shown: $(cat "$T/out")"

if HOSTASHI_APT_SYNC_WAIT=0 run sync ok; then fail "the wait has no end"; fi
[ "$(calls)" = "xz " ] || fail "retried past the wait: $(calls)"

if "$HERE/php/php-install" update extra >/dev/null 2>&1; then fail "update took an argument"; fi
"$HERE/php/php-install" update extra >/dev/null 2>&1
[ $? = 64 ] || fail "update with an argument is not a usage error"

# install.sh's steps use the same function.
printf '%s\n' sync ok >"$T/answers"
: >"$T/calls"
out=$(HOSTASHI_LIB=1 bash -c '. "$1/install.sh"; apt_update' _ "$HERE" 2>&1) || fail "install.sh apt_update failed: $out"
[ "$(calls)" = "xz gz " ] || fail "install.sh does not use php-install's wait: $(calls)"

echo "ok: apt_update"
