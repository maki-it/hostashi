# 5. Security

Security is the main selling point and at the same time the biggest risk of the project. This document defines the threat model and mandatory requirements. It will be checked against an external review before Phase 1.

## 5.1 Assets

1. Root access to the server (panel compromise = full compromise).
2. Customer data of the sites (files, databases, backups).
3. Credentials (panel logins, DB passwords, API keys for DNS, storage, cloud).
4. Availability of the hosted sites.

## 5.2 Attackers and threats

| Attacker | Typical paths | Countermeasure (section) |
|----------|---------------|--------------------------|
| Internet scanners, bots | Pre-auth vulnerabilities in the panel, default credentials, brute force, hijacking fresh installations | 5.3 Panel exposure, 5.4 Auth |
| Attacker with a customer site (for example a hacked WordPress site) | Breaking out into other sites, reading other people's files, abusing the panel | 5.5 Isolation |
| Malicious or compromised panel user with few privileges | Privilege escalation through API flaws (IDOR), path traversal, command injection | 5.6 Input, 5.4 AuthZ |
| Supply chain (packages, installer, third-party repositories) | Tampered script/package, compromised upstream repositories | 5.7 Supply chain |
| Spied-on backups or offsite storage | Plaintext backups, leaked keys | 5.8 Data |

## 5.3 Panel exposure

- **No default credentials.** Initial setup only with a one-time token that the installer prints to the console. The token expires.
- Panel port **not 443** by default, with its own TLS. Optional: bind only to a Unix socket or localhost behind Nginx, access through IP allowlist or VPN.
- The installer suggests restricting the panel port to known IPs through the firewall. Basic auth as an optional additional layer.
- Strict HTTP headers (CSP without `unsafe-inline`, HSTS, `X-Content-Type-Options`, `frame-ancestors 'none'`), CSRF protection, `SameSite` cookies.

## 5.4 Authentication and authorization

- Passwords with **Argon2id**, minimum length instead of character rules, optional offline check against known leaks.
- **TOTP** from phase A, **passkeys** from phase B. 2FA can be enforced for admins.
- Login rate limit and lockout, uniform error messages.
- **Server-side authorization per resource** (RBAC with site grants). Every API handler checks role *and* ownership. Automated test: for every route and every role, it is checked that only what is allowed gets through (IDOR matrix).
- API tokens: scopes, expiry date, only the hash is stored, visible once.
- Audit log append-only, contains actor, source IP, action, target object, diff (without secrets).

## 5.5 Isolation between sites

See [architecture 4.4](04-architecture.md). Also mandatory:

- No PHP-FPM on TCP ports. Unix sockets with tight permissions.
- Configuration only through pool files, **never** `PHP_VALUE`/`PHP_ADMIN_VALUE` from the Nginx vhost.
- A test per release tries to reach files, sockets and processes of a second site from inside a test site. If it succeeds, the release is blocked. It is the step "two sites stay apart" of `packaging/test/container-test.sh`, which CI runs on both distributions (ADR-0033 H8). As the first site's user and from its PHP code, it tries the second site's files, the shared web server log, the second site's PHP messages in the journal, the PHP, panel and agent sockets, the second site's processes and its database.

## 5.6 Input, files, processes

- Everything that ends up in file names, configs, SQL or commands has a **type** (Domain, UnixUserName, DbIdentifier, AbsolutePathUnder(root)) with its own parser. Raw strings do not reach the agent.
- **No shell.** Processes are started only through argv. Cron commands are user content, but are written as a file, not interpolated.
- SQL for administration (CREATE DATABASE, GRANT) through identifier quoting and prepared statements where possible.
- File manager and permission reset: path resolution relative to the site root with symlink protection (`RESOLVE_BENEATH`). Delete only after confirming the resolved target paths. Tests with edge cases (`0`, `..`, symlinks, spaces, Unicode, very long names).
- Fuzzing for all parsers (domains, vhost snippets, cron expressions, paths).

## 5.7 Supply chain

- Releases signed (cosign), SBOM, dependencies through lockfiles, `govulncheck` and Dependabot/Renovate in CI.
- APT repository with a signed `Release` index. The signing key is kept offline or in an HSM/KMS.
- External packages (Nginx, PHP from Ondřej Surý or sury.org, MariaDB, Node) are third-party dependencies. We pin key fingerprints, document the sources and check them in CI.
- The installer runs **no unsigned remote script** as root, except the documented one-liner, whose content is published with a reproducible hash.

## 5.8 Data and secrets

- Backups are always encrypted on the client side (restic). Keys are kept separate from the backup target, an export guide exists.
- Secrets in the DB are encrypted, the key file has mode 0400. Rotation is planned for.
- DB passwords of sites differ per site/DB. Master credentials are known only to the agent, never to the browser.
- Logs contain no secrets (redaction test in CI).

## 5.9 Process

- `SECURITY.md` with a reporting channel (email, PGP/Signal) and response times. Coordinated disclosure, CVE assignment, security advisories.
- Static analysis (gosec, semgrep) and secret scanning in CI.
- **Own security review before 1.0** instead of an external pentest (no budget, Kim, 2026-11-21, see roadmap). An external pentest stays a goal for later.
- Consider a bug bounty once funding is in place.
