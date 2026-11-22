# ADR-0054 Site types Node.js, reverse proxy and redirect

**Status:** Accepted (Kim, 2026-11-22). **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0005 (nginx), ADR-0015 (resource limits), ADR-0017 (import and presets), ADR-0018 (API), ADR-0021 (site logs), ADR-0032 (SSH per site), ADR-0033 (hardening), ADR-0034 (firewall), ADR-0035 (roles), ADR-0036 (PHP versions from the panel), ADR-0037 (UI), ADR-0053 (site logs in the panel).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- v1 scope (03-features, section 3.0): site types Node.js and reverse proxy are P0, redirect is P1 and in v1. Python is P1 and gets its own ADR after this one [V: `docs/03-features.md`, Kim 2026-11-21].
- Today a site has no type field. It is "PHP" when `sites.php` is set and "static" otherwise. Every site gets a Linux account, a home with `htdocs`, a slice, quota, SSH, cron and backups [V: `store.Site`, `web.Site`].
- The CloudPanel scanner already sorts sites into `php`, `wordpress`, `static`, `nodejs`, `proxy` and `python`; the import plan skips every type but the first three with `type_unsupported` [V: `cloudpanel/scan/scan.go`, `cloudpanel/plan/build.go`].
- Per-site units run as the site user in the site's slice, are rendered by the agent and started through the `internal/phpctl` exception, which allows `systemctl` with unit names that match fixed patterns [V: `hostashi-php@`, `hostashi-valkey@`, `phpctl`].
- Node.js release lines on 2026-11-22: 26 (LTS "Lithium", 26.13.1), 24 (LTS "Krypton", 24.22.3), 22 (LTS "Jod", 22.23.5); 25 and 27 are not LTS [V: `https://nodejs.org/dist/index.json`]. The distributions ship one Node.js version each, and it differs by distribution [U: Debian 13 ships 20.x, which is end of life].
- nodejs.org publishes `SHASUMS256.txt` signed by the release team for every release, and `.tar.gz` builds for linux-x64 and linux-arm64 [U].
- A TCP port on 127.0.0.1 can be opened by every local user, so a Node.js app on a loopback port is reachable by the other sites' PHP, cron jobs and SSH sessions [U]. Hostashi's firewall is nftables (ADR-0034) [V: `packaging/firewall/hostashi.nft`].
- A reverse proxy that may point at any address is a server-side request forgery tool: 127.0.0.1 reaches the panel, Adminer (8444) and other local services, 169.254.169.254 the cloud metadata service [V: ADR-0038, ADR-0042; U: metadata address].

## Options

**Where Node.js comes from**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Distribution package `nodejs` | apt updates, nothing new | One version, different per distribution, Debian's is old [U] |
| B. NodeSource apt repository | apt updates | One version per server, third-party repository and key |
| **C. Official nodejs.org builds, LTS lines only, side by side under `/opt/hostashi/node/<major>`** | Same versions on both distributions, several in parallel, like PHP with sury | Hostashi updates them itself; needs a pinned list and a check workflow |
| D. nvm per site user | What CloudPanel does [U] | Every site downloads and runs its own toolchain, no central updates, no checksums |

**How nginx reaches the app**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Loopback TCP port assigned by Hostashi, `PORT` in the environment, nftables lets only nginx and the site user connect** | Works with every framework | One nftables rule set more |
| B. Unix socket in `PORT` | No port at all | Many frameworks expect a number [U] |
| C. Port chosen by the user, no filter | Simple | Other sites can reach the app |

## Recommendation

**Node.js source C, connection A, and the three types as below.**

**Common to all types**

