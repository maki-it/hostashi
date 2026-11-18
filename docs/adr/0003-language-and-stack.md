# ADR-0003 Language and stack

**Status:** Accepted (2026-11-18, by Kim). **Date drafted:** 2026-11-17. **Decides:** Kim. **Depends on:** open question 2 (team and skills). Independent of the license.

## Context

- Architecture requires: a rootless web process, a small root agent with typed operations over a Unix socket, no shell strings, a reconciler that renders and validates configs, one binary with three modes, idle target under 100 MB for panel plus agent, amd64 and arm64 [V: `04-architecture.md`, `02-vision-scope.md`].
- Current stack proposal: Go, embedded SPA (Svelte or React), SQLite (WAL), Nginx behind a `WebServerDriver`, restic, lego for ACME [V: `04-architecture.md` 4.9].
- Team size and skills are unknown. A well-mastered stack beats the ideal one [V: original ADR-0003]. The roadmap assumes two full-time developers, one with Linux systems experience [V: `06-roadmap.md`, itself an estimate].
- Security rules (typed inputs, no `Exec`, path confinement) must be enforceable by the language and tooling, not only by discipline [V: `05-security.md`].

## Owner input (2026-11-17)

- Q1: Hostashi is first for Kim's own servers and customers [V: Kim, thread].
- Q2: the team is Kim alone, who masters Go [V: Kim, thread]. Bus factor is 1.
- Q5: single-server v1, multi-server and reseller maybe much later [V: Kim, thread].
- Q8: no budget for an external pentest or legal review [V: Kim, thread].

- Agent file operations: the agent starts a helper process under the site user instead of dropping root inside the agent itself. To be tested in spikes S1 and S4 [V: Kim, thread].
- SQLite driver: pure Go as default, small benchmark in Phase 0 [V: Kim, thread].
- SPA framework: React with Vite and TypeScript, chosen by Claude on Kim's request, because the maintainer is alone with little frontend experience and React has the largest ecosystem. Spike S7 may overturn it [A].
- Agent code review: strict CI checks (fuzzing, static analysis), a small documented agent interface, public review by volunteers once the code is open, and no "1.0" label before an external review [V: Kim agreed in thread].

Effect: the Go condition below is met. Open risk: one maintainer reviews all security-critical agent code himself.

## Options

### Backend, agent and CLI

| Option | Pro | Contra |
|--------|-----|--------|
| **Go** | Static binary, easy arm64 cross-compile, strong stdlib for processes, files, TLS, `net/http`; memory safe; fuzzing and `govulncheck` built into the toolchain [U]; large pool of infrastructure developers | Not immune to injection or path bugs (those are logic errors). Weaker type system for modeling (no sum types). Privilege dropping inside a multithreaded process is awkward (see risks) |
| **Rust** | Strongest safety guarantees, static binary | Slower development, smaller contributor pool in the hosting scene [A], steeper onboarding |
| **PHP/Symfony** | Large pool in the hosting scene, CloudPanel is probably built this way [A: not verifiable, source is closed] | Runtime and packaging dependency on the server it manages, larger footprint, harder to sandbox the panel from the stack it hosts |
| **Python** | Many system libraries | Runtime dependency, packaging per distro, slower and heavier |
| **TypeScript/Node** (single language with the SPA) | One language for the whole team | Needs a runtime or a bundler-based single binary, weaker for syscalls and privilege handling, large dependency tree for a root-adjacent service |

**Recommendation: Go**, provided at least one core developer is productive in it or can be within a short ramp-up [E: 2 to 4 weeks for an experienced backend developer new to Go, no data]. If nobody knows Go and nobody wants to learn it, that is a real cost and the ranking changes (see decision rule).

### Frontend

- Svelte(Kit) or React with Vite and TypeScript, built to static files and embedded with `go:embed`. Decision deferred to spike S7 (bundle size, terminal and live graphs) [V: `06-roadmap.md`].
- Alternative HTMX plus templ: less JavaScript, weaker for terminal and live graphs [V: `04-architecture.md`]. Keep as fallback if S7 shows the SPA too heavy.
- Recommendation: SPA, framework by S7. Pick the one the team knows better if S7 is a tie.

### State storage

