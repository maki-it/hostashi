# ADR-0062 Git deploy per site

**Status:** Accepted (Kim, 2026-11-24). **Date drafted:** 2026-11-24. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0015 (resource limits), ADR-0016 (storage pools), ADR-0018 (API), ADR-0024 (sealed secrets), ADR-0028 (cron jobs), ADR-0032 (SSH per site), ADR-0033 (hardening), ADR-0035 (roles), ADR-0042 (panel on 443), ADR-0044 (email), ADR-0046 (FastCGI cache), ADR-0054 (site types), ADR-0058 (OpenAPI and CLI), ADR-0061 (Python).

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

None left.

1. ~~Release directories with an atomic switch and rollback (1B) instead of a working tree in place (1A)?~~ Decided (Kim, 2026-11-24): release directories with an atomic symlink switch (1B).
2. ~~Git through go-git as the site user (2A), accepting no Git LFS in v1?~~ Decided (Kim, 2026-11-24): go-git as the site user (2A), no Git LFS in v1.
3. ~~Build commands as free command lines like cron, with presets (4B)?~~ Decided (Kim, 2026-11-24): free command lines as for cron, with presets (4B).
4. ~~Webhook for GitHub, GitLab, Gitea/Forgejo and a generic bearer token (3A)?~~ Decided (Kim, 2026-11-24): yes, optional per site (3A).

## Implementation notes: settings and deploy key (2026-11-24)

Notes on how the first part is built; they change no decision.

- Migration 0057 adds `site_git` (one row per site: repository, branch, deploy directory, shared paths and build commands as JSON arrays, releases to keep, build timeout in seconds, submodules, `auth`, the sealed token and deploy key, the public key, the confirmed host key, the webhook id and sealed secret) and `site_deploys` (the history; filled by the deploy runs that come next). Both go with the site (`ON DELETE CASCADE`) [V: `internal/store/git_test.go`].
- The rules live in `internal/agentapi/git.go`, so the agent can check them again before a run. Addresses: `https://`, `ssh://` and `user@host:path`; no `http://`, `git://`, `file://`, `ext::`, local paths, user info in an https address, passwords, queries or fragments; IP addresses with the proxy rule of ADR-0054, `localhost` and `.localhost` refused. Branches follow `git check-ref-format`, narrowed to ASCII. The deploy directory is a clean relative path below the home without hidden folders, so it never points into `.deploy`; shared paths may be hidden (`.env`) but not inside `.git` or inside each other. Build commands use the cron rules [V: `internal/agentapi/git_test.go`].
- `auth` must match the address: `none` and `token` need https, `ssh` needs an SSH address. With `ssh` the panel makes an Ed25519 key (`hostashi-<site>` as comment), seals the OpenSSH private key bound to the site and keeps it when the repository changes; a new host or port drops the confirmed host key. `POST /api/v1/sites/{name}/git/key` makes a new one. A token is sealed the same way; an empty token on a change keeps the stored one. Neither ever leaves the panel through the API, and the audit log (`site.git.update`, `site.git.delete`, `site.git.key`) holds fingerprints and "set", never the secrets [V: `internal/hosting/git_test.go`, `internal/serve/git_api_test.go`].
- API: `GET /api/v1/sites/{name}/git` (`sites:read`, defaults for a site without a repository), `PUT` and `DELETE` and `POST .../git/key` (action `sites.git:write`, admins and managers, new high-risk scope `git:write`). Static, PHP, Node.js, app and Python sites with an account; proxy and redirect sites answer 409 `wrong_site_type`. The default deploy directory is `htdocs`, so the PHP web root or the app's working directory below it points into the release.
- Not yet: `git-sync`, the deploy unit and runs, the host key confirmation, the webhook, the UI (next PRs).

## Implementation notes: git-sync (2026-11-24)

Notes on how `hostashi git-sync` is built; they change no decision.

