# ADR-0022 Metrics, logs and tracing

**Status:** Proposed. **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0003 (stack), ADR-0004 (web/agent privilege split), ADR-0015 (per-site resource limits), ADR-0018 (public REST API and token scopes), ADR-0021 (brute-force protection), [`03-features.md`](../03-features.md) section 3.8, [`04-architektur.md`](../04-architektur.md) section 4.7.

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- `03-features.md` lists a Prometheus endpoint as P1, "authenticated, off by default" [V].
- `04-architektur.md` 4.7 plans an in-panel ring buffer for the panel's own graphs and says Prometheus or Influx must not be required [V]. This ADR does not replace that buffer. It adds an optional export for people who already run a monitoring stack.
- Kim runs an own monitoring server and wants to scrape Hostashi from there [V: Kim, project chat, 2026-11-18].
- The panel logs JSON through `log/slog` to stderr, which systemd puts into the journal [V: `cmd/hostashi/main.go`, `internal/serve`]. There is no request ID yet, neither in the panel nor in the agent [V: no match for a request ID in `internal/` and `cmd/`].
- The agent protocol is one JSON object per request with only `op` and `params`, and unknown fields are rejected [V: `internal/agentrpc/protocol.go`, `server.go`]. A request ID would be a new envelope field.
- The panel listens on loopback only until TLS exists [V: `internal/serve/serve.go`, `CheckListen`].
- API tokens use a closed scope catalog without wildcards [V: `internal/auth/scopes.go`]. A new endpoint adds its scope there in the same change [V: comment in `scopes.go`].
- The agent already reaches every site through nginx's control socket to check `/.hostashi/health` after a reload [V: `internal/web/nginx.go`]. The same path could serve a PHP-FPM status page [A].
- The current Go client for the Prometheus format is `github.com/prometheus/client_golang` v1.25.0, released 2026-10-08 [V: proxy.golang.org, 2026-11-18]. It is Apache-2.0 licensed [U], which is compatible with AGPL-3.0 [U].
- A metrics endpoint tells a reader which sites exist, how busy they are and when their certificates run out. That is reconnaissance data for an attacker [A].

## Options

### Metrics

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Prometheus text format on `/metrics` with `client_golang`** | The de-facto pull format, read by Prometheus, VictoriaMetrics, Grafana Agent/Alloy, Zabbix and others [U]. One well-known library, no extra daemon | One more dependency tree (`client_model`, `common`, `procfs`, protobuf) [U] |
| B. OpenTelemetry metrics SDK with a Prometheus exporter or OTLP push | One SDK for metrics and later traces | Clearly larger dependency tree, an API that still changes more often, and for a pull scrape the result is the same text format as option A [U]. No gain for a single-server panel |
| C. Own text output without a library | No dependency | Re-implements escaping, histograms and the exposition format, which is error-prone [A]. Not boring tech |
| D. node_exporter plus textfile collector | Kim may already run it [A] | Does not see panel internals (agent operations, logins) and needs a writable drop folder that root and the panel share [A] |

### Tracing

| Option | Pro | Contra |
|--------|-----|--------|
| **E. No tracing in v1, structured logs with a request ID through panel and agent** | Two processes on one host, so one ID in both journals already links a request to its agent call [A]. No collector needed | No timing tree per request |
| F. OpenTelemetry tracing in v1 | Spans with timings, standard tools | Needs a collector (Jaeger, Tempo) to be useful, more dependencies, and spans can leak domains and paths to a third service [A] |

## Recommendation

**Option A for metrics, option E for tracing.** OpenTelemetry tracing can come later as an opt-in, the request ID from option E then becomes the trace ID's partner in the logs.

### Endpoint and access

- **Off by default.** The admin switches it on in the panel settings, the installer does not ask. This matches `03-features.md` [V].
- **Two ways in, nothing else:**
  1. On the panel address with an API token that carries the new scope `metrics:read`. The scope is not high-risk, but a token for metrics should carry only this scope [A]. The token goes in the `Authorization: Bearer` header, which Prometheus supports through `authorization.credentials_file` [U]. The IP allowlist from ADR-0018 applies as for every token.
  2. A separate plain-HTTP listener on loopback (default `127.0.0.1:9471` [A: port not checked against the Prometheus port registry]) without a token, for a local agent such as Alloy or a Prometheus on the same host. It only serves `/metrics`.
- **Why a separate listener and not "remote address is 127.0.0.1" on the main listener.** As soon as anything on the host proxies to the panel (a local nginx, an SSH tunnel), every request looks local, and the check would quietly turn into "public without token" [A]. A listener that cannot bind anything but loopback keeps the rule visible in the config [A].
- Session cookies do not open `/metrics`. The panel UI uses the ring buffer from 4.7.
- The response is cached for 15 seconds [E], so a fast scrape interval or many scrapers cannot drive load on the agent.

### What is measured (v1)

All names start with `hostashi_`. Labels are closed sets, never free text from requests.

