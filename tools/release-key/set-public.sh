#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Puts a renewed public apt key into the repository (ADR-0050): the keyring
# the package ships and the copy built into packaging/get/install.sh. The
# key must have the same fingerprint as the one there now; a new key is a
# different matter (tools/release-key/new.sh --replace).
#
#   tools/release-key/set-public.sh NEW.asc
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: set-public.sh NEW.asc" >&2; exit 64; }
new=$(realpath "$1")
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
KEYRING=$HERE/packaging/keyring/hostashi-archive-keyring.asc
GET=$HERE/packaging/get/install.sh
die() { echo "set-public: $*" >&2; exit 1; }

GNUPGHOME=$(mktemp -d)
export GNUPGHOME
trap 'rm -rf "$GNUPGHOME"' EXIT
fpr() { gpg --batch --with-colons --show-keys "$1" 2>/dev/null | awk -F: '$1 == "fpr" { print $10; exit }'; }

[ "$(gpg --batch --with-colons --show-keys "$new" 2>/dev/null | grep -c '^pub:')" = 1 ] || die "$1 must hold exactly one public key"
! grep -q 'PRIVATE KEY' "$new" || die "$1 holds a private key"
want=$(fpr "$KEYRING")
[ -n "$want" ] && [ "$(fpr "$new")" = "$want" ] || die "$1 is not the key $want"

cp "$new" "$KEYRING.tmp" && mv -f "$KEYRING.tmp" "$KEYRING"
awk -v keyfile="$new" '
  $0 == "cat >\"$KEYRING.tmp\" <<'\''KEY'\''" { print; while ((getline l < keyfile) > 0) print l; skip = 1; next }
  skip && $0 == "KEY" { skip = 0 }
  !skip { print }
' "$GET" >"$GET.tmp"
chmod --reference="$GET" "$GET.tmp"
mv -f "$GET.tmp" "$GET"
echo "set-public: key $want updated in packaging/keyring and packaging/get/install.sh"
