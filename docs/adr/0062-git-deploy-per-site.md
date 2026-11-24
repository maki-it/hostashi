# ADR-0062 Git deploy per site

**Status:** Proposed. **Date drafted:** 2026-11-24. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0015 (resource limits), ADR-0016 (storage pools), ADR-0018 (API), ADR-0024 (sealed secrets), ADR-0028 (cron jobs), ADR-0032 (SSH per site), ADR-0033 (hardening), ADR-0035 (roles), ADR-0042 (panel on 443), ADR-0044 (email), ADR-0046 (FastCGI cache), ADR-0054 (site types), ADR-0058 (OpenAPI and CLI), ADR-0061 (Python).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants proper Git support in sites (2026-11-23). `03-features` has "Git deploy via webhook" as P1, in 1.0 (Kim, 2026-11-21) [V: `docs/03-features.md`].
- Today code reaches a site through SFTP, the file manager or the site's SSH shell (ADR-0032); a site user can run `git` there by hand, but Hostashi knows nothing about it [V: ADR-0032, ADR-0040].
- Sites that run code: `static`, `php`, `node`, `app`, `python`. Node.js, app and Python sites run in `hostashi-{node,app,python}@<site>` with a working directory below the home; PHP and static sites are served from a web root below `htdocs` [V: ADR-0054, ADR-0061].
- nginx passes PHP scripts as `$realpath_root$fastcgi_script_name`, so a web root reached through a symlink resolves to the real directory per request [V: `internal/web/nginx.go`]. With that, OPcache keys on the real path and a symlink switch does not serve stale code [U: the common pattern of Deployer and Envoyer].
- Precedent for running what an admin wrote as the site user: cron commands are stored, written to a root-owned script file and run by `/bin/sh` inside a sandboxed unit as the site user, in the site's slice; the site user can run any code through PHP or SSH anyway [V: ADR-0028]. Precedent for a fixed oneshot unit as the site user driven by the agent: `hostashi-python-venv@` [V: `packaging/systemd/hostashi-python-venv@.service`].
- Outbound traffic is not filtered by the host firewall (`policy accept` in the output chain apart from the app ports) [V: `packaging/firewall/hostashi.nft`]. The panel is reachable on 443 (ADR-0042), so a Git host can call a webhook [V: ADR-0042].
- Git in Go: **go-git** v5.19.3 (2026-10-04) is the stable line, v6 is in beta (v6.0.0-beta.1) [V: proxy.golang.org]. License Apache-2.0 [U]. It clones and fetches over HTTPS and SSH, supports shallow clones and submodules, not Git LFS [U]. go-git had path traversal bugs on checkout in the past (CVE-2023-49568, CVE-2023-49569, fixed in 5.11) [U]. The distribution's `git` is 2.47 on Debian 13 and newer on Ubuntu 26.04 [U]; it runs `ssh` through `GIT_SSH_COMMAND` or `core.sshCommand`, which are shell strings [U: git docs].
- CloudPanel has no Git deploy in the panel; `dploy` (MIT) is a separate CLI with release directories and shared paths [V: `docs/01-cloudpanel-research.md`]. Only the idea is used, no code (clean-room).

## Options

**1. How a deploy changes the site's files**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Working tree in place: the site's directory is the checkout, a deploy fetches and checks out the new commit | Simple, paths stay as they are, little disk | Visitors see a half-updated tree while files change and while `composer install` or `npm run build` runs; a failed build leaves the site broken; rollback means checking out and building again |
| **B. Release directories with an atomic switch: each deploy checks out into a new `<home>/.deploy/releases/<n>`, builds there, then the site's directory becomes a symlink to it; shared paths (uploads, `.env`) are symlinks into `<home>/.deploy/shared`** | The site never sees a half deploy; a failed build never goes live; rollback is one symlink switch; the last releases stay for comparison | More disk (N releases), shared paths must be named, the site's directory becomes a symlink |

**2. Who runs Git**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. go-git inside `hostashi git-sync`, run as the site user in a fixed oneshot unit** | No `os/exec`, no shell, pure Go; runs unprivileged and sandboxed, so a bug in parsing a hostile repository hits only that site; the key comes in through `LoadCredential=` | No Git LFS; slower on very large repositories [U]; one more dependency tree |
| B. Distribution `git` in the same unit (`ExecStart=/usr/bin/git ...`) | Complete Git, LFS with `git-lfs` | SSH options only through `GIT_SSH_COMMAND`, a shell string; one more package |
| C. go-git or `git` in the root agent | Nothing new to run | Parses untrusted data as root; against rule 1 |

**3. What starts a deploy**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Button, API and CLI, plus an optional webhook per site** | Push to deploy, as `03-features` asks | An unauthenticated endpoint on the panel (signature checked) |
| B. Button, API and CLI only | No public endpoint | No push to deploy |
| C. Polling every N minutes | No webhook setup | Delay, load on the Git host, still runs Git on a timer |

