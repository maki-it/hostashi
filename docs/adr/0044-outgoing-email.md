# ADR-0044 Outgoing email through an SMTP relay

**Status:** Proposed. **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0008 (backups), ADR-0010 (v1 scope, no mail server), ADR-0018 (API, scopes), ADR-0022 (metrics and logs), ADR-0033 (hardening), ADR-0035 (users and roles, invitation links), ADR-0036 (PHP end of life), ADR-0039 (certificate expiry warnings), ADR-0041 (credential sealed with the server key), [`03-features.md`](../03-features.md) row "Notifications: email (SMTP), webhook, Telegram/Slack" (P1).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants Hostashi to send notifications and invitation links by email through an SMTP connection [V: Kim, thread, 2026-11-20].
- **This is not a mail server.** A mail server (MTA, mailboxes, inbound mail, port 25 listener) stays out of v1 (ADR-0010). Hostashi only hands finished messages to an external SMTP relay that Kim already runs or rents (for example mail1, a provider's submission server, or a transactional mail service). Hostashi opens no port, installs no Postfix or `sendmail`, and keeps no mail queue beyond its own small outbox.
- Invitation links and password resets are shown to the admin to copy today; ADR-0035 lists "email delivery of invitations" as a non-goal of that ADR [V: `docs/adr/0035-users-and-roles.md`]. An admin resetting a user returns a new invitation link [V: same file].
- There is no self-service password reset; a lost account goes through an admin or `hostashi user recover` [V: no reset route in `internal/serve/routes.go`].
- Users have no email address: the `users` table holds a user name only [V: `internal/store/migrations/0001_init.sql`, `grep email internal/store`].
- The root agent has no network: its unit sets `IPAddressDeny=any` [V: `packaging/systemd/hostashi-agent.service`]. The panel process (`hostashi`, unprivileged) has `AF_INET`/`AF_INET6` and already talks to the network (ACME, external database servers) [V: `packaging/systemd/hostashi.service`, ADR-0041].
- `internal/secrets` seals small secrets with `/etc/hostashi/secret.key` (AES-GCM with a purpose string); ADR-0041 uses it for external database credentials [V: `internal/secrets/secrets.go`].
- The panel knows its public URL (`HOSTASHI_PUBLIC_URL`, later `--panel-domain`, ADR-0042), which links in emails need [V: `packaging/systemd/hostashi.service`].
- The host firewall does not limit outgoing connections (ADR-0034) [V: ADR-0034]. Many cloud providers block outgoing port 25 for new accounts; submission on 587 or 465 is usually open [U].

## Options

### Who sends

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The panel process** | Has network already; knows users, roles and events; no new privilege | The SMTP credential lives in the unprivileged process while it runs [A] |
| B. The agent | Root already holds the key | The agent would need network, which ADR-0033 removed on purpose |
| C. A local MTA (Postfix as a null client) | Proven queue and retries | A mail server on the host in all but name: config, updates, a new attack surface, and outside v1 scope |

### Library

| Option | Pro | Contra |
|--------|-----|--------|
| A. `net/smtp` from the standard library | No dependency | Frozen package (no new features), no context or timeouts on the dial, MIME and header encoding by hand [V: `net/smtp` package doc, Go 1.27.4] |
| **B. `github.com/wneessen/go-mail` v0.8.2** | MIT, actively maintained (v0.8.2 released 2026-11-08), context and timeouts, a mandatory-TLS policy, implicit TLS and STARTTLS, SMTP AUTH PLAIN/LOGIN/CRAM-MD5/SCRAM, safe MIME and header encoding; depends only on `golang.org/x/crypto` and `golang.org/x/text`, both already in `go.mod` [V: `proxy.golang.org`, module file and LICENSE of v0.8.2] | One more direct dependency [A] |
| C. `github.com/emersion/go-smtp` v0.26.0 | MIT, well known | A client and server toolkit; message building needs a second library [V: `proxy.golang.org`] |

The license check (MIT on the allowlist) and the REUSE workflow accept B without changes [A].

## Recommendation

**A and B.**

- **Settings (admin only, new scope `settings:mail`).** Page "Settings → Email": host, port, TLS mode, user name, password, sender address and name, optional reply-to. TLS mode is either `starttls` (port 587, STARTTLS is required, the connection fails if the server does not offer it) or `tls` (port 465, implicit TLS). The server certificate is always verified; an optional CA file covers a private relay. Authentication is optional (a relay that trusts the server's address needs none), and is only ever sent inside TLS. The password is sealed with `secrets.Seal`, purpose `smtp`, and never returned by the API (the form shows "set" and allows replacing it). A "Send test email" button sends to the admin's own address and shows the server's answer.
- **Off by default.** Without a configured relay, everything works as today: invitation links are shown to copy, notifications appear in the panel and in doctor.
- **Email addresses for users.** New optional column `users.email`. An address is used only after the user confirms it through a link (one use, 24 hours). An admin may enter an address when inviting; the invitation email then counts as the confirmation once the user accepts. Changing the address sends a note to the old one.
- **Events in v1.** Each event has a fixed template (plain text, English, the panel's language later), a severity and a category:
  - *Account (always on, to the user themself):* invitation, reset link from an admin, password changed, second factor added or removed, new API token created, address changed.
  - *Operations (opt-in per user, only for sites and server settings the user's role can see, ADR-0035):* backup failed or partial, certificate renewal failed or a certificate expires in 14 or 3 days (ADR-0039 keeps its 30/7-day warnings for custom certificates), doctor errors (one daily digest, warnings only in the digest if chosen), a site uses a PHP version that reaches end of life within 60 days or is past it (weekly, ADR-0036), a disk or quota above 90 percent if the metrics already know it (ADR-0015, ADR-0022).
- **Per-user opt-in.** Page "Account → Notifications": one switch per operations category, off by default for `user`, on by default for `admin` once an address is confirmed [A]. Account emails cannot be switched off.
- **Outbox and retries.** Table `mail_outbox`: recipient, template, sealed body (purpose `mail-outbox`, because invitation and reset links are secrets), attempts, next attempt, state. A worker in the panel sends with a 30-second timeout and retries with backoff for up to 24 hours, then marks the message failed and shows it under "Needs attention". Sent and failed rows lose their body right away and are deleted after 30 days.
- **Rate limits and deduplication.** At most 10 emails per recipient per hour and 100 per server per hour [E]; above that, operations events are folded into the next digest, account emails are still sent (they are rare and security relevant). The same event for the same object is sent at most once per 24 hours.
- **No secrets in logs or metrics.** Logs and the audit log record template name, recipient, message ID, state and the SMTP status code, never the body, a link, a token or the password. SMTP errors are cut to the status line before logging. Metrics count messages by template and state only.
- **Header safety.** Addresses are parsed with `net/mail` and must be a single address without display tricks; subjects come from the templates with only escaped, length-limited values (site name, domain); no user-supplied header fields. Links always use the configured public URL, never the request's `Host` header.
- **Invitations.** With a confirmed relay, the "Invite user" dialog gets an email field; the link is still shown to copy as a fallback, and the email states who invited whom and when the link expires.
- **API.** `GET/PUT /api/v1/settings/mail`, `POST /api/v1/settings/mail/test`, `GET/PUT /api/v1/account/notifications`, `PUT /api/v1/account/email` plus the confirmation route. Changing mail settings needs a browser session, like user management [A].
- **Not in v1.** A mail server, inbound mail, DKIM signing (the relay signs), webhooks, Telegram or Slack (P1 row stays for later), HTML emails, per-site notification recipients.

## Consequences

- New direct dependency `github.com/wneessen/go-mail`, new package `internal/mail` (settings, templates, outbox worker), store migration (`users.email`, `email_confirmations`, `mail_settings`, `mail_outbox`, `notification_prefs`).
- No new `os/exec` exception, no new unit, no change to the agent or the firewall.
- doctor checks the relay (reachable, TLS ok, certificate valid) when it is configured, and reports failed messages.
- Tests with a local SMTP test server in Go (no network in CI): STARTTLS required and refused, implicit TLS, bad certificate, auth failure, timeouts, retries, rate limits, header injection attempts, no link in any log line.
- Docs: a wiki page "Email notifications" with examples for common relays and the hint that SPF/DKIM are the relay's job.
- Work in small PRs [E]: 1. store and settings API with test send; 2. outbox worker, rate limits, logging rules; 3. user addresses and confirmation; 4. invitation and account emails; 5. operations events and preferences; 6. panel pages. All after the UI redesign, as Kim decided for new v1 features.

## Risks to name openly

- **Email is not a secure channel.** An invitation or reset link in a mailbox can be read by anyone with access to that mailbox; 24 hours, one use and the required second factor for `admin` and `manager` (ADR-0035) limit the damage [A].
- **Stored SMTP credential.** A compromise of the panel process or of `/etc/hostashi/secret.key` exposes the relay account, which could then send spam in the operator's name. Mitigation: a dedicated relay account with a sending limit at the provider [A].
- **Spoofed or lost notifications.** Hostashi cannot ensure delivery (spam filters, SPF/DKIM of the sender domain). The panel stays the source of truth; email only points to it.
- **Personal data.** User email addresses are personal data; they are stored only for sending and removed with the user.

## Open questions

0. ~~`go-mail` or `net/smtp`?~~ **Decided (Kim, 2026-11-21): `go-mail`.**
1. ~~Self-service "Forgot password" by email in v1?~~ **Decided (Kim, 2026-11-21): no, not for now.** Recovery stays with admins and `hostashi user recover`.
2. ~~Are the events above the right set for v1?~~ **Decided (Kim, 2026-11-21): yes, exactly this set; other channels after v1.**
3. ~~Only encrypted connections to the relay, or also plain SMTP for a private network?~~ **Decided (Kim, 2026-11-21): encrypted only (`starttls` or `tls`).**
4. ~~Do addresses need a confirmation link before Hostashi sends to them?~~ **Decided (Kim, 2026-11-21): yes.** An address entered with an email invitation counts as confirmed once the invitation is accepted.
