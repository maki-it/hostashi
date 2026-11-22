# ADR-0055 Email notifications for service outages

**Status:** Accepted (2026-11-22, by Kim). **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0044 (outgoing email through an SMTP relay), ADR-0052 (service management and server dashboard), ADR-0054 (site types: Node.js and app units), ADR-0035 (users and roles), [03-features](../03-features.md) section 3.8.

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants warnings by email in release 1.0: failed backup, certificate about to expire, disk almost full, service down (Kim, 2026-11-22).
- Most of this exists since PR #273: `internal/notify` sends emails for failed backups, failed renewals and expiring certificates, disks and site limits at 90 percent, a daily digest of failing server checks and old PHP versions. Each account switches the categories on **Account → Notifications**; they are on by default for admins [V: `internal/notify/notify.go`, `watch.go`, `docs/wiki/Email-notifications.md`].
- **Service outages are not mailed.** The service monitor of ADR-0052 polls every unit of the catalog once a minute and stores `failed` and `crash-restart` events in `service_events`, but nothing turns them into email [V: `internal/servermon/services.go`, `servermon.go` `ServicesEvery = time.Minute`].
- A unit only reaches `failed` after systemd gave up restarting it (`Restart=on-failure` plus the start limit), so `failed` is a real outage, not a blip [V: units in `packaging/`; U: systemd start-limit semantics]. A unit that crashes and comes back on its own shows up as `crash-restart` [V].
- A stop by an admin (for example MariaDB from the services page) or a disabled site leaves the unit `inactive`, not `failed` [U: systemd semantics, A: to confirm in the container test].
- The panel sends the email itself (ADR-0044). If the panel is down, nothing can be sent; if the agent is down, the panel sees only errors from the agent socket [V: ADR-0044, `servermon.PollServices` logs the error].
- `service_events.kind` has a CHECK constraint with four values, so a new kind needs a migration [V: `0043_service_events_host_samples.sql`].
- Kim decided that an all-clear email is sent **only for services**, not for the other categories (Kim, 2026-11-22).

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Hook the existing service monitor into `notify`: one new category `services`, events from `servermon.Changes`** | No new agent operation, no new process, reuses dedup, rate limits and recipient rules of ADR-0044 | Only as good as the 1-minute poll; no email while the panel itself is down |
| B. systemd `OnFailure=` units that call a Hostashi command | Fires within a second, also when the panel is down | A new root unit per service, and the command would need SMTP access from the agent side, which ADR-0044 ruled out (agent has no network) [V: ADR-0044] |
| C. Leave it to external monitoring (Prometheus alerts, CheckMK) | Nothing to build | Kim runs no alerting for Hostashi servers yet, and the feature matrix lists it as built-in [V: 03-features 3.8] |

## Recommendation

**Option A.**

### Category

A new category `services` ("Services") on **Account → Notifications**, after "Server checks". Same defaults as the others: on for admins, off for everyone else.

| Unit | Who may get it |
|------|----------------|
| Server units: `nginx`, `mariadb`, `ssh`, `fail2ban`, `firewall`, `adminer`, `agent` | Admins only (`Site` empty) |
| Site units: `php`, `valkey`, `node`, `app` (ADR-0054) | Admins, managers, and users with a grant for the site |

The panel's own unit is left out: a stopped panel cannot send email [V: ADR-0044].

### Events

| Event | When | Dedup |
|-------|------|-------|
| **Service down** | A unit changes to `failed` | One email per unit and outage (key `service-failed:<id>:<site>:<since>`) |
| **Service keeps crashing** | 3 or more crash restarts within 15 minutes [E], while the unit is still running | At most one per unit per 6 hours [E] |
| **Service is back** | A unit that got a "down" or "keeps crashing" email is `active` again for 2 polls in a row (about 2 minutes) [E] | One per outage |
| **Agent unreachable** | The service poll fails 3 times in a row (about 3 minutes) [E] | One per outage, then "agent is back" |

A single crash restart that systemd fixed on its own does not send an email; it stays visible on the services page as today.

The email names the service, the site if any, the systemd result (for example `exit-code` or `signal`) and the time, and links to the services page (or the site's overview for site units). It does **not** include log lines, so no secrets from a log leave the server by email [A: logs can contain query strings or tokens].

### Where the state lives

- `servermon.Changes` gets one more transition: `failed` → `active` gives a `recovered` event. It is stored in `service_events` like the others, so the history on the services page shows the outage ending too.
- **Migration 0046** rebuilds the CHECK on `service_events.kind` to add `recovered`.
- The monitor passes each new event to a small `notify` hook (an interface, so `servermon` does not import mail code). The crash counter for "keeps crashing" reads the last 15 minutes from `service_events`, so it survives a panel restart.
- After a panel restart, the first poll has no previous state and sends nothing (as today). An outage that started and ended while the panel was down sends no email [A: acceptable, the services page still shows the restarts].

### Later: restore test

The automatic restore test (not in 1.0, see 03-features 3.0) will add its own category (`restore`) the same way; nothing in this ADR has to change for it.

## Consequences

- No new `os/exec` exception, no new agent operation, no new dependency.
- One migration (0046), one new category, five new email templates (down, crashing, back, agent unreachable, agent back).
- Mail volume: rate limits of ADR-0044 still apply (10 per recipient and 100 per server per hour); a server with many failing sites may delay some emails to the next hour [V: `docs/wiki/Email-notifications.md`].
- Wiki page `Email-notifications.md` gets a row "Services", and 03-features row "Service health and disk warning" moves to done for email.
- An outage of the panel itself, of the whole server or of the network is not reported; that needs a check from outside (for example an uptime monitor). The wiki says so.

## Open questions for Kim

None. The thresholds marked [E] are starting values and can be changed in code later without a new ADR.
