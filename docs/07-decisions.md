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
| 0008 | Backups with restic | Accepted on 2026-11-19 ([adr/0008](adr/0008-backups-with-restic.md)) | Done |
| 0009 | Target platforms and packaging | Proposed | Partly |
| 0010 | Target audience and v1 scope | Proposed (draft: [adr/0010](adr/0010-target-audience-and-v1-scope.md)) | **Yes** |
| 0011 | Name and trademark | Accepted (subject to trademark search) | Done, trademark open |
| 0012 | Language of code and documentation | Proposed | Yes, briefly |
| 0013 | Business model and governance | Proposed (draft: [adr/0013](adr/0013-business-model-and-governance.md)) | **Yes** |
| 0014 | Name of the CLI tool | Proposed | Yes, briefly |
| 0015 | Per-site resource limits | Accepted ([adr/0015](adr/0015-per-site-resource-limits.md)) | No |
| 0016 | Per-site storage location (storage pools) | Proposed (draft: [adr/0016](adr/0016-per-site-storage-location.md)) | **Yes** |
| 0017 | CloudPanel import as migration, presets | Accepted on 2026-11-18 ([adr/0017](adr/0017-cloudpanel-import-and-presets.md)) | Done |
| 0018 | Public REST API | Accepted on 2026-11-18 ([adr/0018](adr/0018-public-rest-api.md)) | Done |
| 0019 | PHP package source and multiple PHP versions | Accepted (Kim, 2026-11-18, [adr/0019](adr/0019-php-package-source-and-versions.md)) | **Yes** |
| 0020 | nginx package source and Brotli | Accepted on 2026-11-18 ([adr/0020](adr/0020-nginx-package-source-and-brotli.md)) | Done |
| 0021 | Brute-force and intrusion protection | Accepted on 2026-11-18 ([adr/0021](adr/0021-brute-force-and-intrusion-protection.md)) | Done |
| 0022 | Metrics, logs and tracing | Accepted on 2026-11-18 ([adr/0022](adr/0022-metrics-and-tracing.md)) | Done |
| 0023 | HTTPS for the panel | Accepted on 2026-11-18 ([adr/0023](adr/0023-panel-https.md)) | Done |
| 0024 | MariaDB databases per site | Accepted ([adr/0024](adr/0024-mariadb-databases-per-site.md)) | No |
| 0025 | Database import and export | Accepted ([adr/0025](adr/0025-database-import-export.md)) | No |
| 0026 | Data transfer for the CloudPanel migration | Accepted ([adr/0026](adr/0026-cloudpanel-migration-transfer.md)) | No |
| 0027 | Maintenance and parking pages | Accepted on 2026-11-19 ([adr/0027](adr/0027-maintenance-and-parking-pages.md)) | Done |
| 0028 | Cron jobs per site | Accepted on 2026-11-19 ([adr/0028](adr/0028-cron-jobs-per-site.md)) | No |
| 0029 | Custom nginx snippets per site | Accepted on 2026-11-19 ([adr/0029](adr/0029-custom-nginx-snippets.md)) | No |
| 0030 | Loading imported dumps with definers | Accepted on 2026-11-19 ([adr/0030](adr/0030-loading-dumps-with-definers.md)) | No |
| 0031 | DNS cut-over per site in the CloudPanel migration | Accepted on 2026-11-19 ([adr/0031](adr/0031-dns-cut-over-per-site.md)) | No |
| 0032 | SSH and SFTP access per site | Accepted on 2026-11-20 ([adr/0032](adr/0032-ssh-and-sftp-per-site.md)) | **Yes** |
| 0033 | System hardening baseline before v1 | Proposed ([adr/0033](adr/0033-system-hardening-baseline.md)) | No |
| 0034 | Firewall rules in the panel | Accepted on 2026-11-20 ([adr/0034](adr/0034-firewall-rules-in-panel.md)) | Done |
| 0035 | Users and roles | Accepted on 2026-11-20 ([adr/0035](adr/0035-users-and-roles.md)) | Done |
| 0036 | Installing PHP versions from the panel | Accepted on 2026-11-20 ([adr/0036](adr/0036-php-versions-from-the-panel.md)) | In progress |
| 0037 | Panel UI redesign: layout, look and themes | Accepted ([adr/0037](adr/0037-panel-ui-redesign.md)) | No |
| 0038 | Web database tool with short-lived access | Accepted on 2026-11-20 ([adr/0038](adr/0038-web-database-tool.md)) | No |
| 0039 | Certificate management: own certificates, renew and delete | Accepted on 2026-11-20 ([adr/0039](adr/0039-certificate-management.md)) | No |
| 0040 | File manager in the panel | Accepted on 2026-11-20 ([adr/0040](adr/0040-file-manager.md)) | No |
| 0041 | External database servers | Accepted on 2026-11-20 ([adr/0041](adr/0041-external-database-servers.md)) | No |
| 0042 | Panel on port 443 under its own host name | Accepted on 2026-11-20 ([adr/0042](adr/0042-panel-on-port-443.md)) | No |
| 0043 | User documentation in the GitHub wiki | Accepted on 2026-11-20 ([adr/0043](adr/0043-user-documentation.md)) | No |
| 0044 | Outgoing email through an SMTP relay | Accepted on 2026-11-21 ([adr/0044](adr/0044-outgoing-email.md)) | No |
| 0045 | Object cache (Valkey) per site | Accepted on 2026-11-21 ([adr/0045](adr/0045-object-cache-per-site.md)) | No |
| 0046 | Page cache (nginx FastCGI cache) per site | Accepted on 2026-11-21 ([adr/0046](adr/0046-page-cache-per-site.md)) | No |
| 0047 | Status page for disabled sites | Accepted on 2026-11-21 ([adr/0047](adr/0047-status-page-for-disabled-sites.md)) | No |
| 0048 | Convert a parked domain into a site and back | Accepted on 2026-11-21 ([adr/0048](adr/0048-convert-parked-domain-and-site.md)) | No |
| 0049 | PHP settings per site | Accepted on 2026-11-21 ([adr/0049](adr/0049-php-settings-per-site.md)) | No |
| 0050 | Signed releases, install and updates through apt | Accepted on 2026-11-21 ([adr/0050](adr/0050-signed-releases-and-apt-updates.md)) | No |

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

