# ADR-0073 MCP server for AI assistants

**Status:** Proposed. **Date drafted:** 2026-11-25. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0010 (v1 scope), ADR-0018 (REST API, tokens, scopes, IP allowlist), ADR-0035 (roles and route matrix), ADR-0058 (OpenAPI and CLI parity, device login), ADR-0059 (real client IP).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants an MCP server in Hostashi, so that an AI assistant (Claude) can talk to the panel directly (2026-11-25) [V].
- The Model Context Protocol (MCP) is an open JSON-RPC protocol between an AI client and a server that offers *tools* (functions the model can call), *resources* (data to read) and *prompts* [U]. The current protocol version is **2026-07-28** [V: modelcontextprotocol.io versioning page, checked 2026-11-25]. It defines two transports: **stdio** (the client starts the server as a child process) and **Streamable HTTP** (one HTTP endpoint, POST for requests, optional SSE) [U]. For HTTP it describes authorization as OAuth 2.1 with the server as resource server; it is optional, and a plain bearer token in the `Authorization` header also works with clients that let the user set headers [U].
- The official Go SDK is `github.com/modelcontextprotocol/go-sdk`, latest release **v1.8.0** (2026-09-04), with v1.9.0-pre.1 (2026-11-17) as a pre-release [V: proxy.golang.org]. v1.8.0 speaks protocol versions 2024-11-05 up to 2026-07-28, supports stdio and Streamable HTTP including a stateless mode, needs Go 1.25 [V: `mcp/shared.go`, `mcp/streamable.go`, `go.mod`]. Its licence is moving from MIT to Apache-2.0; both are on our allowlist [V: `LICENSE`]. Its direct dependencies are `golang-jwt/jwt/v5`, `google/jsonschema-go`, `segmentio/encoding`, `yosida95/uritemplate/v3`, `golang.org/x/oauth2`, `golang.org/x/time`, `golang.org/x/tools` [V: `go.mod`]; their licences are checked by the Licenses workflow before merge [A: all MIT/BSD/Apache, not checked one by one]. An alternative is `github.com/mark3labs/mcp-go` v1.6.0 [V: proxy.golang.org], community-maintained.
- Hostashi already has everything an MCP server needs below the protocol:
  - The public API `/api/v1` with 222 described operations; each has an operation record with id, tag, summary and Go request/response types, and `api/openapi.json` is generated from them [V: ADR-0058, `internal/openapi`].
  - API tokens (`hst_…`) with a closed scope catalog, high-risk scopes that are never granted by accident, optional site limits, expiry, last use, revoke, and an IP allowlist for tokens with loopback always allowed [V: `internal/auth/scopes.go`, ADR-0018].
  - Role checks (ADR-0035) on every route: a token never grants more than its owner's role [V: `authn.allow`, `authn.allowSite`].
  - The audit log records actor, token, IP and diff [V].
  - `hostashi login` with the device flow (RFC 8628), so a token can be issued from any browser login, including passkeys [V: ADR-0058 Addendum 1].
