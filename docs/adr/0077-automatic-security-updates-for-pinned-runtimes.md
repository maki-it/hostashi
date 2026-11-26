# ADR-0077 Automatic security updates for pinned runtimes

**Status:** Proposed. **Date drafted:** 2026-11-26. **Decides:** Kim. **Related:** ADR-0033 (hardening, unattended-upgrades), ADR-0036 (PHP versions), ADR-0038 (Adminer), ADR-0050 (signed releases, `--auto-update`), ADR-0054 (site types, Node.js), ADR-0061 (Python sites), ADR-0064 (malware scan, ClamAV timer), ADR-0069 (WP-CLI).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

Kim does not want to act by hand when a critical security fix is published. Today the server side is automatic for everything that comes from apt, but not for what Hostashi pins itself.

**What is automatic today**

- unattended-upgrades runs daily and takes the distribution's security archive, plus sury.org and apt.postgresql.org when used, never reboots [V: `packaging/apt/52hostashi-unattended-upgrades`, `uu_origins` in `packaging/install.sh`]. This covers nginx, PHP (distro and sury.org), MariaDB, PostgreSQL, Valkey, OpenSSL and the rest of the base system [A: all of them come from these origins on a default install].
- ClamAV has its own daily timer `hostashi-clamav-upgrade.timer`, which runs `apt-get install --only-upgrade` for exactly the installed ClamAV packages from every apt source [V: `packaging/systemd/hostashi-clamav-upgrade.timer`, ADR-0064 "Updates"].
- When the installed Node.js or CPython release of a line differs from the pin in the running binary, the agent installs the pinned release at start, without a click [V: `rtinstall.Manager.Update`, `internal/agent/agent.go`].

**Why ClamAV is not in unattended-upgrades**

