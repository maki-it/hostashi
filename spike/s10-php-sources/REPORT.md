# Spike S10 report: PHP default version from the distribution, extra versions from sury.org

Status: throwaway spike, nothing here is production code. Evidence tags as in the ADRs: **[V]** verified by a test in this spike, **[A]** assumption, **[U]** unverified.

Question (ADR-0019, open question 2): after the sury.org repository is added, can the distribution's own PHP (Debian 13: 8.4, Ubuntu 24.04: 8.3) stay on the distribution's packages while extra versions come from sury.org? Second question (ADR-0019, open question 1): which PHP versions still get security fixes?

Environment: fresh `debian:trixie` and `ubuntu:24.04` containers, only apt operations (nothing is started, no systemd needed), run on 2026-11-18 against the live package indexes. Nothing was created or changed at Hetzner. `s10-test.sh` is the test, `run.sh` starts a container and runs it (sandbox-only lines for the TLS-intercepting egress proxy are marked).

## Result

**Yes, with a pin file. [V on both distributions]** Without a pin the setup is silently half-sury; with the pin below the default version stays on the distribution, the extra versions come from sury.org, `apt` stays consistent and a simulated `apt-get upgrade` has nothing pending.

### The three scenarios

Each scenario: install the distribution PHP (`php-fpm` plus nine common extensions), add sury.org, then install `php8.1-fpm`, `php8.1-mysql`, `php7.4-fpm`, `php7.4-mysql`.

| Scenario | Debian 13 | Ubuntu 24.04 |
|----------|-----------|--------------|
| **No pin** | Install works. But `php-common` is silently replaced by sury.org's (2:96 to 2:101). A later `apt-get upgrade` would replace **23** distribution PHP packages by sury.org builds (8.4.27-1 to 8.4.27-2+deb.sury.org). Mixed state, then full switch at the next upgrade | Same. `php-common` from sury.org, later upgrade would replace **13** distribution packages (8.3.6-0ubuntu0.24.04.13 to 8.3.36-2+deb.sury.org) |
| **Pin sury.org to priority 100 everywhere** | **Fails**: `php8.1-mysql` needs `php-common (>= 1:81~)`, which only sury.org offers, and the pin keeps `php-common` at the distribution's 2:96 (`Breaks php8.1-common`). Exit 100 | Same failure |
| **Pin: 100 for sury.org, 600 only for `php-common` and the versioned packages of every version except the distribution default** | Works. Exit 0. `php8.4-*` stays `8.4.27-1~deb13u1` (Debian). `php-common`, `php8.1-*`, `php7.4-*` from sury.org. `php-fpm7.4 -t`, `php-fpm8.1 -t` and `php-fpm8.4 -t` pass. `apt-get check` clean, 0 pending PHP upgrades | Same with `php8.3-*` staying `8.3.6-0ubuntu0.24.04.13` (Ubuntu) |

The working pin (Debian 13, default 8.4; for Ubuntu 24.04 swap 8.4 and 8.3 in the list):

```text
Package: *
Pin: origin packages.sury.org
Pin-Priority: 100

Package: php-common php5.6* php7.0* php7.1* php7.2* php7.3* php7.4* php8.0* php8.1* php8.2* php8.3* php8.5*
Pin: origin packages.sury.org
Pin-Priority: 600
```

## Findings

1. **`php-common` always comes from sury.org once an extra version is installed [V].** The extra versions depend on `php-common (>= 1:81~)` or similar, which the distribution does not satisfy. Only the default version's own packages (`php8.4-*` or `php8.3-*`) can stay on the distribution.
2. **Distribution PHP with sury.org's `php-common` works at the configuration level [V], at runtime it is untested [U].** `php-fpm8.4 -t` and `php-fpm8.3 -t` pass, `apt-get check` is clean, nothing is held broken. No site was served with that combination.
3. **Without the pin, nothing fails at install time [V].** The switch happens at the next `unattended-upgrades` run, a day or so later, without anyone deciding it. That is the dangerous variant, and the reason the pin must be part of "enable more PHP versions".
4. **The pin has to be generated per distribution [A].** The default version is excluded from the 600-list. If Debian or Ubuntu change their default (Debian 14, Ubuntu 26.04), the generator changes with the OS version, not with a user choice.
5. **Package-name sources are not visible in the version string [V].** Some sury.org versions contain `sury`, others (`+0~20261021.78+debian13~1.gbp...`) do not. A check "where does this come from" must read `apt-cache policy` or `dpkg` origin data, not match the version text. This matters for `hostashi doctor` and the UI.
6. **If sury.org disappears, the fallback works as expected [A].** Removing the source and the pin leaves the default PHP on the distribution untouched. Extra versions would stop receiving updates. Not tested.

## PHP release schedule (checked 2026-11-18)

Source: `https://www.php.net/supported-versions.php` [V].

| Version | Active support until | Security fixes until | Status on 2026-11-18 |
|---------|----------------------|----------------------|----------------------|
| 8.2 | 2024-12-31 | **2026-12-31** | Security only, **ends in about six weeks** |
| 8.3 | 2025-12-31 | 2027-12-31 | Security only |
| 8.4 | 2026-12-31 | 2028-12-31 | Active |
| 8.5 | 2027-12-31 | 2029-12-31 | Active |
| 8.1, 8.0 and older | | | Not on the supported list, end of life [V: absent from the page] |

**Consequence for ADR-0019:** the accepted standard list (8.2 to 8.5) contains a version that loses security fixes on 2026-12-31. Suggestion: treat **8.3, 8.4 and 8.5 as standard** and **8.2 as legacy from the start**, together with 7.4 and 8.1. Kim decided the list as "8.2 to 8.5 standard"; this is a proposal to adjust it because of the date, not a change.

Whether sury.org keeps patching end-of-life versions is not known [U]. The UI must not suggest they are safe.

## Not done

- Real traffic against distribution PHP plus sury.org `php-common` (finding 2).
- Debian 13 and Ubuntu 24.04 arm64: package names and versions are identical in the indexes [V: ADR-0019 research], the pin test ran on amd64 only.
- Removing an extra version again, or removing sury.org entirely.
- Integration into `install.sh`: no step yet. Proposed shape: step `php_versions` (only on explicit request) writes keyring, source and the generated pin, then installs the requested versions from the allowlist.
