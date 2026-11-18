# Spike S6: CloudPanel adopt (read a server from the file system)

**Status:** throwaway prototype, not product code. Nothing here is a decision; any follow-up goes through an ADR.

**Question (from [docs/06-roadmap.md](../../docs/06-roadmap.md) S6 and [docs/03-features.md](../../docs/03-features.md) 3.11, path 1):** Can a real CloudPanel instance be read through the file system? Abort criterion: if fewer than 80 % of the sites are adopted cleanly, shelve the importer.

Evidence tags as in [docs/adr/README.md](../../docs/adr/README.md): **[V]** verified by test or code in this spike, **[A]** assumption, **[E]** estimate, **[U]** unverified general knowledge.

**Result on real data (`web2`, second run): 0 % clean, 90 % adoptable (19 of 21 sites without a blocker).** Whether that passes the abort criterion depends on what "adopted cleanly" means, which is the owner's call (finding 2). Every real site carries 10 to 43 directives Hostashi does not model; most of them recur on almost every site, so a small set of built-in presets would absorb the bulk [V for counts, A for the preset effect]. The scanner itself is small, read-only, safe against a hostile tree, and sorts every site into clean, review or blocked with a reason [V]. The synthetic corpus (45 % clean, 82 % adoptable) only tests the scanner; its numbers say nothing about real servers.

## Environment and limits of this spike

- Sandbox container, root, kernel 6.18, Go 1.24.7, `golang.org/x/sys` v0.33.0. Tests also run as `nobody` (ownership tests skip) and under `-race` [V].
- **No CloudPanel installation, no access to `web2`.** Nothing was created at Hetzner, no server was touched.
- **The CloudPanel file layout is from memory** [U], not checked against an installation. The scanner assumes:

  | What | Assumed path |
  |------|--------------|
  | Vhost per site | `/etc/nginx/sites-enabled/<domain>.conf` |
  | Certificate and key | `/etc/nginx/ssl-certificates/<domain>.crt` / `.key` (the scanner follows whatever `ssl_certificate` says, so a different path only matters for the fixture) |
  | PHP-FPM pool | `/etc/php/<version>/fpm/pool.d/<name>.conf`, `listen = 127.0.0.1:<port>` (TCP is verified from the template review in [01-recherche](../../docs/01-recherche-cloudpanel.md) 1.4, the path is not) |
  | Document root | `/home/<site-user>/htdocs/<domain>` (site user with home below `/home` is verified from the docs review, 1.2) |
  | Crontab | `/var/spool/cron/crontabs/<site-user>` (Debian/Ubuntu default) |
  | Panel | own vhost on port 8443 (port verified, 1.2), system user `clp` [U] |

- **Clean-room:** every vhost, pool and crontab in the fixture was written for this spike in generic nginx and FPM syntax. Nothing was copied or adapted from CloudPanel's `vhost-templates` or `docs`, and the scanner contains no CloudPanel text. The CloudPanel SQLite database (path 2 in 03-features) was not looked at.

## What was built

Module `spike/s6-adopt`, depends on S1 for the user and domain validators (local `replace`). About 2,100 lines of Go without tests, 600 lines of tests.

| Package | Content |
|---------|---------|
| `nginxconf` | Strict tokenizer and parser for nginx syntax: directives, blocks, both quote styles, comments, line numbers, the raw text of each directive. Limits: 1 MiB per file, depth 16, 20,000 directives. No evaluation of variables, regex or includes. |
| `scan` | Read-only scanner. Reads `/etc/passwd`, all FPM pools, every file in `sites-enabled`, the certificates the vhosts reference, and the crontab of each site user. Produces a `Report` with one `Site` per vhost (domains, redirects, user, document root, type, pool and PHP settings, TLS, cron, custom snippets) and findings with a severity. |
| `fixture` | Writes a synthetic CloudPanel-like server with 22 sites (same count as `web2`, invented mix) plus the panel vhost, a default server, the distribution pool and an orphaned pool and user. Each case carries the expected status. |
| `cmd/s6adopt` | `-gen DIR` writes the fixture, `-root DIR [-json]` scans a tree. Custom snippets and cron commands are hidden by default (`-show-raw` shows them), so a report can be shared. |

### How a site is classified

