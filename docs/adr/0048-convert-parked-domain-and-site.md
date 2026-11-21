# ADR-0048 Convert a parked domain into a site and back

**Status:** Accepted (Kim, 2026-11-21). **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0006 (state store and reconciler), ADR-0018 (API), ADR-0023 (panel certificate names), ADR-0027 (maintenance and parking pages), ADR-0035 (roles), ADR-0037 (UI redesign), ADR-0039 (certificate management).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants to turn a parked domain into a site and a site back into a parked domain, as a v1 feature after the redesign (Kim, 2026-11-20) [V: project chat].
- ADR-0027 says only: "A domain is either parked or belongs to a site, never both; turning a parked domain into a site removes the parking." There is no conversion flow in the panel [V: ADR-0027, `web/src/Parked.tsx`].
- Today a site that is created with a parked domain silently deletes the parking in the same transaction that stores the site's domains [V: `store.insertDomains`]. If the new site then fails to go live, `removeAfterFailure` removes the site, but the parking is not restored: the domain is then neither parked nor a site [V: `hosting.Create`, `hosting.removeAfterFailure`].
- The other direction does not exist. Parking a domain of a site is refused with `ErrDomainTaken` [V: `store.PutParked`]. Today it takes two steps: delete the site, then park its domains one by one. Between the two steps the domains are served by nothing [V: code].
- A parked domain's certificate is stored under the agent name `_park-<n>`, a site's under the site's name [V: `agentapi.ParkName`]. The certificate cannot simply move, because a site usually has more names (for example `www.`) [A].
- Deleting a site removes its files, databases, cron jobs and SSH logins (agent) and cannot be undone. Server backups (restic) keep their snapshots after a site is deleted [V: `hosting.Delete`, ADR-0008].
- Scopes: creating a site is `sites:create`, deleting `sites:delete`, parking `parked:write` [V: `internal/authz`].
- "Show a status page when a site is disabled" is a separate v1 thread and keeps the site's files [V: project memory]. It is not part of this ADR.

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| A. Leave it as it is (two separate steps, documented) | No code | The parking is lost if a site create fails. Site → parked leaves the domains unserved between the steps |
| **B. Two guided actions in the panel and the API: "Make a site" on a parked domain, "Delete and park" on a site, each one change on the server** | One click, no gap where nothing answers, parking is restored after a failure | Two new endpoints, one more confirmation dialog |
| C. Site → parked keeps the site's files and only swaps the vhost for a parking page | Nothing is lost | This is "disable with a status page" (separate thread) under another name; the site still uses disk, PHP user and quota |

## Recommendation

**Option B.**

**Parked domain → site**

- On the Parked domains page each row gets the icon button "Make a site". It opens the New site form with the domain filled in, HTTPS on if the parked domain had HTTPS, and a note: "The parking page ends when the site is live."
- The API stays `POST /api/v1/sites` (ADR-0018). Using a parked domain there is allowed as today, so no new endpoint is needed.
- Fix: if the new site cannot go live, its parked domains are parked again with their previous page and HTTPS setting, and the audit log says so. The store keeps the removed parked rows from the create transaction for that.
- The site gets its own certificate in the background, as every new site does. Until it is there, the domain answers over HTTP only [V: `hosting.Create`]; the gap is usually under a minute [E]. The parked certificate `_park-<n>` is removed with the parking.
- Needs `sites:create` (as today). Removing the parking this way needs no extra `parked:write`, because the domain is used, not changed.

**Site → parked domain**

- On the site's settings page, in the danger zone next to "Delete site", a second action "Delete and park domains". The dialog lists what is deleted (files, databases, cron jobs, SSH logins), shows the time of the last backup, lets you pick a parking page (default: built-in) and HTTPS, and asks you to type the site's name, like delete.
- The dialog lists every domain of the site (main domain, aliases, `www.` names) with a checkbox, all checked by default (Kim, 2026-11-21). Checked domains are parked, unchecked ones are free afterwards. At least one must be checked; with none checked, the action is plain "Delete site".
- API: `POST /api/v1/sites/{name}/park {"domains": [...], "page", "tls", "confirm": "<name>"}`; `domains` must be a non-empty subset of the site's domains, otherwise `400`, scopes `sites:delete` **and** `parked:write`. A missing scope is a 403 like everywhere else (ADR-0035).
- Order, in one store transaction: the site goes to state `deleting` and the chosen domains become parked rows; then one `web.apply` swaps the site's vhost for the parking vhosts, so there is no moment where nobody answers. Only then the databases, account and files are removed, as in today's delete. If `web.apply` fails, the transaction is undone and the site stays active. If removing files stops halfway, the site stays in `deleting` (as today), the domains stay parked, and a second delete finishes the job.
- The parked domains get new certificates under `_park-<n>` in the background; until then HTTP only, as for any newly parked domain.

**Not in scope:** moving a single domain from a site to parked (today: remove it from the site, then park it), and keeping the site's files (see the status-page thread).

## Consequences

- Store: `CreateSite` returns the parked rows it removed; a new `ParkSiteDomains` transaction (site to `deleting`, domains to parked rows). Hosting: restore in `removeAfterFailure`, new `DeleteAndPark`. Serve: one endpoint, audit action `site.park`. No new agent operation, no new `os/exec` exception.
- Frontend: icon button on the Parked domains page, prefilled New site form, a second action in the site's danger zone.
- Tests: create with a parked domain that fails on `web.apply` restores the parking; delete-and-park with a failing `web.apply` keeps the site active and nothing parked; a failure after `web.apply` leaves `deleting` plus parked; a wrong `confirm`, an empty or foreign `domains` list, missing scopes and a domain that is the panel's address (ADR-0042) are refused.
- User docs (ADR-0043): a short section on the Parked domains page in the wiki.

## Open questions for Kim

None left.

1. ~~Site → parked: park all domains of the site?~~ Decided (Kim, 2026-11-21): the dialog lists the domains with checkboxes, all checked by default; unchecked ones become free.