| Area | Example series | Labels |
|------|----------------|--------|
| Panel HTTP | `hostashi_http_requests_total`, `hostashi_http_request_duration_seconds` (histogram) | `route` (the registered mux pattern, for example `GET /api/v1/sites/{id}`, never the raw path), `code` (status class `2xx` to `5xx`) |
| Agent operations | `hostashi_agent_ops_total`, `hostashi_agent_op_duration_seconds` | `op` (from the closed op list), `result` (`ok` or the error code) |
| Logins | `hostashi_auth_failures_total` | `kind` (`password`, `totp`, `passkey`, `token`). No user name, no IP |
| Rate limits | `hostashi_auth_throttled_total` | `kind` |
| Reconcile | `hostashi_web_apply_total`, `hostashi_web_generation` | `result` |
| Certificates | `hostashi_tls_cert_not_after_timestamp_seconds` | `site`, `cert` (Hostashi's certificate ID, not the domain) |
| Per site, PHP | `hostashi_site_php_up`, `hostashi_site_php_memory_bytes` (from the site's cgroup), later `hostashi_site_php_active_processes` and `hostashi_site_php_max_children_reached_total` | `site`, `php_version` |
| Per site, disk | `hostashi_site_disk_used_bytes`, `hostashi_site_disk_quota_bytes` | `site` |
| Server | Go runtime and process metrics from `client_golang`'s default collectors | none |

- **The `site` label is the site's system name** (the Linux user from `site.create`), not a domain. Domains are not a label anywhere, because one site has many domains and they change [A]. That also keeps domains out of the endpoint, although the system name often resembles the main domain [A].
- **Cardinality budget:** about 15 series per site plus about 300 for the panel [E]. At 22 sites (web2 today) that is under 1,000 series [E], fine for any Prometheus.
- **Per-site data comes from the agent**, because the panel runs without root and cannot read cgroups, quotas or FPM status of other users [V: ADR-0004]. The agent gets one new typed, read-only operation, `metrics.snapshot`, that returns a fixed struct (no free-form text, no passthrough of exposition format). The panel turns that struct into metrics.
- **Disk usage** comes from the quota system of ADR-0015 where it is active, because a file walk over every site on every scrape is too expensive [A]. Without quota the agent walks each home at most every 15 minutes [E] and serves the cached value with a `hostashi_site_disk_measured_timestamp_seconds` gauge.
- **PHP-FPM details** (active processes, max children reached) need the FPM status page. The proposal is to expose it only through nginx's control socket, the same way as the health check, so no site can read another site's status [A: not tried]. Until a small spike confirms that, v1 ships only `up` and memory from the cgroup.

### Logs and request ID

- The panel creates a random request ID for every HTTP request, returns it as `X-Request-ID` and puts it into every log line of that request. It does **not** take an `X-Request-ID` from the client, so an attacker cannot forge entries that look connected [A].
- The agent protocol gets an optional `request_id` field in the envelope. The agent logs it with every line for that operation. The agent validates it (fixed length, hex only) and never uses it for decisions.
- Log lines keep the JSON format in the journal. Error answers of the API carry the same ID, so a user can quote it in a support request [A].

## Consequences

- New dependency `github.com/prometheus/client_golang` (current version at the time of the change, v1.25.0 today) and its tree. Check the transitive licenses in the license check that is still missing in CI.
- New scope `metrics:read` in `internal/auth/scopes.go`, documented in the OpenAPI spec.
- New agent operation `metrics.snapshot` in `internal/agentapi`, read-only.
- New envelope field `request_id` in `internal/agentrpc`. The agent rejects unknown envelope fields today [V: `DisallowUnknownFields` in `internal/agentrpc/server.go`], so a new panel against an old agent fails every call. Panel and agent ship in one package and are updated together [A], so this is acceptable, but the agent must learn the field first or in the same release.
- New setting for the metrics endpoint and the loopback listener, both off by default. The setting lives in the database, the listener address in the unit's flags [A].
- Tokens for monitoring are long-lived on purpose. With ADR-0018's default of 365 days, a scrape breaks silently after a year. The panel should warn 30 days before a token with `metrics:read` expires [E: 30 days].
- `04-architektur.md` 4.7 stays as it is. A note there should link this ADR.
- Tracing with OpenTelemetry is out of v1. If it comes later, it is opt-in, and spans must not carry domains or paths by default.

## Risks to name openly

- **Reconnaissance.** Even without domains, the endpoint shows site names, load and certificate dates. Anyone with a `metrics:read` token or local shell access sees that. Mitigation: off by default, own scope, IP allowlist, loopback-only listener [A].
- **Local users.** Every local user, including site users with SSH or a PHP process, can reach the loopback listener [A]. On a host with untrusted site users the admin should use only the token path. The settings page should say that plainly.
- **Load.** A scrape that triggers expensive work can be used to load the server. Mitigation: the 15-second cache and the cached disk walk [E].
- **Dependency growth.** `client_golang` pulls protobuf and `procfs` [U]. That is accepted as the price for the standard format.
- **Not a security review.** This ADR describes observability, not an assessment of the panel.

## Open questions

Decided (Kim, 2026-11-18): the endpoint is off by default, as `03-features.md` says.

Decided (Kim, 2026-11-18): the token-free path is a separate listener that binds only loopback, not a "comes from localhost" check on the main listener.

1. Is the site's system name fine as the `site` label, or should it be the numeric site ID, which says even less? Recommendation: system name, because dashboards without names are hard to read.
