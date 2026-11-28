# ADR-0079 Website and documentation with Zensical, self-hosted on Hostashi

**Status:** Proposed. **Date drafted:** 2026-11-28. **Decides:** Kim. **Related:** ADR-0043 (user documentation in the GitHub wiki, superseded in part by this ADR), ADR-0050 (releases, `get.hostashi.com`), ADR-0054 addendum 1 (static sites), ADR-0062 (Git deploy), ADR-0002 (documentation is CC-BY-4.0).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants the user documentation built with Zensical, and both the documentation and the project website `hostashi.com` hosted on a web server of the maintainer that itself runs Hostashi [V: Kim, thread, 2026-11-28].
- ADR-0043 put the user documentation in the GitHub wiki: pages are written in `docs/wiki/` (39 pages, 66 screenshots) and `wiki.yml` mirrors them one-way to the wiki. It named "a static site generator, later" as an option [V: `docs/wiki/`, `.github/workflows/wiki.yml`].
- `get.hostashi.com` (install script, signed apt repository with the suites `stable` and `runtimes`, WP-CLI mirror) is deployed to GitHub Pages by `pages.yml` [V: `.github/workflows/pages.yml`, DNS points to GitHub Pages on 2026-11-28].
- `hostashi.com` and `docs.hostashi.com` have no DNS records yet [V: lookup, 2026-11-28]. The domains are registered by Kim [V: Kim].
- Zensical 0.1.4 is the newest release (PyPI, 2026-11-27), MIT licensed, Python 3.11 or newer, status Beta [V: PyPI metadata, 2026-11-28]. It reads Markdown and builds a static site with search; its configuration file is `zensical.toml` [U].
- Hostashi has static sites (ADR-0054 addendum 1) and Git deploy with release directories, an atomic switch, rollback and a signed webhook (ADR-0062). For a public repository over HTTPS it needs no key [V: ADR-0062, `internal/gitsync`]. The repository is public [V: 2026-11-28].

## Decision 1: what stays on GitHub Pages

**`get.hostashi.com` stays on GitHub Pages, unchanged.** Installs and apt updates of every Hostashi server must not depend on one server being up, least of all one that runs Hostashi itself and is updated from that repository. GitHub Pages gives a CDN and availability that one server does not [V: Kim, thread, 2026-11-28]. Only the website and the documentation move to the maintainer's server.

## Decision 2: how the built site reaches the server

| Option | Pro | Contra |
|--------|-----|--------|
| **A. CI builds, pushes only the built HTML to a branch; a static Hostashi site deploys that branch with Git deploy and a webhook** | No write credential for the server in GitHub; no build tool on the server; atomic switch and rollback come from Hostashi; dogfoods ADR-0062 | A generated branch in the repository; the webhook is a repository setting |
| B. Git deploy of `main` with a build command (`pip install zensical`, `zensical build`) on the server | No generated branch | Python packages from PyPI are installed and run on a production server at every deploy |
| C. rsync or SFTP from CI | Simple, common | An SSH key with write access to the site lives in GitHub secrets; no atomic switch or rollback; the target host has to be stored in GitHub |

**Option A**, chosen by Kim (thread, 2026-11-28).

- **Build.** A workflow `website.yml` runs on pushes to `main` that touch the site sources, installs Zensical from a hash-pinned requirements file (Renovate keeps it on the newest version), runs `zensical build` in strict mode and checks the output (no symlinks, nothing outside the output folder).
- **Publish.** The output goes to an orphan branch `website` as one commit that replaces the previous one (force push with the job token, `contents: write` for that job only). The branch never grows a history, and each commit message names the source commit [A: go-git fetches a force-pushed branch, as `gitsync` uses `Force` and a `+` refspec, V: `internal/gitsync/gitsync.go`].
- **Deploy.** On the server, one static site whose Git deploy follows `https://github.com/maki-it/hostashi.git`, branch `website`, no build commands, deploy directory the web root. A GitHub repository webhook (push events) calls the site's deploy hook with the shared secret (`X-Hub-Signature-256`). Pushes to other branches only queue a deploy of `website`, which then finds nothing new [V: ADR-0062 webhook notes].
- **No host in the repository.** Neither the workflow nor any public file names the server. The webhook address and secret live only in the repository's webhook settings [V: Kim, thread, 2026-11-28].

