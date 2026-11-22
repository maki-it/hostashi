# ADR-0058 OpenAPI specification and CLI parity

**Status:** Proposed. **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0003 (stack), ADR-0004 (web/agent privilege split), ADR-0018 (public REST API), ADR-0035 (roles and route matrix), ADR-0050 (releases).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- ADR-0018 makes an OpenAPI 3.1 document the contract of `/api/v1`, says CI checks that code and document match, and leaves the Go tooling (spec-first or code-first) open [V: ADR-0018 rule 3]. `03-features.md` 3.0 lists "OpenAPI 3.1 specification" and "CLI with parity" as P0 rows without code for v1 [V].
- There is no OpenAPI file in the repository [V: v1 completeness check, 2026-11-21; `grep`].
- The panel registers about 190 routes under `/api/v1` with the Go 1.22 `http.ServeMux` patterns, through a small wrapper `routeMux` that records every pattern [V: `internal/serve/routes.go`]. `TestRouteMatrixComplete` already fails when a route has no entry in the route matrix of ADR-0035, and `TestRouteMatrix` checks each entry's guard against the running handler [V: `internal/serve/routes_test.go`].
- Request bodies are mostly anonymous structs inside the handlers (at least 52 `var in struct`), responses are a mix of named `...JSON` types and `map[string]...` [V: `internal/serve/*_api.go`]. A schema cannot be read from anonymous structs without moving them to named types first.
- The SPA calls the API through a hand-written `web/src/api.ts` (68 lines) and per-page types [V].
- The CLI `cmd/hostashi` talks to the agent socket directly and only works as root on the server. It has `site create/delete/list`, `pool`, `site limits`, `db`, `backup`, `service`, `cloudpanel`, `user recover`, `doctor` [V: `usage` in `cmd/hostashi/main.go`].
- Some of these bypass the panel: `site create` calls the agent operation `site.create` directly, while the panel's `hosting.Service.Create` also writes the site, its PHP version, headers and TLS wish to the SQLite store [V: `cmd/hostashi/admin.go`, `internal/hosting/hosting.go`]. A site made with the CLI is therefore not the same as one made in the panel [A: not tested end to end].
- Most features (cron, headers, caches, limits presets, users, tokens, mail, audit, status pages …) keep their desired state in the panel's store, which only `serve` writes [V: migrations 0001–0048]. A CLI that talks only to the agent can never reach parity without duplicating that logic [A].
- `serve` listens on `127.0.0.1:8080` by default [V: `cmd/hostashi/main.go`]. API tokens (`hst_…`) with scopes and expiry exist, and creating one needs a browser or login session [V: route matrix, `POST /api/v1/tokens` is `session`].
- Current library versions (checked 2026-11-22 on proxy.golang.org): `github.com/getkin/kin-openapi` v0.153.0 (MIT), `github.com/invopop/jsonschema` v0.14.0 (MIT), `github.com/danielgtaylor/huma/v2` v2.41.0 (MIT) [V: versions; U: licenses from memory, checked by the Licenses workflow before merge].

## Options

**How the specification is made**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Hand-written `openapi.yaml`, CI only compares paths and methods with the router | Fast to start, no code changes | Schemas drift silently; a second place to edit on every change, which ADR-0018 wants to avoid |
| B. Spec-first, generate server stubs (oapi-codegen) | One source of truth | Rewrite of about 190 handlers into generated interfaces; large risky change shortly before v1 [E] |
| C. Framework that owns routing and schemas (huma) | Spec falls out for free | Replaces `routeMux`, `authn.allow` and error handling; same rewrite cost as B |
| **D. Code-first export: each route gets a small operation record (id, summary, tag, request and response Go type) next to its `HandleFunc`; a generator builds `openapi.json` from the records, the route matrix and Go types by reflection** | Fits today's code, can be done area by area, scopes come from the existing route matrix; CI fails on a route without a record or a stale file | Anonymous request structs must become named types (mechanical); reflection needs JSON-tag conventions for optional fields |

