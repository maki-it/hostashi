# 7. Decisions (ADRs)

All entries are **proposals** (`Proposed`) until you confirm them. Format: context, options, recommendation, consequences. On acceptance, the status is set to `Accepted` and the date is added. Later changes get a new ADR number instead of overwriting.

| ADR | Topic | Status | Decision needed from you? |
|-----|-------|--------|---------------------------|
| 0001 | Clean-room and handling of reference repositories | Proposed | No, minimum legal rule |
| 0002 | Project license | Accepted on 2026-11-18 ([adr/0002](adr/0002-license.md)), AGPL-3.0 + CC-BY-4.0 + DCO | Done |
| 0003 | Language and stack | Accepted on 2026-11-18 ([adr/0003](adr/0003-language-and-stack.md)) | **Yes** (team know-how) |
| 0004 | Web/agent privilege separation | Proposed | No |
| 0005 | Web server | Accepted on 2026-11-18 ([adr/0005](adr/0005-web-server.md)), nginx | No, S3 is done |
| 0006 | State store and reconciler | Proposed | No |
| 0007 | Isolation model per site | Proposed | No |
| 0008 | Backups with restic | Proposed | No |
| 0009 | Target platforms and packaging | Proposed | Partly |
| 0010 | Target audience and v1 scope | Proposed (draft: [adr/0010](adr/0010-target-audience-and-v1-scope.md)) | **Yes** |
| 0011 | Name and trademark | Accepted (subject to trademark search) | Done, trademark open |
| 0012 | Language of code and documentation | Proposed | Yes, briefly |
| 0013 | Business model and governance | Proposed (draft: [adr/0013](adr/0013-business-model-and-governance.md)) | **Yes** |
| 0014 | Name of the CLI tool | Proposed | Yes, briefly |
| 0015 | Per-site resource limits | Proposed (draft: [adr/0015](adr/0015-per-site-resource-limits.md)) | **Yes** (priority of disk quota) |
| 0016 | Per-site storage location (storage pools) | Proposed (draft: [adr/0016](adr/0016-per-site-storage-location.md)) | **Yes** |
| 0017 | CloudPanel import as migration, presets | Accepted on 2026-11-18 ([adr/0017](adr/0017-cloudpanel-import-and-presets.md)) | Done |
| 0018 | Public REST API | Accepted on 2026-11-18 ([adr/0018](adr/0018-public-rest-api.md)) | Done |
| 0019 | PHP package source and multiple PHP versions | Accepted (Kim, 2026-11-18, [adr/0019](adr/0019-php-package-source-and-versions.md)) | **Yes** |
| 0020 | nginx package source and Brotli | Accepted on 2026-11-18 ([adr/0020](adr/0020-nginx-package-source-and-brotli.md)) | Done |
| 0021 | Brute-force and intrusion protection | Accepted on 2026-11-18 ([adr/0021](adr/0021-brute-force-and-intrusion-protection.md)) | Done |
| 0022 | Metrics, logs and tracing | Accepted on 2026-11-18 ([adr/0022](adr/0022-metrics-and-tracing.md)) | Done |
| 0023 | HTTPS for the panel | Accepted on 2026-11-18 ([adr/0023](adr/0023-panel-https.md)) | Done |
| 0024 | MariaDB databases per site | Proposed ([adr/0024](adr/0024-mariadb-databases-per-site.md)) | Yes, acceptance |

---

## ADR-0001 Clean-room and handling of reference repositories

**Context.** `cloudpanel-ce` contains no source code. `vhost-templates` and `docs` have no LICENSE file and are therefore protected by copyright. `dploy` is MIT, `clp-wp-varnish-cache` is GPLv3.

**Decision (proposal).**

- CloudPanel is used as a **functional reference**, not as a text template. Do not take over vhost templates, documentation texts, screenshots or UI texts.
- Write our own templates and texts. Whoever writes them should not copy 1:1 from the reference files, the structure follows from the Nginx documentation and the applications themselves.
- MIT code (`dploy`) may be used with its license notice. GPLv3 code only if ADR-0002 allows it.
- The name "CloudPanel" is only used nominatively and descriptively in comparisons ("alternative to CloudPanel").
- A legal review by a lawyer before publication is recommended. This is not legal advice.

