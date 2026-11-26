#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# The apt signing key appears in three places (ADR-0050): the keyring the
# package ships, the key built into the bootstrap script packaging/get/
# install.sh, and that script's fingerprint. They must be the same key, an
# Ed25519 signing key that does not expire within 90 days. The runtimes
# key (ADR-0077) is checked the same way and must be a different key.
#
#   packaging/test/key-test.sh        (needs gpg)
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
KEYRING=$HERE/packaging/keyring/hostashi-archive-keyring.asc
GET=$HERE/packaging/get/install.sh
fail() { echo "FAIL: $*" >&2; exit 1; }

GNUPGHOME=$(mktemp -d)
export GNUPGHOME
trap 'rm -rf "$GNUPGHOME"' EXIT

info=$(gpg --batch --with-colons --show-keys "$KEYRING" 2>/dev/null)
[ "$(grep -c '^pub:' <<<"$info")" = 1 ] || fail "the keyring must hold exactly one key"
fpr=$(awk -F: '$1 == "fpr" { print $10; exit }' <<<"$info")
algo=$(awk -F: '$1 == "pub" { print $17 }' <<<"$info")
exp=$(awk -F: '$1 == "pub" { print $7 }' <<<"$info")
[ "$algo" = ed25519 ] || fail "key algorithm $algo, want ed25519"
[ -n "$exp" ] || fail "the key has no expiry"
[ "$exp" -gt $(( $(date +%s) + 90 * 86400 )) ] || fail "the key expires on $(date -u -d "@$exp" +%F): renew it"

grep -qx "FINGERPRINT=$fpr" "$GET" || fail "fingerprint in packaging/get/install.sh is not $fpr"
embedded=$(sed -n "/^cat >\"\$KEYRING.tmp\" <<'KEY'$/,/^KEY$/p" "$GET" | sed '1d;$d')
[ "$embedded" = "$(cat "$KEYRING")" ] || fail "the key in packaging/get/install.sh differs from the keyring"

echo "OK: apt key $fpr, expires $(date -u -d "@$exp" +%F)"

RKEYRING=$HERE/packaging/keyring/hostashi-runtimes-keyring.asc
info=$(gpg --batch --with-colons --show-keys "$RKEYRING" 2>/dev/null)
[ "$(grep -c '^pub:' <<<"$info")" = 1 ] || fail "the runtimes keyring must hold exactly one key"
rfpr=$(awk -F: '$1 == "fpr" { print $10; exit }' <<<"$info")
algo=$(awk -F: '$1 == "pub" { print $17 }' <<<"$info")
rexp=$(awk -F: '$1 == "pub" { print $7 }' <<<"$info")
[ "$algo" = ed25519 ] || fail "runtimes key algorithm $algo, want ed25519"
[ -n "$rexp" ] || fail "the runtimes key has no expiry"
[ "$rexp" -gt $(( $(date +%s) + 90 * 86400 )) ] || fail "the runtimes key expires on $(date -u -d "@$rexp" +%F): renew it"
[ "$rfpr" != "$fpr" ] || fail "the runtimes key is the apt key"
grep -q "RUNTIMES_FINGERPRINT: $rfpr" "$HERE/.github/workflows/runtimes.yml" ||
  fail "fingerprint in .github/workflows/runtimes.yml is not $rfpr"

echo "OK: runtimes key $rfpr, expires $(date -u -d "@$rexp" +%F)"
