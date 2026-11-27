#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests packaging/get/wpcli-mirror.sh against a local "release" (file://):
# a matching phar is mirrored in GitHub's layout, the real pin file parses,
# a wrong checksum and a malformed pin file are refused and leave nothing.
#
#   wpcli-mirror-test.sh
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
MIRROR=$HERE/get/wpcli-mirror.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

mkdir -p "$T/rel/v9.9.9"
printf '<?php // phar 9.9.9\n' >"$T/rel/v9.9.9/wp-cli-9.9.9.phar"
sum=$(sha512sum "$T/rel/v9.9.9/wp-cli-9.9.9.phar" | cut -d' ' -f1)
pin() { printf 'var WPCLI = WPCLIRelease{\n\tVersion: "%s",\n\tSHA512:  "%s",\n}\n' "$1" "$2" >"$3"; }

pin 9.9.9 "$sum" "$T/good.go"
WPCLI_URL=file://$T/rel "$MIRROR" "$T/out" "$T/good.go" "$T/good.go"
cmp "$T/rel/v9.9.9/wp-cli-9.9.9.phar" "$T/out/wp-cli/v9.9.9/wp-cli-9.9.9.phar" || fail "phar not mirrored"
[ "$(stat -c %a "$T/out/wp-cli/v9.9.9/wp-cli-9.9.9.phar")" = 644 ] || fail "mode"

# The real pin of this tree parses (version and checksum shape).
version=$(sed -n 's/^[[:space:]]*Version:[[:space:]]*"\([^"]*\)",.*$/\1/p' "$HERE/../internal/agentapi/wpcli.go")
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "internal/agentapi/wpcli.go: version '$version'"

pin 9.9.9 "$(printf '0%.0s' $(seq 128))" "$T/wrong.go"
if WPCLI_URL=file://$T/rel "$MIRROR" "$T/out2" "$T/wrong.go" 2>/dev/null; then fail "wrong checksum accepted"; fi
[ -z "$(find "$T/out2" -type f 2>/dev/null)" ] || fail "wrong file left: $(find "$T/out2" -type f)"

printf 'Version: "../../x",\nSHA512: "%s",\n' "$sum" >"$T/bad.go"
if WPCLI_URL=file://$T/rel "$MIRROR" "$T/out3" "$T/bad.go" 2>/dev/null; then fail "bad version accepted"; fi
if "$MIRROR" "$T/out3" "$T/missing.go" 2>/dev/null; then fail "missing pin file accepted"; fi

echo "PASS: wpcli-mirror.sh"
