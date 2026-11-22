# ADR-0061 Site type Python

**Status:** Proposed. **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0015 (resource limits), ADR-0017 (import and presets), ADR-0018 (API), ADR-0032 (SSH per site), ADR-0033 (hardening), ADR-0034 (firewall), ADR-0035 (roles), ADR-0050 (signed releases, auto-update), ADR-0053 (site logs), ADR-0054 (site types), ADR-0058 (OpenAPI and CLI).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- v1 scope (03-features, section 3.0): "Site type Python (WSGI/ASGI, venv)" is P1 and in v1. ADR-0054 left it for its own ADR [V: `docs/03-features.md`, ADR-0054].
- ADR-0054 is built: the types `static`, `php`, `node`, `app`, `proxy` and `redirect`, the type change, environment variables, the CloudPanel import of Node.js and proxy sites. Apps run in `hostashi-node@<site>` or `hostashi-app@<site>` as the site user in the site's slice, on a loopback port from 20000–29999 that nftables opens only to root, `www-data` and the site's uid; nginx proxies to it [V: `internal/appunit`, `packaging/firewall/hostashi.nft`].
- Node.js comes as official builds, pinned with SHA-256 in `internal/agentapi/node_versions.go`, installed by the fixed unit `hostashi-node-install@<major>` (Go downloads, checks and unpacks; no shell), updated by a daily workflow and installed again by the agent after a Hostashi update [V: `internal/nodeinstall`, `tools/nodeversions`, ADR-0054 notes].
- The CloudPanel scanner already detects `python` sites (a local `proxy_pass` and a `requirements.txt` or `pyproject.toml`); the import plan skips them with `type_unsupported` [V: `internal/cloudpanel/scan/scan.go`, `internal/cloudpanel/plan/build.go`]. CloudPanel offers Python 3.10–3.14 [V: `docs/01-cloudpanel-research.md`].
- Python lines on 2026-11-22: 3.15 (3.15.2, end 2031-10), 3.14 (3.14.10, end 2030-10), 3.13 (3.13.18, end 2029-10), 3.12 (3.12.16, end 2028-10), 3.11 (3.11.18, end 2027-10); 3.10 ended 2026-10 [V: `https://endoflife.date/api/python.json`]. Python has no LTS lines; every line gets five years [U: PEP 602].
- Each distribution ships one Python 3 [U: Debian 13 3.13, Ubuntu 26.04 a newer one]; `venv` needs the separate package `python3-venv` there [U].
- python.org publishes no Linux builds, only sources [U]. **python-build-standalone** (Astral, MPL-2.0 build scripts) publishes relocatable CPython builds for every supported line: release `20261110` has `cpython-<version>+20261110-{x86_64,aarch64}-unknown-linux-gnu-install_only_stripped.tar.gz` for 3.11.18, 3.12.16, 3.13.18, 3.14.10 and 3.15.2, a `SHA256SUMS` file, and GitHub build provenance attestations (Sigstore) for every archive; `latest-release.json` names the newest release [V: release assets, `latest-release.json`, `.github/workflows/release.yml` uses `actions/attest-build-provenance`]. uv, the Python tool of the same vendor, downloads exactly these builds [V: `docs/running.rst`]. The builds link libedit instead of readline to avoid a GPL dependency since 2023 [V: `docs/running.rst`, Licensing]. Hostashi downloads them on the server and does not ship them, so the license allowlist for Go dependencies is not touched [A].
- Python web apps need an app server: gunicorn for WSGI (27.0.1), uvicorn for ASGI (0.54.0) [V: PyPI, 2026-11-22]. gunicorn binds `0.0.0.0:$PORT` when `PORT` is set and nothing else is given [V: `gunicorn/config.py`]; uvicorn reads `UVICORN_*` variables [V: `uvicorn/main.py`]. The host firewall drops inbound traffic to the app ports (policy drop, only the opened ports pass), but with `--no-firewall` an app on `0.0.0.0` is public [V: `hostashi.nft`].

## Options

**Where Python comes from**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Distribution package `python3` + `python3-venv` | apt and unattended-upgrades, nothing new | One version, different per distribution; a distribution upgrade changes it under the sites; CloudPanel imports with another version break |
| B. deadsnakes PPA | apt updates, several versions | Ubuntu only, third-party repository and key, no Debian |
| **C. python-build-standalone, every line that has not ended, side by side under `/opt/hostashi/python/<line>`** | Same versions on both distributions, several in parallel, the same install code and workflow as Node.js, checksums plus Sigstore provenance | One vendor (Astral) builds what runs; Hostashi updates it itself |
| D. Build from python.org sources on the server | Upstream sources | Compiler and build dependencies on every server, minutes per build, no reproducible result |
| E. uv or pyenv per site user | What developers use | Every site downloads its own toolchain, no central updates, no checks by Hostashi |

