# Releases

How Hostashi releases are signed and published (ADR-0050). This page is for maintainers.

## The apt signing key

apt checks every update against one OpenPGP key (Ed25519, valid for two years). The private key lives only in the GitHub environment `release` of the repository, as the secret `APT_SIGNING_KEY`. A job in that environment runs only for a tag `v*` and only after a maintainer approves it.

The current key (created 2026-11-21, expires 2028-11-20):

```text
64F6 AE8D C40D 1299 E68A  008F 735A 7A2D 7411 49DB
Hostashi archive signing key <kontakt@maki-it.de>
```

It is in `packaging/keyring/hostashi-archive-keyring.asc` (the package installs it to `/usr/share/keyrings/`) and built into the install script `packaging/get/install.sh`. CI checks that both are the same key and that it does not expire within 90 days; the release job refuses to sign with any other key.

### Create the key (once)

On your own computer, as an admin of the repository, with `gpg` and the [GitHub CLI](https://cli.github.com) logged in (`gh auth login`):

```sh
curl -fsSLO https://raw.githubusercontent.com/maki-it/hostashi/main/tools/release-key/new.sh
less new.sh
bash new.sh
```

The script creates the environment `release` (your approval, tags `v*` only), creates the key in a temporary gpg home and stores it as the secret without showing it. It leaves a backup `hostashi-apt-signing-key.asc` (mode `0600`) in the current folder: put it into the password manager and delete the file. It prints the fingerprint and the public key; both are public and go into the repository.

The script refuses to replace an existing key. A new key reaches servers only through a package update signed with the old one, so `--replace` is for a planned change or a leak.

### Renew the key

Nothing to do by hand. When the key has less than a year left, the approved apt job of the next stable release extends it by two years (same key, same fingerprint) and opens a pull request `packaging: extend the apt signing key by two years` with the renewed public key. Merge it; the release after that brings the renewed key to the servers with `apt upgrade`. The secret `APT_SIGNING_KEY` stays as it is.

If no release comes along, the monthly workflow **apt key** opens an issue a year before expiry, asking for a stable release. Servers that still have the old key when it expires cannot update from `get.hostashi.com` until they get the new key by hand.

## Make a release

1. Tag a commit on `main` and push the tag: `git tag v1.2.3 && git push origin v1.2.3`. A tag with a `-` (`v1.2.3-rc.1`) is a pre-release.
2. The workflow **Release** builds the binaries, the `.deb` files and `SHA256SUMS` into a draft release, signs `SHA256SUMS` with Sigstore (no key: the signature names the workflow) and records the build provenance.
3. For a stable release it then waits for your approval (environment `release`). Approve it in the workflow run. That job builds the apt repository from this release and the four stable releases before it, signs it with the apt key, attaches it as `apt-repo.tar.gz` and publishes the release.
4. After the apt job, the workflow **Pages** deploys `get.hostashi.com` from that release's `apt-repo.tar.gz`: the repository at `/apt`, the install script at `/install.sh` and a short start page. It checks the repository's signature against `packaging/keyring` first. Run **Pages** by hand to deploy the latest release again.

A pre-release is published right after step 2 and never reaches apt.

## Check a download

```sh
cosign verify-blob SHA256SUMS --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github.com/maki-it/hostashi/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c SHA256SUMS
gh attestation verify hostashi_1.2.3_amd64.deb --repo maki-it/hostashi
```
