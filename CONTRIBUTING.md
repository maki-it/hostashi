# Contributing to Hostashi

Thanks for your interest. Hostashi is a hosting control panel for a single Linux server. The license ([ADR-0002](docs/adr/0002-license.md)), the stack ([ADR-0003](docs/adr/0003-language-and-stack.md)) and the scope of 1.0 ([`docs/03-features.md`](docs/03-features.md), section 3.0) are decided. Bug reports are welcome; for a larger change, please open an issue first so we can agree on the approach.

## Code layout

One Go module at the repository root and one binary, `hostashi` (see [`docs/04-architecture.md`](docs/04-architecture.md)). `hostashi serve` runs the panel and the API without root, `hostashi agent` runs as root and only accepts typed operations, and every other subcommand is a client of the API or the agent.

- `cmd/hostashi`: the binary and its subcommands.
- `internal/serve`: the web process (HTTP API, sessions, the embedded UI). It refuses to run as root. The API is described in [`api/openapi.json`](api/openapi.json), which is generated from the routes (see below).
- `internal/agent`, `internal/agentrpc`: the root agent and the typed RPC to it (Unix socket, length-prefixed JSON, peer checked with `SO_PEERCRED`).
- `internal/agentapi`: the complete list of agent operations. A new operation goes here, with a request type that validates its own input. There is no generic exec operation.
- `internal/store`: SQLite with embedded, numbered migrations in `internal/store/migrations/`. Only `serve` opens it. Migrations are append-only and their numbers have no gaps: never edit a merged one, add the next number.
- `internal/hosting`: the panel side of sites. The database is the desired state; a change ends with a complete `web.apply`, so an interrupted change is repaired by the next one.
- `internal/web`, `internal/nginxconf`, `internal/php`: the reconcilers for nginx and PHP-FPM. A new configuration is checked (`nginx -t`, `php-fpm -t`) and rolled back if it does not go live.
- Only a few packages may start processes, each with fixed absolute paths and validated arguments: `sysuser`, `webctl`, `phpctl`, `resticctl`, `mariadbctl`, `pgctl`, `wpcli` and `f2bctl` (depguard exceptions in [`.golangci.yml`](.golangci.yml)). A new exception needs an ADR.
- `web/`: the React + TypeScript UI (Vite), embedded in the binary. Texts are in English and German, see below.
- `packaging/`: the `.deb`, systemd units and `install.sh` for a fresh Debian 13 or Ubuntu 26.04 server (`plan`, then `apply --yes`; idempotent). `packaging/test/` holds the installer and upgrade tests that CI runs in systemd containers.
- `docs/`: planning documents, ADRs in [`docs/adr/`](docs/adr/), security reviews, and the user documentation in [`docs/wiki/`](docs/wiki/).
- `spike/`: reports of the planning spikes; the prototype code was removed (see `spike/README.md`).

Run the checks locally:

```sh
golangci-lint fmt ./...    # gofmt and goimports, config in .golangci.yml
golangci-lint run ./...    # includes staticcheck, gosec and a ban on os/exec
go vet ./...
go test -race -tags hostashi_fasthash ./...   # cheap password hashes and pre-migrated test databases, as in CI
sudo env "PATH=$PATH" go test -race -run '^TestCrossUser$' ./internal/agentrpc/   # needs root
sudo env "PATH=$PATH" go test -race -run '^TestRoot' ./internal/sysuser/          # needs root, creates and removes real test users
```

For the UI:

```sh
cd web
npm ci
npm run typecheck && npm test   # TypeScript, unit tests, text catalog checks
npm run dev                     # development server; the component gallery is at /gallery.html
```

## Clean-room rule

CloudPanel is a **functional reference only**. Contributions must not contain anything taken from it:

- no vhost templates, configuration snippets or scripts,
- no documentation text, UI text or screenshots,
- no code from `cloudpanel-ce` or related repositories, unless its license explicitly allows it (`dploy` is MIT, `clp-wp-varnish-cache` is GPLv3; both need a maintainer decision first).

Write your own text and templates from primary sources (Nginx, PHP, MariaDB documentation and the applications themselves). Describing what a feature does is fine, copying how CloudPanel words or implements it is not. The name "CloudPanel" may only be used descriptively ("alternative to CloudPanel"). See ADR-0001.

