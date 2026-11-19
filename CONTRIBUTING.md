# Contributing to Hostashi

Thanks for your interest. Hostashi is moving from **Phase 0 (planning)** to the alpha: the first product code is the Go skeleton described below. The license ([ADR-0002](docs/adr/0002-license.md)) and the stack ([ADR-0003](docs/adr/0003-language-and-stack.md)) are decided. Until the first alpha, contributions are mostly discussion, review of the planning documents, and research. Please ask in an issue before you start on code.

## Code layout

One Go module at the repository root, one binary with three modes (see [`docs/04-architecture.md`](docs/04-architecture.md)):

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
- `internal/hosting`: the panel side of sites. The database (`sites`, `site_domains`) is the desired state; every change ends with `web.apply` carrying the complete state of all active sites, so a failed or interrupted change is repaired by the next one (or `POST /api/v1/web/sync`). A create whose virtual host cannot go live removes the account again, a failed domain change restores the old domains, and a delete that stops halfway leaves the site in state `deleting` and can be repeated. Routes: `GET/POST /api/v1/sites`, `GET/DELETE /api/v1/sites/{name}`, `PUT /api/v1/sites/{name}/domains`, `PUT /api/v1/sites/{name}/php`, `PUT /api/v1/sites/{name}/tls` (or `"tls": true` and `"hsts": true` on create), `PUT /api/v1/sites/{name}/hsts`, `PUT /api/v1/sites/{name}/disabled` (a disabled site answers 503 for everything except ACME challenges, and its PHP pool stops; account, files, databases and certificate renewal stay), `GET /api/v1/php/versions`, `GET /api/v1/panel/tls` (the panel certificate, ADR-0023) (scopes `sites:read`, `sites:write`, `sites:delete`). Maintenance and parking pages (ADR-0027): `GET/POST /api/v1/status-pages`, `GET/PUT/DELETE /api/v1/status-pages/{id}` (multipart fields `name`, `kind`, `file`; scopes `pages:read`, `pages:write`) and `PUT /api/v1/sites/{name}/maintenance` (scope `sites.maintenance:write`). `internal/statuspage` checks an upload (one HTML file or a ZIP, counted while unpacking) and holds the built-in pages; `web.apply` sends a page to the agent first when the agent lacks that version. Parked domains: `GET /api/v1/parked-domains`, `GET/PUT/DELETE /api/v1/parked-domains/{domain}` (`{"page": "builtin-parking", "tls": true}`; scopes `sites:read` and `sites.maintenance:write`). A parked domain answers every path with a parking page and 200, gets its certificate under `_park-<n>` and is dropped when a site takes the domain.
- `internal/web`: the nginx reconciler (spike S2). Every change renders a new generation below `/var/lib/hostashi-agent/web`, runs `nginx -t`, switches the `current` symlink, reloads and only counts as live when nginx's control socket reports the new generation eight times in a row and every site's health location (`/.hostashi/health`, only through that socket, never the application) answers with the site's name; otherwise the previous generation is restored. Templates are typed (no free-text snippets yet) and must work on nginx 1.26. Its tests start a private nginx and skip without one (`apt install nginx`).
- `internal/webctl`: the second place allowed to start processes: `nginx -t` and `systemctl reload|restart nginx.service` with fixed arguments.
- `internal/php`: one PHP-FPM master per site (ADR-0015, ADR-0019) as the unit `hostashi-php-<version>@<site>.service` (template in `packaging/systemd`). It renders the pool file below `/var/lib/hostashi-agent/php`, checks it with `php-fpm -t`, starts, reloads or switches the unit, and restores the previous state if the new one does not start. The pool file does not depend on the version. `web.apply` starts new pools before nginx and stops removed ones after. Its real-`php-fpm` test skips without one (`apt install php-fpm`).
- `internal/phpctl`: the third place allowed to start processes: `php-fpm<version> -t` and `systemctl enable|reload-or-restart|disable` of the PHP units, with fixed arguments and checked unit names.
- `internal/pages`: maintenance and parking pages on the agent side (ADR-0027). `page.begin`, `page.write` (chunks of at most 40 KiB) and `page.commit` (SHA-256 of all files, `agentapi.PageDigest`) build a page in `/var/lib/hostashi-pages/.upload-<id>` with `openat2` and no symlinks; the commit checks names, types and sizes again and renames it to the read-only directory `<id>.<digest>`. `web.apply` names a page by id and digest; nginx serves its files under `/.hostashi/page/<id>/` with a strict Content-Security-Policy and answers addresses outside the site's allowlist (`geo`) with the page and 503. ACME challenges and the health check are not affected. A page that the live configuration uses cannot be deleted; old versions are pruned after every commit and apply.
- `internal/certs`: site TLS keys on the agent side. `tls.csr` creates an ECDSA P-256 key below `/var/lib/hostashi-agent/tls/<site>` (root, 0600) and returns only a CSR; `tls.install` accepts a chain only if it matches that pending key, is valid now and names valid hosts. The private key never leaves root. `web.apply` serves a site over HTTPS only while its certificate covers all its domains, with HTTP/3 (QUIC on UDP 443) when nginx has `http_v3_module` (checked with `nginx -V` at agent start) and optional HSTS per site.
- `internal/acme`: the panel's ACME client (lego as a library, ADR-0003), HTTP-01 only. Challenge files go to `/var/lib/hostashi-acme/.well-known/acme-challenge` (panel writes, nginx reads through the setgid group `www-data`). The account key is sealed in the settings table, one per directory URL. `hosting.RunTLS` obtains and renews certificates in the background (30 days before expiry, a failed attempt waits an hour). Tests never use the real Let's Encrypt: `container-test.sh` runs Pebble when `PEBBLE_DIR` is set, as CI does.
- Panel HTTPS (ADR-0023): `serve` derives the panel name from `-public-url` (a host name, not localhost or an IP) and `hosting` gets its certificate under `agentapi.PanelCert` (`_panel`). The agent's `-panel-port` (default 8443) is where nginx serves it; `web.apply` carries the panel next to the sites. nginx proxies to the socket of `hostashi-panel.socket`, which `serve` takes over from systemd (`LISTEN_FDS`); only requests on that socket take the client address from `X-Real-IP`.
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

See [`docs/05-security.md`](docs/05-security.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

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
