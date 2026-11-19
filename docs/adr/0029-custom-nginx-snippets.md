# ADR-0029 Custom nginx snippets per site

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0005 (web server, generator rules), ADR-0017 (CloudPanel import, Part 2: custom snippets), ADR-0018 (API, high-risk scope), ADR-0027 (maintenance pages), [`04-architecture.md`](../04-architecture.md) 4.3, [`03-features.md`](../03-features.md) row "Vhost editor with `nginx -t` and rollback" (P0).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- ADR-0017 decided that the importer keeps every directive it cannot map as a **custom snippet**, after a one-time review by a person, and that nothing from the old server reaches a live config without the `nginx -t` candidate check [V: ADR-0017 Part 2, open question 4].
- On `web2` the scanner found 10 to 43 such directives per site, 399 in total: mostly `add_header` (141), `if` at server level (70), static caching locations, `location /` variants (14), `rewrite`, `error_page`, `auth_basic`, `set`, plus about 25 individual hardening rules. A few are `map` and `limit_req_zone`, which only exist in the `http` context [V: `spike/s6-adopt/REPORT.md`, second run].
- The reconciler renders one config per generation and has no snippet slot yet [V: `internal/web/nginx.go`]. S2 showed that a snippet in its own include file cannot close the server block, because nginx rejects an unmatched `}` per file [V: S2 finding 3].
- S2 also noted that inside the server block a snippet can do anything a server block allows [V: S2 finding 3]. The nginx **master runs as root and opens log files itself** [U]. Checked for this ADR with nginx 1.24 in the sandbox (2026-11-19): a server-level snippet `access_log /any/path;` makes `nginx -t`, run as root, create that file owned by root; an existing file is not truncated, and every request then appends a line with the request URI to it [V]. nginx escaped a `%0a` in the URI, so a quick newline injection did not work [V]; that lowers, but does not remove, the risk of steering root into writing into system files [A].
- So free nginx text lets whoever can write a snippet (a stolen admin session, an API token, a bug in the web process) make root create and append to files anywhere. That already breaks the core rule that the web process runs without root and the agent only executes typed operations (ADR-0004), and the next paths below do not need any trick at all.
- Further cross-site paths through free text [A, from the nginx docs U, not tested]: `fastcgi_pass` to another site's PHP socket runs code as that site's user; `proxy_pass` to `unix:/run/hostashi-panel.sock` with a forged `X-Real-IP` bypasses the panel's IP allowlist and fail2ban; `root`/`alias` into another home serves its files (homes are group `www-data`, so the nginx workers can read them [V: `internal/sites/manager.go`]); `listen` or `server_name` claims another site's traffic; `ssl_certificate_key` points the root master at any key on the host.
- The panel has one admin today (Kim), but the API (ADR-0018) and later roles make the snippet a remote interface [V: ADR-0018 names a high-risk scope for snippets].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Allowlist: the snippet is parsed, and every directive must be on a list with a typed check of its arguments. The agent checks again before rendering, then `nginx -t`** | The agent stays the trust boundary: a compromised panel can only produce what the list allows. Clear error per line. Covers the web2 patterns (see below) | Some legitimate directives are rejected until they are added to the list. We maintain the list |
| B. Free text, guarded by the admin role, a high-risk scope and `nginx -t` | Everything works, no list to maintain | Panel compromise equals root (see Context). `nginx -t` only checks syntax, not safety [V: S2]. Contradicts ADR-0004 |
| C. Free text with a denylist (`listen`, `*_log`, `include`, ...) | Less work than A | nginx has hundreds of directives and the distribution ships extra modules [U]; one forgotten directive reopens the hole. Denylists are what `05-security.md` avoids elsewhere |

## Recommendation

**Option A.**

**Where a snippet goes**