**Consequence.** Somewhat more effort, but no legacy burden.

## ADR-0002 Project license

**Full text (status Accepted on 2026-11-18):** [adr/0002-license.md](adr/0002-license.md). Decided: code under AGPL-3.0-or-later, documentation and translations under CC-BY-4.0, contributions with DCO instead of CLA, no open core. Next step: `LICENSE`, documentation license and DCO notice into the repository. Before every publication, the own review (licenses of dependencies, name and trademark) stays mandatory. Not legal advice.

Original comparison:

| Option | Pro | Contra |
|--------|-----|--------|
| **Apache-2.0** | Maximum adoption, patent clause, companies use it without worry | Forks may become proprietary |
| **AGPL-3.0** | Protects against proprietary SaaS forks, GPL-compatible (Varnish plugin usable) | Some companies avoid AGPL, contributors hesitate |
| **GPL-3.0** | Classic for panels (HestiaCP), copyleft | Does not protect against SaaS use without releasing the source |
| **Open core** (for example Apache + proprietary pro modules) | Funding model | Needs a CLA, community distrust, double maintenance |

**Recommendation:** Apache-2.0 for adoption, if the goal is community. AGPL-3.0, if protection against "a hosting provider clones it and sells it" matters more. The decision depends on ADR-0013. Decide before the first line of published code, changing it later is painful with contributors. If dual licensing is conceivable, use **a CLA instead of DCO from the start**.

## ADR-0003 Language and stack

**Full text (status Accepted on 2026-11-18):** [adr/0003-language-and-stack.md](adr/0003-language-and-stack.md). Summary: Go confirmed; spikes S1 and S4 did not disprove anything. S2 and S7 are still pending, a failure of S2 reopens the ADR, S7 may change the frontend framework.

Original proposal:

**Proposal:** Go for backend, agent and CLI. SPA (Svelte or React) embedded. SQLite. Rationale: one static binary, small footprint, easy arm64 builds, good standard library for processes, files and TLS, memory-safe compared with C/C++ and PHP pitfalls.