**Load check (2026-11-20):** SQLite holds a mixed saturation load at 1000 sites without errors and with writes under 70 ms; the store now uses one write connection and a query-only read pool. See [sqlite-load.md](sqlite-load.md).

## ADR-0007 Isolation model per site

Linux user per site, FPM pool over a Unix socket, systemd sandbox, cgroup limits. Containers (Podman/Docker) as an **optional** site type later. Rationale: the same isolation strength as established panels, but without a container runtime as a mandatory dependency and with little RAM.

## ADR-0008 Backups with restic

**Accepted (Kim, 2026-11-19):** [adr/0008-backups-with-restic.md](adr/0008-backups-with-restic.md). Summary: a sandboxed systemd unit runs restic for the agent, one repository per server on the Storage Box over SFTP, files, database dumps and the panel per run.

Original entry: Restic as the engine (encryption, deduplication, many backends, also through Rclone). Databases through consistent dumps (MariaDB: `mariadb-dump --single-transaction` or `mariabackup`, PostgreSQL: `pg_dump`). Restore test as a feature. The alternative Borg is good too, but has fewer backends.

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

**Full text (status Accepted, 2026-11-19):** [adr/0015-per-site-resource-limits.md](adr/0015-per-site-resource-limits.md). Summary: disk space per site through file system quota (preferably project quota on ext4/XFS), CPU/RAM/processes through one systemd slice per site with its own PHP-FPM master, `pm=ondemand` as the default, databases only with a connection limit and a size warning (MariaDB has no size quota), Nginx rate limits optional, traffic allowances later. Disk quota should be raised from P2 to P1. Spike S8 verified project quota on a fresh Hetzner Debian 13 image (one reboot) and measured about 7 to 10 MB per idle FPM master. Presets are editable data, CPU uses priority modes (weights) without a hard limit by default.

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