If you are unsure whether something crosses the line, ask in an issue before you submit it.

## Security first

Design proposals must not weaken these principles without naming the conflict:

- The web process runs without root. A small agent executes only typed operations.
- No shell strings and no general exec interface.

See [`docs/05-security.md`](docs/05-security.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Decisions are made through ADRs

New or changed decisions are proposed as an ADR in [`docs/adr/`](docs/adr/) and listed in the index [`docs/07-decisions.md`](docs/07-decisions.md): context, options, recommendation, consequences. Proposals start as `Proposed`. Only the maintainer sets `Accepted`. Later changes get a new ADR number instead of rewriting an old one.

Separate what is verified (sources, tests, code) from assumptions and estimates.

## Scope

Mail server, DNS server, billing, reseller features and multi-server are non-goals for v1 ([`docs/02-vision-scope.md`](docs/02-vision-scope.md)). Proposals touching them need to say so.

## Language

- Code, comments, commit messages, issues and public documentation: **English** (ADR-0012).
- The panel and its emails are in English and German ([ADR-0071](docs/adr/0071-panel-ui-in-english-and-german.md)). A text a user reads goes through `t()`, and a new key gets its English and German text in the same pull request. The rules and the glossary are in [`web/src/locales/README.md`](web/src/locales/README.md); the tests in `web/src/locales/` fail for text written directly in the JSX and for a key missing in one language. Texts of emails and server checks live in `internal/mail` and `internal/doctor`.

## Commits and pull requests

- Commit messages and pull request titles follow [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `ci:`, `chore:`, with an optional scope (`fix(backup): …`). A breaking change gets `!` after the type and a `BREAKING CHANGE:` footer. Subject in English, imperative mood, about 72 characters.
- One logical change per pull request, with tests including error cases. Fill in the PR template. Each pull request gets a `Kind/*` label; the release notes are grouped by these labels ([`.github/release.yml`](.github/release.yml)).
- A pull request that adds or changes API routes regenerates the OpenAPI document: `go test ./internal/serve -run TestOpenAPIDocument -update`, then commit `api/openapi.json`.
- User documentation lives in [`docs/wiki/`](docs/wiki/) and is mirrored to the GitHub wiki after each merge ([ADR-0043](docs/adr/0043-user-documentation.md)). Change it there, not in the wiki. A pull request that changes what users see or do should update the matching page. Pages link each other as `Page.md`. Screenshots in `docs/wiki/images/` come from `cd web && npm run wiki-shots`, which serves the UI with the sample data in `web/e2e/wiki-fixtures.mjs`; renew them after UI changes instead of taking them by hand.
- Documentation changes must pass the CI checks (Markdown lint and link check). Go changes must pass the Go workflow (golangci-lint format and lint, vet, tests with race detector, short fuzzing, govulncheck). The Go version is pinned in `go.mod`. To run the lint locally: `npx markdownlint-cli2 "**/*.md"`.
- Do not commit secrets. `.env` files are ignored.

## License and sign-off (DCO)

- Code is licensed under **AGPL-3.0-or-later** (see [`LICENSE`](LICENSE)). Documentation and translations are licensed under **CC-BY-4.0** (see [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt)).
- There is no CLA. Instead, every commit must carry a `Signed-off-by:` line, which certifies the [Developer Certificate of Origin 1.1](https://developercertificate.org/). Use `git commit -s`.
- Do not contribute code or text you do not have the right to license under these terms.
<!-- REUSE-IgnoreStart -->
- Every new source file starts with an `SPDX-License-Identifier: AGPL-3.0-or-later` comment (for example `// SPDX-License-Identifier: AGPL-3.0-or-later` in Go and TypeScript). Files without a comment syntax (JSON, lockfiles, static assets) are listed in [`REUSE.toml`](REUSE.toml) instead, and Markdown files are CC-BY-4.0 through it. CI runs [`reuse lint`](https://reuse.software) on every pull request.
<!-- REUSE-IgnoreEnd -->

## Conduct

Be respectful and constructive. A formal Code of Conduct will be added before the repository is opened to contributions.
