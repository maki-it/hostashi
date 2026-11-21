# ADR-0050 Signed releases, install and updates through apt

**Status:** Accepted (2026-11-21, by Kim). **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0020 (nginx package source), ADR-0019 and ADR-0036 (PHP versions), ADR-0033 (hardening, unattended-upgrades), ADR-0002 (license), roadmap phase 3 ("signed releases, SBOM, support and update policy").

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- There is no release or update flow yet. The installer takes a locally built binary (`install.sh apply --yes --binary PATH`) and copies it to `/usr/local/bin/hostashi` [V: `packaging/install.sh`, step `binary`]. Units, configs and helper files come from the checkout next to the installer [V].
- The binary already carries a version (`-ldflags -X main.version=...`, `hostashi version`) [V: `cmd/hostashi/main.go`].
- Database migrations are embedded and run when the panel opens the store; a binary that is older than the schema refuses to start with `ErrSchemaTooNew` [V: `internal/store/migrate.go`, `internal/store/store.go`].
- The installer is idempotent with check/apply steps and already manages unattended-upgrades: distribution security updates only, no automatic reboot, plus an origin pattern for third-party sources it adds (nginx, sury.org) [V: `packaging/apt/52hostashi-unattended-upgrades`, step `updates`].
- Eleven installer steps call `apt-get install` themselves [V: `grep`]. Inside a dpkg maintainer script the dpkg lock is held, so these steps cannot run from `postinst` [U].
- The repository `maki-it/hostashi` is public [V: GitHub API, 2026-11-21], so GitHub Releases and GitHub Pages can serve files without a login.
- Kim wants signed releases on GitHub and the simplest possible update, ideally `apt upgrade` (2026-11-21).
- apt in Debian 13 verifies repositories with `sqv` and rejects weak keys and SHA-1 signatures; Ed25519 or RSA 3072+ keys work [U].

## Decisions in this ADR

1. Package format and build
2. Where the apt repository lives
3. Signing keys and where they are kept
4. Install with `apt install`, and what the package does on upgrade (`postinst`)
5. Rollback
6. Automatic updates
7. Release channels

## 1. Package format and build

| Option | Pro | Contra |
|--------|-----|--------|
| A. Tarball with binary, users download and run the installer again | Nothing new | Not `apt upgrade`; every update is manual |
| **B. A `.deb` per architecture, built by GoReleaser (nFPM inside)** | One tool builds binaries, `.deb`, checksums, SBOM and the GitHub release; widely used for Go projects [U] | One more build dependency; GoReleaser config must be kept in sync with the installer files |
| C. Hand-written `debian/` directory and `dpkg-buildpackage` | Most "Debian-like" | Much more packaging code, slower CI, little gain for a single static binary |

**Recommendation: B.** A tag `vX.Y.Z` on `main` starts a release workflow. GoReleaser (newest release, checked when the workflow is written) builds `hostashi` for amd64 and arm64 with the embedded SPA, the version and `-trimpath`, and packs a `.deb` named `hostashi` with:

