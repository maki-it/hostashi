# Second own security review before 1.0 (2026-11-26)

**Scope:** the code on `main` after v0.13.0 (commit `8ed3164`, PR 568), with the focus on what was built since the first own review of 2026-11-21: staging and cloning (ADR-0070), backups of deleted sites (ADR-0074), application profiles and the WordPress quick install and reinstall (ADR-0069), Git deploy (ADR-0062), password protection and IP rules (ADR-0066), changing the domain of a site (ADR-0068), the file manager's ZIP features, API tokens with site lists and the IP allowlist, the browser login of the CLI, and the malware scan with its quarantine (ADR-0064).

**Method:** reading the code at every trust boundary, plus tools. This is a code review by the project itself. It is **not a penetration test** and does not replace one (ADR-0010, [05-security](../05-security.md) section 5.9). Every statement is marked: **[V]** verified (code read, test run or tool output), **[A]** assumption, **[E]** estimate.

## Result

No critical or high finding. All six findings were fixed with tests in this review; the rest are notes.

| # | Finding | Severity | State |
|---|---|---|---|
| 1 | The Git token or deploy key of the last run stayed in systemd's credentials directory until the next deploy | Medium | fixed, PR 571 |
| 2 | A staging refresh could read or overwrite a database of a third site that took a freed name | Low | fixed, PR 569 |
| 3 | A token limited to some sites could clone a site (a new site) and park domains | Low | fixed, PR 570 |
| 4 | A site user could point a reverse proxy site at any host of the private network | Medium | fixed, PR 572 |
| 5 | The build commands of a Git deploy can read the run's token or key | Low | fixed, PR 575 |
| 6 | Unknown webhook ids fill the webhook's rate limiter, which then blocks every webhook | Low | fixed, PR 574 |
| 7 | Notes | Info | – |

Severity is the project's own rating: how bad the effect is, weighed by what an attacker needs [E].

## Findings

### 1. Deploy credentials stayed readable after the run (Medium, fixed)