**Full text (status Accepted, 2026-11-19):** [adr/0024](adr/0024-mariadb-databases-per-site.md). Summary: the agent talks to the distribution's MariaDB itself, over the Unix socket as root (`unix_socket` authentication, no administrator password), with `go-sql-driver/mysql` v1.10.2. Typed operations `db.create`, `db.delete`, `db.list`, `db.user.create`, `db.user.delete`, `db.user.password`, no SQL interface. Checked in a Debian 13 container: MariaDB takes no placeholder in `CREATE USER`, so names and passwords have strict rules, and `_` in `GRANT` is a wildcard that must be escaped, otherwise a user also reaches a similar database of another site. Each user gets all privileges on exactly one database, only from localhost. The agent manages only what it created itself. Decided (Kim, 2026-11-18): names are free to choose without a site prefix, the agent keeps its own list. Sites get no database automatically. The panel stores no database password, it shows a generated one once (Kim, 2026-11-19). Scopes `databases:read`, `databases:write` and the high-risk `databases:delete`. No open questions.

## ADR-0025 Database import and export

**Full text (status Accepted, 2026-11-19):** [adr/0025](adr/0025-database-import-export.md). Summary: import and export use the distribution's `mariadb` and `mariadb-dump` through a fourth `os/exec` exception. Checked in a Debian 13 container: the client runs `system` lines from a dump as a shell command, `--sandbox` stops that, and a temporary user with rights on the target database only cannot reach other databases or create users. So: temporary user per run, client as the site's Linux user, `--sandbox`, options through a pipe, dump files only inside the site home. Long runs are agent jobs, started by the CLI or the API (`/api/v1/databases/{db}/import`, `/export`, `/jobs/{id}`); the jobs live in the agent, without a panel queue. Decided (Kim, 2026-11-19): the fourth exception is acceptable with this hardening.

## ADR-0026 Data transfer for the CloudPanel migration

