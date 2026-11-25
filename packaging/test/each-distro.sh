#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Runs one command for Debian 13 and Ubuntu 26.04 at the same time and
# fails if either fails. Every {} in the arguments becomes the image. The
# output of each run is printed as a whole after both have finished, so
# the CI log does not interleave them.
#
#   packaging/test/each-distro.sh docker run --rm IMAGE... {} /src/test.sh
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: $0 COMMAND [ARG|{}]..." >&2; exit 2; }

images=(debian:trixie ubuntu:26.04)
logs=$(mktemp -d)
trap 'rm -rf "$logs"' EXIT

pids=()
for i in "${!images[@]}"; do
  args=()
  for a in "$@"; do args+=("${a//\{\}/${images[$i]}}"); done
  "${args[@]}" >"$logs/$i" 2>&1 &
  pids+=("$!")
done

rc=0
for i in "${!images[@]}"; do
  status=0
  wait "${pids[$i]}" || status=$?
  echo "::group::${images[$i]} (exit $status)"
  cat "$logs/$i"
  echo "::endgroup::"
  if [ "$status" != 0 ]; then
    echo "::error::${images[$i]} failed with exit $status"
    tail -n 40 "$logs/$i"
    rc=1
  fi
done
exit "$rc"