- **SQLite in WAL mode**: no extra service, simple backups (`VACUUM INTO`), fits single-server scope [V: ADR-0006].
- Driver choice matters for the static-binary promise: a pure-Go driver avoids cgo and keeps cross-compiling simple, a cgo driver is faster in some cases but complicates arm64 builds and static linking [U]. **Decide in a spike**, default to the pure-Go driver.
- PostgreSQL only if multi-server arrives, re-evaluate after 1.0 [V: ADR-0006].

### Web server

- **Nginx** behind a `WebServerDriver` interface (Render, Validate, Reload). Caddy is the alternative and spike S3 decides with measurements [V: ADR-0005]. This ADR only confirms that the driver abstraction exists, it does not decide Nginx vs Caddy.

### Backups

- **restic** as an external binary called with argv (never a shell), pinned to a tested version range. Restic is a CLI tool, not a stable Go library, so we wrap the CLI and parse its JSON output [U]. Borg is the alternative with fewer backends [V: ADR-0008].

### ACME

- **lego** as a library (Go, avoids a Python dependency from certbot) [V: `04-architecture.md` 4.9]. Check lego's license and maintenance status before relying on it [U].

## Recommendation

Confirm the proposed stack **with two conditions**:

1. Open question 2 is answered and at least one core developer can review Go code security-critically. If not, the fallback is **Go with a paid or external reviewer for the agent only**, not a switch to a language nobody masters.
2. Spikes S1 (agent RPC), S2 (reconciler), S4 (isolation) and S7 (UI) must not contradict the choice. A failed spike reopens this ADR.

Decision rule:

- Team has Go skills: accept as proposed.
- Team is strong in Rust and not in Go: Rust for the **agent only** is defensible, the rest stays Go or TypeScript. This increases the number of languages, which is a cost [A].
- Team is PHP-only: the proposal is a poor fit. A PHP panel would have to run beside the PHP versions it manages. Prefer learning Go for the agent over writing a root-adjacent service in PHP.

## Consequences

- Single static `hostashi` binary for serve, agent and CLI. `.deb` packaging with signed APT repo stays simple [V: `04-architecture.md` 4.8].
- Tests: Go tests with golden files, `go test -fuzz` for parsers, VM-based integration tests [V: `06-roadmap.md`].
- Boilerplate per agent operation (schema, validator, handler) is the main cost of the typed-RPC rule. Spike S1 decides on code generation if boilerplate exceeds 30 percent [V: `06-roadmap.md`].
- Supply chain: Go modules and npm lockfiles, `govulncheck`, license inventory in CI (see ADR-0002).

## Risks to name openly

- **Privilege drop in Go:** after `setuid` semantics for all threads were fixed in modern Go [U], dropping root inside the agent for file work is still easy to get wrong because the agent needs root afterwards. Safer pattern to test in S1 and S4: the agent **re-executes the same binary as a helper under the site user** (credentials set at process start) with `openat2`/`RESOLVE_BENEATH` for confined file operations [A, matches `04-architecture.md` 4.2 but is not yet tested].
- Go's memory safety does not stop path traversal, injection or logic errors. The typed-input rule and fuzzing carry that load, not the language.
- Dependency risk of a root-adjacent process: keep the agent's dependency set minimal and reviewed.
- Pure-Go SQLite driver maturity and performance under write load are unverified [U]. Needs a small benchmark.

## Acceptance note (2026-11-18)

Kim accepted this ADR after spikes S1 and S4 confirmed the stack:

- S1 (agent RPC): typed RPC over a Unix socket with peer credentials works, no code generation needed [V: [`spike/s1-agent-rpc/REPORT.md`](../../spike/s1-agent-rpc/REPORT.md)].
- S4 (file access as the site user): the helper-process pattern with `openat2` confinement works, including real quota in a Debian 13 VM [V: [`spike/s4-site-helper/REPORT.md`](../../spike/s4-site-helper/REPORT.md)].

Still open, and the original reopen rule stays in force:

- S2 (reconciler) and S7 (UI prototype) have not run. A failed S2 reopens this ADR. S7 may still replace React with another framework or fall back to HTMX plus templ [A].
- The SQLite benchmark (pure Go driver) is still to be done in Phase 0.
- No "1.0" label before an external review of the agent (see Owner input).

## Open questions

None at this level.