## Decision 3: one site or two

**One Zensical project** that builds the start page at `https://hostashi.com/` and the documentation below `/docs/`, chosen by Kim (thread, 2026-11-28). One build, one branch, one site, one certificate, one search. `docs.hostashi.com` is not used. The start page gets its own layout through a template override [U: Zensical supports theme overrides].

**Start page** (chosen by Kim, thread, 2026-11-28): what Hostashi is and is not (v1 scope), the install command, a feature overview with screenshots from `npm run wiki-shots` (mocked sample data, no real servers or customers), who it is for, and links to the docs, GitHub and the releases. Written from Hostashi's own behaviour; no CloudPanel texts or screenshots, and no feature table against other panels (clean-room rule, and claims about other products would need checking) [A]. Screenshots go to Kim before the PR, as for UI changes.

## Decision 4: repository layout

- `website/zensical.toml`, `website/requirements.txt` (hashes), `website/overrides/` (start page template, logo, colours of the Maki IT design system where they fit).
- `website/docs/` is the Zensical source folder: `index.md` (start page) and `docs/*.md` (the user documentation). The pages of `docs/wiki/` move there with `git mv`, so their history stays; images move along.
- The navigation comes from `zensical.toml`, replacing `_Sidebar.md`; `_Footer.md` goes away.
- Planning documents and ADRs stay in `docs/` and are not published on the website.
- REUSE: everything below `website/` that is prose is CC-BY-4.0 like the rest of the documentation; configuration and templates AGPL-3.0-or-later.

## Decision 5: the GitHub wiki

The wiki sync (`wiki.yml`) is removed once the site is live. The wiki keeps one page that points to the new documentation. Links to the wiki in `install.sh`, the man page and the README change to the new address in the same PR.

## Consequences

- No new `os/exec` exception and no code change in Hostashi; this is CI, content and one site on the server.
- Docs PRs keep markdownlint, lychee and REUSE; the website build runs in PRs as a check (strict mode fails on broken internal links) without publishing.
- Screenshots keep coming from `npm run wiki-shots`; its output folder moves to `website/docs/docs/images/` [A].
- Steps with external effect, each after Kim's approval: DNS records for `hostashi.com` (and `www`), creating the site on the server and turning on its Git deploy, adding the repository webhook. Kim does DNS.
- Rough order of PRs: (1) this ADR, (2) Zensical project with the moved pages and a build check, (3) publish workflow to the `website` branch, (4) after the site is live: drop the wiki sync, change links [E: 4 PRs].

## Risks to name openly

- **Zensical is young** (0.1.x, Beta). Breaking changes in minor releases are likely [A]. Mitigation: hash-pinned version, Renovate PRs with the build check; the sources stay plain Markdown, so a switch to another generator stays cheap.
- **One server.** When the maintainer's server is down, the website and docs are down; installs and updates are not, as `get.hostashi.com` stays on GitHub Pages.
- **Self-hosting a project's site on the project.** A Hostashi bug that breaks Git deploy also stops docs updates; the live release stays served, and a rollback is one click [V: ADR-0062].
- **Force-pushed branch.** Anyone with push access can change the published site, as today with the wiki. Branch protection for `website` allows only the workflow's pushes [A].

## Open questions for Kim

None left; decided by Kim in the thread on 2026-11-28: deploy option A, one site, a detailed start page. `www.hostashi.com` redirects to `hostashi.com` with the site's www setting (ADR-0065), and `hostashi.de` with `www.hostashi.de` redirect there too, as redirect domains of the same site (ADR-0068) [A: recommendation, not asked yet].
