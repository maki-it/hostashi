#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Creates the apt signing key and the GitHub environment that holds it
# (ADR-0050, section 3). Run once, on your own computer, by a repository
# admin; nothing else needs gpg by hand.
#
#   tools/release-key/new.sh [--repo OWNER/NAME] [--replace]
#
# Needs bash, gpg (2.2 or later) and the GitHub CLI, logged in as an admin
# of the repository (gh auth login). The script
#   1. creates or updates the environment "release": your approval for each
#      job, deployments only from tags v*;
#   2. creates one Ed25519 OpenPGP key with a two-year expiry in a temporary
#      gpg home that is deleted at the end;
#   3. stores the private key as the environment secret APT_SIGNING_KEY,
#      straight from gpg to gh, never on the terminal;
#   4. writes a backup of the private key to ./hostashi-apt-signing-key.asc
#      (mode 0600) for your password manager; delete the file afterwards;
#   5. prints the public key and its fingerprint, which go into the
#      repository (the package and the installer check against them).
#
# It refuses to replace an existing key unless --replace is given: servers
# trust the old key until a package update brings the new one.
set -euo pipefail

REPO=maki-it/hostashi
ENV=release
SECRET=APT_SIGNING_KEY
UID_TEXT="Hostashi archive signing key <kontakt@maki-it.de>"
BACKUP=./hostashi-apt-signing-key.asc
replace=""

while [ $# -gt 0 ]; do
  case "$1" in
    --repo) [ $# -ge 2 ] || { echo "--repo needs OWNER/NAME" >&2; exit 64; }; REPO=$2; shift;;
    --replace) replace=1;;
    -h | --help) sed -n '3,25p' "$0"; exit 0;;
    *) echo "unknown argument: $1" >&2; exit 64;;
  esac
  shift
done

die() { echo "release-key: $*" >&2; exit 1; }
step() { echo "== $*"; }

command -v gpg >/dev/null || die "gpg is missing (Debian/Ubuntu: apt install gnupg, macOS: brew install gnupg)"
command -v gh >/dev/null || die "the GitHub CLI is missing (https://cli.github.com)"
gh auth status >/dev/null 2>&1 || die "gh is not logged in: run gh auth login"
[ "$(gh api "repos/$REPO" -q .permissions.admin)" = true ] || die "you are not an admin of $REPO"
[ ! -e "$BACKUP" ] || die "$BACKUP exists; move it away first"

if gh api "repos/$REPO/environments/$ENV/secrets/$SECRET" >/dev/null 2>&1; then
  [ -n "$replace" ] || die "$ENV already holds $SECRET. Replacing it means a new key for every server (ADR-0050); rerun with --replace if that is what you want."
fi

step "environment $ENV: your approval, tags v* only"
me=$(gh api user -q .id)
gh api -X PUT "repos/$REPO/environments/$ENV" --input - >/dev/null <<JSON
{
  "wait_timer": 0,
  "prevent_self_review": false,
  "reviewers": [{"type": "User", "id": $me}],
  "deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}
}
JSON
policies=$(gh api "repos/$REPO/environments/$ENV/deployment-branch-policies" -q '.branch_policies[] | "\(.type) \(.name)"')
while read -r type name; do
  [ -n "$type" ] || continue
  [ "$type $name" = "tag v*" ] || die "environment $ENV allows $type '$name' as well; remove that policy in the repository settings, then run again"
done <<<"$policies"
grep -qx 'tag v\*' <<<"$policies" ||
  gh api -X POST "repos/$REPO/environments/$ENV/deployment-branch-policies" -f name='v*' -f type=tag >/dev/null

step "key: Ed25519, expires in two years"
GNUPGHOME=$(mktemp -d)
export GNUPGHOME
trap 'gpgconf --kill all >/dev/null 2>&1 || true; rm -rf "$GNUPGHOME"' EXIT
chmod 0700 "$GNUPGHOME"
gpg --batch --quiet --trust-model always --passphrase '' --quick-gen-key "$UID_TEXT" ed25519 sign 2y
fpr=$(gpg --batch --with-colons --list-keys "$UID_TEXT" | awk -F: '$1 == "fpr" { print $10; exit }')
[ ${#fpr} -eq 40 ] || die "could not read the new key's fingerprint"

step "secret $SECRET in $ENV"
gpg --batch --armor --export-secret-keys "$fpr" | gh secret set "$SECRET" --env "$ENV" --repo "$REPO"

(umask 077 && gpg --batch --armor --export-secret-keys "$fpr" >"$BACKUP")
public=$(gpg --batch --armor --export "$fpr")

cat <<TXT

Done. The private key is in the environment "$ENV" of $REPO.

1. Put $BACKUP into your password manager, then delete it:
     rm $BACKUP
2. Send the fingerprint and the public key below to the release thread
   (both are public). They go into the repository with the next PR.

Fingerprint: $fpr

$public
TXT