- **clean:** every directive maps onto the model, or is something Hostashi regenerates with its own defaults (TLS protocol settings, gzip, FastCGI parameters, the HTTP-to-HTTPS redirect, a `www` redirect, a dotfile deny rule). Lossless automatic changes are info findings and keep the site clean, for example moving an FPM pool from TCP to a unix socket.
- **review:** adoptable, but a human must look: any unknown directive or location (kept verbatim as a custom snippet), `env[...]` in a pool, unknown PHP settings, expired or self-signed certificates, unusual cron lines, Node.js or Python apps whose process manager is not scanned, non-standard user names, wildcard `server_name`.
- **blocked:** must not be adopted automatically: syntax error, file not trusted, document root outside `/home/<user>` or unclean, missing or system user, symlinked home, pool missing or running as a different user, two applications in one vhost file, include outside `/etc/nginx`, non-ASCII domain.

## Results

### Synthetic corpus

| Status | Count | Cases |
|--------|-------|-------|
| clean | 10 | 4 WordPress (PHP 8.1 to 8.3, `www` redirect, cron), 3 plain PHP (one on 7.4), static, reverse proxy to another host, purchased certificate |
| review | 8 | Node.js app, hand-added security headers and cache rule, basic auth on a path, pool with `env[DB_PASSWORD]`, expired certificate, crontab with `MAILTO` and a reference to another home, `include` of a rewrite snippet, self-signed certificate |
| blocked | 4 | pool runs as another site's user, vhost syntax error, PHP without pool, document root in `/var/www` |

Every case gets exactly the expected status and type, and the mapped values (domains, redirect hosts, pool user, PHP version, `pm.max_children`, PHP settings, cron schedule and verbatim command, upload limit, certificate kind) are checked field by field [V] `TestFixtureCorpus`, `TestMappedDetails`. The panel vhost, the default server, the distribution `www` pool and the panel user are reported as ignored, the unused pool and the users without a site as orphans [V].

### Safety and robustness

| Check | Result |
|-------|--------|
| The scan changes nothing: mode, size, mtime and content hash of every file are identical before and after | [V] `TestScanIsReadOnly` |
| Two scans of the same tree give byte-identical JSON | [V] `TestScanIsDeterministic` |
| `env[...]` values, private keys and site files never appear in the report; env key names do | [V] `TestSecretsDoNotLeak` |
| Site-controlled trees (home, document root) are only checked for existence and mode, never read. The only site-owned file that is read is the crontab, and only when it is owned by that user | [V] code, `TestHostileTrees/crontab_owned_by_another_user` |
| Vhost symlinked to a file outside the root (absolute or with `..`): refused | [V] `TestHostileTrees/symlink_out_of_the_root` |
| Absolute symlink inside the tree (`sites-enabled/x -> /etc/nginx/sites-available/x`) resolves **inside** the root, like on the live system | [V] `TestHostileTrees/absolute_symlink...` |
| Home directory replaced by a symlink: blocked | [V] same test |
| FIFO or directory in `sites-enabled`: blocked, the scan does not hang | [V] `TestHostileTrees/FIFO...` |
| Vhost that is world-writable or owned by a site user: blocked (root only) | [V] two subtests |
| 2 MiB vhost, 100 nested blocks: blocked | [V] |
| `include /etc/passwd`, missing include: blocked; plain include below `/etc/nginx`: read and analysed inline, its directives keep their own file name | [V] |
| Document roots with `..`, `$variable`, `/home`, `/home/root`, relative path: blocked | [V] |
| Upper-case domain lowercased, wildcard reported, `münchen.de` (not punycode) and `1.2.3.4` blocked | [V] |
| `nginxconf` fuzz, 30 s, 4 million executions: no panic, every parsed directive reparses from its raw text | [V] (short run only) |
| Crontab, pool and passwd parsers fuzz, 30 s: no panic | [V] |

## First run on real data (`web2`, 2026-11-18)

Kim ran the scanner (commit `3ad8fd1`) read-only on `web2` and shared the JSON. Only counts and structural observations are recorded here; customer domains, user names and paths stay out of the repository.

| | Count |
|-|-------|
| Sites | 22 (matches the number Kim reported) [V] |
| clean / review / blocked | 0 / 8 / 14 [V] |

What the run showed [V, from the report]:

- **The layout assumptions held**: vhosts in `sites-enabled`, certificates in `/etc/nginx/ssl-certificates`, document roots in `/home/<user>/htdocs/<domain>`, one TCP FPM pool per PHP site, the panel user `clp` and a catch-all default server.
- **13 of the 14 blocks were a scanner gap, not a real problem** (the 14th is a real finding, see below). Real vhosts are **two-tier**: a TLS server proxies to `127.0.0.1:8080`, and a second server block in the same file listens there and runs PHP. One site has a cache port (`6081`, probably Varnish [A]) in between, one vhost is the panel under its own domain (proxy to `8443`). The first scanner treated the backend block as a second application (`vhost_multi_app`) and left the pools orphaned.
- **Every site carries directives Hostashi does not model**: 276 custom findings in total. The same few locations appear on almost every site (ACME challenge path, `.git` deny, `xmlrpc.php`, WordPress login, static asset caching), plus individual hardening rules on single sites. So with "clean" meaning "nothing to review", the result is 0 % on real data.
- **`[global]` and `[default]` sections in `pool.d`** (one per PHP version) were misread as pools; `rlimit_core` was unknown.
- Real findings for Kim, not scanner bugs: all 22 private keys have mode `0644` (risk depends on the directory mode, which this version did not check); on 9 sites a `server_name` (`www1.` alias) is not covered by the certificate; one vhost includes a file below `/etc/nginx` that is not root-owned or is writable by others (correctly blocked); 5 users with a home below `/home` have no site; one of them has an unusual name that looks like a key file name.

Changes after the run (this commit), each covered by a new fixture case or test [V]:

- Two-tier vhosts are merged into one site: front block gives domains and TLS, backend block gives document root and PHP pool. A cache tier in between is a review finding. A vhost that proxies to the panel port is ignored.
- `[global]` sections are skipped, `www-data` default pools are ignored instead of orphaned, `rlimit_core` is known.
- A private key with mode `0644` is only a review finding if its directory lets others traverse it.
- Custom server-level findings name the directive (`server: add_header`), still without its value, so the next run shows *which* directives are missing from the model.

## Second run on `web2` (2026-11-18, commit `4b26912`)

| | Count |
|-|-------|
| Sites | 21 (the 22nd vhost is the panel under its own domain, now ignored) [V] |
| clean / review / blocked | 0 / 19 / 2 [V] |
| Custom directives per site | 10 to 43, median 17, 399 in total [V] |

The two blocked sites: the include of a file that is not root-owned or is writable by others (real), and one vhost with a server block the two-tier logic does not cover yet (`vhost_multi_app`, not analysed further) [V].

What the 399 custom findings are [V, directive names and location patterns only]:

| Kind | Count | Sites | Comment |
|------|-------|-------|---------|
| `add_header` (front and backend block) | 141 | most | security and cache headers |
| `if` at server level | 70 | most | probably host or scheme redirects [A] |
| ACME challenge location | 21 | 21 | Hostashi has its own |
| static asset cache location | 20 | 20 | candidate preset |
| `pagespeed` | 18 | about 18 | module Hostashi does not ship [A] |
| `try_files` at server level (backend) | 14 | 14 | standard, should be mapped (scanner gap) |
| `location /` variants | 14 | 14 | |
| `.git` deny, WordPress login, `xmlrpc.php` | 33 | 10 to 12 | candidate presets |
| `rewrite`, `error_page`, `auth_basic`, `set`, `limit_req_zone`, `map` | 18 | few | individual |
| unique hardening and path rules | about 25 | few | individual, need a person |

So about 90 % of the custom findings fall into fewer than ten recurring patterns [E from the table], and about 50 individual rules remain across 21 sites.

Real findings for Kim from this run: on 21 of 21 sites the private key has mode `0644` **and** its directory lets others in, so every local user, including every site's PHP code, can read every site's TLS key [V from the report, not checked on the server]. The other real findings from the first run still hold.

## Findings

1. **The real question is still open, and it is cheap to answer.** The scanner is ready to run read-only against real data. One run on `web2` (22 real sites) would answer the abort criterion directly. This needs Kim's explicit go-ahead, because it means running a binary on a production server, even if it only reads [A: Kim's rules, "Server verändern" only after approval]. Alternative: a fresh CloudPanel test VM with a few hand-made sites, which checks the layout but not the real mix of hand edits.