- The release builds Linux binaries only [V: no `darwin` in `.github/`]. A stdio server inside the `hostashi` binary would therefore not run on Kim's Mac without a new build target [A].
- An AI client is not a trusted operator. What the model reads through MCP (access logs with visitors' user agents, file contents, database names, Git commit messages, error pages) can contain text written by strangers, and the model may follow instructions hidden in it ("prompt injection") [U: well-known and unsolved class of attack]. A model can also simply misunderstand a request. Tool annotations such as `readOnlyHint` and `destructiveHint`, and confirmation dialogs, are shown by the *client*; the server cannot rely on them [U].
- v1 is not finished yet: four features, the second security review, browser tests and the release checklist are open [V: v1 completeness check, 2026-11-25]. An MCP server is not in `03-features.md` 3.0 [V].

## Options

**Transport**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Streamable HTTP endpoint in `serve`, `POST /api/v1/mcp`, bearer API token** | Same listener, TLS, IP allowlist, rate limits and audit as the API; nothing to install on the client side; works from Claude Code, cloud sessions and other clients that can send a header [U]; stateless mode needs no session store | A new public endpoint (more attack surface); clients that only accept OAuth (e.g. connectors added in a web UI) cannot use it without OAuth [U] |
| B. stdio server in the CLI, `hostashi mcp`, which calls the API with the stored login | No new endpoint on the server; reuses `hostashi login` | Runs only where the Linux CLI runs: on the server itself or a Linux laptop, not on a Mac without a new build [A]; every client machine needs the binary and a login |
| C. Both | Covers every client | Twice the surface to test; B adds little once A exists |
| D. No MCP; let assistants call the REST API with `curl` and the OpenAPI document | Nothing to build | Works only for assistants with a shell; no tool descriptions, no scoping of what the model sees; the assistant must read a 222-operation spec |

**Which tools the model gets**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Every API operation becomes a tool, generated from the operation records | Parity by construction, like the CLI | 222 tools are too many for a model to choose well [U]; it exposes deletes, restores, file writes and firewall changes to a client that can be steered by injected text |
| **B. A curated, closed list of tools, each mapped to one existing operation, marked read or write; the list a token sees is filtered by its scopes; high-risk operations are not offered at all** | Small surface that can be reviewed and tested; the model gets clear descriptions; destructive actions stay in the panel | The list must be kept by hand; some things the CLI can do are missing on purpose |
| C. Read-only tools only | Safest | The model cannot do the small everyday actions Kim may want (maintenance on, purge cache, start a backup) |

**How a tool runs**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Each tool call is dispatched in-process to the existing HTTP handler of its operation, with the caller's principal** | Every check stays where it is: scope, role, site grant, validation, audit; no second code path to get wrong | A small adapter that builds an internal request |
| B. Tools call the store and the agent client directly | Slightly less overhead | Duplicates authorization and validation; easy to forget a check |

## Recommendation

**Transport A, tools B, execution A. Built after 1.0, as part of 1.1** [A: no v1 user needs it, and v1 has a security review still open; the work is small and can start right after the release].

### Endpoint and authentication

1. `POST /api/v1/mcp` in `serve`, Streamable HTTP in **stateless** mode (no `Mcp-Session-Id`, `GET` and `DELETE` answer 405). Protocol version 2026-07-28, older versions as the SDK negotiates them [A].
2. Library: `github.com/modelcontextprotocol/go-sdk` at the newest release when the work starts (v1.8.0 today, not the pre-release). It is the official SDK and follows the spec closely [U]. Only its `mcp` package is used; no OAuth parts.
3. Authentication only with an API token (`Authorization: Bearer hst_…`). Browser sessions are refused on this route, so a page in the browser cannot be turned into an MCP client by CSRF [A]. The token IP allowlist of ADR-0018 applies.
4. A new scope **`mcp:use`**, not high-risk, which a token must carry to call the endpoint at all. Without it, a leaked CI token cannot be used through MCP [A]. The tools then still need their own scopes.
5. No OAuth in this ADR. If Kim later wants to add Hostashi as a connector in a client that only does OAuth, that is a separate ADR (the panel would become an OAuth authorization server, which is a large security surface).
6. Origin check: requests with an `Origin` header that is not the panel's own origin are refused, as the spec asks for HTTP servers [U].

### Tools

1. A closed list in `internal/mcptools`, each entry: tool name, description written for a model, the operation id it maps to, `read` or `write`, and the MCP annotations (`readOnlyHint`, `destructiveHint: false`, `idempotentHint`). Input and output schemas come from the operation's Go types through the existing reflector [A: the SDK accepts a ready JSON Schema].
2. `tools/list` returns only the tools the token's scopes allow. A tool for one site checks the site limit of the token as the route does today.
3. Proposed first list [E: about 20 tools]:
   - **Read:** sites (list, get, settings, domains, usage, limits), databases (list, sizes), backups (status, snapshots), services and dashboard figures, health/doctor results, certificate expiry, malware scan results, recent deploys, audit log (own entries for non-admins), site logs (last N lines, filtered).
   - **Write, reversible:** maintenance mode on/off, purge page cache, flush object cache, start a backup, start a Git deploy with the stored commands, restart one PHP-FPM pool of a site.
4. **Never offered through MCP**, whatever the scopes: every `HighRisk` scope of the catalog (delete site or database, restore, files write, cron, Git settings, SSH keys, firewall, services, system, installers, external DB servers, snippets), plus users, grants, tokens and other account security. A test fails if a tool maps to an operation whose scope is high-risk or session-only.
5. Write tools return what they changed and a link to the panel page. A future write tool that cannot be undone needs an addendum to this ADR first.
6. No MCP *resources* and *prompts* at first; tools are enough and the clients support them best [U].

### Safety around the model

1. **Prompt injection is named, not solved.** Tool results that carry text from outside (log lines, file names, commit messages, page titles) are returned as data in a structured field, cut to a size limit, and the tool description says that the content is untrusted [A: reduces, does not remove the risk].
2. The real protection is what the token allows: Kim creates an MCP token with read scopes only, and adds the few write scopes when wanted. The panel's token dialog gets a preset "AI assistant (read-only)" that selects `mcp:use` plus all read scopes [A].
3. Every write through MCP lands in the audit log like any API call, plus `via: mcp` and the tool name, so the audit log page can filter for it.
4. Rate limit per token on the MCP route (e.g. 60 tool calls per minute) [E], so a looping model cannot flood the agent.
5. No tool returns secrets: database passwords, the restic key, token values, SMTP passwords and deploy keys are never in a tool result. The existing API does not return them either [V for passwords and token values; A for every route, to be checked by a test over the tool list].

### Docs and tests

- Wiki page "AI assistants (MCP)": what it is, how to create the token, the risk of prompt injection in plain words, example client configs (Claude Code `claude mcp add --transport http …` with a header [U]).
- Tests: tool list per scope set; a token without `mcp:use`; a session cookie is refused; a high-risk operation can never be a tool; site limits on tokens; audit entry with `via: mcp`; Origin check; rate limit; JSON-RPC errors for unknown tools and bad input; OpenAPI and route matrix entries for the new route (ADR-0058, ADR-0035).

## Consequences

- One new route, one new scope, one new dependency (the MCP Go SDK and its dependencies). No new agent operation, no new `os/exec` exception; the security boundary of ADR-0004 does not move.
- The MCP surface is a subset of the API, so anything the model can do, a script with the same token could already do. MCP adds convenience, not power [A].
- Hostashi takes on a protocol that is still changing (four versions in 2025–2026) [V]. SDK updates must be followed like other dependencies; the stateless HTTP mode keeps the part we depend on small [A].
- The curated list needs care: a new feature gets a tool only when someone adds it on purpose.
- Prompt injection stays a residual risk the person accepts by giving the token write scopes. This is said in the docs and in the token dialog.
- Effort [E]: 1 PR endpoint, scope, adapter, first read tools and tests; 1 PR write tools, audit filter and token preset; 1 PR wiki.

## Open questions for Kim

1. ~~Transport: HTTP endpoint in the panel with an API token (A), or stdio through the CLI (B)?~~ Decided (Kim, 2026-11-25): HTTP endpoint in the panel (A).
2. Tools: curated list with reversible writes and no high-risk actions (B), or read-only at first (C)?
3. Timing: after 1.0 as part of 1.1, or still in v1?
4. Do you want to add Hostashi as a connector in a client that only supports OAuth (for example in a web UI)? If yes, that becomes its own ADR later.