**How the app is started**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Typed forms `wsgi` (gunicorn), `asgi` (uvicorn), `script` (a `.py` file), Hostashi adds the bind address** | Covers Django, Flask, FastAPI and the rest; the app listens on `127.0.0.1:<port>` even with `--no-firewall` | gunicorn or uvicorn must be installed in the environment by the site |
| B. Program and arguments as for `app` | Nothing new | `$PORT` is not expanded, so each user has to know the port; gunicorn's default listens on all addresses |
| C. Free command line | Flexible | A shell string, against rule 1 |

**Where the virtual environment lives**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. One environment per site at `<home>/.venv`, created by Hostashi** | Always there, matches the site's version, outside `htdocs` | Recreated on a line change |
| B. The site creates it, Hostashi only runs `<workdir>/.venv/bin/python` | Nothing to build | A site without one does not start; easy to create it with the wrong Python |

## Recommendation

**Source C, start A, environment A.** Python sites are the Node.js type with another runtime; everything that is not about the runtime is shared.

**Type and account**

- New type `python`, like `node`: Linux account, home, `htdocs`, slice, quota, SSH/SFTP, cron, file manager, databases, backups, domains, HTTPS, status page, maintenance, site logs, environment variables (ADR-0054 notes). Type changes to and from `python` follow ADR-0054 addendum 3; a change between `node`, `app` and `python` keeps the port.

**Versions**

- Every Python line that has not ended (today 3.11–3.15). The list with the exact version, the python-build-standalone release tag and the SHA-256 of both builds (`x86_64`, `aarch64`, `install_only_stripped`) lives in `internal/agentapi/python_versions.go`, with the end of life.
- Installed by the fixed root unit `hostashi-python-install@<line>`, which runs `hostashi python-install <line>`: Go downloads the pinned archive, checks the SHA-256 before it opens it, unpacks it with the rules of `internal/nodeinstall` (shared code: only relative links inside the tree, modes 0755/0644, `os.Root`, swap with `renameat2`) to `/opt/hostashi/python/<line>` (root, read-only for others). No apt, no shell, nothing is run.
- Workflow "Python versions", daily like Node.js: reads `latest-release.json`, takes per line the newest build, checks it against `SHA256SUMS` **and** verifies each archive's build provenance with `gh attestation verify --repo astral-sh/python-build-standalone` (signer workflow pinned to the project's release workflow), then opens one PR; Kim merges. A new Hostashi release brings the pins, the agent installs them on start and restarts the apps of that line (as `RestartLine` for Node.js). Doctor `system.python` warns about failed updates and lines that end within 180 days.
- Patch updates do not touch the environments: `.venv` points at `/opt/hostashi/python/<line>/bin/python3`, which keeps its path [A: venv links the interpreter by path; checked in the container test].

**Virtual environment**

- Hostashi creates `<home>/.venv` with the site's line when the site gets the type `python`, through a fixed oneshot unit `hostashi-python-venv@<site>` that runs as the site user, in the site's slice, with the sandbox of the app unit: `ExecStart=/opt/hostashi/python/<line>/bin/python3 -m venv <home>/.venv` (written by the agent as a drop-in, every argument quoted by `appunit.Quote`). pip comes from the build's bundled `ensurepip`, offline.
- Changing the line rebuilds it (`venv --clear`), so installed packages have to be installed again. The dialog says so; the site name is typed to confirm, shown highlighted and copyable. "Recreate environment" exists on its own with the same dialog.
- Packages: `pip install -r requirements.txt` (or uv, Poetry …) in the site's SSH shell in v1, like `npm install` for Node.js. The shell finds the site's Python and `.venv` first in `PATH`, and `UV_PROJECT_ENVIRONMENT` points uv at the same `.venv` [A: the managed shell file is not built for Node.js yet either; U: uv honours `UV_PROJECT_ENVIRONMENT`].
- Packages without wheels need a compiler; Hostashi does not install one [A: most common packages ship manylinux wheels, which run on both distributions].

**The app**