2. **"Adopted cleanly" needs a definition before the number means anything.** With hand-edited vhosts, nearly every real site will have at least one directive Hostashi does not model (headers, cache rules, redirects) [A]. If "clean" means "nothing to review", the 80 % bar is probably unreachable for any importer; if it means "no blocker, custom parts carried as a validated snippet", it is plausible [A]. Proposal: count *clean + review* as adopted, and use the share of *clean* as a measure of how much the model still lacks. The custom snippet must go through the S2 generation switch with `nginx -t`, never straight into a live config.

3. **`os.Root` is the wrong tool for reading a whole system tree.** It refuses *every* absolute symlink, even with `/` as root [V: checked in this spike, Go 1.24.7, `path escapes from parent`]. Debian-style `sites-enabled` links and many certificate paths are absolute [U]. The scanner therefore uses `openat2(RESOLVE_IN_ROOT | RESOLVE_NO_MAGICLINKS)`: `..` stops at the root and absolute links resolve relative to it, like in a chroot. That also makes it work on the old disk mounted read-only somewhere else, which is the likely migration path (finding 4). S4 uses `RESOLVE_BENEATH` for site trees, which is right there, because site trees should not contain absolute links at all.

4. **For Kim's own case "adopt" means "migrate", not "take over in place".** Hostashi is installed only on fresh servers (ADR-0009, ADR-0015 context). So the importer reads the old server (live over SSH, or a mounted snapshot), produces a plan, and then the new server pulls files and database dumps. The scanner here fits that: it has no write path at all. Taking over a running CloudPanel server in place would need quota activation with a reboot on a live system and a way to stop CloudPanel from rewriting files, both out of scope [A].

5. **PHP-FPM over TCP turns into a security gain on import.** CloudPanel pools listen on `127.0.0.1:<port>` [V from template review in 01-recherche 1.4], so any local process, including other sites' PHP, can talk to them. The scanner matches vhost and pool by address and reports the move to a per-site unix socket as an automatic info finding [V]. A pool that runs as a different user than the site's home owner is a blocker [V], because adopting it would carry a cross-site hole over.

6. **Certificates do not need to be copied in the common case.** Let's Encrypt certificates are recognised by issuer and simply reissued by Hostashi after DNS points to the new server; only purchased certificates need their key moved [V for detection on synthetic certs, A that issuer matching is enough for real LE chains].

7. **The scanner should not read site-owned content.** WordPress is detected by the existence of `wp-config.php` or `wp-includes`, Node.js by `package.json`; nothing in the home directory is opened [V]. The crontab is the only site-owned file read, as data, with an owner check. Free text that may carry credentials (custom snippets, cron commands) is hidden in reports by default [V].

## Not covered

- **Databases.** MariaDB databases, users and grants are not discovered. That needs database access (credentials or the socket as root) and is the next piece of the importer. Guessing database names from `wp-config.php` would mean reading site-owned files, which this design avoids on purpose.
- CloudPanel's own database (path 2), Varnish, Redis, ProFTPD users, extra SSH users and authorized keys, basic-auth password files, IP and bot blocking rules, Cloudflare settings, backup configuration, firewall rules.
- How Node.js and Python processes are supervised today (reported as review, not scanned).
- Disk usage per site (needed to plan pools and quotas, ADR-0015/0016). It means walking site-controlled trees and was left out.
- The actual migration: copying files with ownership, database dumps, DNS cut-over, rollback.
- Ubuntu 24.04 and Debian 13 specifics, arm64, a real CloudPanel installation.

## Next step (recommendation)

1. **Owner decides the definition** (finding 2). Recommendation: count a site as adopted when it has no blocker, and require that every custom part goes through the S2 candidate check (`nginx -t`) and is shown to a person once. Under that reading `web2` passes with 90 % [V], so the importer stays on the roadmap.
2. If it stays: write an ADR for the importer as a migration tool (finding 4), with built-in presets for the recurring patterns above (own wording and implementation, clean-room), database discovery, and a review screen for the remaining individual rules.
3. Independently of Hostashi: tighten the TLS key permissions on `web2` (only with Kim's approval, nothing was changed).

## How to run

```sh
cd spike/s6-adopt
go test ./...                                 # as root, so the ownership tests run too
go test ./nginxconf -run x -fuzz FuzzParse -fuzztime 30s
go run ./cmd/s6adopt -gen /tmp/s6fixture      # synthetic server
go run ./cmd/s6adopt -root /tmp/s6fixture     # table; add -json for the full report, -show-raw for snippets
```