- `/usr/bin/hostashi` (moves from `/usr/local/bin`; the installer's `binary` step switches to the package path and removes the old file once),
- systemd units under `/usr/lib/systemd/system/` (vendor path, so the admin's overrides in `/etc/systemd/system/` keep working),
- the installer and its data files under `/usr/share/hostashi/` (sysctl, ssh, fail2ban, apt, Adminer, nginx drop-in),
- `Depends:` on the packages the installer installs today (nginx, php-fpm and modules, mariadb-server, restic, fail2ban, valkey, nftables, unattended-upgrades …), so apt installs them before `postinst` runs.

The release on GitHub carries the `.deb` files, plain binaries, `SHA256SUMS`, an SPDX SBOM per artifact and the signatures from section 3.

## 2. Where the apt repository lives

| Option | Pro | Contra |
|--------|-----|--------|
| A. Only GitHub Releases, no apt repository | Simplest | No `apt upgrade`; the user downloads each `.deb` |
| B1. GitHub Pages in a second public repo (for example `maki-it/hostashi-apt`) under `apt.hostashi.com` | Own domain for apt; history of every published index in git | A second repo to maintain |
| **B2. GitHub Pages of `maki-it/hostashi` itself on `hostashi.com`: start page at `/`, docs at `/docs`, apt at `/apt`** | One repo for everything; free and static, no server | One repo has one Pages site, so apt and docs share one domain and one deploy; Pages limits the site to 1 GB [U], enough if old versions are pruned [E] |
| B3. A new GitHub org `hostashi` with one repo per site | Clean separation from Maki IT, stable names | Repo transfer and Go module rename; Kim keeps the repo in `maki-it` for now (2026-11-21) |
| C. Forgejo package registry on `git.prod.maki-it.de` (it supports Debian packages) [U] | Already runs, has a Debian registry built in | The registry signs with a key held by the server; a Maki IT server becomes part of every user's supply chain; ties a public project to private infra |
| D. Third-party hosting (Cloudsmith, packagecloud) | Managed, signing built in | Account and possibly cost; key held by the vendor |

**Recommendation: B2** (decided by Kim, 2026-11-21: GitHub, one repo, `hostashi.com/apt`, stay in the `maki-it` org).

- The release workflow builds the repository with `apt-ftparchive` (or `reprepro`) from the `.deb` files of the last five releases [E], signs `InRelease` and `Release.gpg`, and attaches the whole tree as `apt-repo.tar.gz` to the GitHub release. Only this job holds the signing subkey.
- Pages is deployed by one workflow (GitHub Actions as Pages source): it builds the start page and the docs and unpacks the newest `apt-repo.tar.gz` into `/apt`. A docs change re-deploys the same signed tree and never touches the key.
- The install script is served at `https://hostashi.com/install.sh`, the public key at `https://hostashi.com/apt/hostashi.asc`.
- `docs.hostashi.com` and `hostashi.de` redirect to `hostashi.com` at the registrar. This changes the plan of 2026-11-20 (docs under `docs.hostashi.com`) in so far as the docs live under `hostashi.com/docs`; the source stays `docs/wiki/` (ADR-0043).
- Layout: one suite `stable` with component `main` for both distributions, because the package is a static Go binary and the distro differences sit in `Depends:` alternatives and in the installer [A: needs a check that all dependency names exist in both distros; otherwise one suite per codename, `trixie` and `resolute`].

## 3. Signing keys

Two different signatures, with different jobs:

- **apt repository:** apt only accepts OpenPGP signatures on `InRelease`. This key protects every `apt upgrade`, so it is the critical one.
- **GitHub release assets:** keyless Sigstore signatures (cosign, identity = the release workflow on `maki-it/hostashi`) on `SHA256SUMS`, plus GitHub artifact attestations (SLSA provenance). No long-lived secret; anyone can verify which workflow built a file [U].

| Option for the apt key | Pro | Contra |
|--------|-----|--------|
| A. One OpenPGP key without expiry as a plain repository secret | Simplest | Anyone with write access to workflows could use it; a leaked key without expiry signs valid updates forever, because apt never checks revocations [U] |
| B. Primary key offline with Kim, an expiring signing subkey in CI | A leaked subkey can be replaced without changing the key servers trust | Kim must handle `gpg` for renewals; Kim does not want that (2026-11-21) |
| **C. One Ed25519 key with a two-year expiry, held only in a GitHub Actions environment `release` that needs Kim's approval and only allows tags on `main`; expiry extended by a scheduled workflow** | Kim never runs `gpg` by hand: setup is one script, renewal is one click on "Approve" | The whole key lives in GitHub, not offline; after a leak a new key can only reach servers through a package update |
| D. Hardware key (YubiKey) signs each release by hand | Strongest | Releases need Kim's machine; no CI release |

**Recommendation: C** (decided by Kim, 2026-11-21).

- Setup: `tools/release-key/new.sh` creates the key in a temporary `GNUPGHOME`, prints the armored private key for the `release` environment secret, writes the public key to `packaging/apt/hostashi-archive-keyring.asc` and prints the fingerprint for the installer. Kim keeps a copy of the private key in the password manager as a backup. Creating the key and the environment is Kim's step (outside effect).
- Renewal: a scheduled workflow in the `release` environment (monthly check [E]) extends the expiry by two years once less than a year is left, and opens a PR with the updated public key. It needs Kim's approval like a release. The release workflow warns when less than 90 days are left.
- The `hostashi` package ships the public key at `/usr/share/keyrings/hostashi-archive-keyring.asc` and the source points `Signed-By:` there, so every update also updates the key (new expiry, or a new key after a leak). The installer checks the first download against the fingerprint built into the script.
- After a leak: revoke, create a new key, ship it in a package update signed with the old key while it is still trusted, then switch signing. Servers that did not update before the switch need the new key by hand; the docs describe this.
- The public key is also served at `https://hostashi.com/apt/hostashi.asc` and printed with its fingerprint in the docs.
- Release assets get keyless Sigstore signatures and attestations as above; they need no key at all.

## 4. Install and upgrade (`apt install`, `postinst`)

### First install

Kim asked for installation through apt too (2026-11-21), and then decided (2026-11-21): the install script stays the entry point for end users, and it installs Hostashi through apt underneath, so every later update is `apt upgrade`.

The documented way for end users:

```text
curl -fsSLO https://hostashi.com/install.sh   # until the domain is set up: from the latest GitHub release
less install.sh                                    # recommended, not enforced
sudo bash install.sh --panel-domain cp.example.com [options]
```

The script checks the distribution, downloads the public key and compares it with the fingerprint built into the script, writes the key and the source below, runs `apt-get install hostashi` and then `hostashi setup` with its arguments. At the end it prints the panel address and the first admin link. Without `--panel-domain` on a terminal it asks. We document `curl -O` plus `bash` on purpose, not `curl | sudo bash`, so the script can be read first.

The same thing by hand, for admins who prefer plain apt:

```text
# 1. trust the key for this repository only
sudo curl -fsSL https://hostashi.com/apt/hostashi.asc -o /usr/share/keyrings/hostashi-archive-keyring.asc
# 2. add the source (deb822)
sudo tee /etc/apt/sources.list.d/hostashi.sources <<'EOF'
Types: deb
URIs: https://hostashi.com/apt
Suites: stable
Components: main
Signed-By: /usr/share/keyrings/hostashi-archive-keyring.asc
EOF
# 3. install, then set up
sudo apt update && sudo apt install hostashi
sudo hostashi setup --panel-domain cp.example.com [options]
```

The docs print the key's fingerprint next to step 1 so it can be checked (`gpg --show-keys`). The key is trusted for this source only (`Signed-By`), never globally. A pin file in the package lets the repo provide only the `hostashi` package (priority 1 for everything else from that origin), the same idea as the sury.org pin in ADR-0019.

How the installer's options map to apt:

| Option | Pro | Contra |
|--------|-----|--------|
| A. `postinst` sets up the whole server on first install with defaults | One command | `apt install` would silently change SSH, sysctl, firewall and fail2ban; a panel domain cannot be guessed; surprising for a package |
| B. debconf questions (panel domain, port, PHP versions, firewall ports) | The Debian way, preseedable | Awkward for lists and checks (DNS of the panel domain, port conflicts); debconf answers become a second source of truth next to the installer; hard to re-run with other options |
| **C. `apt install hostashi` only installs files and dependencies; `hostashi setup [options]` configures the server** | Same options and checks as `install.sh` today, interactive questions on a terminal and flags for automation; re-running it with other options changes the setup, as now | Two commands instead of one; until `setup` ran, the services stay off |

**Recommendation: C.** On first install `postinst` enables nothing, writes no config and prints one line: "Run `sudo hostashi setup` to configure this server." `hostashi setup` is a thin root-only CLI command that runs `/usr/share/hostashi/install.sh apply` with the given flags (no `os/exec` in the panel or agent: the installer is the admin's own root shell, as today [A: the `cmd/hostashi` CLI path is outside depguard's product-code rule; otherwise `setup` stays a shell wrapper in `/usr/sbin/hostashi-setup`]). Without flags on a terminal it asks for the panel domain and shows the plan before applying. The options are saved to `/etc/hostashi/install.env` (0640 root), so upgrades and later `setup` runs reuse them.

Decided by Kim (2026-11-21): option C, with the install script above as the documented entry point.

`--binary PATH` stays for development and CI (installs a local `.deb` or binary).

### Upgrades

What the package does:

| Option | Pro | Contra |
|--------|-----|--------|
| A. `postinst` only restarts the services | Simple | Changed configs (units, sysctl, nginx drop-in, fail2ban …) never reach existing servers |
| **B. `postinst` runs the installer in a new mode `upgrade`: every step except the ones that install packages, with the saved options; then restarts agent and panel** | Every server converges to the release's state on each `apt upgrade`; uses the tested idempotent steps | Steps that need new packages must move the package into `Depends:`; a failing step fails the upgrade (visible, but must leave a working system) |
| C. `postinst` starts a oneshot unit that runs the full installer after apt finishes | Can install packages | Races with apt and unattended-upgrades; errors are invisible to the person running `apt upgrade` |

**Recommendation: B** (decided by Kim, 2026-11-21). Order on upgrade:

1. `preinst`: stop nothing yet; copy the SQLite database with `VACUUM INTO` to `/var/lib/hostashi/upgrade/hostashi-<old version>.db` (0600, keep the last three) [A: `VACUUM INTO` works while the panel runs; else stop the panel first].
2. dpkg unpacks the new files.
3. `postinst configure`: `install.sh upgrade` (no apt calls, no prompts, logs to `/var/log/hostashi/upgrade.log`), `systemctl daemon-reload`, restart `hostashi-agent` then `hostashi`. The panel runs the new migrations when it opens the store, as today.
4. `postinst` waits until the panel answers its health check (timeout 60 s [E]) and fails loudly with the log path if it does not.

The package never changes site configs by itself; the agent's reconciler re-applies nginx and PHP configs only if the new release changed a template, the same path as a panel action. No new agent operation and no new `os/exec` exception: maintainer scripts and the installer run as root outside the panel, like the installer today.

## 5. Rollback

Decided by Kim (2026-11-21) as below.

- Plain apt works when the database schema did not change between the two versions: `sudo apt install --allow-downgrades hostashi=<old version>`. The apt repository keeps the last five versions [E].
- If the newer release migrated the database, the old binary would refuse to start (`ErrSchemaTooNew`, verified). On a downgrade dpkg runs the installed package's `prerm upgrade <old version>` [U]; it compares the schema versions and, when they differ, stops the downgrade with a message that points to `hostashi-rollback`, before anything is changed.
- `sudo hostashi-rollback [version]` (a root-only shell script from the package, default: the version before the current one) shows which snapshot it uses and that changes made in the panel since the upgrade are lost, asks for confirmation, stops panel and agent, restores the snapshot that `preinst` took when upgrading from that version, installs the old version with apt and starts the services. It is a script like the installer, so the panel and the agent gain no `os/exec`.
- Sites, their files and their databases are not touched by a rollback; only the panel database goes back.
- Migrations stay additive within a minor version where possible, so most rollbacks within a minor need no snapshot [A: a review rule, not enforced by code].
- An upgrade and rollback test in CI (install the last release, create data, upgrade to the new `.deb`, check, roll back, check) is mandatory per release, as the roadmap's test table already says [V: `docs/06-roadmap.md`].

## 6. Automatic updates

| Option | Pro | Contra |
|--------|-----|--------|
| A. Hostashi updates itself automatically by default | Least work for admins | A bad release reaches every server overnight; a panel that restarts by itself surprises people |
| **B. Off by default; installer option `--auto-update` adds the Hostashi origin to the existing unattended-upgrades file; the panel shows "update available" from the apt cache** | `apt upgrade` stays one command; who wants it automatic gets it with one flag; security updates of the distro stay automatic as today | Servers without the flag can fall behind; the panel notice and the email notification (ADR-0044) remind the admin |
| C. A self-updater inside the panel (download, verify, replace binary) | Works without apt | Duplicates apt, needs root in or for the panel, conflicts with the package manager |

**Recommendation: B** (decided by Kim, 2026-11-21). The panel reads the candidate version from the apt lists (file read, no `apt` call) and shows it on Health and in the doctor digest. An "update now" button in the panel would need a new root unit (like `hostashi-php-install@`) and is left for later.

## 7. Release channels

**Recommendation** (decided by Kim, 2026-11-21): one channel `stable` for v1. Pre-releases (`v1.0.0-rc.1`) are published on GitHub only, not in the apt repo. A `testing` suite can come later without breaking clients.

## Consequences

- New: `.goreleaser.yaml`, a release workflow with the `release` environment, a Pages deploy for `hostashi.com` (start page, docs, apt), maintainer scripts, `hostashi setup`, the install script's apt bootstrap, `install.sh` `upgrade` mode, `hostashi-rollback`, upgrade test in CI, release and key renewal docs in the wiki.
- `install.sh` must split "install packages" from "configure"; package lists move into the `.deb`'s `Depends:` and are checked against both distributions in CI.
- Supply chain: the build runs on GitHub-hosted runners; actions are pinned by commit SHA; the release job has no other secrets than the signing subkey.
- Outside effects that need Kim's approval: creating the OpenPGP key, enabling Pages with the custom domain `hostashi.com`, the `release` environment with its secret, and the DNS records and redirects for `hostashi.com`, `docs.hostashi.com` and `hostashi.de`.
- Not in scope: other distributions, an RPM repository, a panel button that updates, Docker images.

## Questions for Kim (all decided on 2026-11-21)

1. ~~Installation~~ Decided (Kim, 2026-11-21): install script as entry point, it installs the package through apt and runs `hostashi setup`; plain `apt install hostashi` plus `hostashi setup` works too.
2. ~~Apt repository~~ Decided (Kim, 2026-11-21): GitHub Pages of `maki-it/hostashi` on `hostashi.com`, apt under `/apt`.
3. ~~Signing~~ Decided (Kim, 2026-11-21): one key with expiry in the `release` environment, renewed by a workflow after Kim's approval, no manual `gpg`.
4. ~~Upgrade~~ Decided (Kim, 2026-11-21): snapshot, configure steps with the saved options, restart, wait for the health check.
5. ~~Automatic updates~~ Decided (Kim, 2026-11-21): off by default, `--auto-update` opts in; the panel shows available updates.
6. ~~Channels~~ Decided (Kim, 2026-11-21): only `stable` for v1; pre-releases on GitHub only.
