# Release checklist

Go through this list for every stable release `vX.Y.Z`. How the release workflow signs and publishes is described in the wiki page [Releases](wiki/Releases.md) ([ADR-0050](adr/0050-signed-releases-and-apt-updates.md)); this page is what to check around it. Section 5 applies to 1.0 only.

The changelog is the release notes that GitHub generates from the merged pull requests, grouped by their `Kind/*` labels ([.github/release.yml](../.github/release.yml)). There is no separate `CHANGELOG` file.

## 1. Before the tag: `main` is ready

- [ ] All workflows of the commit to tag are green on `main`: Go (with both installer halves, the package job and the IPv6-only job), Web, Docs, Licenses, Vulnerabilities. A job that a pull request skipped by its paths runs in full on `main`.
- [ ] **Upgrade from the latest stable release** is green for that commit on Debian 13 and Ubuntu 26.04 (job "Upgrade from the latest release" in the Go workflow, `packaging/test/release-upgrade-test.sh`). It installs the release from `get.hostashi.com`, creates an admin, a PHP site and a database, installs the new package and checks that login, site and data are still there.
- [ ] The same-package upgrade, downgrade and rollback test is green (`packaging/test/upgrade-test.sh`, package job).
- [ ] A fresh install is green: the installer job (`container-test.sh`, parts `core` and `runtimes`) on both distributions.
- [ ] The weekly version workflows (PHP, Node.js, Python, Adminer, WP-CLI, distros) have no open pull request that should be in this release.
- [ ] No open security advisory or `Kind/Security` pull request that should be in this release.
- [ ] Every pull request merged since the last tag has a `Kind/*` label (or `skip-release-notes`), so the release notes group it:

  ```sh
  gh pr list --repo maki-it/hostashi --state merged --search "merged:>$(git log -1 --format=%cI "$(git describe --tags --abbrev=0)")" \
    --json number,title,labels --jq '.[] | select([.labels[].name | select(startswith("Kind/") or . == "skip-release-notes")] | length == 0) | "\(.number) \(.title)"'
  ```

- [ ] Docs match the release: the README's status line and feature list, wiki pages for new features (`docs/wiki/`), `SECURITY.md` "Supported versions", and the status line of section 3.0 in [03-features](03-features.md).
- [ ] The apt signing key does not expire within a year, or the renewal pull request from the last release is merged (checked by `packaging/test/key-test.sh` in CI and the monthly workflow "apt key").

## 2. Tag (maintainer)

- [ ] Tag exactly the commit checked above and push the tag: `git tag vX.Y.Z <commit> && git push origin vX.Y.Z`. Only maintainers tag; Claude sessions cannot push tags.

## 3. Release workflow

- [ ] "Build, sign and attest" is green: draft release with the binaries, the `.deb` files, `SHA256SUMS` and `SHA256SUMS.sigstore.json`.
- [ ] Approve the job "Signed apt repository and publish" (environment `release`). It publishes the release with `apt-repo.tar.gz`.
- [ ] The Pages workflow deploys `get.hostashi.com`. If it fails, run **Pages** by hand on `main`, do not re-run the old run (it uses the old workflow file).
- [ ] If the apt job renewed the key, merge its pull request `packaging: extend the apt signing key by two years`.

## 4. After the release: check what servers get

- [ ] The apt repository serves the new version in the suite `stable` for both architectures, signed by the release key:

  ```sh
  for a in amd64 arm64; do curl -fsS "https://get.hostashi.com/apt/dists/stable/main/binary-$a/Packages" | grep -c "^Version: X.Y.Z$"; done
  curl -fsSO https://get.hostashi.com/apt/dists/stable/InRelease
  gpgv --keyring <(gpg --dearmor <packaging/keyring/hostashi-archive-keyring.asc) InRelease
  ```

- [ ] The suite `runtimes` still answers and is signed by the runtimes key (`https://get.hostashi.com/apt/dists/runtimes/InRelease`, key `packaging/keyring/hostashi-runtimes-keyring.asc`).
- [ ] The downloads verify (Sigstore and build provenance), as in the wiki page [Releases](wiki/Releases.md#check-a-download).
- [ ] `https://get.hostashi.com/install.sh` is the script from the tagged commit (`diff` against `packaging/get/install.sh`).
- [ ] Fresh install from `get.hostashi.com` on a new Debian 13 and Ubuntu 26.04 server (or `release-upgrade-test.sh` with `FROM=X.Y.Z`, whose first step is exactly that).
- [ ] Own servers: `apt upgrade` (or the automatic update) on the maintainer's servers, then **Health** in the panel and `hostashi doctor` show no new warnings.
- [ ] Release notes: add a short summary at the top of the generated notes, with breaking changes and manual steps first.

## 5. Only for 1.0

The acceptance of [phase 3 in the roadmap](06-roadmap.md#phase-3-release-10-810-weeks): no open critical or high findings from the own security review, upgrade tests from beta to 1.0, this checklist complete.

- [ ] Own security review: no open critical or high finding ([second review, 2026-11-26](security-reviews/2026-11-26-second-own-review.md)). Review the changes merged since then the same way before the tag.
- [ ] Upgrade from the last beta (0.x) to 1.0: section 1, job "Upgrade from the latest release".
- [ ] Every row of section 3.0 in [03-features](03-features.md) is built or moved out of 1.0 by the maintainer. On 2026-11-27 the only row left was the panel UI in English and German ([ADR-0071](adr/0071-panel-ui-in-english-and-german.md)), which the maintainer kept in 1.0.
- [ ] No ADR is still `Proposed` that 1.0 depends on ([07-decisions](07-decisions.md), `docs/adr/`).
- [ ] SBOMs are attached to the release and listed in `SHA256SUMS` (`*.tar.gz.sbom.json`, `*_web.sbom.json`; [roadmap phase 3](06-roadmap.md#phase-3-release-10-810-weeks): "Signed releases, SBOM, support and update policy").
- [ ] Support and update policy in `SECURITY.md` ("Supported versions": only the newest release) still holds, and the README links it.
- [ ] README and `SECURITY.md` no longer say "before 1.0" or "Phase 0".
- [ ] Licenses: REUSE lint and the dependency license allowlist are green (workflow Licenses).