**4. Build steps after the checkout**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Only fixed typed steps (`composer install --no-dev`, `npm ci`, `npm run <script>`, `pip install -r <file>`) | Nothing free-form | Never enough (Laravel `artisan migrate`, Hugo, custom scripts); users fall back to SSH |
| **B. Command lines as for cron (ADR-0028): stored, written to a root-owned script file, run by `/bin/sh` as the site user in the deploy unit; the UI offers presets that fill in common commands** | Covers every stack; reuses the reviewed cron design; no new privilege, as the site user can run anything over SSH | Free text inside the site's sandbox (as cron) |
| C. A script from the repository (`.hostashi/deploy.sh`) | Lives with the code | Whoever can push can change what runs on deploy without the panel showing it |

## Recommendation

**1B, 2A, 3A, 4B.**

**Settings per site** (types `static`, `php`, `node`, `app`, `python`; not `proxy` and `redirect`)

- Repository URL: `https://…` or `ssh://git@host[:port]/path` or the scp form `git@host:path`; no `file://`, no local paths, no `ext::`; host must not be loopback, link-local or one of the server's own addresses (the proxy-target rule of ADR-0054) [A].
- Branch (default `main`), deploy directory (the directory that becomes the symlink: the PHP or static web root, or the app's working directory; default the current one), shared paths (relative, at most 32, for example `.env`, `wp-content/uploads`, `storage`), releases to keep (default 5, 2–20), build commands (at most 20 lines of 4 KiB, the cron rules), build timeout (default 15 minutes, at most 60), submodules on or off.
- After the switch, by site type: PHP gets a graceful reload of its FPM pool, Node.js, app and Python get a restart, and the site's FastCGI cache is purged when it is on (ADR-0046). No free-form "after switch" hook in v1.

**Access to the repository**

- Public HTTPS: nothing. Private HTTPS: a token (user name optional), sealed with the server key like database passwords (ADR-0024), never shown again.
- SSH: Hostashi makes one Ed25519 deploy key per site; the panel shows the public key to paste into GitHub, GitLab, Gitea/Forgejo or Bitbucket as a read-only deploy key. The private key is sealed in the database; for a run the agent writes it to a root-only file that the unit gets through `LoadCredential=`, so the site user's processes outside the deploy never see it [A: credentials are readable only by the unit's user, during the run].
- Host key: on first setup Hostashi reads the server's host key and shows its SHA-256 fingerprint; the admin confirms it and it is pinned for the site. A changed key fails the deploy until the admin confirms again. GitHub's, GitLab's and Codeberg's published fingerprints are shown next to it for comparison [A].

**The deploy run**

- One fixed unit `hostashi-deploy@<site>.service`, `Type=oneshot`, `User=` the site user, in the site's slice, with the sandbox of `hostashi-python-venv@` (`ProtectSystem=strict`, the home writable, other homes hidden, `NoNewPrivileges=yes`); `systemd` never starts a second run of a site while one is running. The agent writes a drop-in per run with:
  1. `ExecStart=/usr/lib/hostashi/hostashi git-sync` with the typed values (URL, branch, commit, target `<home>/.deploy/releases/<n>`, depth), every argument quoted by `appunit.Quote`. It fetches the branch into a bare cache `<home>/.deploy/repo` (shallow, depth 1 on the first run), checks the exact commit the panel resolved out into the new release, refuses links that point out of the release, and links the shared paths. The release gets only the files, no `.git`; the commit is recorded next to it.
  2. One `ExecStart=/bin/sh <script>` per build command, the scripts root-owned in `/var/lib/hostashi-deploy/<site>/` as for cron, working directory the new release. `PATH` holds the site's Node.js and Python (`.venv/bin`) first.
- Only after every step exits 0 does the agent switch: a new symlink next to the deploy directory, then `renameat2(RENAME_EXCHANGE)` or `rename(2)` over it, so the change is atomic [A: as `internal/rtinstall` swaps runtimes]. Then the reload or restart above, then old releases beyond the limit are deleted.
- First deploy on a site with files: the existing deploy directory is moved to `<home>/.deploy/releases/0-initial`, so nothing is lost and the first rollback goes back to it.
- A failed step stops the run; the live release is untouched; the failed release is deleted after its log is kept.
- Rollback: pick a kept release, the agent switches to it and runs the reload or restart; no build runs.
- Log of each run (Git and build output) in `/var/log/hostashi/app/<site>/deploy-<id>.log`, opened by systemd as root; the panel shows the history (commit, author, message, who or what started it, start, duration, result) for the last 50 runs and the log of each.
- Disk and limits: releases count toward the site's quota and storage pool (ADR-0015, ADR-0016); a deploy is refused when free space in the quota is below the size of the live release [E]. Build steps use the site's slice limits.