**How the CLI reaches parity**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Hand-written subcommands for every endpoint | Nicest commands | 190 commands to write and keep in step; parity can only be checked by a list |
| B. CLI keeps talking to the agent | Works without a running panel | Can never reach parity (desired state lives in `serve`), keeps the bypass above |
| **C. The CLI is an API client and builds its commands from the embedded `openapi.json` at run time (`hostashi api sites list`, `hostashi api sites get blog`); a few hand-written shortcuts on top** | Parity by construction: every operation is a command; a test checks that every operation id resolves; same scopes, roles, audit and validation as the panel | Generic commands are less pretty; needs a token or login |

## Recommendation

**Specification: option D.**

1. `routeMux` gets `Op(pattern, op, handler)`. `op` names `ID` (camelCase, unique), `Tag`, `Summary`, and optional `In`/`Out` Go values, `Query` parameters, the success status and `Stream: true` for SSE. Path parameters come from the pattern.
2. The required scope and access kind (public, session, any, action) come from the route matrix of ADR-0035, which moves from the test file into `internal/serve` so the generator can read it. The spec shows it as `x-hostashi-action` and as the bearer security requirement.
3. A generator (`go run ./tools/openapi`) reflects the Go types into JSON Schema 2020-12 (OpenAPI 3.1) with `invopop/jsonschema` and writes `api/openapi.json`. Errors are one shared Problem Details schema (ADR-0018 rule 7) [A: today's error body is `{"error","message"}`, it is described as it is and changed separately].
4. CI: a test fails if any registered route has no operation record, if two records share an id, or if `api/openapi.json` differs from a fresh generation. Handler tests validate every response they get against the document with `kin-openapi` (router plus `openapi3filter`), so a wrong schema fails the existing tests.
5. `serve` serves the document at `GET /api/v1/openapi.json` only to a logged-in caller (access kind `any`: session or any token). The CLI uses its embedded copy and does not need it (Kim, 2026-11-22).
6. Done area by area in small PRs (sites, databases, backups …). A route without a record is allowed only through an explicit "not yet described" list that shrinks to zero before v1.

**CLI: option C.**

1. `hostashi api` reads the embedded document and turns each operation into `hostashi api <tag> <verb>`, path parameters as arguments, query parameters as flags, the body from `-d '{…}'`, `-d @file.json` or `--set key=value`. Output is the JSON from the API; `-o table` for lists later.
2. Login: `hostashi login [URL]` asks for name, password and TOTP once, creates a token with chosen scopes and expiry through the normal API and stores it in `~/.config/hostashi/credentials.json` (0600). `HOSTASHI_URL` and `HOSTASHI_TOKEN` override it for scripts. Default URL on the server is `http://127.0.0.1:8080`. No new way to log in, no root bypass of roles.
3. Parity test: every operation in the document maps to a command, except a short, documented list that cannot work in a terminal (passkey ceremonies, SSE streams get a `--follow` mode later, browser downloads become `-O file`).
4. The existing root commands stay where there is no API on purpose (`doctor`, `backup key show`, `user recover`, `cloudpanel scan`, the `*-run` helpers for systemd units, `agent`, `serve`, `setup-token`, `version`). Commands that duplicate the API and bypass the panel (`site create/delete/list`, `db …`, `site limits`, `backup get/set/…`, `service`) print a deprecation note pointing to `hostashi api`, and are removed after the CLI parity PRs [A].
5. A TypeScript client for the SPA from the same document (e.g. `openapi-typescript`) is useful, but not part of this ADR.

## Consequences

- Every new route needs an operation record and a named request type; reviews see the API shape in one line next to the handler.
- The spec and the CLI cannot drift from the code, because both are generated from it and CI checks it.
- Moving about 52 request structs to named types touches many files and may conflict with the threads that change the site API (site types) and the limits presets (ADR-0057). The work is split per area and rebased often [A].
- Two new dependencies (`kin-openapi` only in tests and the generator, `invopop/jsonschema` only in the generator), both MIT [U]. The production binary embeds the JSON file, not the libraries [A].
- The CLI becomes usable from a laptop against a remote panel, with the same roles, scopes, IP allowlist and audit log as the browser.
- No new agent operation, no new `os/exec` exception.

## Open questions for Kim

1. ~~CLI as API client with login/token instead of root access to the agent?~~ Yes, CLI as API client (Kim, 2026-11-22).
2. ~~Serve `GET /api/v1/openapi.json` without login, or only for logged-in users?~~ Only after login (Kim, 2026-11-22).
3. Remove the duplicate root commands (`site create` …) after parity, or keep them as aliases that call the API?