- Unit `hostashi-python@<site>.service`, `User=<site>`, in the site's slice, the sandbox of `hostashi-node@` (one shared template, a third unit file), `Restart=on-failure`, log to `/var/log/hostashi/app/<site>/app.log` (kind `app`), port from 20000–29999, nftables filter, nginx proxy branch with an optional static folder, as for Node.js. `MemoryDenyWriteExecute=` stays off [U: cffi and ctypes callbacks can need writable executable memory].
- Environment: `HOST=127.0.0.1`, `PORT`, `HOME`, `USER`, `LOGNAME`, `PATH` with `.venv/bin` first, `VIRTUAL_ENV`, `PYTHONUNBUFFERED=1` (so the log is live), plus the site's own variables.
- Start command, one of three typed forms, no shell:
  - `wsgi`: `<home>/.venv/bin/python -m gunicorn --bind 127.0.0.1:<port> <target>`; target `module.path:callable` (`[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*`, for Django `project.wsgi:application`).
  - `asgi`: `... -m uvicorn --host 127.0.0.1 --port <port> <target>`, same target rule (FastAPI `main:app`).
  - `script`: `... python <file>` for a `.py` file below the working directory.
  - Each takes at most 16 extra arguments with the rules of the `app` type (for example `--workers 3`). For `wsgi` and `asgi` the bind options (`-b`, `--bind`, `--host`, `--port`, `--fd`, `--uds`) are refused. This catches mistakes; it is no boundary, as the site user can run anything over SSH anyway.
- Before a start, the agent checks that `.venv/bin/python` exists and, for `wsgi` and `asgi`, that gunicorn or uvicorn is installed in it (a file check below `site-packages` through `os.Root`, nothing is run). Otherwise the app stays stopped and `GET /api/v1/sites/{name}/app` says why in `problem` ("gunicorn is not installed in .venv: run pip install gunicorn"), like the program check of the `app` type.
- Panel: start, stop, restart, state and app log, as for Node.js.

**Roles, API and CLI (ADR-0018, ADR-0035, ADR-0058)**

- `POST /api/v1/sites` with `type: "python"` and `python {version, workdir, startKind, target, args}`; `PUT /api/v1/sites/{name}/python` (`sites.app:write`); `POST /api/v1/sites/{name}/python/venv` recreates the environment (`sites.app:write`, audit `site.python.venv`). Start, stop, restart, state, log and environment variables are the existing app routes.
- `GET /api/v1/python` (`sites:read`), `POST /api/v1/python/install` and `/remove` (scope `python:install`, admin only, audit `python.install`, `python.remove`; a line in use is refused, as for Node.js).
- All new routes with `mux.Op` and an OpenAPI entry; CLI `hostashi python status|install|remove` and the site flags through the API.

**CloudPanel import (ADR-0017)**

- `python` sites are imported as `python`: the newest installed pinned line (or the newest pinned one with `python_missing`), working directory `htdocs`, a new port, no start command, so the app stays stopped. A review finding says "set the start command, then install the packages in .venv over SSH" and names the old port. `requirements.txt` or `pyproject.toml` is mentioned in the finding when found; nothing is installed automatically.

## Consequences

- Store: one migration for the Python settings (line, start kind, target; arguments as in `sites.app_args`) and `sites.type` value `python`. Number from the coordinator when the PR starts.
- Agent: typed operations `python.install`, `python.status`, `python.remove`, `site.python.venv`; `site.app.apply` and `site.app.action` take the type `python`. `phpctl` gets the unit patterns `hostashi-python@<site>.service`, `hostashi-python-install@<line>.service` and `hostashi-python-venv@<site>.service`. **No new binary, no new `os/exec` exception**: Python only ever runs inside these units, started through the existing `systemctl` exception.
- Code reuse: the unpacking of `internal/nodeinstall` moves to a shared package used by both installers; the app unit template gets a third runtime.
- nginx: the Node.js proxy branch, no new template branch.
- CI: the installer test installs one line, creates a Python site, installs gunicorn from PyPI into `.venv` and gets a page through nginx, on both distributions [A: CI runners reach PyPI].
- Frontend: Python in New site and in "Change type", the start form, "Recreate environment"; screenshots to Kim before the UI PR. Wiki "Site types" gets a Python section.
- Rough order of PRs: (1) versions list and installer, (2) workflow "Python versions", (3) type, migration, venv and app unit, (4) API, CLI and OpenAPI, (5) UI after screenshots, (6) import, (7) wiki [E: 7 PRs].
- Risks: trust in one vendor's builds (mitigated by pinned checksums, Sigstore provenance checked in the workflow, and Kim's review of each pin PR); a line change deletes the installed packages (explicit dialog); Python apps with many workers use much memory, the site's slice limits apply (ADR-0015).

## Open questions for Kim

1. ~~Python from python-build-standalone (source C), every line that has not ended (today 3.11–3.15)?~~ Decided (Kim, 2026-11-22): yes, pinned by SHA-256 and checked with Sigstore.
2. Hostashi creates and owns `<home>/.venv` (environment A), and a line change rebuilds it?
