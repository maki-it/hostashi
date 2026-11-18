# Contributing to Hostashi

Thanks for your interest. Hostashi is moving from **Phase 0 (planning)** to the alpha: the first product code is the Go skeleton described below. The license ([ADR-0002](docs/adr/0002-license.md)) and the stack ([ADR-0003](docs/adr/0003-language-and-stack.md)) are decided. Until the first alpha, contributions are mostly discussion, review of the planning documents, and research. Please ask in an issue before you start on code.

## Code layout

One Go module at the repository root, one binary with three modes (see [`docs/04-architektur.md`](docs/04-architektur.md)):

- `cmd/hostashi`: the `hostashi` binary (`serve`, `agent`, `ping`, `version`).
- `internal/serve`: the web process. Refuses to run as root.
- `internal/agent`: the root agent. Unix socket only, accepts only root and the serve user (by uid).
- `internal/agentrpc`: the typed RPC between them (length-prefixed JSON, `SO_PEERCRED`).
- `internal/store`: SQLite (pure Go driver, WAL) with embedded, numbered migrations in `internal/store/migrations/`. Only `serve` opens it. Migrations are append-only: never edit a merged one, add the next number.
- `internal/auth`: Argon2id passwords, server-side sessions (only the token hash is stored), login rate limits and the one-time setup token (file `setup-token` next to the database, mode 0600, valid 24 hours, shown with `hostashi setup-token`).
- API tokens (`internal/auth/tokens.go`, ADR-0018): `hst_` plus 32 random bytes, only the SHA-256 is stored and the secret is shown once. Scopes come from the closed catalog in `internal/auth/scopes.go` (no wildcards); a feature that adds an endpoint adds its scope there and wraps the handler with `scoped`. Token management, the IP allowlist and account security need a browser session, never a token.
- Passkeys (`internal/auth/passkeys.go`, library go-webauthn): discoverable credentials with user verification required, so a passkey login counts as both factors. They are bound to the host name of `serve -public-url` and are off without it. Tests use the software authenticator in `internal/auth/passkeytest`.
- `internal/secrets`: AES-256-GCM for stored secrets such as TOTP seeds. The key file (`/etc/hostashi/secret.key`, mode 0400) is created on first start and must never be committed or backed up together with the database.
- `internal/sites`: root-side site accounts. The registry (`sites.json`, in `/var/lib/hostashi-agent`, root-owned, mode 0700, deliberately not below the web process's directory) is the only source of uid, gid and storage pool. Home directories are created and removed with `openat2` on open fds, never with path strings. Deleting a site refuses homes owned by someone else and never crosses mounts or symlinks.
- `internal/sysuser`: `useradd` and `userdel`, the only place in product code that starts a process (depguard exception in `.golangci.yml`): fixed absolute paths, arguments from validated names, empty environment.
- `internal/agentapi`: the complete list of agent operations. New operations go here, each with a request type that validates its own input. There is no generic exec operation.
- `packaging/`: systemd units and `install.sh` for a fresh Debian 13 or Ubuntu 26.04 server (step model of spike S5: `plan`, then `apply --yes`; idempotent). The agent unit must not set `RestrictSUIDSGID`, which makes `openat2` fail with ENOSYS. `packaging/test/container-test.sh IMAGE BINARY` runs the installer in a systemd container; CI runs it for both distros.
- `spike/`: throwaway prototypes, separate modules, not product code.

Run the checks locally:

```sh
golangci-lint fmt ./...    # gofmt and goimports, config in .golangci.yml
golangci-lint run ./...    # includes staticcheck, gosec and a ban on os/exec
go vet ./...
go test -race ./...
sudo env "PATH=$PATH" go test -race -run '^TestCrossUser$' ./internal/agentrpc/   # needs root
sudo env "PATH=$PATH" go test -race -run '^TestRoot' ./internal/sysuser/          # needs root, creates and removes real test users
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

See [`docs/05-sicherheit.md`](docs/05-sicherheit.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Decisions are made through ADRs

New or changed decisions are proposed as an ADR in [`docs/07-decisions.md`](docs/07-decisions.md): context, options, recommendation, consequences. Proposals start as `Proposed`. Only the maintainer sets `Accepted`. Later changes get a new ADR number instead of rewriting an old one.

Separate what is verified (sources, tests, code) from assumptions and estimates.

## Scope

Mail server, DNS server, billing, reseller features and multi-server are non-goals for v1 ([`docs/02-vision-scope.md`](docs/02-vision-scope.md)). Proposals touching them need to say so.

## Language

- Code, comments, commit messages, issues and public documentation: **English** (ADR-0012).
- The internal planning documents in `docs/` are currently German and will be translated before publication.

## Commits and pull requests

- Commit messages in English, imperative mood, short subject (about 72 characters), for example `Add threat model for agent RPC`.
- One logical change per pull request. Fill in the PR template.
- Documentation changes must pass the CI checks (Markdown lint and link check). Go changes must pass the Go workflow (golangci-lint format and lint, vet, tests with race detector, short fuzzing, govulncheck). The Go version is pinned in `go.mod`. To run the lint locally: `npx markdownlint-cli2 "**/*.md"`.
- Do not commit secrets. `.env` files are ignored.

## License and sign-off (DCO)

- Code is licensed under **AGPL-3.0-or-later** (see [`LICENSE`](LICENSE)). Documentation and translations are licensed under **CC-BY-4.0** (see [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt)).
- There is no CLA. Instead, every commit must carry a `Signed-off-by:` line, which certifies the [Developer Certificate of Origin 1.1](https://developercertificate.org/). Use `git commit -s`.
- Do not contribute code or text you do not have the right to license under these terms.

## Conduct

Be respectful and constructive. A formal Code of Conduct will be added before the repository is opened to contributions.