- New column `sites.type`: `static`, `php`, `node`, `proxy`, `redirect` (and `app`, Addendum 2). Migration 0045 sets `php` where `sites.php` is set, `static` otherwise. The type can be changed at any time, see Addendum 3.
- Every type keeps domains, aliases, HTTPS (Let's Encrypt or own certificate), HSTS, HTTP→HTTPS, ACME challenges, the health check, disable with status page (ADR-0047), maintenance (ADR-0027) and site logs (ADR-0053).
- New site form: the type is the first choice; the form then shows only the fields of that type. The site's settings page hides tabs that do not apply.

**Node.js (`node`)**

- Like PHP: a Linux account, home, `htdocs`, slice, quota, SSH/SFTP, cron, file manager, databases and backups.
- Versions: the LTS lines that are still supported (today 22, 24, 26). The list with the exact release and the SHA-256 of each build lives in `internal/agentapi/node_versions.go`. A weekly workflow (like "PHP versions", ADR-0036) checks nodejs.org, verifies `SHASUMS256.txt` against the release keys and opens a PR; Kim merges. A version is installed by the fixed root unit `hostashi-node-install@<major>`, which runs `hostashi node-install <major>`: Go downloads the pinned `.tar.gz`, checks the SHA-256 and unpacks it to `/opt/hostashi/node/<major>` (root, read-only for others). No apt, no shell, no new binary. Patch releases arrive through the next Hostashi update; the agent then restarts the apps of that line.
- The app: unit `hostashi-node@<site>.service`, `User=<site>`, in the site's slice, `WorkingDirectory=` a folder in the home (default `htdocs`), `Restart=on-failure`, the same sandbox as the PHP units, `NODE_ENV=production`, `PORT=<port>`, `HOST=127.0.0.1`.
- Start command, one of two typed forms, no free shell text in the panel: `script` (a `.js`/`.mjs`/`.cjs` file below the working directory, run as `node <file>`) or `npm` (`npm run <script>`, the script name matches `[a-z0-9:_-]{1,64}`). What `package.json` runs is the site's own code under the site user, as PHP files are.
- Environment variables: name `[A-Z_][A-Z0-9_]{0,63}`, value up to 4 KiB, at most 100. Stored encrypted with `/etc/hostashi/secret.key` (like the TOTP seeds) [V: `internal/secrets`], written by the agent to a root-owned `0600` file that systemd reads. The panel shows names, values only on request (`sites:write`).
- Port: assigned by the panel from 20000–29999, stored, never chosen by the user. nftables output rule: to these ports on `lo` only uid `www-data` and the owning site uid, everything else is rejected.
- nginx: `proxy_pass http://127.0.0.1:<port>`, headers `Host`, `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Real-IP`, WebSocket upgrade on. A folder for static files is optional (`location` with `try_files` before the proxy), off by default.
- Panel: start, stop, restart, state (active, failed, restarting) and the app log (stdout/stderr to `/var/log/hostashi/sites/<site>/app.log`, rotated and shown like the other site logs, ADR-0053). `npm install`/`npm ci` run in the site's SSH shell in v1; the site's Node.js version is first in `PATH` of its shell [A: through a managed file in the home that `.profile` reads].

**Reverse proxy (`proxy`)**

- No Linux account, no home, no PHP, no databases, no cron, no SSH, no backup (the config lives in the panel's database, which is backed up with the server).
- Target: one URL `http://` or `https://`, host and optional port and path, no user info, no query. Refused: loopback, link-local (169.254.0.0/16, fe80::/10), multicast, unspecified addresses, and names that resolve to them when the site is saved and on every apply [A: nginx resolves once per reload, so a DNS change after save is a gap; recheck on apply narrows it]. Private networks (10/8, 172.16/12, 192.168/16, fc00::/7) are allowed, because that is the typical use (another host in the same network, `10.0.0.5:8080` in the CloudPanel test data) [V: `cloudpanel/fixture`].
- `https` targets verify the certificate and send the host name (SNI) by default. A switch "Do not verify the target's certificate" exists for self-signed targets, with a warning.
- `Host` header: the visitor's host by default, or the target's host (switch). WebSocket upgrade on, `X-Forwarded-*` headers as for Node.js, timeouts nginx's default.
- Site snippets (ADR-0029) are allowed in the server block as today.

**Redirect (`redirect`)**

- No Linux account, no home, nothing else; nginx only.
- Target URL `http(s)://` with host, optional path; status 301 (default), 302, 307 or 308; switch "Keep path and query" (on: `$request_uri` is appended).
- A redirect to one of the site's own domains is refused (loop).
- ACME challenges still answer, so the redirect works over HTTPS.

**Roles and API (ADR-0018, ADR-0035)**

- `POST /api/v1/sites` takes `type` (default `static` or `php` from `php`, so old clients work) and one object per type: `node {version, workdir, start, env}`, `proxy {target, host_header, tls_verify}`, `redirect {target, status, keep_path}`. `PATCH /api/v1/sites/{name}` changes these fields; changing `type` is its own endpoint (Addendum 3).
- `POST /api/v1/sites/{name}/app/{start|stop|restart}` with `sites:write`. Installing Node.js versions: `node:install`, admin only (like `php:install`).
- Proxy and redirect sites need the same `sites:create` as any site.

**CloudPanel import (ADR-0017)**

- `proxy` sites are imported as `proxy`, with a blocker if the target is refused above.
- `nodejs` sites are imported as `node` with the port taken over into the Hostashi range (new port) and a review finding "set the start command"; the app stays stopped until it is set. The Node.js version is the nearest supported LTS line, with a finding.
- `python` stays `type_unsupported` until its own ADR.

## Consequences

- Store: migration 0045 (`sites.type`, tables for the app, the proxy and the redirect settings, the port with a unique index). Hosting: create and delete without an account for `proxy` and `redirect`; features that need a home refuse those sites with `400`.
- Agent: new typed operations `node.install`, `site.app.apply` (unit drop-in, environment file), `site.app.action`; `phpctl` gets the unit patterns `hostashi-node@<site>.service` and `hostashi-node-install@<major>.service`. No new binary, no new `os/exec` exception.
- nginx: three new branches in the site template (`node`, `proxy`, `redirect`), tested like the existing ones with `nginx -t`.
- Firewall: one output rule set for the app ports in `hostashi.nft`.
- CI: the installer test starts a small Node.js app and a proxy site on both distributions.
- Workflow "Node versions" (weekly), like "PHP versions".
- Frontend: type choice in New site, per-type settings, app controls; screenshots to Kim before the UI PR.
- User docs (ADR-0043): one wiki page "Site types".
- Rough order of PRs: (1) type column and migration, (2) redirect, (3) reverse proxy, (4) Node.js install, (5) Node.js app unit and port filter, (6) import, (7) UI after screenshots, (8) wiki [E: 8 PRs].
- Risk: Node.js apps can use much memory; the site's slice limits (ADR-0015) apply to the app unit too.

## Open questions for Kim

None left.

1. ~~Node.js from the official nodejs.org builds (option C), LTS lines only?~~ Decided (Kim, 2026-11-22): yes.
2. ~~Reverse proxy: allow private networks, refuse loopback and link-local, as recommended?~~ Decided (Kim, 2026-11-22): yes; public addresses and host names are allowed too.
3. ~~Proxy and redirect sites without a Linux account (no SSH, files, cron, backups)?~~ Decided (Kim, 2026-11-22): yes.

## Addendum 1: static sites as a first-class type (Accepted, Kim, 2026-11-22)

**Context.** Kim needs static sites often (plain HTML, builds of Hugo, Astro and similar) and gives them high priority (Kim, 2026-11-22). A site without PHP already works today: "No PHP" in the New site form, no FPM pool, nginx serves `htdocs` with `try_files $uri $uri/ =404` and refuses `.php` files [V: `web/src/Sites.tsx`, `internal/web/nginx.go`]. What is missing: the form presents it as "PHP: none" rather than as a type, the document root is always `<home>/htdocs` [V: `sites.WebRootName`], and single-page apps have no fallback to `index.html`.

**Decision proposed.**

- `static` is one of the types in the New site form, listed second after PHP; existing sites without PHP become `static` in migration 0045 (as above).
- Document root: an optional folder below `htdocs` (for example `public`, `dist`, `build`), for `static` and also for `php` (Laravel's `public/`). Relative path, segments `[A-Za-z0-9._-]`, no `..`, at most 4 levels; the agent checks with `openat2`-style resolution that it stays below `htdocs` and is not a symlink out [A: same check as the file manager, ADR-0040]. A missing folder shows nginx's 404 and a warning in the panel, so a deploy can create it later.
- Switch "Single-page app": `try_files $uri $uri/ /index.html` instead of `=404`. Off by default.
- Static sites get long cache headers for fingerprinted assets only when the site asks for it: switch "Cache assets" (`expires 30d` for css, js, fonts, images), off by default [A: wrong caching of non-fingerprinted files is worse than none].
- Everything else as for PHP sites without PHP today: account, SFTP/SSH, files, cron, backups, quota. Git deploy (later thread) will build into these folders.
- No new agent operation beyond the docroot check, no new `os/exec` exception.

**Order.** Right after the type column, before the redirect: (1) type column and migration, (1a) static type with document root folder and SPA switch, then redirect and the rest.

## Addendum 2: own app as a binary (Accepted, Kim, 2026-11-22)

**Context.** Kim wants a type for apps that are one executable, for example Go or Rust services (Kim, 2026-11-22). Technically this is the Node.js type without a runtime [A].

**Decision proposed.**

- New type `app`. Like `node`: Linux account, home, slice, quota, SSH/SFTP, files, cron, databases, backups.
- Unit `hostashi-app@<site>.service` with the same sandbox, slice, restart policy, environment file, assigned loopback port (`PORT`, `HOST=127.0.0.1`), nftables filter, nginx proxy branch and app log as `hostashi-node@` (one shared template in the code, two unit files).
- Start command, typed: a file below the home (relative path, rules as the document root folder of Addendum 1, must be a regular file with an execute bit and owned by the site user) plus at most 16 arguments, each up to 256 bytes, no NUL or newline. The agent writes them as a systemd `ExecStart=` with every argument quoted by Go code; no shell [A: systemd's own quoting rules are applied by a tested encoder, with tests for spaces, quotes, `$`, `%` and `\`].
- The binary is the site's own code under the site user, like PHP files and Node.js apps; nothing about its content is checked.
- Upload and update of the binary: SFTP or the file manager, then "Restart" in the panel. Git deploy (later thread) can build into the home.
- Panel: start, stop, restart, state and app log, as for Node.js.
- No new agent operation beyond those of Node.js (`site.app.apply`, `site.app.action` take the type), `phpctl` gets the unit pattern `hostashi-app@<site>.service`. No new `os/exec` exception.

**Order.** After Node.js (it reuses the app unit, the port and the filter): static, redirect, proxy, Node.js, app, import, UI, wiki.

## Addendum 3: change the type of a site at any time (Accepted, Kim, 2026-11-22)

**Context.** Kim wants to change a site's type at any time after create (Kim, 2026-11-22). Types with an account (`static`, `php`, `node`, `app`) and types without one (`proxy`, `redirect`, decided in question 3) differ in what exists on the server.

**Decision proposed.**

- `POST /api/v1/sites/{name}/type {"type", <settings of the new type>}`, scope `sites:write`, audit action `site.type` with the old and new type and settings (diff as in the audit log). In the panel: "Change type" on the site's settings page, a dialog with the fields of the new type and a summary of what starts and stops.
- Between types with an account, everything of the account stays: home, files, databases, cron jobs, SSH logins, backups, quota, limits. Only the runtime changes: the PHP pool, the Node.js or app unit and its port are stopped and removed, the new one is set up. Example: `php` → `static` stops the pool; `static` → `node` assigns a port and starts the app once its start command is set.
- To `proxy` or `redirect` from a type with an account: the account and its files **stay** and keep working (SSH, files, cron, backups), only nginx serves the proxy or redirect. The settings page shows "This site still has a Linux account and files" with the action "Remove account and files" (danger zone, typed confirmation like delete, ADR-0048 style list of what goes). So no data is lost by a type change.
- From `proxy` or `redirect` to a type with an account: the account and home are created as for a new site (pool and limits preset as on create).
- Order, as in ADR-0048: store transaction with the new type and settings, then the new runtime is prepared (pool, unit, port filter), then one `web.apply` switches nginx, and only then the old runtime is stopped. If preparing or `web.apply` fails, the transaction is undone and the old type keeps running; the new runtime is removed again. A failure after the switch (stopping the old runtime) is logged and retried by the reconciler, the site already serves the new type.
- Things that only make sense for one type are kept but inactive after a switch (page cache and PHP settings of a former `php` site, environment variables of a former `node` site), so switching back restores them. The page cache is turned off for non-PHP types.
- No new agent operation, no new `os/exec` exception.

**Order.** The endpoint comes with the type column PR series: after static (1a) the switch between `static` and `php` (1b); each later type PR adds its switch paths and tests.

## Addendum 4: asset caching for PHP sites too (Accepted, Kim, 2026-11-22)

**Context.** Addendum 1 made asset caching a switch of static sites. Kim wants it for PHP sites as well, for example WordPress (Kim, 2026-11-22). Kim also wants the time and the file types configurable.

**Decision.**

- "Cache assets in the browser" is a switch of `static` and `php` sites; other types refuse it. The single-page app switch stays static only.
- Time: 1 minute to 365 days, default 30 days. More than a year brings nothing, as HTTP caches cap freshness at one year in practice [A].
- File types: a list of up to 40 extensions, letters and digits, without the dot. Default: css, js, mjs, woff, woff2, ttf, otf, eot, svg, svgz, png, jpg, jpeg, gif, webp, avif, ico, mp4, webm, ogg, ogv. `php` is refused: PHP answers change per request. HTML stays fresh unless listed.
- nginx: one `map $uri` per site plus `expires` in the server block; PHP answers reach nginx through `index.php` and never match the map. WordPress and similar software change asset addresses with `?ver=` when a file changes, so a long time is safe there [A].
- Not taken over, on purpose: `Access-Control-Allow-Origin: *` on all assets (lets every other site embed the files; maybe later as its own switch for fonts) and turning the access log off for assets (statistics and fail2ban need the log).
- The settings are kept while the switch is off or the type has no asset cache, so turning it on again restores them.

## Implementation notes: reverse proxy (2026-11-22)

Notes on how the reverse proxy decision above is built; they change no decision.

- The agent has no network access (`IPAddressDeny=any`), so it cannot ask DNS. The panel resolves the target's name and sends the addresses with the web state; the agent checks every address again and also refuses the addresses of the server's own interfaces (a proxy to the server's public address would reach its services past the firewall). nginx gets the checked addresses in an `upstream` block and never resolves the name itself [V: `internal/agent/proxy.go`, `internal/web/nginx.go`].
- The name is resolved when the site is saved (an error then refuses the change) and on every apply. A name that does not resolve on a later apply keeps the live addresses of the same target; a refused address leaves none, and the site answers 502. Neither stops the apply of the other sites. Like nginx's own lookup, a DNS change takes effect at the next apply [A: a periodic re-check may follow].
- One refused address among several refuses the whole answer, as a sign of a name that points back here.
- Request bodies go to the target as they arrive (`proxy_request_buffering off`) with no size limit in nginx; the target decides.
- Site snippets for reverse proxy sites come later: the snippet check (ADR-0029) works on a site's home, which a proxy site does not have.

## Implementation notes: Node.js install (2026-11-22)

Notes on how the Node.js source (option C) is built; they change no decision.

- `hostashi node-install <major>` runs in `hostashi-node-install@<major>.service` as root without any capability, with `ProtectSystem=strict` and `/opt/hostashi/node` as its only writable path. It downloads `node-v<version>-linux-<x64|arm64>.tar.gz`, at most 256 MB, into that directory and checks the SHA-256 from `agentapi.NodeReleases` before it opens the archive [V: `internal/nodeinstall`].
- Unpacking takes only directories, regular files and relative symbolic links that stay inside the tree, with `..` only in front of a link's target; every entry must lie below the archive's top directory. Modes become 0755 or 0644, so no setuid bit and nothing writable for others survives. All writes go through `os.Root` [V: `internal/nodeinstall/nodeinstall_test.go`].
- The new tree is swapped in with one `renameat2(RENAME_EXCHANGE)`, so a running app never sees a half-written line. `.hostashi-version` in the line's directory names the installed release.
- API: `GET /api/v1/node` (scope `sites:read`, every role, so a site's version can be chosen: the lines, their pinned release and end of life, what is installed, the last installation and its log) and `POST /api/v1/node/install` `{"major":"24"}` (scope `node:install`, high risk, admin only, audited as `node.install`). A line whose pinned release is installed answers 409. CLI: `hostashi node status`, `hostashi node install <major>`.
- Not yet: reinstalling a line on a Hostashi update that pins a newer release (comes with the app unit, which must restart the apps of that line), the weekly "Node versions" workflow, and the panel page (screenshots first).

## Implementation notes: Node.js app (2026-11-22)

Notes on how the app of a Node.js site is built; they change no decision.

- The app runs in `hostashi-node@<site>.service` as the site user, without any capability, with `ProtectSystem=strict`, `ProtectHome=tmpfs` and the site's home bound back in, inside the site's slice, so its limits (ADR-0015) apply. The agent writes two drop-ins: the slice and isolation, and the app itself (working directory, `ExecStart`, `NODE_ENV=production`, `HOST=127.0.0.1`, `PORT`). `MemoryDenyWriteExecute` stays off, as V8 compiles code at run time [V: `internal/appunit`, `packaging/systemd/hostashi-node@.service`].
- The start command is typed: `script` runs `node <file>` for a `.js`, `.mjs` or `.cjs` file below the working directory, `npm` runs `npm run <script>` with a script name of `[a-z0-9:_-]`. There is no shell and no free text. The working directory lies below the home, `htdocs` by default.
- The panel hands each Node.js site the lowest free port of 20000–29999, below Linux's ephemeral range; the store keeps it unique (migration 0052). nginx passes every request of the site to `127.0.0.1:<port>`, with the headers of the reverse proxy.
- Loopback is open to every local user, so the firewall filters it: a new TCP connection to a port of the range is accepted only from root, `www-data` and the uid that owns the port; others get a TCP reset. The agent writes the pairs of port and uid to `apps.nft`, which the base rules include. A file that does not load refuses the change and keeps the old one. With `--no-firewall` the filter is missing; the agent only logs a warning [V: `packaging/test/firewall-test.sh`].
- Stdout and stderr go to `/var/log/hostashi/app/<site>/app.log` (log kind `app`, ADR-0053), opened by systemd as root, so the app cannot replace the file with a link. logrotate truncates it in place.
- The app stays stopped while it has no start command, after a stop in the panel (until it is started again, also across reboots), and while the site is disabled. A crash restarts it after 2 seconds, at most 5 times a minute.
- API: `type: "node"` with `node: {version, workdir, startKind, start}` on `POST /api/v1/sites`; `PUT /api/v1/sites/{name}/node`; `GET /api/v1/sites/{name}/app`; `POST /api/v1/sites/{name}/app/start|stop|restart` (scope `sites:write`, every role on its own sites: the app runs as the site user, like cron jobs). A line that is not installed answers 409 `node_missing`.
- Not yet: environment variables for the app (encrypted, with their own migration), restarting the apps of a line after it is reinstalled on a newer release, the panel page, and type changes.

## Implementation notes: removing a Node.js line (2026-11-22)

- `POST /api/v1/node/remove {major}` (action `node:remove`, scope `node:install`, admin only, audit `node.remove`). The agent (`node.remove`) refuses a line that a site's app runs on (from its own app state, also for stopped apps), one that is not installed and one while an installation runs.
- The agent renames `/opt/hostashi/node/<major>` out of the way before it deletes it, so a half-removed line never looks installed, and deletes the line's install log. It runs in the agent, no unit: nothing is downloaded or run.
- The panel shows a remove button only for an installed line without sites, and asks first [V: container test].
