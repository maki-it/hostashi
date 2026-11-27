# 2. Vision & scope

> **Planning document.** Written in November 2026 during the planning phase, before the first release, and kept as the record of why Hostashi is built the way it is. Where it differs from the product, the [ADRs](adr/) and the [user documentation](wiki/Home.md) are current.

## 2.1 Vision

A server should be productive in under five minutes and should not turn into a security construction site afterwards. The panel takes routine work off admins' hands (sites, TLS, databases, backups, updates) without them having to trust it blindly. The code is open, privileges are minimal, the state is traceable.

## 2.2 Target audience (proposal)

**Primary (v1):** Developers, agencies and admins who run several client sites on **one or a few servers of their own** (VPS, dedicated servers). This is CloudPanel's niche.

**Later:** Small hosting providers with reseller needs (packages, quotas, white label), multi-server operation.

**Not the target audience for v1:** cPanel-style shared hosting with thousands of accounts.

Confirmed in [ADR-0010](adr/0010-target-audience-and-v1-scope.md) (option A): release 1.0 is built for the maintainer's own servers and clients first.

## 2.3 Positioning and differentiation

| Lever | In concrete terms |
|-------|-------------------|
| **Truly open source** | All code including installer, templates and documentation under an OSI license. The community can find and fix security vulnerabilities. |
| **Privilege separation** | The web UI runs without root. A small agent only runs typed, allowlist-based operations. |
| **Isolation per site** | Own Linux user, own FPM pool over a Unix socket, systemd sandboxing, cgroup limits. |
| **Declarative and traceable** | State in a DB, generated configs, diff preview, rollback, export as YAML. |
| **CloudPanel import** | Take over existing servers ("adopt") without rebuilding. |
| **Backups you can test** | Restic (encrypted, deduplicated), restore test, notification on failure. |
| **Modern auth** | Passkeys (WebAuthn), TOTP, OIDC SSO, API tokens with scopes. |
| **API-first** | Everything the UI can do, the API can do. The CLI and later a Terraform provider use it. |
| **More runtimes** | PostgreSQL, Docker Compose apps, optional LXC operation. |

Not every lever has to be in v1. Prioritization in the [feature matrix](03-features.md).

## 2.4 Non-goals (v1)

- Own **mail server** (integration with Mailcow or Stalwart later, see Phase 4).
- Own **DNS server** (provider integrations instead).
- **Windows**, BSD, RHEL family.
- **Mass shared hosting**, billing, domain registration.
- **Kubernetes**.

## 2.5 Guiding principles

1. **Security before features.** A feature that needs root privileges in the web process is built differently or not at all.
2. **Generated, not patched.** We do not edit configuration files with regexes. We render them from the state.
3. **Never without a way back.** Every change is validated and can be rolled back.
4. **Boring tech.** Nginx, PHP-FPM, MariaDB, systemd, restic, ACME: proven building blocks instead of building our own.
5. **One binary, little RAM.** Idle target: under 100 MB for the panel including the agent.
6. **No lock-in.** Sites stay normal Linux users with normal files. Whoever removes the panel keeps a running server.
7. **Documentation and tests are part of the feature.**