**Full text (status Accepted, 2026-11-19):** [adr/0026](adr/0026-cloudpanel-migration-transfer.md). Summary: the new server pulls files and databases from the old CloudPanel server over SSH (Kim's choice, 2026-11-19); the scan runs on the old server as `hostashi cloudpanel scan`. Files come over SFTP in Go (`github.com/pkg/sftp`), the old side locks the key in `authorized_keys` to `restrict`, a read-only `sftp-server -R` and one port forward to MariaDB. Databases are dumped on the new server with `mariadb-dump` through that tunnel, with a read-only dump user on the old server, and go straight into the ADR-0025 import. One job per site, which drops to the site user before writing files. No new `os/exec` exception. Decided (Kim, 2026-11-19): the locked root key on the old server is acceptable.

## ADR-0027 Maintenance and parking pages

**Full text (status Accepted, 2026-11-19):** [adr/0027](adr/0027-maintenance-and-parking-pages.md). Summary: a page is a single HTML file or a small ZIP (HTML plus CSS, JavaScript, images, fonts; at most 2 MiB) that the panel checks in Go (names, types, sizes, no links), stores in its database and sends to the agent in chunks; the agent unpacks it with `openat2` and swaps it in atomically. nginx serves the files under `/.hostashi/page/<id>/` with a strict Content-Security-Policy (nothing from other hosts; the page's own scripts run, a named conflict), instead of sanitizing. Maintenance per site: 503 with `Retry-After` and the chosen page, up to 20 allowed IPs/CIDRs see the site, PHP keeps running; "disabled" (PR #93) stays separate and wins. Parked domains get their own small vhost with a parking page (200), with HTTPS. Pages and maintenance first, parking second. Decided (Kim, 2026-11-19): pages may have their own files (option B), uploaded as a ZIP or as a single HTML file, their JavaScript may run, and parked domains get HTTPS. No open questions.

## ADR-0028 Cron jobs per site

**Full text (status Accepted, 2026-11-19):** [adr/0028](adr/0028-cron-jobs-per-site.md). Summary: one systemd timer and service per job, rendered by the agent from the panel's job list; the service runs a root-owned script as the site user, in the site's slice and sandbox, with a timeout and no overlapping runs. Schedules pass a validator for the five cron fields and the usual keywords (no `@reboot`) and are translated into `OnCalendar=`. Pause, run now and the last run's status come from systemd and the journal. Decided (Kim, 2026-11-19): option A, and imported CloudPanel jobs start paused.

## ADR-0029 Custom nginx snippets per site

**Full text (status Accepted, 2026-11-19):** [adr/0029](adr/0029-custom-nginx-snippets.md). Summary: free nginx text in a server block is a path from the panel to root (the nginx master opens log paths as root, verified with `access_log`) and across sites (`fastcgi_pass` to another site's socket, `proxy_pass` to the panel socket, `root`/`alias` into other homes). Recommendation: an allowlist of directives with typed argument checks, checked in the panel and again in the agent, then the usual `nginx -t` candidate generation with rollback. Two slots per site (server block and inside `location /`), not rendered while the site is disabled or in maintenance, own high-risk API scope. The importer turns the scanner's custom directives into a draft that a person reviews once (ADR-0017). Decided (Kim, 2026-11-19): option A, and `proxy_pass` may point at loopback with an explicit port. Open: whether the first list is enough for web2 (dry run against the scan).

## ADR-0030 Loading imported dumps with definers

**Full text (status Accepted, 2026-11-19, by Kim):** [adr/0030](adr/0030-loading-dumps-with-definers.md). Summary: views, triggers and routines in CloudPanel dumps name a definer of the old server, which the hardened import of ADR-0025 cannot create. Recommended: a locked owner account per loaded database (`ALL` on that database only, random password nobody keeps) that becomes the definer, while the agent strips the old `DEFINER` clauses as it streams the dump; after the import job the panel loads each dump on its own. Kim accepted the owner account on 2026-11-19.

## ADR-0031 DNS cut-over per site in the CloudPanel migration

**Full text (status Accepted, 2026-11-19, by Kim):** [adr/0031](adr/0031-dns-cut-over-per-site.md). Summary: imported sites have no certificate until their domains point to the new server. Recommended: Kim changes the records at the DNS provider; the panel shows the records to set and the TTL it sees, watches every domain once a minute for up to 48 hours, and turns on HTTPS as soon as all answers name only this server. No DNS credential in Hostashi; a provider API (INWX) can follow with DNS-01. Writes on the old site during the switch: per site, accept the short gap or stop writes there by hand before the final sync. Decided by Kim (2026-11-19): the records are changed by hand and the panel watches.

## ADR-0032 SSH and SFTP access per site

**Full text (status Accepted, 2026-11-20):** [adr/0032](adr/0032-ssh-and-sftp-per-site.md). Summary: the distribution's OpenSSH, site users in the group `hostashi-sites`, one static `Match Group` block in `/etc/ssh/sshd_config.d/` (keys only, no forwarding), and keys in a root-owned file per site that the agent writes from the panel's list, so a site user cannot add keys behind the panel. Each key is SFTP only (`command="internal-sftp"`) or shell; the login shell switches between `nologin` and `bash` with `usermod` in the existing `sysuser` exception. No chroot in v1. Sessions fall under the site's limits through `user-<uid>.slice` (ADR-0015). Checked with OpenSSH 9.6: the `Match` block in the drop-in leaves root untouched, a key in the home is refused, an SFTP-only key refuses commands, passwords are refused. Decided (Kim, 2026-11-20): shell and SFTP chosen per key, keys only, one login per site; pool directories become `0711` and `/proc` gets `hidepid=invisible` so site users no longer see other sites' names and processes; no chroot in v1. Built in PRs 148 to 157.

## ADR-0033 System hardening baseline before v1

**Full text (status Proposed, 2026-11-20):** [adr/0033](adr/0033-system-hardening-baseline.md). Summary: an audit of what is already hardened (privilege split, panel unit with an exposure limit, sandboxed job and cron units, PHP `open_basedir`, `0711` pools and `hidepid`, keys-only SSH for sites, fail2ban, strict panel headers, secret file modes, MariaDB on loopback, govulncheck and gosec in CI) and what is missing. Gaps: no firewall, no automatic security updates, per-site PHP masters keep an old binary after a PHP update, no sysctl baseline, no `disable_functions`, weaker sandbox for the PHP master and the agent, core dumps on, the admin's SSH left with password logins, MariaDB `local_infile`, and no two-site isolation test. Recommended: one baseline built into the installer and the agent and checked by `hostashi doctor`, in eight small PRs, firewall first (own nftables table, never `flush ruleset`, lockout check against the current SSH port, no new `os/exec` exception). Decided by Kim on 2026-11-20: firewall on by default with `--no-firewall`; security updates only, no automatic reboot; `disable_functions` on for new sites with a switch per site, imports start with the switch on; doctor only warns about the admin's SSH; panel firewall rules in v1 (ADR-0034).

## ADR-0034 Firewall rules in the panel

**Full text (status Accepted, 2026-11-20, by Kim):** [adr/0034](adr/0034-firewall-rules-in-panel.md). Summary: the panel manages opened ports (TCP or UDP, a port or a range, optionally limited to addresses) and allowlists for SSH and the panel port; the web ports, loopback, ICMP, DHCP and fail2ban's table stay fixed. Recommended: the agent renders `/var/lib/hostashi-agent/firewall/panel.nft` from typed values, the installer's base file includes it, and the agent reloads `hostashi-firewall.service` through the existing `systemctl` exception, so there is no new `os/exec` exception. Every change is pending and rolls back after 120 seconds unless the browser confirms; the panel refuses an allowlist that leaves out the address of the request that sets it. Decided by Kim on 2026-11-20: rollback after 120 seconds; v1 manages opened ports (optionally limited to addresses) and allowlists for SSH and the panel port only.

## ADR-0035 Users and roles

**Full text (status Accepted, 2026-11-20, by Kim):** [adr/0035](adr/0035-users-and-roles.md). Summary: today the panel has exactly one admin, the role column allows only `admin`, and every handler checks it through one of 16 local copies of the same `admin` check, while the API IP allowlist checks no role at all (checked in the code). Recommendation: three fixed roles, `admin` (everything), `manager` (all sites, no server settings, no users) and `user` (only the sites granted to them, day-to-day work there, but no domains, TLS, limits, snippets, disabling or deleting). Sites without a grant are invisible (`404`). One package `internal/authz` with a closed table "action to roles", called by every handler, and a route matrix test that fails for any route without an entry. A token never grants more than its owner, read on every request; tokens can optionally be limited to sites (ADR-0018). Admins invite users through a one-time link, a second factor is required for `admin` and `manager`, the last admin cannot be demoted or removed, `hostashi user recover` as root for a lost last admin, and the audit log keeps the actor's name. Decided (Kim, 2026-11-20): the three fixed roles; a second factor is required for `admin` and `manager`, optional for `user`; a `user` may not change domains, TLS, limits or snippets, nor disable or delete the site. New users come in through a one-time invitation link (24 hours), not a password the admin sets. No open questions left.

## ADR-0036 Installing PHP versions from the panel

**Full text (status Accepted, 2026-11-20, by Kim):** [adr/0036](adr/0036-php-versions-from-the-panel.md). Summary: the panel installs an allowlisted PHP version by starting a fixed root unit `hostashi-php-install@<version>.service` through the existing `phpctl` exception; the unit runs a root-owned script that does what the installer step `phpversions` does (pinned key fingerprints, pin first, fixed packages). No new `os/exec` exception. Admin only, high-risk scope `php:install`, audit log. Newer versions that sury.org offers above the allowlist are shown greyed out; they come with a Hostashi release. End-of-life versions keep running, need a confirmation to install or choose, are marked in the site list and warned about by doctor; Hostashi never switches or removes a version by itself. Removing unused versions comes after v1.

## ADR-0037 Panel UI redesign: layout, look and themes

**Full text (status Accepted, 2026-11-20):** [adr/0037](adr/0037-panel-ui-redesign.md). Summary: the current panel is one narrow column with 12 links in a top row and one long card per site. After a clickable mockup with three looks, Kim chose a new layout and, on 2026-11-20, the Maki look (B) with Phosphor icons. Recommended: a fixed sidebar with grouped navigation, list pages as tables with one primary action, and a page per site with tabs that are part of the address (`#/sites/<name>/php`); hash routing kept. Own small component set on CSS tokens (no UI library, fits the strict CSP), Phosphor icons (MIT) and Sora, IBM Plex Sans and IBM Plex Mono self-hosted (OFL-1.1, added to the npm allowlist for fonts). Light, dark or "follow system" stored per user on the server; the theme name is stored too, so the looks A (neutral) and C (Konsole) can be added later as themes. WCAG 2.2 AA. Six steps, one PR each.

## ADR-0038 Web database tool with short-lived access

**Full text (status Accepted, 2026-11-20):** [adr/0038](adr/0038-web-database-tool.md). Summary: Kim wants to browse and edit database contents from the panel in v1. Recommended: Adminer as a pinned MySQL-only single file with a Hostashi wrapper, in its own PHP-FPM pool under its own system user, served by nginx on its own port 8444 so that a bug in Adminer cannot reach the panel's origin. Each opening creates a temporary MariaDB user for one database (30 minutes or 2 hours, optionally read-only), handed over through a one-time ticket on the server, so no password reaches the browser and Hostashi still stores none. No new `os/exec` exception. Open: whether the role `user` may write.

## ADR-0039 Certificate management: own certificates, renew and delete

**Full text (status Accepted, 2026-11-20):** [adr/0039](adr/0039-certificate-management.md). Summary: Kim wants to upload own certificates, renew Let's Encrypt certificates by hand and delete certificates. Recommended: per site the source `letsencrypt` or `custom`; custom either uploaded (certificate, chain, key) or issued for a key and CSR that the agent creates, so the key can stay with root. The agent checks key match, chain, validity and that every domain is covered before anything goes live. "Renew now" for Let's Encrypt with a rate limit. Delete only when HTTPS is off or another certificate takes over. Custom certificates are never renewed automatically; warnings at 30 and 7 days. Decided by Kim: both ways, and own certificates also for parked domains and the panel address in v1.

## ADR-0040 File manager in the panel

**Full text (status Accepted, 2026-11-20):** [adr/0040](adr/0040-file-manager.md). Summary: Kim wants a file manager per site in v1, with bulk operations and ZIP download of a selection. Recommended: an own file manager in the panel; file operations run in a per-site helper unit `hostashi-files@<site>` as the site user, in the site's slice, sandboxed like a pool, without network; every path resolved with `openat2` beneath the home without following symlinks (spike S4 pattern). The panel talks to the helper over a per-instance Unix socket. v1: list, upload in chunks, download, ZIP of a selection streamed, create, rename, move, copy, delete, mode changes without setuid/setgid, text editor up to 1 MiB, ZIP extraction with limits. Scopes `files:read` and `files:write`; audit log for writes. Decided by Kim: option A, with a code editor (CodeMirror 6) that guards unsaved changes, no recycle bin.

## ADR-0041 External database servers

**Full text (status Accepted, 2026-11-20):** [adr/0041](adr/0041-external-database-servers.md). Summary: Kim wants external database servers next to localhost, a default server preselected when creating a database, and the version visible in the list, and accepts storing the admin password encrypted with the server key (exception to ADR-0024 for server credentials only). Recommended: the panel process talks to external servers (the agent stays without network), MariaDB and MySQL 8.0+ over verified TLS, database users bound to this server's address, backups, import/export and Adminer work for external databases too. Managing servers is admin-only. Decided by Kim: MariaDB and MySQL 8.0+, the panel process as the client, unencrypted connections allowed with a warning.

## ADR-0042 Panel on port 443 under its own host name

**Full text (status Accepted, 2026-11-20):** [adr/0042](adr/0042-panel-on-port-443.md). Summary: Kim wants the panel under a host name like `https://cp.maki-it.de/` without a port. Recommended and decided in the thread: installer option `--panel-port 443`, 8443 stays the default; on 443 the panel shares the sites' listeners by host name, the name is reserved against sites, and the IP allowlist is enforced by nginx instead of the firewall. `--panel-domain` replaces `--public-url`.

## ADR-0043 User documentation in the GitHub wiki

**Full text (status Accepted, 2026-11-20):** [adr/0043](adr/0043-user-documentation.md). Summary: Kim wants proper end-user documentation in the repository's GitHub wiki. Recommended: write the pages in the main repository under `docs/wiki/` so they get PR review, markdownlint, link check and REUSE, and mirror them to the wiki with a small `git`-only workflow after each merge. Task-oriented pages for server admins, panel admins, managers and users, from install to recovery. Screenshots only after the UI redesign, taken with the Playwright e2e setup. Decided by Kim: mirrored from the repository.

## ADR-0044 Outgoing email through an SMTP relay

**Full text (status Accepted, 2026-11-21, by Kim):** [adr/0044](adr/0044-outgoing-email.md). Summary: Kim wants Hostashi to send notifications and invitation links by email. This is not a mail server, which stays out of v1: Hostashi only hands messages to an external SMTP relay. Recommended: the panel process sends (the agent stays without network), with `github.com/wneessen/go-mail` v0.8.2 (MIT); STARTTLS or implicit TLS with a verified certificate, the password sealed with the server key; off by default. Optional, confirmed email addresses per user; account emails always on, operations events (backup failures, certificate renewal and expiry, doctor digest, PHP end of life, disk and quota) opt-in per user and limited to what the role can see. An outbox with sealed bodies and retries for 24 hours, rate limits per recipient and server, and no links, tokens or passwords in logs. No new `os/exec` exception. Decided by Kim on 2026-11-21: `go-mail`; no self-service password reset by email for now; the event set as proposed, other channels after v1; encrypted connections only; addresses are used only after confirmation.

## ADR-0045 Object cache (Valkey) per site

**Full text (status Accepted, 2026-11-21):** [adr/0045](adr/0045-object-cache-per-site.md). Summary: sites on web2 use Redis, so v1 gets an opt-in object cache per site. Recommended: Valkey from the distribution (BSD-3; Debian 13 main 8.1, Ubuntu 26.04 universe 9.0), one instance per site as the site user in the site's slice, Unix socket only, cache only without persistence, `maxmemory` with LRU, admin commands disabled. Typed agent operations, no new `os/exec` (one more unit pattern for the existing `systemctl` call). The CloudPanel import detects Redis on web2 and asks per site; contents are not migrated. Decided by Kim: one instance per site as recommended; `user` may switch it on and off and flush it, only admin and manager change the size. web2 uses Redis only as a WordPress object cache, as far as Kim knows.

## ADR-0046 Page cache (nginx FastCGI cache) per site

**Full text (status Accepted, 2026-11-21, by Kim):** [adr/0046](adr/0046-page-cache-per-site.md). Summary: after comparing Varnish and the nginx FastCGI cache, Kim asked for a draft of the FastCGI cache instead of Varnish. Recommended: an opt-in page cache per site, off by default, with a zone and a directory per site outside the site home (owned by `www-data`, tagged with the site's project ID so it counts against the disk quota), a fixed cache key, upstream cache headers honoured, and fixed skip rules for non-GET requests, `Authorization`, logged-in and cart cookies and admin paths. Presets for WordPress, WooCommerce and generic PHP, cache times from 1 to 60 minutes, validated excluded paths, an `X-Cache` header. Purge is a typed agent operation in pure Go, no new `os/exec`. Roles as in ADR-0045. The CloudPanel import pre-selects the cache for sites with a cache tier. Changes in part ADR-0017: decided by Kim on 2026-11-21, the FastCGI cache replaces Varnish for now, and both caches (ADR-0045 and this one) ship in v1.

## ADR-0047 Status page for disabled sites

**Full text (status Accepted, 2026-11-21, by Kim):** [adr/0047](adr/0047-status-page-for-disabled-sites.md). Summary: a disabled site answers with a fixed English 503 string today. Recommended: disabling a site takes an optional maintenance page (ADR-0027), default the built-in one (Kim, 2026-11-21: reuse the maintenance pages, no kind of its own). nginx serves it like a maintenance page (503, same CSP, no `Retry-After`, no allowlist), PHP stays stopped. No new agent operation, no new `os/exec`. No open questions.

## ADR-0048 Convert a parked domain into a site and back

**Full text (status Accepted, 2026-11-21, by Kim):** [adr/0048](adr/0048-convert-parked-domain-and-site.md). Summary: Kim wants to turn a parked domain into a site and back. Recommended: "Make a site" on a parked domain opens the New site form prefilled (API unchanged), and a failed create now restores the parking; "Delete and park domains" on a site deletes it like delete (typed name, last backup shown) and parks the domains checked in its dialog (all checked by default; Kim, 2026-11-21), with the vhost swap in one `web.apply` so the domains are always answered. Scopes `sites:delete` and `parked:write`. No new agent operation, no new `os/exec`.

## ADR-0049 PHP settings per site

**Full text (status Accepted, 2026-11-21, by Kim):** [adr/0049](adr/0049-php-settings-per-site.md). Summary: Kim wants more PHP ini settings per site, from an allowlist only. Recommended: a closed, typed allowlist (memory, upload and post size, execution and input time, input vars, file uploads, time zone, error display and reporting presets, session lifetime, short tags) rendered as `php_value` defaults that the site's `.user.ini` and `ini_set()` may still change; the hard limits stay in the slice and in nginx, whose `client_max_body_size` and `fastcgi_read_timeout` now follow the PHP values (today the site vhost has nginx's 1 MB default). Security-relevant settings stay fixed `php_admin_*` values. No new `os/exec`. Decided by Kim on 2026-11-21: defaults (`php_value`), the twelve settings as listed, and `user` may change all of them for its own site.

## ADR-0050 Signed releases, install and updates through apt

**Full text (status Accepted, 2026-11-21, by Kim):** [adr/0050](adr/0050-signed-releases-and-apt-updates.md). Summary: Kim wants signed GitHub releases and install and update through apt. Recommended: GoReleaser builds a `hostashi` `.deb` per architecture with SBOM, cosign signatures and attestations; a static apt repository on the GitHub Pages site of this repo under `hostashi.com/apt`, signed by one expiring key held only in a `release` environment behind Kim's approval and renewed by a workflow (no manual `gpg`); the install script installs through apt and runs `hostashi setup`; `postinst` re-runs the installer's configure steps on upgrade after a database snapshot; `hostashi-rollback`; automatic Hostashi updates opt-in; one `stable` channel. No new `os/exec` exception in panel or agent.