The deploy unit `hostashi-deploy@<site>.service` is `Type=oneshot` with `RemainAfterExit=yes`, so a successful run stays "active" until the agent stops it for the next deploy [V: `packaging/systemd/hostashi-deploy@.service`, `internal/deployunit`]. The token or deploy key reaches the run through `LoadCredential=`. The agent deleted its own copy under `/run/hostashi-deploy/<site>/` after the run [V], but systemd keeps its copy under `/run/credentials/hostashi-deploy@<site>.service/` until the unit is deactivated ("acquired at the moment of service activation, and released on service deactivation", [systemd credentials](https://systemd.io/CREDENTIALS/)) [V: documentation]. Access is "restricted to the service's user", which is the site user [V: documentation]. Whether a process of the site user outside the unit (an SSH session, PHP) can open that directory depends on the mount namespaces [A: likely yes from an SSH session]. A token is often a personal access token of an admin or manager with more rights than the one repository; a site user with the role "user" must not learn it.

Fix (PR 571): once the agent has read the outcome of a finished run it stores it in its state and stops the unit, so systemd releases the credentials. Later status calls and the busy check use the stored outcome. Unit test `TestStatus` covers the stop and the stored outcome [V]; the installer test checks that the unit is inactive after a deploy [A: needs the CI run of PR 571].

### 2. Staging refresh and reused database names (Low, fixed)

"Refresh from source" reused the database pairs of the first copy and only checked that both names still exist [V: `internal/hosting/clone.go`]. Database names are unique only while a database exists and carry no site prefix by rule. If the clone or the source dropped a database and another site later created one with that name, a refresh would load the source's dump over the third site's database, or copy the third site's database into the clone (whose users can read it). It needs a reused name, so it is unlikely [E], but the effect crosses sites.

Fix (PR 569): a pair whose source no longer belongs to the source site, or whose target no longer belongs to the clone, is skipped. Test `TestCloneRefreshSkipsForeignDatabases` fails without the change and passes with it [V].

### 3. Site-limited tokens and second actions (Low, fixed)

A token with a site list may do only site actions and the read-only actions everyone has (`authz.ForSiteToken`) [V]. Routes that need a second action checked its scope and role, but not this rule [V: `internal/serve/clone_api.go`, `sites_api.go`]. A token limited to site A (of a manager) could therefore clone A into a new site and delete A while parking its domains, although it cannot create a site or a parked domain directly.

Fix (PR 570): one helper `alsoAllowed` checks scope, role and the site-list rule for every second action; cloning now also needs `sites:create`. The WordPress install uses the same helper. Test `TestSiteTokenSecondActions` fails without the change and passes with it [V].

### 4. Reverse proxy targets in the private network (Medium, fixed)

ADR-0054 allows private networks (10/8, 172.16/12, 192.168/16, fc00::/7) as proxy targets and refuses loopback, link-local and the server's own addresses, also after resolving a name [V: `agentapi.ProxyAddrAllowed`, `ResolveProxyTarget`]. Changing the target is the action `sites.proxy:write`, which every role with a grant has [V: `internal/authz/authz.go`]. A customer account (role "user") with a proxy site can therefore publish any host and port of the server's private network under its domain: a hypervisor's web UI, a database, a router, other customers' servers. On a server in a Proxmox or cloud private network this is a server-side request forgery into that network [A: depends on the network].

Fix (PR 572, decided by Kim on 2026-11-26): a target that is or resolves to a private network, including `100.64.0.0/10`, needs the new action `sites.proxy.private:write` (admins and managers); a user gets 403 `private_target`. Test `TestProxyPrivateTargetRoles` [V]. Creating a proxy site and changing a site's type were already manager actions [V]. [A] A name that resolves to a public address when saved and to a private one later is not caught; ADR-0054 names the same gap for every DNS change after saving.

### 5. Build commands can read the deploy credentials (Low, fixed)

git-sync and the build commands run in the same unit, so the credentials are in `$CREDENTIALS_DIRECTORY` while the build runs [V: `DropInContent`]. Only admins and managers store build commands, but the commands usually run code from the repository and its dependencies (`npm ci`, `composer install`). Whoever can push to the repository can read the token through it [V by construction]. Fix (PR 575): the build runs in a second unit `hostashi-build@<site>.service` with the same sandbox and no credentials. The agent stops the deploy unit once git-sync succeeded, so systemd releases the token or key, and only then starts the build. `TestBuildUnit` covers the order and `TestBuildTemplate` that both sandboxes are the same [V]; the installer test checks that the build ran in the build unit [A: needs the CI run of PR 575]. The wiki still recommends a token for the one repository.

### 6. Webhook rate limiter fills up (Low, fixed)

The webhook route counts every call against its id, which comes from the URL, before the id is checked [V: `internal/serve/webhook_api.go`]. The limiter holds 10 000 keys and blocks new keys while it is full of live entries (fail closed) [V: `internal/auth/limiter.go`]. About 10 000 calls with random ids within a minute therefore block every webhook for that minute. Each failed call writes the fail2ban line, so the caller's address is banned long before that [A: depends on the jail's threshold]. Fix (PR 574): the route looks the id up first and counts only calls of a known webhook; an unknown id answers 401 and writes the fail2ban line without an entry in the limiter. The number of keys is now bounded by the number of webhooks. `TestWebhookOverHTTP` calls an unknown id more often than the limit and expects 401 each time; it fails without the change [V]. A known id now answers 429 after ten calls while an unknown one never does, which tells a caller that an id exists; ids have 128 random bits, so guessing one is not practical [E].

### 7. Notes (Info)

- **Clones of other applications keep the live database** in their configuration: only WordPress, Laravel and Symfony are rewritten; the UI shows a checklist [V: ADR-0070]. A staging clone of such a site writes into production until someone changes its configuration. Known and documented; worth a warning on the clone dialog.
- **Browser login of the CLI** (RFC 8628) can be phished like every device flow: whoever approves a code someone else sent them gives that person a token. The approval page shows the client name and address [V: `internal/auth/device.go`].
- **Git host names** are checked but not resolved; a name pointing at 127.0.0.1 makes git-sync connect to a local port as the site user [V: `agentapi.ParseGitURL`]. Only admins and managers set the repository, and the site user can connect there anyway.
- **Database server list:** admins with only `databases:read` see the server details meant for `db_servers:write` [V: `internal/serve/dbservers_api.go`]; no secrets are in them [A].

## What was checked and found sound

| Area | What was looked at | Result |
|---|---|---|
| Authorization (ADR-0035) | `internal/authz`, `allow`/`allowSite`, the route matrix test that runs every route against the roles, a granted and a foreign user, a token without the scope | sound; the matrix covers every route [V: `TestRouteMatrix`]. Routes that name their site through another resource (restores, imports, findings, deploys) resolve it from the stored record [V] |
| CSRF and client address | `secure`, `sameOrigin`, cookies `SameSite=Strict`, `Sec-Fetch-Site`; the panel socket (0660, group www-data) as the only place X-Real-IP is trusted | sound [V]. `same-site` requests are refused too, which matters when sites share the panel's registrable domain |
| Clone file copy (ADR-0070) | `internal/sites/copy.go` | sound: openat2 with `RESOLVE_BENEATH`/no symlinks, O_PATH and a second open checked with `sameObject`, hard links of other owners skipped, other file systems skipped, set-id bits dropped [V] |
| Permission reset (ADR-0067) | `internal/sites/reset.go` | same pattern as the copy [V] |
| Restore of deleted sites (ADR-0074) | `internal/backup/restore.go`, `hostashi-restore@.service` | sound: admins only, the target must exist, a foreign target only while the source does not exist, sandboxed unit with the one home writable [V] |
| Git deploy (ADR-0062) | URL parser (no `ext::`, `file://`, options), drop-in quoting (`%`, `$`, quotes), build script quoting, switch and prune through `os.Root`, result file checked | sound apart from findings 1, 5 and 6 [V] |
| Webhook proof | HMAC-SHA256 and constant-time compares, 1 MiB limit, unknown id and wrong proof answer alike | sound [V] |
| Password protection and IP rules (ADR-0066) | names, paths, bcrypt form, generated nginx map and geo | sound: no character can leave its nginx string, paths match the normalized `$uri` case-insensitively, the password is not passed to the application [V] |
| Security headers | value check | no control characters, `\`, `$`, braces or the quote [V] |
| App environment | `agentapi.ValidateAppEnv`, unit quoting | no NUL or line breaks [V] |
| Quarantine (ADR-0064) | `internal/scanunit/quarantine.go` | sound: `os.Root`, hash checked on the copy, restore with `O_EXCL` and `O_NOFOLLOW`, root-only quarantine [V] |
| ZIP extraction | `internal/files/archive.go` | runs as the site user; entry and size limits, no absolute or `..` names, symlinks skipped [V] |
| WordPress install and clone | `internal/wpcli` | passwords on standard input or as credentials, never in argv or the log [V] |
| API tokens | `AuthenticateToken`, allowlist on the peer address | sound [V] |
| `os/exec` | every use | only the packages with an ADR exception (mariadbctl, pgctl, phpctl, resticctl, sysuser, webctl, wpcli) [V] |

## Tools

- **semgrep** 1.x with the rule sets `p/golang` and `p/gosec`, without tests: 16 results, all false positives: `unsafe` for the quota ioctls and SQL built from identifiers that `pgIdent` quotes or from fixed column lists [V].
- **govulncheck** v1.9.0 (the CI version): "No vulnerabilities found" [V].
- **gosec** runs in CI through golangci-lint [V: `.golangci.yml`].

## Not covered

Adminer and its sessions (first review), the CloudPanel import, the release and apt pipeline, the frontend's own code (XSS in React is limited by the CSP `default-src 'none'` of the API, the SPA's CSP was not reviewed), Python and Node.js runtime installs, nftables rules for app ports, and anything that needs a running server: no dynamic test, no fuzzing beyond the existing fuzz tests.
