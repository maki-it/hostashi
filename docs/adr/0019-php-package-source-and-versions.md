# ADR-0019 PHP package source and multiple PHP versions

**Status:** Accepted (Kim, 2026-11-18). **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0015 (one FPM master per site), ADR-0005 (nginx.org as third-party apt source), ADR-0009 (supported distributions), Spike S5 (installer).

## Context

- Kim decided: **use official packages, no own fork** of PHP [V: Kim, thread, 2026-11-18].
- Kim also named the constraint: sites such as WordPress installations need **different, sometimes old, PHP versions** [V: Kim, thread, 2026-11-18].
- The distributions ship exactly one PHP: **8.4 on Debian 13, 8.3 on Ubuntu 24.04** [V: Spike S5, installed in containers].
- ADR-0015 plans one PHP-FPM master per site, so each site can use its own binary and configuration [V: ADR-0015].
- Hostashi installs only on fresh servers, so there is no existing PHP setup to respect [V: ADR-0009].

## Options

| Option | Pro | Con |
|--------|-----|-----|
| **A. Distribution packages only** | Boring, signed by the distro, security updates through the normal channel | One PHP version per server. Sites that need 7.4 or 8.1 cannot be hosted. Does not meet Kim's constraint |
| **B. Distribution default plus the sury.org repository for more versions** | Same repository mechanism on both distros. Covers PHP 5.6 to 8.5 on amd64 and arm64 [V: package indexes, 2026-11-18]. Versions install side by side [V: test below] | A third party that installs root-level packages. Its maintenance is not under our control |
| **C. Own fork or own builds** | Full control, own patches | We would build, test and ship every PHP security release for two distros and two architectures, alone. Rejected by Kim |
| **D. One container per PHP version** | Strong separation, any version | New runtime, networking and update model on a single-server panel. Conflicts with "boring tech" and ADR-0007 isolation plans |

## Recommendation

**B, with A as the default.** A fresh server gets the distribution PHP and works without any third-party source. Additional versions are an explicit admin action ("enable more PHP versions") that adds the sury.org repository, the same way ADR-0005 treats nginx.org [A].

What was checked [V unless marked]:

- `packages.sury.org/php` has `trixie` and `noble`, each with `amd64` and `arm64`. The package index lists `php5.6-fpm` to `php8.5-fpm` for both architectures on both distributions. A PPA mirror for Ubuntu (`ppa.launchpadcontent.net/ondrej/php`) exists as well, but the single `packages.sury.org` source avoids a second mechanism.
- In a Debian 13 container, with the repository added: `php8.4-fpm`, `php8.1-fpm` and `php7.4-fpm` installed together. Each has its own binary (`php-fpm7.4`, `php-fpm8.1`, `php-fpm8.4`), its own `/etc/php/<version>/` and passes `php-fpm<version> -t`.
- The key file contains two keys (`15058500A0235D97F5D10063B188E2B695BD4743`, `45BEA3E529112086C622F8A4B214EAC28059B8AC`). Like for nginx.org, the installer would pin these fingerprints and refuse any other key [A].
- **Side effect to design for:** once the repository is added, `php8.4-fpm` on Debian 13 was offered from sury.org (`8.4.27-2+0~deb.sury.org.1`) instead of the Debian build. Mixing distribution and sury.org PHP packages could break dependencies between them [U, not tested]. Simplest rule: **after enabling, all PHP packages come from sury.org**, and Hostashi shows the source per version [A]. Whether pinning can keep the default version on the distribution is for a small spike (S10).

Rules:

1. Agent operations are typed: `EnablePhpRepository` and `InstallPhpVersion{version}` with an **allowlist of versions**, never a free package name.
2. Each site stores its PHP version. Its FPM master runs that version's binary (ADR-0015). Switching a site's version is a normal, reversible site change.
3. Hostashi **shows end-of-life status** per version and warns before a site is put on an unsupported one. By the published PHP release schedule 8.2 and newer still receive security fixes and 7.4, 8.0 and 8.1 do not [U: from the release schedule, not re-checked]. Old versions are offered under an explicit "legacy" label, because Kim needs them for old applications.
4. Updates: `unattended-upgrades` gets the sury.org origin added, as for nginx.org [A].

## Named conflicts

- **Third-party source with root-level installs.** A sury.org package runs maintainer scripts as root. This is the same kind of trust as nginx.org, but weaker because it is one maintainer's repository [U]. Mitigation: explicit opt-in, pinned key fingerprints, source shown in the UI. It stays a conscious exception to "official packages only", not a hidden default.
- **Old PHP versions are insecure by nature.** Offering them is the product's value for legacy applications and its risk. The EOL warning and the per-site isolation (ADR-0007, ADR-0015) are the mitigation, not a fix.

## Consequences

- The installer step `php` keeps installing the distribution's `php-fpm` and stays unchanged for v1 [V: S5]. A new step enables the repository on request.
- Extension sets must be defined per version (the S5 list `php-mysql`, `php-gd` and so on exists for the default only).
- Several versions installed means more disk and more patch surface. The UI should list versions with no site using them and offer removal.
- If the sury.org repository disappears or changes its terms, the fallback is option A (one version) plus a documented list of affected sites [A].

## Open questions

1. ~~Which versions are offered?~~ Decided (Kim, 2026-11-18): PHP 8.2, 8.3, 8.4 and 8.5 as standard, 7.4 and 8.1 as legacy on explicit request, 8.0 and older not offered. The support status of each version is still to be checked against the official release schedule before the first release [U].
2. All PHP from sury.org once enabled, or pin the default version to the distribution (needs Spike S10)?
3. Is a PHP version also needed for the CLI per site (cron, SSH)? Probably yes, via the site's FPM version [A].