**Alternatives:** Rust (stronger safety guarantees, slower start, smaller contributor pool), PHP/Symfony (probably CloudPanel's basis, large contributor pool in the hosting scene, but runtime and packaging are heavier), Python (many system libraries, but a runtime dependency).

**For you to decide:** What does your team work with most safely? A well-mastered stack beats the "ideal" one. If nobody knows Go, that is a real cost.

## ADR-0004 Web/agent privilege separation

See [architecture 4.2](04-architecture.md). Two processes, typed RPC over a Unix socket, no shell, no general exec interface.

**Consequence:** More initial effort per operation (schema, validator, handler). In return a much smaller attack surface. Boilerplate is reduced through codegen from a schema (spike S1).

## ADR-0005 Web server

**Full text (status Accepted on 2026-11-18):** [adr/0005-web-server.md](adr/0005-web-server.md). Summary: nginx (nginx.org stable) behind the `WebServerDriver` interface, Kim decided for nginx after spike S3. Reasons: built-in FastCGI cache, smaller footprint, largest base of recipes. Caddy remains possible as a later second driver, LiteSpeed is not chosen for v1 (proprietary or its own configuration world).

## ADR-0006 State store and reconciler

SQLite (WAL) as the source of truth, generated configs in versioned generations, validation before switching, rollback on error, drift detection. See [architecture 4.3](04-architecture.md).

**Open:** If multi-server comes early, PostgreSQL fits better. Re-evaluate after 1.0, not before.

## ADR-0007 Isolation model per site

Linux user per site, FPM pool over a Unix socket, systemd sandbox, cgroup limits. Containers (Podman/Docker) as an **optional** site type later. Rationale: the same isolation strength as established panels, but without a container runtime as a mandatory dependency and with little RAM.

## ADR-0008 Backups with restic

Restic as the engine (encryption, deduplication, many backends, also through Rclone). Databases through consistent dumps (MariaDB: `mariadb-dump --single-transaction` or `mariabackup`, PostgreSQL: `pg_dump`). Restore test as a feature. The alternative Borg is good too, but has fewer backends.

## ADR-0009 Target platforms and packaging

Start: **Debian 13 and Ubuntu 26.04** (Ubuntu 24.04 is dropped for v1 because of ADR-0020; previously: Ubuntu 24.04 and Debian 13, plus 22.04/Debian 12), **amd64 and arm64**. Add Ubuntu 26.04 once the dependencies are stable. No EOL systems (CloudPanel still supports Debian 11, which we deliberately do not take over). Distribution through a signed APT repository and `.deb`.

**Fresh servers only (Kim, 2026-11-17):** Hostashi is always set up on a freshly installed server, never added to grown systems. Recommended image: **Debian 13**, second supported option: **Ubuntu 24.04 LTS**. Ubuntu 26.04 LTS is added once the dependencies and spikes S5 and S8 run on it. The initial setup should be as simple and automated as possible, for example a one-liner or a cloud-init script when ordering the server (spike S5).

Rationale (general knowledge, unverified): Debian is more conservative, has fewer breaks between package versions and no additional services such as snap or vendor advertising notices. That fits a server that should run unobtrusively for a long time. Ubuntu LTS is stable too and widely used by providers. The only argument against Ubuntu 26.04 as the first choice is that it is new. Multiple PHP versions need an additional package repository on both systems (to be checked). Open: quota modules on the kernels of both systems, checked in spike S8 (see [ADR-0015](adr/0015-per-site-resource-limits.md)).

**To decide:** How far back should we support? Every additional version enlarges the matrix. With the "fresh servers only" rule, there is a strong case for not including 22.04 and Debian 12.

## ADR-0010 Target audience and v1 scope

**Full draft (status Proposed):** [adr/0010-target-audience-and-v1-scope.md](adr/0010-target-audience-and-v1-scope.md). Summary: option A (single server for developers and agencies) with guardrails.

Original proposal: single server for developers and agencies. Resellers, mail, DNS server and multi-server are non-goals for v1 (see [vision](02-vision-scope.md)).

## ADR-0011 Name and trademark

**Decision (2026-11-17):** The project is called **Hostashi**, an invented word from "host" and a hint of sushi.

**Rejected:**

- *Hostwerk*: The name is already widely used, also as a trademark and as a `.io` and `.de` domain.
- Other candidates that were checked and not chosen: Maki names, shipyard and coastal terms, nautical terms (Skerry, Keelson …), Hostwarte, Hostwacht, Hostmill, Hostkeep, Bentowerk, Servishi and others.

**State of the check (2026-11-17):**

- According to an RDAP query, the domains `hostashi.de`, `.dev`, `.com`, `.io` were **not registered**.
- The web search (US, standard) found no hit for the name, only similar ones (Hisashi as a first name, Hondashi as a seasoning). That is no proof, the search is fuzzy.

**Still to do before anything is published:**

1. Secure the domains (`.de`, `.dev`, `.com`, `.io`) while they are free.
2. Trademark search at DPMA, EUIPO and USPTO (classes 9 and 42), ideally with a review by a lawyer.
3. Check the GitHub organization and package names (apt, npm, Go module), this was not reachable during the research.
4. Check sound and meaning in the most important target languages.

**State 2026-11-17 (information from Kim, not checked by Claude):**

- `hostashi.de` is registered by Kim. `.dev`, `.com` and `.io` stay unregistered for now.
- Own trademark search (guide: `markenrecherche-anleitung.md` in the project folder): no hits. That is no proof and does not replace a review by a lawyer.
- The repository stays in the GitHub organization `maki-it`, there is no separate organization for Hostashi.

**Risk:** If the name sounds like "Hostinger" or other "Host…" trademarks, that may cause a likelihood of confusion in the German market. That is a point for the trademark search, not for this decision.

## ADR-0012 Language of code and documentation

Proposal: **English** for code, comments, issues, public documentation and the default UI language. German as a fully maintained UI translation. These planning documents were written in German because they were internal. For publication they are translated into English (done on 2026-11-18).

## ADR-0013 Business model and governance

**Full draft (status Proposed):** [adr/0013-business-model-and-governance.md](adr/0013-business-model-and-governance.md). Summary: a hybrid of own use, a community project with sponsoring and paid services, without open core in v1, DCO instead of CLA.

Original options:

Options: (a) pure community project with donations/sponsoring, (b) free plus paid support and hosting offers, (c) open core with pro features (for example multi-server, reseller, SSO), (d) an internal tool of your company that you open up later. This determines the license (ADR-0002), contribution rules (CLA/DCO), roadmap priority and funding of the pentest.

## ADR-0014 Name of the CLI tool

**Proposal:** Binary, package and services are called **`hostashi`**. Short form as an optional alias (symlink): **`ashi`**.

```bash
hostashi site add example.com --type php --php 8.4
ashi site add example.com --type php --php 8.4   # same program
```

**Rationale:** One name for everything (binary, `.deb`, systemd units `hostashi.service` and `hostashi-agent.service`, config under `/etc/hostashi`) avoids confusion. The alias saves typing. No `…ctl` suffix like `clpctl`, because the tool also starts the server (`hostashi serve`) and the agent (`hostashi agent`).

**Rejected:** `hostctl` (as far as I know, a well-known tool of the same name exists for the hosts file), `hashi` (confusion with HashiCorp), `hsi` (Xilinx tool), `hostashictl` (too long).

**Open:** Whether `ashi` or `hostashi` already exists as a package or program on Debian/Ubuntu is **not checked** (no package lists were available in the container). Check against the Debian and Ubuntu package sources before the first release.

## ADR-0015 Per-site resource limits

**Full draft (status Proposed):** [adr/0015-per-site-resource-limits.md](adr/0015-per-site-resource-limits.md). Summary: disk space per site through file system quota (preferably project quota on ext4/XFS), CPU/RAM/processes through one systemd slice per site with its own PHP-FPM master, `pm=ondemand` as the default, databases only with a connection limit and a size warning (MariaDB has no size quota), Nginx rate limits optional, traffic allowances later. Disk quota should be raised from P2 to P1. All numbers and mechanisms are unverified, spike S8 (quota on the real server, RAM measurement) clarifies this.

## ADR-0016 Per-site storage location (storage pools)

**Full draft (status Proposed):** [adr/0016-per-site-storage-location.md](adr/0016-per-site-storage-location.md). Summary: the storage location can be chosen through named **storage pools** (for example a second volume), never through a free path from the API. The agent checks mount point, marker file and quota per pool, so that an unmounted volume does not fill the root disk. The site's home directory lives directly in the pool, without symlinks. Hostashi does not format or partition disks. Moving between pools is a later, separate operation. All mechanisms are unverified, spike S9 clarifies this.

## ADR-0017 CloudPanel import as migration, presets

**Full text (status Accepted on 2026-11-18):** [adr/0017-cloudpanel-import-and-presets.md](adr/0017-cloudpanel-import-and-presets.md). Summary: the basis is spike S6 with two runs on `web2`, result 0 % clean and 90 % adoptable. Kim decided that a site counts as adopted if there is no blocker and every custom rule is reviewed once. The importer is a **migration tool**: it reads the old server read-only, and the new server pulls the data. The recurring rules are **not taken over 1:1**, but redesigned as our own typed presets. For each pattern it is assessed whether it is taken over, improved or dropped; for example, security headers are rebuilt and `if` redirects become typed redirects. Kim's requirements from 2026-11-18: PageSpeed is dropped, Varnish stays and comes together with the importer (release 1.0), presets are active per site type (WordPress, TYPO3, generic PHP, static and so on). Decided (Kim, 2026-11-18): the start happens on a fresh server, the old CloudPanel server is switched off after the migration. Custom snippets are taken over unchanged after a one-time review (Kim, 2026-11-18). HTTP/3 and compression are on by default, Kim prefers Brotli; the nginx.org default build has no Brotli, however, so gzip comes first until a spike (S11, proposal). Kim confirmed the default matrix per site type (2026-11-18). Spike S11 (2026-11-18): there is no Brotli module for the nginx.org builds, the distribution modules only fit Debian's and Ubuntu's own nginx. Recommendation: gzip as the default, Brotli later ([report](../spike/s11-brotli/REPORT.md)). Kim decides.

## ADR-0018 Public REST API

**Full text (status Accepted on 2026-11-18):** [adr/0018-public-rest-api.md](adr/0018-public-rest-api.md). Summary: REST with OpenAPI 3.1 as the contract, **API-first**: every UI feature goes through a documented endpoint, UI and CLI are clients of the same API. Path `/api/v1/`, only compatible changes from 1.0 on. Resources through fixed IDs instead of domains. Writes change the desired state, long operations run as a job with SSE. API tokens with expiry date and scopes (`resource:action`, optionally limited to individual sites); every request checks scope, role and ownership. The API runs in the web process without root and only uses typed agent operations. Named conflict: custom Nginx snippets are free text and therefore need their own high-risk scope. Example in the draft: switching maintenance and parking pages through the API. Decided (Kim, 2026-11-18): the API runs under the panel's address, with an optional IP allowlist for token access. Decided (Kim, 2026-11-18): tokens are valid for at most 365 days by default, but unlimited tokens are easy to create. Named conflict: a token without expiry is a permanent secret; mitigated by hash storage, scopes, IP allowlist, audit log, revocation with one click and a "last used" display.

---

# Open questions for you

1. **What is the project for?** Own needs (hosting for your own clients), a public open source project, a commercial product or a mix? This decides license, business model and pace.
2. **How large is the team, and which languages does it know?** This determines whether Go makes sense or another stack fits better.
3. **License:** Done, see ADR-0002 (AGPL-3.0, CC-BY-4.0 for documentation, DCO).
4. **Name:** Done, see ADR-0011. Only domains and trademark search are open.
5. **Scope v1:** Single-server panel for developers/agencies, or reseller/multi-server from the start?
6. **Where should the repository live?** GitHub (visibility, contributions) or your own Git server (`git.prod.maki-it.de`)? I can create it there if you want. I have not done that yet.
7. **Integrations:** Are Hetzner (Cloud, Robot, Storage Box), INWX DNS and a Lexware/Lexoffice connection mandatory for you or only nice to have? This affects the order of the provider integrations.
8. **Budget:** Is there a budget for an external pentest before 1.0 and for legal review (trademark, license)?

## ADR-0019 PHP package source and multiple PHP versions

**Full text (status Accepted):** [adr/0019-php-package-source-and-versions.md](adr/0019-php-package-source-and-versions.md). Summary: no PHP fork of our own (Kim's decision), but official packages. Because distributions ship only one version (Debian 13: 8.4, Ubuntu 24.04: 8.3), but sites need different versions, the recommendation is: the default is the distribution version, further versions come from the sury.org repository on explicit request (checked: PHP 5.6 to 8.5 for amd64 and arm64 on trixie and noble, in the test 7.4, 8.1 and 8.4 ran in parallel). Named conflicts: a third-party source with root installation, and old versions are insecure, hence an EOL warning and an allowlist instead of free package names. Decided (Kim, 2026-11-18, adjusted after spike S10): the default is PHP 8.3 to 8.5, legacy (only on explicit request) 8.2, 8.1 and 7.4, older versions are not offered. Spike S10 (2026-11-18): with a pin, the default version stays with the distribution, further versions come from sury.org (Debian 13 and Ubuntu 24.04 tested); without a pin, the default version would be replaced silently on the next update. According to the official release plan, support for 8.2 ends on 2026-12-31, so 8.2 is listed as legacy right away.

## ADR-0020 nginx package source and Brotli

**Full text (status Accepted on 2026-11-18):** [adr/0020-nginx-package-source-and-brotli.md](adr/0020-nginx-package-source-and-brotli.md). Summary: Kim wants Brotli and HTTP/3 as the default. The nginx.org repository has no Brotli module, the distributions' Brotli packages only fit the respective distribution's nginx (spike S11). Checked in the package lists and build rules: Debian 13 (nginx 1.26.3) and Ubuntu 26.04 (nginx 1.28.3) have HTTP/3 built in and matching Brotli modules, Ubuntu 24.04 (nginx 1.24) has no HTTP/3. Decided (Kim, 2026-11-18): nginx from the distribution with Brotli modules on Debian 13 and Ubuntu 26.04, Ubuntu 24.04 is dropped for v1, different nginx versions are fine. This replaces the package source from ADR-0005 (nginx.org). Spike S12 (2026-11-18) confirmed real requests over HTTP/2 and HTTP/3 with Brotli, gzip and precompressed `.br` files on both distributions in fresh containers. The installer run on Ubuntu 26.04 is still pending.

## ADR-0021 Brute-force and intrusion protection

**Full text (status Accepted on 2026-11-18):** [adr/0021](adr/0021-brute-force-and-intrusion-protection.md). Summary: the panel already throttles logins itself (checked in the code), but protects neither SSH nor the sites and blocks nothing at the network level. Recommendation: fail2ban from the distribution is on by default in v1, with the jails `sshd`, `hostashi-panel` and `recidive`, bans through nftables, loopback and the admin allowlist are never banned. Per-site jails (for example the WordPress login) exist, but are off by default. CrowdSec comes later at the earliest, as an opt-in, because by default it reports attacker IPs to an external service. Consequences: the panel writes failed logins as a structured JSON line with the IP to the journal (so far only to the audit log in SQLite, checked), nginx gets per-site logs in a directory the site cannot write to (so far one global log, checked), and the later firewall reconciler must not delete fail2ban's nftables table. Named conflict: `fail2ban-client` needs a second `os/exec` exception with fixed arguments. Decided (Kim, 2026-11-18): fail2ban is on by default, the installer does not ask. The site logs live under `/var/log/nginx/sites/<site>/` and are owned by root (Kim, 2026-11-18). The agent controls fail2ban through `fail2ban-client` as the second `os/exec` exception with fixed arguments (Kim, 2026-11-18), a ban engine of our own in Hostashi is rejected. No open questions left.

## ADR-0022 Metrics, logs and tracing

**Full text (status Accepted on 2026-11-18):** [adr/0022](adr/0022-metrics-and-tracing.md). Summary: metrics in Prometheus format under `/metrics` with `client_golang` (currently v1.25.0, checked on 2026-11-18). OpenTelemetry for metrics is rejected (more dependencies, the same result when scraped). The endpoint is off by default and reachable only in two ways: with an API token with its own scope `metrics:read`, or through a separate listener on loopback only, not through a "comes from localhost" check, because a local proxy would defeat that. Measured are HTTP requests (by route, not by path), agent operations, login failures (without user and IP), certificate expiry, and per site PHP-FPM and disk space. The `site` label is the system name, domains are never a label (cardinality and information disclosure). Per-site data is delivered by the agent through a new read-only operation `metrics.snapshot`. Tracing does not come in v1; instead a request ID that the panel generates and passes on to the agent. Checked: the agent rejects unknown fields in the protocol, so panel and agent must get the new field together. Decided (Kim, 2026-11-18): the endpoint is off by default. Access without a token goes through a separate listener on loopback only (Kim, 2026-11-18). The `site` label is the site's system name (Kim, 2026-11-18). No open questions left.

## ADR-0023 HTTPS for the panel

**Full text (status Accepted on 2026-11-18):** [adr/0023](adr/0023-panel-https.md). Summary: nginx terminates TLS for the panel's address on its own port 8443 and proxies to the panel on `127.0.0.1:8080`. The certificate comes the same way as for sites, so the key stays with root, and HTTP/3 and HSTS come from the same templates. The panel stays on loopback. Consequence: the panel takes the client IP from the header nginx sets, but only for requests from loopback. If nginx is down, only the SSH tunnel is left. Without a DNS name, the panel stays on the SSH tunnel. Decided (Kim, 2026-11-18): option A on port 8443, HSTS on for the panel by default. No open questions.

## ADR-0024 MariaDB databases per site

**Full text (status Proposed):** [adr/0024](adr/0024-mariadb-databases-per-site.md). Summary: the agent talks to the distribution's MariaDB itself, over the Unix socket as root (`unix_socket` authentication, no administrator password), with `go-sql-driver/mysql` v1.10.2. Typed operations `db.create`, `db.delete`, `db.list`, `db.user.create`, `db.user.delete`, `db.user.password`, no SQL interface. Checked in a Debian 13 container: MariaDB takes no placeholder in `CREATE USER`, so names and passwords have strict rules, and `_` in `GRANT` is a wildcard that must be escaped, otherwise a user also reaches a similar database of another site. Each user gets all privileges on exactly one database, only from localhost. The agent manages only what it created itself. Decided (Kim, 2026-11-18): names are free to choose without a site prefix, the agent keeps its own list. Sites get no database automatically. The panel stores no database password, it shows a generated one once (Kim, 2026-11-19). Scopes `databases:read`, `databases:write` and the high-risk `databases:delete`. No open questions.