**Webhook**

- Off by default. When on: `POST https://<panel>/api/v1/hooks/deploy/<hook-id>` with a random 128-bit `hook-id` and a separate secret. Accepted proofs: GitHub `X-Hub-Signature-256` (HMAC-SHA256 of the body), Gitea and Forgejo `X-Gitea-Signature` / `X-Forgejo-Signature` (HMAC-SHA256), GitLab `X-Gitlab-Token` (constant-time compare), and a generic `Authorization: Bearer <secret>` [U: provider docs].
- The endpoint reads at most 1 MiB, checks the proof, takes only the pushed ref and ignores everything else in the body; a push to another branch answers 202 and does nothing. A valid call queues a deploy of the branch head (the panel resolves the commit itself) and answers 202; a deploy already queued is not queued twice. Rate limit 10 calls per minute per hook; failures are logged with the `auth.failed` line for fail2ban (ADR-0021).
- The secret is sealed and shown once; it can be renewed. Behind ADR-0059 the rate limit uses the real client IP.

**Roles, API and CLI (ADR-0018, ADR-0035, ADR-0058)**

- `GET /api/v1/sites/{name}/git` (`sites:read`): settings without secrets, public key, pinned fingerprint, live release, deploy history. `PUT /api/v1/sites/{name}/git` and `DELETE` (scope `sites.git:write`, admins and managers of the site; audit `site.git.update` with diff, secrets masked). `POST .../git/key` makes a new deploy key, `POST .../git/hostkey` confirms the fingerprint, `POST .../git/webhook` turns it on or renews the secret.
- `POST /api/v1/sites/{name}/deploys` starts a deploy (scope `sites.deploy`, every role that can work on the site, audit `site.deploy`); `POST .../deploys/{id}/rollback`; `GET .../deploys` and `.../deploys/{id}/log`.
- All routes with `mux.Op` and an OpenAPI entry; CLI `hostashi api sites git …` and `hostashi api sites deploy NAME` through the API.
- Email (ADR-0044): a failed deploy is a new event in the `sites` category, opt-in per user.

**Not in v1**: Git LFS, building in containers, deploying several branches as staging sites (comes with staging/cloning), pushing from the server, polling.

## Consequences

- No new `os/exec` exception: Git runs as go-git in `hostashi git-sync` inside the unit, build commands run through the unit's `ExecStart=` as for cron, and the agent drives the unit through the existing `phpctl` `systemctl` exception with the pattern `hostashi-deploy@<site>.service`.
- New dependency `github.com/go-git/go-git/v5` (newest release when the PR starts; its tree checked against the license allowlist). v6 once it is stable.
- Store: one migration for the Git settings, sealed secrets, releases and deploy history. Number from the coordinator when the PR starts.
- Agent: typed operations `site.deploy.run`, `site.deploy.switch`, `site.deploy.status`, `site.deploy.prune`; the site's directory becomes a symlink, so the file manager, backups and the type change must accept that [A: restic stores symlinks as links and backs up `.deploy/` with the home].
- The type change (ADR-0054 addendum 3) to `proxy` or `redirect` keeps `.deploy/` with the files; "remove account and files" removes it.
- CI: the installer test creates a PHP site, deploys a small public repository over HTTPS with one build command, checks the page through nginx, deploys a second commit, rolls back, and checks a failing build keeps the old release live, on both distributions [A: CI reaches github.com].
- Frontend: a "Git" tab per site (repository, key, host key confirmation, branch, shared paths, build commands with presets, webhook), a deploy button, history and log; screenshots to Kim before the UI PR. Wiki page "Git deploy".
- Rough order of PRs: (1) store, settings API and deploy key, (2) `git-sync` with go-git and tests against a local repository, (3) deploy unit, build steps, switch, rollback, (4) webhook, (5) UI after screenshots, (6) email event, (7) wiki [E: 7 PRs].
- Risks: a site whose code writes into its own directory at run time (WordPress updating plugins) loses those changes on the next deploy unless the paths are shared; the dialog and the wiki say so. go-git bugs on hostile repositories are limited to the site user's sandbox. The webhook is a public endpoint; it only queues a deploy of the configured branch.

## Open questions for Kim

1. ~~Release directories with an atomic switch and rollback (1B) instead of a working tree in place (1A)?~~ Decided (Kim, 2026-11-24): release directories with an atomic symlink switch (1B).
2. ~~Git through go-git as the site user (2A), accepting no Git LFS in v1?~~ Decided (Kim, 2026-11-24): go-git as the site user (2A), no Git LFS in v1.
3. ~~Build commands as free command lines like cron, with presets (4B)?~~ Decided (Kim, 2026-11-24): free command lines as for cron, with presets (4B).
4. Webhook for GitHub, GitLab, Gitea/Forgejo and a generic bearer token (3A)?
