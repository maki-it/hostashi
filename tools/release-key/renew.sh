#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Extends the apt signing key by two years once less than a year is left
# (ADR-0050). The release workflow runs it in the approved job that holds
# the private key; nobody runs gpg by hand.
#
#   tools/release-key/renew.sh FINGERPRINT OUT.asc
#
# Needs the private key in $GNUPGHOME. If the key expires within
# RENEW_DAYS (default 365) days, it sets the expiry to two years from now
# and writes the public key with the new expiry to OUT.asc; otherwise it
# writes nothing. Only the self-signature changes: the fingerprint, and so
# the key that servers trust, stays the same. tools/release-key/
# set-public.sh puts OUT.asc into the repository.
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: renew.sh FINGERPRINT OUT.asc" >&2; exit 64; }
fpr=$1 out=$2
days=${RENEW_DAYS:-365}
die() { echo "renew: $*" >&2; exit 1; }

expiry() { gpg --batch --with-colons --list-secret-keys "$fpr" 2>/dev/null | awk -F: '$1 == "sec" { print $7; exit }'; }
gpg --batch --list-secret-keys "$fpr" >/dev/null 2>&1 || die "no private key $fpr in GNUPGHOME"
exp=$(expiry)
[ -n "$exp" ] || die "key $fpr has no expiry"
if [ "$exp" -gt $(( $(date +%s) + days * 86400 )) ]; then
  echo "renew: key $fpr expires on $(date -u -d "@$exp" +%F), more than $days days away"
  exit 0
fi

gpg --batch --quiet --pinentry-mode loopback --passphrase '' --quick-set-expire "$fpr" 2y
new=$(expiry)
[ -n "$new" ] && [ "$new" -gt "$exp" ] || die "the expiry did not change"
gpg --batch --armor --export "$fpr" >"$out.tmp"
mv -f "$out.tmp" "$out"
echo "renew: key $fpr extended from $(date -u -d "@$exp" +%F) to $(date -u -d "@$new" +%F)"
