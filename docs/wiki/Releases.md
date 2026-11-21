# Releases

How Hostashi releases are signed and published (ADR-0050). This page is for maintainers.

## The apt signing key

apt checks every update against one OpenPGP key (Ed25519, valid for two years). The private key lives only in the GitHub environment `release` of the repository, as the secret `APT_SIGNING_KEY`. A job in that environment runs only for a tag `v*` and only after a maintainer approves it.

### Create the key (once)

On your own computer, as an admin of the repository, with `gpg` and the [GitHub CLI](https://cli.github.com) logged in (`gh auth login`):

```sh
curl -fsSLO https://raw.githubusercontent.com/maki-it/hostashi/main/tools/release-key/new.sh
less new.sh
bash new.sh
```

The script creates the environment `release` (your approval, tags `v*` only), creates the key in a temporary gpg home and stores it as the secret without showing it. It leaves a backup `hostashi-apt-signing-key.asc` (mode `0600`) in the current folder: put it into the password manager and delete the file. It prints the fingerprint and the public key; both are public and go into the repository.

The script refuses to replace an existing key. A new key reaches servers only through a package update signed with the old one, so `--replace` is for a planned change or a leak.

## Make a release

1. Tag a commit on `main` and push the tag: `git tag v1.2.3 && git push origin v1.2.3`. A tag with a `-` (`v1.2.3-rc.1`) is a pre-release.
2. The workflow **Release** builds the binaries, the `.deb` files and `SHA256SUMS` into a draft release, signs `SHA256SUMS` with Sigstore (no key: the signature names the workflow) and records the build provenance.
3. For a stable release it then waits for your approval (environment `release`). Approve it in the workflow run. That job builds the apt repository from this release and the four stable releases before it, signs it with the apt key, attaches it as `apt-repo.tar.gz` and publishes the release.

A pre-release is published right after step 2 and never reaches apt.

## Check a download

```sh
cosign verify-blob SHA256SUMS --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github.com/maki-it/hostashi/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c SHA256SUMS
gh attestation verify hostashi_1.2.3_amd64.deb --repo maki-it/hostashi
```
