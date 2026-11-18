# Security Policy

Hostashi is a hosting control panel. A vulnerability in it can mean full server compromise, so we take reports seriously.

> **Status:** Phase 0 (planning). There is no released code yet. This policy already applies to the design documents in `docs/`, for example flaws in the threat model in [`docs/05-security.md`](docs/05-security.md).

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately by one of these routes:

1. GitHub private vulnerability reporting: the **Security** tab of this repository, then **Report a vulnerability**.
2. Email: `kontakt@maki-it.de`

Please include affected version or commit, steps to reproduce, impact, and any proof of concept. If you want to encrypt your report, ask for a PGP key in a first, content-free mail. <!-- TODO(maintainer): publish PGP key or Signal contact -->

## What to expect

These are targets, not guarantees, until a team exists:

- Acknowledgement within 5 working days.
- A first assessment within 14 days.
- Coordinated disclosure: we agree on a date with you, usually within 90 days of the report.
- Credit in the advisory, unless you prefer to stay anonymous.

## Scope

In scope once code exists: the web service, the privileged agent and its RPC interface, the installer, generated configuration, and release artifacts.

Out of scope: vulnerabilities in third-party software we only configure (Nginx, PHP, MariaDB, ...). Please report those upstream.

## Supported versions

None yet. This section will list supported releases from the first release on.