- Two slots per site: **server** (inside the site's serving server block, after Hostashi's own locations) and **location /** (inside Hostashi's own `location /`, before its `try_files`). The second slot is for the 14 `location /` variants on web2, because a second `location /` is a duplicate and fails `nginx -t` [V: sandbox test].
- Each slot is its own include file in the generation, so a stray `}` cannot leave the block [V: S2].
- Locations that Hostashi owns cannot be redefined in a snippet: `/`, `^~ /.well-known/acme-challenge/` and anything below `/.hostashi/` [A].
- A disabled site and a site in maintenance mode do not render the snippets, so a snippet cannot bypass the 503 (ADR-0027, PR #93) [A].

**What a snippet may contain (first list, extended by PR when needed)**

| Group | Directives | Argument check |
|-------|------------|----------------|
| Structure | `location` (nested, depth at most 4), `if`, `limit_except` | Same list applies inside |
| Responses and routing | `return`, `rewrite`, `try_files`, `error_page`, `set`, `break`, `index`, `default_type`, `charset`, `autoindex` | Variables allowed, no file paths |
| Headers and caching | `add_header`, `expires`, `etag`, `if_modified_since` | Free values |
| Access | `allow`, `deny`, `auth_basic`, `auth_basic_user_file`, `satisfy` | User file: literal path below the site's home, no variables |
| Files | `root`, `alias` | Literal, clean path below the site's home, no variables, no `..` |
| Compression and limits | `gzip*`, `brotli*`, `client_max_body_size`, `client_body_timeout` | Sizes and times only |
| Upstreams | `proxy_pass`, `proxy_set_header`, `proxy_hide_header`, `proxy_redirect`, `proxy_*_timeout`, `proxy_buffering`, `proxy_http_version` | `proxy_pass` only `http(s)://` with a literal host or IP and port, never `unix:`, no variables in the host |
| PHP | `fastcgi_param`, `fastcgi_*_timeout`, `fastcgi_buffers`, `fastcgi_buffer_size`, `fastcgi_pass`, `fastcgi_index`, `fastcgi_split_path_info` | `fastcgi_pass` only to the site's own socket |
| Logging | `access_log off`, `log_not_found` | No log paths |

Everything else is rejected with "not allowed in custom snippets", including `listen`, `server_name`, `ssl_*`, `include`, `*_log` with a path, `*_temp_path`, `perl*`, `js_*`, `lua*`, `uwsgi_*`, `scgi_*`, `grpc_*`, `resolver`. `map` and `limit_req_zone` cannot live in a server block at all ("not allowed here" [V: sandbox test]); they are reported, and rate limiting becomes a typed feature later (ADR-0017 Part 3).

**Checks and storage**

- The panel parses with the existing strict parser (`internal/cloudpanel/nginxconf`, moved to a neutral package) and shows errors per line. The **agent runs the same check again** on every `web.apply`, because the panel is not trusted with root-level text (ADR-0004). Then the S2 candidate generation with `nginx -t` and the health check, with rollback.
- Limits: 64 KiB per slot, 500 directives per site [E].
- The panel stores the snippet per site and slot, with who saved it and when. A snippet from the import is stored only after the review screen confirmed it (ADR-0017).
- API: own scope `sites.snippets:write` (ADR-0018 high-risk scope), never part of a default token. Reading with `sites:read`. Every change goes to the audit log [A: audit log not built yet].

**Import**

- The scanner's custom entries per site become a draft for the two slots. ACME challenge locations, `pagespeed`, `listen`/`server_name`/`ssl_*` and everything a preset covers are dropped with a reason. The rest goes through the same check, and the review screen shows each rejected line with its reason. The person can edit the draft before confirming.

## Consequences

- No new `os/exec` exception; `nginx -t` already runs through the reconciler [V: `internal/web`].
- Some web2 lines will be rejected until the list grows or a preset exists, for example the `limit_req_zone`/`map` pairs and the include on the one blocked site [A: exact number known only after a dry run of the check against web2's scan].
- The list is security-relevant code: every addition needs a test with a hostile example and a short reason in the PR.
- Fuzzing the snippet check (`05-security.md` 5.6) becomes concrete.
- An "unmanaged" site with a fully hand-written vhost (`04-architecture.md` 4.3) stays out of scope; it would bring the root problem from Context back.

## Open questions

1. Option A (allowlist checked by the agent) or B (free text for the admin)? Recommendation: A.
2. Is the first list enough to start, or should something be added before the web2 migration? Answered best by a dry run against web2's scan.
3. May `proxy_pass` point at loopback addresses (`127.0.0.1`, `::1`)? Recommendation: yes, for local apps, with the named risk that a snippet can reach local HTTP services.
