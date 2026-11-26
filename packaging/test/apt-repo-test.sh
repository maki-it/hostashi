#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Builds the apt repository (packaging/apt-repo/build.sh, ADR-0050) from the
# .deb files GoReleaser made, signed with a throwaway key, and checks it
# inside a throwaway container of a supported distribution with apt's own
# verification: the source with Signed-By updates cleanly, apt offers the
# package and downloads the same file, and a changed index, a repository
# signed by another key or an unsigned one are refused.
#
#   docker run --rm -v "$PWD/dist:/dist:ro" -v "$PWD:/src:ro" IMAGE \
#     /src/packaging/test/apt-repo-test.sh /dist
set -euo pipefail

DIST=${1:?usage: apt-repo-test.sh DIST_DIR}
fail() { echo "FAIL: $*" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive

apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends apt-utils gnupg xz-utils >/dev/null

work=$(mktemp -d)
chmod 0755 "$work" # apt reads file: sources as _apt
mkdir "$work/debs"
cp "$DIST"/hostashi_*.deb "$work/debs/"

# newkey DIR: a throwaway Ed25519 key in its own gpg home.
newkey() {
  mkdir -m 0700 "$1"
  GNUPGHOME=$1 gpg --batch --quiet --trust-model always --passphrase '' \
    --quick-gen-key "Hostashi test <test@example.invalid>" ed25519 sign 1d 2>/dev/null
}
newkey "$work/gpg"
newkey "$work/other"

GNUPGHOME=$work/gpg /src/packaging/apt-repo/build.sh "$work/debs" "$work/repo"
repo=$work/repo
for f in dists/stable/Release dists/stable/InRelease dists/stable/Release.gpg hostashi.asc \
  dists/stable/main/binary-amd64/Packages.xz dists/stable/main/binary-arm64/Packages.gz; do
  [ -s "$repo/$f" ] || fail "$f missing"
done
grep -qx 'Suite: stable' "$repo/dists/stable/Release" || fail "suite"
grep -qx 'Architectures: amd64 arm64' "$repo/dists/stable/Release" || fail "architectures"
grep -q '^SHA256:' "$repo/dists/stable/Release" || fail "no SHA256 sums"
if GNUPGHOME=$work/gpg /src/packaging/apt-repo/build.sh "$work/debs" "$repo" 2>/dev/null; then fail "built into a non-empty directory"; fi

keyring=/usr/share/keyrings/hostashi-archive-keyring.asc
src=/etc/apt/sources.list.d/hostashi.sources
use() { # use REPO_DIR KEY_FILE
  cp "$2" "$keyring"
  cat >"$src" <<SRC
Types: deb
URIs: file:$1
Suites: stable
Components: main
Signed-By: $keyring
SRC
  rm -rf /var/lib/apt/lists/*
}
update() { apt-get update -o APT::Update::Error-Mode=any 2>&1; }

use "$repo" "$repo/hostashi.asc"
out=$(update) || fail "apt-get update: $out"
grep -qiE '^(W|E):' <<<"$out" && fail "apt-get update warned: $out"
arch=$(dpkg --print-architecture)
want=$(dpkg-deb -f "$work"/debs/hostashi_*_"$arch".deb Version)
cand=$(apt-cache policy hostashi | awk '/Candidate:/ { print $2 }')
[ "$cand" = "$want" ] || fail "apt offers '$cand', want $want"
mkdir "$work/dl" && chown _apt "$work/dl"
(cd "$work/dl" && apt-get download -q hostashi >/dev/null) || fail "apt-get download"
cmp -s "$work"/dl/hostashi_*_"$arch".deb "$work"/debs/hostashi_*_"$arch".deb || fail "downloaded package differs"

# A changed index no longer matches the signed Release.
bad=$work/bad
cp -a "$repo" "$bad"
echo "Description: changed" >>"$bad/dists/stable/main/binary-$arch/Packages"
rm "$bad/dists/stable/main/binary-$arch/Packages".{gz,xz}
use "$bad" "$repo/hostashi.asc"
if update >/dev/null; then fail "apt accepted a changed Packages file"; fi

# Signed by a key the source does not trust.
GNUPGHOME=$work/other gpg --batch --armor --export >"$work/other.asc"
use "$repo" "$work/other.asc"
if update >/dev/null; then fail "apt accepted a repository signed by another key"; fi

# No signature at all.
cp -a "$repo" "$work/unsigned"
rm "$work/unsigned/dists/stable/InRelease" "$work/unsigned/dists/stable/Release.gpg"
use "$work/unsigned" "$repo/hostashi.asc"
if update >/dev/null; then fail "apt accepted an unsigned repository"; fi

# The runtimes suite (ADR-0077), when the job built hostashi-runtimes into
# DIST/runtimes: its own key, next to stable, installs the data file.
rdeb=""
for f in "$DIST"/runtimes/hostashi-runtimes_*_all.deb; do [ -f "$f" ] && rdeb=$f; done
if [ -n "$rdeb" ]; then
  mkdir "$work/rdebs"
  cp "$rdeb" "$work/rdebs/"
  if SUITE=runtimes PACKAGE=hostashi-runtimes GNUPGHOME=$work/other /src/packaging/apt-repo/build.sh "$work/debs" "$work/wrong" 2>/dev/null; then
    fail "the runtimes suite took the hostashi package"
  fi
  SUITE=runtimes PACKAGE=hostashi-runtimes GNUPGHOME=$work/other /src/packaging/apt-repo/build.sh "$work/rdebs" "$work/rrepo"
  rrepo=$work/rrepo
  for f in dists/runtimes/InRelease dists/runtimes/main/binary-amd64/Packages.xz hostashi-runtimes.asc; do
    [ -s "$rrepo/$f" ] || fail "runtimes: $f missing"
  done
  grep -qx 'Codename: runtimes' "$rrepo/dists/runtimes/Release" || fail "runtimes codename"
  grep -qx 'Origin: Hostashi' "$rrepo/dists/runtimes/Release" || fail "runtimes origin"

  rkeyring=/usr/share/keyrings/hostashi-runtimes-keyring.asc
  rsrc=/etc/apt/sources.list.d/hostashi-runtimes.sources
  cat >"$rsrc" <<SRC
Types: deb
URIs: file:$rrepo
Suites: runtimes
Components: main
Signed-By: $rkeyring
SRC
  use "$repo" "$repo/hostashi.asc"
  cp "$rrepo/hostashi-runtimes.asc" "$rkeyring"
  out=$(update) || fail "apt-get update with both suites: $out"
  grep -qiE '^(W|E):' <<<"$out" && fail "apt-get update warned: $out"
  rwant=$(dpkg-deb -f "$rdeb" Version)
  rcand=$(apt-cache policy hostashi-runtimes | awk '/Candidate:/ { print $2 }')
  [ "$rcand" = "$rwant" ] || fail "apt offers hostashi-runtimes '$rcand', want $rwant"
  apt-get install -y -qq hostashi-runtimes >/dev/null || fail "install hostashi-runtimes"
  [ "$(stat -c '%U:%G %a' /usr/share/hostashi/runtimes.json)" = "root:root 644" ] || fail "runtimes.json owner or mode"
  grep -q '"schema": 1' /usr/share/hostashi/runtimes.json || fail "runtimes.json content"

  # The installer's pin (ADR-0077): the suite may provide hostashi-runtimes
  # only. A suite runtimes that offers hostashi, signed with the runtimes
  # key, must not reach apt's candidates.
  cp /src/packaging/apt/00hostashi-runtimes /etc/apt/preferences.d/00hostashi-runtimes
  rcand=$(apt-cache policy hostashi-runtimes | awk '/Candidate:/ { print $2 }')
  [ "$rcand" = "$rwant" ] || fail "with the pin apt offers hostashi-runtimes '$rcand', want $rwant"
  evil=$work/evil
  cp -a "$repo" "$evil"
  mv "$evil/dists/stable" "$evil/dists/runtimes"
  sed -i 's/^Suite: stable$/Suite: runtimes/; s/^Codename: stable$/Codename: runtimes/' "$evil/dists/runtimes/Release"
  rm "$evil/dists/runtimes/InRelease" "$evil/dists/runtimes/Release.gpg"
  GNUPGHOME=$work/other gpg --batch --yes --quiet --clearsign -o "$evil/dists/runtimes/InRelease" "$evil/dists/runtimes/Release"
  sed -i "s|^URIs: .*|URIs: file:$evil|" "$rsrc"
  rm -f "$src"
  rm -rf /var/lib/apt/lists/*
  out=$(update) || fail "apt-get update with the changed runtimes suite: $out"
  pol=$(apt-cache policy hostashi)
  grep 'Candidate: (none)' <<<"$pol" >/dev/null || fail "the runtimes suite can provide hostashi: $pol"
  rm /etc/apt/preferences.d/00hostashi-runtimes
  sed -i "s|^URIs: .*|URIs: file:$rrepo|" "$rsrc"
  use "$repo" "$repo/hostashi.asc"

  # The stable key does not open the runtimes suite, and the other way round.
  cp "$repo/hostashi.asc" "$rkeyring"
  if update >/dev/null; then fail "the runtimes suite was accepted with the stable key"; fi
  rm -f "$rsrc" "$rkeyring"
fi

echo "OK: apt repository with hostashi $want on $(. /etc/os-release && echo "$PRETTY_NAME") ($arch)"