- `internal/gitsync` uses go-git v5.19.3. At start it removes go-git's `file`, `git` and `http` transports: the file transport would run `git-upload-pack` [V: `plumbing/transport/file/client.go` uses `execabs`], `git://` and `http://` are unencrypted. Only `https` and `ssh` remain, and the address passes `agentapi.ParseGitURL` before anything connects [V: `internal/gitsync/gitsync_test.go`].
- The branch is fetched (depth 1, no tags) into a bare cache `<home>/.deploy/repo` whose only remote is the site's address. The release is not written by go-git's worktree: the commit's tree is walked and every entry is created through an `os.Root` of the new release with `O_EXCL`, files 0644 or 0755, folders 0755. Entries with `..`, `.git` (any case), backslashes or absolute paths fail the run, as do symbolic links whose target leaves the release or points into `.git`. At most 200,000 entries and 512 MiB per file. Submodule entries become empty folders until submodules are built. The release has no `.git`.
- Shared paths become absolute links into `<home>/.deploy/shared/<path>`. The first deploy moves the commit's own file there (a default `.env` or an `uploads` folder becomes the start); later deploys drop the commit's copy and keep the shared one.
- SSH checks the server against the confirmed host key only (`ssh.FixedHostKey`, only that key's algorithm offered); there is no `known_hosts` and no agent. A token goes as HTTP basic auth, user `hostashi` when none is set. Token and key come from files (systemd credentials in the deploy unit), never as arguments. The command refuses to run as root and writes the commit, author, first line of the message and entry count as JSON to `-result`.
- go-git's in-process server, used by the tests, has no shallow support, so the unit tests fetch without depth; shallow fetches are covered by the installer test with a real repository [A: next PR].

## Implementation notes: deploy unit (2026-11-24)

Notes on how the agent runs a deploy; they change no decision.

- `packaging/systemd/hostashi-deploy@.service` is a oneshot template with the sandbox of `hostashi-python-venv@`, without `MemoryDenyWriteExecute=` (Node.js builds). `internal/deployunit` writes two drop-ins from checked values: the slice and the home (as for the apps) and `60-hostashi-deploy.conf` with `ExecStart=/usr/local/bin/hostashi git-sync ...` (each argument quoted), then `ExecStart=/bin/sh /var/lib/hostashi-deploy/<site>/build.sh` when there are commands, `TimeoutStartSec=` of 15 minutes for the fetch plus the build timeout, and `StandardOutput=truncate:/var/log/hostashi/app/<site>/deploy-<id>.log` (the newest 30 logs stay) [V: `internal/deployunit/deployunit_test.go`]. The binary is `/usr/local/bin/hostashi`, not `/usr/lib/hostashi` as the summary above says.
- The build script is root:site 0640 in a root:site 0750 folder, as for cron jobs: `set -e`, `cd` into the release, and every command printed before it runs. Commands follow the cron rules. The PATH starts with the site's Node.js line or `<home>/.venv/bin`; `HOSTASHI_DEPLOY_ID` and `HOSTASHI_RELEASE` are set.
- Token and deploy key are written root-only to `/run/hostashi-deploy/<site>/` and reach the run as systemd credentials (`LoadCredential=`, `-token-file=%d/token`); they never appear in the drop-in or the unit's properties and are deleted once the run is over.
- `site.deploy.start` stops the last run and queues the unit (`systemctl start --no-block`). The template has `RemainAfterExit=yes`, so a finished run stays `active` (succeeded) or `failed` and loaded until the next start; an `inactive` unit is a start still queued, and after 5 minutes one that never ran. (A first version compared `InvocationID`, which an unloaded oneshot unit loses; the installer test caught it.) `site.deploy.status` maps this to running, succeeded or failed; a successful run also needs git-sync's result file (40-hex commit, author and message cut and cleaned of control characters), read through an `os.Root` of the home. A second start while one is queued or running is refused.
- `site.deploy.switch` works through an `os.Root` of the home: on the first switch a real deploy directory becomes the release `0-initial`; then a relative link is made next to it, given to the site user and renamed over the deploy directory in one step. `site.deploy.prune` deletes releases that are neither kept nor live, and nothing while a run is queued or running. Links that lead out of the home fail both [V: `internal/deployunit/switch_test.go`].
- The systemctl calls go through the existing `phpctl` exception with a fixed unit pattern `hostashi-deploy@<site>.service` and the properties `ActiveState`, `SubState` and `Result` only. A deleted site loses its drop-ins, script, credentials and state.
- Not yet: the panel side (history, polling, switch, restart of the app, rollback, API), then the host key confirmation, the webhook and the UI.

## Implementation notes: deploy runs in the panel (2026-11-24)

Notes on the panel side of a deploy; they change no decision.

- `POST /api/v1/sites/{name}/deploys` (202) queues a row in `site_deploys`, unseals the token or key, hands the run to the agent and marks it running; one queued or running run per site (409 `deploy_running`). An SSH repository without a confirmed host key answers 409 `host_key_unconfirmed`. Action `sites.deploy` with the new scope `git:deploy` (every role that works on the site): it runs only the commands an admin or manager stored. The source is `panel` for a session, `api` for a token [V: `internal/serve/deploy_api_test.go`].
- One loop in the panel (`RunDeploys`) asks the agent about every active run every 3 seconds and when a deploy starts; after a panel restart it picks them up again. A succeeded run is switched live, then a running app restarts (Node.js, app, Python) and an enabled page cache is emptied; a failed restart or purge stays a warning on a live run. A failed build, an unknown run or a failed switch never goes live. Afterwards the releases of the newest `keep` successful deploys stay, plus `0-initial` and the live one. A row left queued for 2 minutes, or a rollback cut short, fails [V: `internal/hosting/deploy_test.go`].
- `POST .../deploys/{id}/rollback` switches back to the release of a successful deploy, without a build, as a run of its own in the history; the commit fields are copied. Rolling back to `0-initial` has no route yet. `GET .../deploys` lists the newest 100 runs (`sites:read`), `GET .../deploys/{id}/log` returns the end of the run's log, at most 256 KiB (`sites.deploy`, as the log may show what the build prints).
- The web server's check of the document root (ADR-0054: real folders only, no links) refused the deploy link and would have stopped every later `web.apply`. It now allows exactly one link on the path: a relative link of the form `[../]*.deploy/releases/<release>` that points from its own place to a release of the site; the path through the release must again be real folders below the home, without links or other file systems [V: `internal/sites/webdir_test.go`]. Every other link stays refused. This changes a rule of ADR-0054, recorded there as Addendum 5 (Accepted by Kim, 2026-11-24).
- `<home>/.deploy` is `0711`: the home's group is the web server's, but `.deploy` belongs to the site's group, so nginx passes it as "other" without listing it; the cache and the results below stay `0700`. git-sync gives a base left closed the search bit again [V: installer test, `internal/gitsync/gitsync_test.go`].
- The installer test deploys `octocat/Hello-World` from GitHub with a build command, checks the link, owner, shared path, served file, log, rollback and that a failing build does not go live [A: needs GitHub reachable from CI].

## Implementation notes: host key confirmation (2026-11-24)

Notes on pinning the host key of an SSH repository; they change no decision.

- `POST /api/v1/sites/{name}/git/hostkey/scan` reads the host key the server presents, without logging in, as for the backup target (`backup.FetchHostKeyAt`: Ed25519 and ECDSA first, RSA only with SHA-2 and at least 3072 bits). The panel resolves the name first and refuses it when any address is one a reverse proxy may not reach (loopback, link-local and the like, `ResolveProxyTarget`); it then connects to those addresses only, so the answer cannot come from a name that changed in between. Why a server did not answer goes to the log, the API says only `unreachable` (502) [V: `internal/hosting/git_hostkey_test.go`].
- `PUT /api/v1/sites/{name}/git/hostkey` pins the key the administrator confirmed; the setting then shows its SHA-256 fingerprint (`hostKeyFingerprint`). Both need `sites.git:write`; the audit log (`site.git.hostkey`) holds the old and the new fingerprint. A new host or port in the address drops the key again, a new path keeps it [V: `internal/serve/git_api_test.go`].
- Nothing is pinned without that confirmation: the UI shows the scanned fingerprint next to the ones GitHub, GitLab and Codeberg publish (UI PR).
- `git-sync` offers an RSA host key with `rsa-sha2-512` and `rsa-sha2-256` only; before, it asked for `ssh-rsa` (SHA-1), which current servers refuse [A: OpenSSH has refused SHA-1 RSA signatures by default since 8.8].
- The installer test scans a second sshd on the machine's own address, compares the fingerprint with `ssh-keygen -lf` of its Ed25519 host key and pins it; a deploy before that answers 409 `host_key_unconfirmed` [A: needs the CI run].

## Implementation notes: webhook (2026-11-24)

Notes on the deploy webhook; they change no decision.

- `POST /api/v1/sites/{name}/git/webhook` (`sites.git:write`) turns the webhook on or gives it a new secret and shows `{id, path, secret}` once; the id (128 bits, hex) stays on renewal, so the address in the Git host stays. `DELETE` turns it off. The secret (256 bits, base64url) is sealed bound to the site; the audit log (`site.git.webhook`) says only on, new secret or off [V: `internal/hosting/deploy_test.go`, `internal/serve/webhook_api_test.go`].
- `POST /api/v1/hooks/deploy/{id}` needs no login. It counts every call against 10 per minute and webhook (then 429) and reads at most 1 MiB. The first proof present must be valid: `X-Hub-Signature-256` (`sha256=` and the HMAC-SHA256 of the body), `X-Gitea-Signature`, `X-Forgejo-Signature`, `X-Gitlab-Token` or `Authorization: Bearer`, all compared in constant time. An unknown id and a wrong proof answer alike (401) and write the `auth.failed` line with `"kind":"webhook"`, which the fail2ban filter `hostashi-panel` now matches too (ADR-0021).
- Only the pushed `ref` is read from the body. A push to the configured branch deploys (202 `started`); a ping from GitHub answers 200 `pong`; other events, tags and other branches answer 202 `ignored`. A call without a body and without an event header (a script with the bearer token) deploys the branch.
- A push during an active run is not lost: it is noted (202 `queued`) and `RunDeploys` starts one more run after that run ends; more pushes in that time still give one run. The note is kept in memory only, so a panel restart in that window drops it.
- A webhook deploy has no user (`actor` empty in the history, no user in the audit log); `source` is `webhook`.
- The installer test turns the webhook on, checks that a wrong GitLab token gets 401 and the fail2ban line, and that a push with the right one deploys and goes live [A: needs the CI run].