- Debian ships new ClamAV versions through `stable-updates` (and point releases), Ubuntu through `-updates` as well as `-security` [V: ADR-0064 Context, checked 2026-11-24; U: Debian's policy to update ClamAV in stable-updates because signatures need a current engine].
- unattended-upgrades filters by origin. Its package filters (`Package-Whitelist`, `Package-Blacklist`) apply to every origin at once [U: unattended-upgrades documentation]. "All of `-security` plus only ClamAV from `-updates`" therefore cannot be expressed in one configuration: adding `-updates` as an origin would install every non-security update of the distribution, which ADR-0033 rules out ("only security").
- The timer is the narrow way: same apt, same signatures, only the ClamAV packages. It does nothing while the scan is off [V: timer comment]. **No change is proposed here.**

**What is not automatic today**

| Component | Pinned in | Update path today |
|-----------|-----------|-------------------|
| Node.js LTS releases | `internal/agentapi/node_versions.go` | daily workflow opens a PR, a maintainer merges, the next Hostashi release ships it, servers install it only with `--auto-update` or `apt upgrade` [V: `.github/workflows/node-versions.yml`] |
| CPython (python-build-standalone) | `internal/agentapi/python_versions.go` | same, daily [V: `python-versions.yml`] |
| WP-CLI | `internal/agentapi/wpcli.go` | same, weekly [V: `wpcli-version.yml`] |
| Adminer | `packaging/adminer/SHA256SUMS` | no watcher at all [V: no workflow] |
| Hostashi itself (Go toolchain, Go and npm dependencies) | `go.mod`, `web/package-lock.json` | govulncheck runs on pull requests only [V: `go.yml`]; a release needs a tag by Kim |

So a Node.js security release reaches a server only after three manual steps: merge, tag, upgrade (or `--auto-update`, off by default per ADR-0050).

**Constraints**

- Kim's working rules: no GitHub auto-merge; Claude merges its own PRs once all checks incl. installer are green; tags are set by Kim [V: project memory]. This ADR proposes to change the first and to work around the second for data only, so Kim has to decide it explicitly.
- The apt signing key lives in the GitHub environment `release`, which only accepts tags `v*` [V: `release.yml`, ADR-0050]. Required reviewers on that environment are off since 2026-11-24 [V: project memory].
- `main` usually contains unreleased features. An automatic release from `main` would ship them untested on real servers.

## Options

**Part 1: who merges a security pin PR**

| Option | Pro | Contra |
|--------|-----|--------|
| 1A. A maintainer merges (today) | A human looks at every pin | Hours to days of delay; Kim has to watch |
| **1B. The pin workflow merges its own PR when the diff touches only pin files, the upstream signature/attestation check passed, and all checks incl. installer tests are green; only for a new patch release of a line Hostashi already has** | No human step for the common case; new lines, end-of-life changes and anything else stay manual | A compromised upstream release that passes the signature check lands without a human look; changes Kim's "no auto-merge" rule for bot PRs |

**Part 2: how a merged pin reaches the servers**

| Option | Pro | Contra |
|--------|-----|--------|
| 2A. With the next Hostashi release and `--auto-update` (today) | One delivery path | Needs Kim's tag; servers without `--auto-update` never get it by themselves |
| 2B. A workflow cuts an automatic patch release (`vX.Y.Z+1`) from the last tag plus the pin commit | Same package, same signing | A workflow sets tags (against Kim's rule); release branches and cherry-picks for every pin; the whole binary changes for a data change; still needs `--auto-update` |
| **2C. Pins as signed data in their own apt suite: a small package `hostashi-runtimes` (architecture `all`) in suite `runtimes` of the existing repository, built and signed whenever pin files change on `main`; the agent prefers its pins over the built-in ones; unattended-upgrades takes the suite by default** | Security pins reach every server within a day without a Hostashi release and without `--auto-update`; no code ships this way, only versions and SHA-256 sums; same key, same apt trust as today | The signing key becomes usable from `main`, not only from tags; a second publish path for the apt repository; the agent has to merge two pin sources safely |

**Part 3: Hostashi's own vulnerabilities**

| Option | Pro | Contra |
|--------|-----|--------|
| 3A. As today: govulncheck on PRs only | Nothing new | A Go standard library CVE in a released binary goes unnoticed until someone looks |
| **3B. Daily govulncheck and `npm audit --omit=dev` against the latest release tag; a finding opens (or updates) one issue labeled `security` and assigned to Kim** | Kim is told the same day; no automatic release of code | Kim still tags the fix release |
| 3C. 3B plus `--auto-update` on by default for new installs | Fix releases reach servers without a flag | Reverses ADR-0050 decision 5; every feature release also installs itself |

## Recommendation

**1B + 2C + 3B, and an Adminer watcher.** ClamAV stays on its timer. `--auto-update` stays opt-in (ADR-0050); Kim can enable it on their own servers.

**Part 1: auto-merge of security pin PRs (1B)**

- Not GitHub auto-merge. The pin workflow itself waits for the checks of its PR and merges with `gh pr merge --squash` when all are green, so the rule lives in one reviewed workflow file.
- Only when: the diff changes nothing but the pin file of that runtime and its test data; every changed entry is a newer patch release of a line already in the list (Node.js `24.22.3 → 24.22.4`, CPython `3.12.16 → 3.12.17`, or the same CPython release with a newer python-build-standalone build tag, which is how OpenSSL fixes arrive); the upstream proof was checked in the same run (Node.js: SHASUMS256.txt signed by a release key from nodejs/release-keys; CPython: GitHub attestation of python-build-standalone) [V: existing verify steps in both workflows].
- New lines, removed lines, changed end-of-life dates, WP-CLI (not a security-critical runtime) and every other bot PR stay manual and are labeled as today.
- A merged PR gets the label `security` and a comment that says it was merged by the workflow.

**Part 2: runtimes suite (2C)**

- Data format: `/usr/share/hostashi/runtimes.json` with `schema`, `generated` (UTC), and per runtime the same fields as the Go pins (line, version, end of life, SHA-256 per arch). Built by a Go tool from the pin files, so the binary and the package cannot disagree in format.
- The agent reads the file at start and once a day. Per line it takes the newer of built-in pin and file pin; it ignores unknown runtimes, unknown lines, a lower version, missing checksums and an unknown schema, and logs why. It never adds a line the binary does not know, so the file cannot install anything new, only a newer patch of what is there [A: that is enough for security fixes].
- The existing `rtinstall.Manager.Update` then installs the new release with its checksum check. A failure is shown by doctor and the panel as today.
- Repository: same signed apt repository on get.hostashi.com, new suite `runtimes` next to `stable`. The Pages job publishes both from the latest stable release and the latest runtimes build. unattended-upgrades gets `"origin=Hostashi,codename=runtimes";` always; `codename=stable` stays behind `--auto-update`. The `hostashi` package depends on nothing new; `hostashi-runtimes` is recommended and installed by the installer.
- Signing: a new environment `runtimes` that accepts only `main`, with a second subkey or a separate key that the keyring pins for this suite only [A: apt's `Signed-By` per source line makes that possible; needs a check]. If the separate key turns out to be too much, the fallback is to allow `main` in environment `release`; that widens who can sign stable packages, so it is not recommended.
- The agent never fetches anything itself; apt and its signature check stay the only download path for pins.

**Part 3: watch Hostashi itself (3B)**

- One scheduled workflow, daily: check out the latest stable tag, run govulncheck (call graph mode, so only reachable findings count) and `npm audit --omit=dev --audit-level=high` in `web/`. A finding opens or updates the issue "security: vulnerable dependency in vX.Y.Z" with label `security`, assigned to Kim; GitHub mails Kim.
- The fix PR follows the normal path (Claude or Kim), then Kim tags. Servers with `--auto-update` install it the same night.

**Adminer**

- A weekly workflow like `wpcli-version.yml` for `packaging/adminer`, manual merge (it ships in the package anyway).

## Consequences

- A Node.js or CPython security patch reaches every server in about one to two days without any human step [E: workflow run + checks ~1 h, unattended-upgrades daily with random delay, agent daily check].
- Supply chain: the main protection becomes the upstream signature check plus the CI tests, not a human review. A compromised Node.js release signed with a valid release key, or a compromised python-build-standalone build with a valid attestation, would reach all servers automatically [A: low likelihood, high impact]. Mitigations: patch releases of known lines only, checksums pinned in signed data, the change is visible in the repo and the audit log, and Kim can stop it by disabling the workflow.
- Kim's rules change: bot PRs for security pins merge themselves; the runtimes suite is published from `main` without a tag. Code still ships only with Kim's tag.
- New moving parts: one apt suite, one Pages input, one environment and key, one agent code path for merging pins, one scheduled vulnerability workflow, one Adminer workflow.
- doctor: new check `hostashi.runtimes` (package installed, file readable, not older than 14 days while pins on `main` are newer [A: threshold]).
- Effort [E]: Part 3 and Adminer watcher 1 PR each; Part 1 1 PR (both pin workflows); Part 2 three PRs (data tool and agent merge with tests; package, suite and Pages; installer, unattended-upgrades origin, doctor, wiki). Order: Part 3, Part 1, Part 2.
- Tests: pin merge rules (newer patch wins, lower version, unknown line, missing checksum, bad schema, both sources equal), workflow guard (diff outside pin files, new line, failed verify → no merge), apt repo test with both suites, installer test that unattended-upgrades lists the runtimes origin, upgrade test that a newer runtimes package triggers the update.

## Open questions for Kim

1. Part 1: should security pin PRs for Node.js and CPython merge themselves (1B), as an exception to "no auto-merge"?
2. Part 2: runtimes suite on by default (2C), automatic patch releases (2B), or keep today's path (2A)?
3. Part 3: daily vulnerability check against the latest release (3B), and `--auto-update` stays opt-in?
