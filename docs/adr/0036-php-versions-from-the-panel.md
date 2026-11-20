# ADR-0036 Installing PHP versions from the panel

**Status:** Accepted (Kim, 2026-11-20). **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0018 (API, scopes), ADR-0019 (PHP package source and versions), ADR-0033 (hardening baseline, updates), ADR-0034 (firewall: the agent starts a fixed root unit), ADR-0035 (roles), ADR-0017 (import needs older PHP versions).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Since PR #180, `install.sh apply --yes --php-versions 8.1,7.4` adds sury.org with pinned key fingerprints and the Spike S10 pin, and installs a fixed package set per version. Without the option nothing of this happens [V: `packaging/install.sh` step `phpversions`, container test on Debian 13].
- Kim wants the same from the panel: "Install PHP 8.1" as a button, for example when a CloudPanel import needs a version that is not installed (ADR-0017, review `php_missing`) [V: Kim, thread, 2026-11-20].
- The agent may start processes only through reviewed `os/exec` exceptions. It does not run `apt-get` today; `apt-get`, `nft` and `sysctl` run only in the installer (ADR-0033) [V: `.golangci.yml`, ADR-0033 consequences].
- The agent already starts fixed root units through `internal/phpctl`, with unit names that must match fixed patterns, for example `hostashi-firewall.service` (ADR-0034) and the cron units (ADR-0028) [V: `internal/phpctl`].
- The panel lists installed, supported and legacy versions (`GET /api/v1/php/versions`), and a site can switch between installed versions [V: `internal/serve/sites_api.go`, `web/src/Sites.tsx`].
- sury.org already publishes PHP 8.6 packages for trixie and resolute [V: package index, 2026-11-20]. `agentapi.PHPVersions` ends at 8.5 [V].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. A fixed root unit per version, `hostashi-php-install@<version>.service`, that runs a root-owned script; the agent only starts the unit through `phpctl`** | No new `os/exec` exception. Same code as the installer step, so one tested path. The panel and the agent cannot change what runs. apt runs in its own unit with its own log and timeout, the agent does not wait on dpkg locks | The agent sees only "unit active, succeeded or failed" and the journal, not a structured progress report [A] |
| B. A new `os/exec` exception `internal/aptctl` that runs `apt-get` with fixed arguments | Errors directly in the agent | `apt-get` as root from the long-running agent: package maintainer scripts run with the agent's rights, and a bug in argument building installs any package. Exactly the general interface the rules forbid [A] |
| C. Installer only, the panel shows the command | Smallest surface | Kim wants the button. An import then needs an SSH session in the middle [V: Kim] |

## Recommendation

**Option A.**

- **Script.** The installer writes `/usr/local/lib/hostashi/php-install` (root, `0755`) and the template `hostashi-php-install@.service`. The script takes one argument, checks it against the same allowlist as `agentapi.PHPVersions` (a Go test keeps the lists equal), and does exactly what the installer step `phpversions` does: key with the pinned fingerprints, the pin first, the source, the fixed packages of that version, the package's own FPM unit off, and the version recorded in the installer state. The installer step itself calls the same script, so there is one path [A].
- **Unit.** `Type=oneshot`, a timeout of 15 minutes [E], no core dumps, its own log in the journal. `ConditionPathExists=` none: the instance name is checked by the script, and `phpctl` only starts names matching `hostashi-php-install@(7.4|8.1|8.2|8.3|8.4|8.5).service`. One installation at a time (the agent refuses a second start while one runs).
- **Agent operations.** `php.install{version}` starts the unit with `--no-block`; `php.install.status` returns `idle`, `running`, `succeeded` or `failed` and the last 50 journal lines of that unit [A: journal lines are the script's own output plus apt's, nothing from sites].
- **API and roles.** `POST /api/v1/php/versions/{version}/install` and `GET /api/v1/php/install`. New scope `php:install`, high-risk like `firewall:write`, and only for the role `admin` (ADR-0035). Every start and its result go to the audit log.
- **Panel.** The PHP page lists the supported versions with "installed", "not installed" (button "Install"), "legacy" with the end-of-life date and a confirmation for legacy versions. The import review `php_missing` links to it.
- **New PHP releases are not picked up automatically.** A new minor version (8.6) is added to the allowlist in a Hostashi release, after the pool template, the unit sandbox and the end-of-life date are checked for it. Then it shows up as "not installed" in the panel. Hostashi never installs a version by itself.
- **Newer versions are shown greyed out.** When sury.org is set up, the agent reads apt's local package lists (`/var/lib/apt/lists/packages.sury.org_*_Packages`, a file read, no process) and returns the `php<version>-fpm` versions above the allowlist. The panel shows them disabled, with "Not yet released for Hostashi; comes with a Hostashi update". Without sury.org nothing extra is shown [A].
- **Unmaintained versions.** Hostashi keeps a table of the official dates per version (active support, security support; source `php.net/supported-versions.php`, checked at each release) and derives three states: *supported*, *security fixes only*, *end of life*. Rules:
  1. An end-of-life version is never installed or chosen without an explicit confirmation that names the date; new sites never default to it.
  2. Sites on an end-of-life version keep running. Hostashi never switches or removes a version by itself, because that breaks the site.
  3. The site list and the site page mark such sites; doctor warns (`system.php_eol`) with the sites and the date; 60 days before a version's end the warning starts as a notice.
  4. Whether sury.org still ships fixes after the official end is not promised by anyone [U]; the panel says "no security fixes from PHP" and does not suggest otherwise.
- **Updates of installed versions** come through unattended-upgrades as today (sury.org origin, ADR-0019 implementation).

## Consequences

- `internal/phpctl` may start one more fixed unit pattern; its comment and tests name it. No new package with `os/exec`.
- The installer step `phpversions` is reduced to calling the script; the container test installs one version through the panel API as well.
- Removing a version that no site uses is a later addition with the same pattern (`hostashi-php-remove@<version>`) [A].
- Doctor reports a failed installation and a sury.org key that expires within 60 days.

## Risks to name openly

- **Root package installs triggered from the web.** An admin session or a leaked `php:install` token can make the server install packages from sury.org. Only the fixed packages of allowlisted versions can be chosen, but their maintainer scripts run as root, and sury.org is one maintainer's repository (ADR-0019) [A].
- **apt locks and partial installs.** A run of unattended-upgrades may hold the dpkg lock; the script waits for it up to the unit timeout. A failed run leaves the pin and source in place and can be retried [A].
- **Old versions.** Installing 7.4 or 8.1 is easy now; the panel's legacy warning is the only brake [V: ADR-0019].
- **Not a pentest.**

## Open questions

1. ~~Install from the panel through a fixed root unit (option A), admin only, with the scope `php:install`?~~ **Decided (Kim, 2026-11-20): yes.**
2. ~~Should removing unused versions come in v1 as well, or later?~~ **Decided (Kim, 2026-11-20): later, after v1.**
3. ~~End-of-life versions: keep running with warnings and a confirmation on install and choice, never removed automatically?~~ **Decided (Kim, 2026-11-20): yes**, and newer versions from sury.org are shown greyed out with a note.
