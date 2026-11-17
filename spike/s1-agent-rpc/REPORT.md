# Spike S1: Agent RPC

**Status:** throwaway prototype, not product code. Nothing here is a decision; any follow-up goes through an ADR.

**Question (from [docs/06-roadmap.md](../../docs/06-roadmap.md)):** Is a typed RPC over a Unix socket with peer credentials enough, and does the code stay manageable? Abort criterion: more than 30 % boilerplate per operation means codegen.

Evidence tags as in [docs/adr/README.md](../../docs/adr/README.md): **[V]** verified by test or code, **[A]** assumption, **[E]** estimate.

## What was built

- `agentrpc/protocol.go`: 4-byte length prefix plus JSON body, frame limit 64 KiB, length checked before allocation.
- `agentrpc/server.go`: Unix socket listener (mode 0660, optional group), `SO_PEERCRED` per connection, an `Authorizer` callback, dispatch by operation name to typed handlers. One generic `Register[Req, Resp]` function does decode (unknown fields rejected), validation, and error mapping.
- `agentrpc/client.go`: generic `Call[Req, Resp]`.
- `agentrpc/validate.go`: positive validation for domains (lower-case ASCII/punycode, at least two labels, no numeric TLD) and site user names (fixed regex plus reserved names).
- `agentrpc/ops.go`: three operations (`Ping`, `SiteCreate`, `SiteDelete`). They only compute a plan and touch nothing on disk.
- `cmd/s1client`: tiny client used by the cross-user test.

Only the Go standard library is used. No `Exec`-style operation exists.

## Results

| Check | Result |
|-------|--------|
| Round trip, several calls per connection | [V] `TestRoundTripAndPeerUID` |
| Kernel-reported peer uid reaches the handler | [V] same test, and `TestCrossUser` with a real process as uid 65534 |
| Socket mode 0660 blocks other users at `connect()` | [V] `TestCrossUser` step 1 (EACCES), step 2 works after group change |
| Authorizer rejects a peer the socket mode lets in | [V] `TestCrossUser` step 3, `TestUnauthorizedPeerGetsNoReply` |
| Nil authorizer denies everyone (fail closed) | [V] `TestNilAuthorizerDeniesAll` |
| Unknown operation, unknown or wrongly typed field, trailing data, non-JSON | [V] stable error codes, `TestErrors`, `TestMalformedInput` |
| Oversized frame header closes the connection, agent survives | [V] `TestMalformedInput`, `TestFrameLimits` |
| Internal error text is not sent to the client | [V] `TestErrors` (`Boom`) |
| Handler panic does not kill agent or connection | [V] `TestErrors` (`Panic`) |
| Domain and user validation, including path, newline, NUL and shell characters | [V] `validate_test.go` |

`TestCrossUser` needs root and skips otherwise. It passed in a root container with `go test -race`.

## Boilerplate per operation

Counted on `SiteDelete` (smallest real operation, 10 code lines):

- Schema (request and response fields): 2 lines. Codegen would need these too, just in a different file.
- Validation and logic: 2 lines.
- Plumbing that is not schema or logic: 1 `Register` line, 1 handler signature, struct braces. About 3 lines, roughly 30 %.
- For `SiteCreate` (about 35 lines) the plumbing share drops below 10 %, because validation dominates.

[V] Thanks to generics there is **no per-operation code** in server or client. The shared plumbing is about 180 lines (server) plus 50 (client) and 55 (framing).

**Verdict:** the abort criterion is not met for operations of realistic size. Only trivial operations reach about 30 %, and codegen would save at most two lines each. Recommendation: **no codegen, keep hand-written typed structs with generics.** [E] Revisit if the operation count passes about 50.

## Findings worth carrying into the design

1. **Authorize by uid, not gid.** [V] `SO_PEERCRED` only reports the peer's primary gid, not supplementary groups. Group `hostashi` should control who may `connect()` (socket mode), and the agent should then accept only the uid of the `serve` user (and root for the CLI).
2. **Op name and types are not tied together on the client.** [V] `Call[Req, Resp](c, "SiteDelete", …)` compiles with mismatched types; the tests use that on purpose. Fix for the real code: a shared typed descriptor such as `var SiteDelete = Op[SiteDeleteReq, SiteDeleteResp]{Name: "SiteDelete"}` used by both sides. Cost about one line per operation.
3. **chmod after bind leaves a short window.** [A] Harmless if the socket directory is 0750 and owned by root:hostashi, for example via systemd `RuntimeDirectory=` with `RuntimeDirectoryMode=0750`. Not tested here.
4. **PID from `SO_PEERCRED` is informational only.** [A] It can be reused, so it is fine for logs but must not be used for decisions.
5. **No connection limit yet.** [V] A local user in group `hostashi` could open many idle connections until the 30 s idle timeout. Add a semaphore in real code.
6. **JSON over gob or gRPC.** [A] JSON keeps the wire format debuggable and needs no dependency. Throughput is irrelevant for a handful of calls per minute.

## Not covered by this spike

- Starting the helper process as the site user for file operations. That is part of S4 (isolation), see ADR-0003.
- systemd hardening of the agent and serve units.
- Long-running jobs and streaming progress (the real agent will need job IDs instead of blocking calls).
- Fuzzing the framing and validation. [A] Worth a `go test -fuzz` target before Phase 1.

## How to run

```sh
cd spike/s1-agent-rpc
go test -race ./...          # TestCrossUser skips without root
sudo go test -race ./...     # includes the real cross-user test
```
