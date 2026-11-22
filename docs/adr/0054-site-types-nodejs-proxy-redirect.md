# ADR-0054 Site types Node.js, reverse proxy and redirect

**Status:** Proposed. **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0005 (nginx), ADR-0015 (resource limits), ADR-0017 (import and presets), ADR-0018 (API), ADR-0021 (site logs), ADR-0032 (SSH per site), ADR-0033 (hardening), ADR-0034 (firewall), ADR-0035 (roles), ADR-0036 (PHP versions from the panel), ADR-0037 (UI), ADR-0053 (site logs in the panel).

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

- New column `sites.type`: `static`, `php`, `node`, `proxy`, `redirect`. Migration 0045 sets `php` where `sites.php` is set, `static` otherwise. The type is fixed after create in v1 (change type: delete and create).
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

- `POST /api/v1/sites` takes `type` (default `static` or `php` from `php`, so old clients work) and one object per type: `node {version, workdir, start, env}`, `proxy {target, host_header, tls_verify}`, `redirect {target, status, keep_path}`. `PATCH /api/v1/sites/{name}` changes these fields, not `type`.
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
