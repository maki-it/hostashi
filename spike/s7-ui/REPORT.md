# Spike S7: UI prototype (React vs Svelte, terminal, live graphs)

**Status:** throwaway prototype, not product code. Nothing here is a decision; any change to ADR-0003 goes through a Proposed ADR (ADR-0003 is Accepted, S7 was its last open spike condition together with S2).

**Question (from [docs/06-roadmap.md](../../docs/06-roadmap.md) S7):** Svelte vs React, terminal and live graphs, bundle size. ADR-0003 chose React + Vite + TypeScript with the rule "S7 may overturn it", and keeps HTMX + templ as a fallback if the SPA turns out too heavy.

Evidence tags as in [docs/adr/README.md](../../docs/adr/README.md): **[V]** verified by test or measurement in this spike, **[A]** assumption, **[E]** estimate, **[U]** unverified general knowledge.

**Result: neither candidate has a blocker, React stays.** Both implement the same four screens and pass the same 10 end-to-end checks (20 of 20 in total), with the SPA embedded in a Go binary [V]. Svelte is clearly smaller (first-load JS 42 KB vs 96 KB gzip), but the terminal chunk (xterm.js, 71 KB gzip, identical in both) is the largest single item and is loaded on demand. The size gap is not large enough to outweigh React's ecosystem for a maintainer without frontend experience [A, judgment, see recommendation]. The HTMX fallback was not needed and not tested.

## Environment and limits of this spike

- Sandbox container, Node 22, Go 1.24.7, Vite 7, React 19.3, Svelte 5.58, TypeScript 5, headless Chromium via Playwright [V]. **Only Chromium was tested.** No Firefox, no Safari, no phone.
- One run per measurement on loopback, so load timings are noise-level and are listed only for completeness.
- Both frontends were written by the same author (Claude) in one sitting. Which one a human maintainer would be more productive in is **not measured**; it is the deciding criterion and stays an assumption.
- The backend is synthetic: 22 fake sites (Kim's current count), generated metrics, one shell on a PTY. Nothing was created at Hetzner, no server was touched.
- The terminal runs `/bin/bash` as the server user. That is **spike only and off by default** (`-terminal`). The product must never start a shell from the web process; there it would be an agent operation that starts a helper as the site user (S1, S4).

## What was built

Directory `spike/s7-ui/`, one command (`./build.sh`) builds everything.

| Path | Content |
|------|---------|
| `server/` | Go module: `go:embed all:ui/dist` with SPA fallback, REST (`GET/POST /api/sites` with strict validation), SSE metrics stream, WebSocket-to-PTY terminal (`coder/websocket`, `creack/pty`, both pure Go), bearer token, loopback-only, security headers incl. CSP. `go test -race` covers token handling, validation, SPA fallback and the WebSocket origin check. |
| `shared/` | Framework-independent TypeScript used by both candidates: API client, SSE reader (fetch streaming), uPlot wiring, xterm.js wiring (lazy), CSS. About 210 lines. |
| `web-react/`, `web-svelte/` | The same four screens in each framework, about 200 and 185 lines of component code (tsx vs svelte). |
| `e2e/run.cjs` | Playwright run against the two built binaries. |

The screens: site table (filter, sort, quota bar), create-site form (client validation, server field errors, success), live CPU/RAM graph, terminal tab. Sharing the non-UI code was deliberate: the comparison measures component glue, runtime cost and bundle size, not different fetch code. It also means the glue work is understated for both.

## Results

| Check | Result |
|-------|--------|
| 22 sites listed, filter, sort | [V] both |
| Form: client errors, server 422 field errors shown, create adds a row | [V] both |
| Live graph from SSE (4 samples in 4.5 s at 1 Hz) | [V] both. Abort of the stream on leaving the tab is implemented but not asserted |
| Terminal: type `echo hi$((40+2))`, see `hi42` | [V] both. Resize messages and close on leaving the tab are implemented but not asserted |
| API without token returns 401; query-string token is only accepted on WebSocket handshakes | [V] Go tests and e2e |
| WebSocket with a valid token but a foreign `Origin` is refused (403) | [V] Go test |
| No console errors with the CSP below | [V] both |
| Strict `style-src 'self'` (no `'unsafe-inline'`) | [V] **breaks both**: 8 CSP violations for inline styles. xterm.js and uPlot are suspects, and my own bar widths use inline styles too; I did not attribute each one |

### Size and cost

| | React | Svelte |
|--|-------|--------|
| App JS, first load (raw / gzip) | 284.6 KB / 96.0 KB | 102.0 KB / 42.5 KB |
| xterm.js chunk, loaded on demand (raw / gzip) | 289.7 KB / 71.3 KB | same |
| App CSS (raw) | 3.6 KB | 3.6 KB |
| Bytes transferred on first load (Go serves uncompressed) | 292 KB | 109 KB |
| Go binary: empty UI placeholder 6.55 MB, with SPA | 7.13 MB (+0.58 MB) | 6.95 MB (+0.40 MB) |
| Time to 22 rows, loopback, single run | 73 ms | 56 ms |
| JS heap after the full click-through, after GC | 4.8 MB | 4.3 MB |
| npm packages pulled in for the framework | 3 (react, react-dom, scheduler) | ~20 (compiler, acorn, sourcemap helpers). Build time only, the runtime is inside the bundle counted above |
| Production build time | 1.5 s | 1.3 s |

uPlot is about 20 KB gzip and sits in the first-load chunk of both. xterm.js plus its fit addon is the cost of the terminal feature whatever the framework [V].

## Findings

1. **Both frameworks do everything the roadmap asked for with small code.** Terminal and live graph are plain DOM libraries wrapped in a mount/unmount hook (about 25 lines each). The framework barely matters for these two features, which is why the shared code was possible [V].
2. **Svelte saves about 54 KB gzip at first load and 0.2 MB of binary.** For an admin panel on an own server this is a small difference [E: about 0.4 s extra on a 1 Mbit/s link, none on a normal one]. It would matter more on a public product with cold-cache visitors, which Hostashi is not in v1 [A].
3. **The product must precompress assets.** The prototype served raw files (292 KB first load for React). Embedding `.br` or `.gz` variants next to the originals and serving by `Accept-Encoding` is the usual answer; it grows the binary by a few hundred KB [A, not built].
4. **WebSocket and SSE authentication is a design question, not a framework one.** Browsers cannot set an `Authorization` header on `WebSocket` or `EventSource`. The prototype used a token in the URL fragment and in the query string for the handshake (leaks into proxy and access logs) and `fetch` streaming for SSE. The product should use a session cookie with `SameSite` plus the `Origin` check that `coder/websocket` already does (tested), or a short-lived single-use ticket for the handshake. Needs a decision in the auth design [A].
5. **CSP needs `style-src 'unsafe-inline'` with this stack** (see results). Scripts stay strict: `default-src 'self'`, no inline script, no `eval` [V]. Whether the style exception can be removed by nonces or by fixing the culprits was not tried [U].
6. **React caught nothing that Svelte missed or the reverse, with one small difference:** `svelte-check` flagged an accessibility problem (`role="tablist"` on `<nav>`) at build time, `tsc` flagged `class` vs `className` in React. Both were fixed in seconds. Not a ranking criterion [V].
7. **Svelte 5 is the newer API (runes).** React 19 is the larger and older ecosystem. Both are stable today; the churn risk over the next years is judged higher for Svelte [A, no data].
8. **Go side was trivial:** `go:embed` plus `http.FileServerFS` and a 15-line SPA fallback worked for both builds unchanged [V]. Pure-Go `coder/websocket` and `creack/pty` keep `CGO_ENABLED=0`, so the static-binary promise and arm64 cross-compiling hold for the spike server (arm64 was not built here) [A].

## Recommendation

Keep **React + Vite + TypeScript** as in ADR-0003. Reasons:

- No measured blocker for either, so the tie-break rule from ADR-0003 applies: pick the one the maintainer can get help with more easily. Kim has no frontend experience with either; React has the larger ecosystem, the most examples, and the largest contributor pool once the code is open [A].
- The size advantage of Svelte is real but small in absolute terms for this use case, and precompression (finding 3) narrows it further.
- The heavy part (terminal) is identical in both.

When to reopen: if first-load size becomes a stated goal (public product, low-bandwidth users), or if the maintainer tries both and clearly prefers Svelte. The prototype makes a switch cheap, since the API, chart and terminal code in `shared/` is framework-free.

No ADR change is proposed. ADR-0003's acceptance note still lists S7 as not run; Kim can update that line after merging this report.

## Not covered

- Firefox, Safari, mobile layout, accessibility audit, i18n, dark mode beyond the CSS variables.
- Large lists (virtualization): only 22 to 23 rows were rendered. A few hundred rows are expected to be fine in both [E].
- Job log streaming with long output, reconnect and backoff for SSE and WebSocket, and terminal behavior behind a reverse proxy or with idle timeouts.
- Generating TypeScript types from the planned OpenAPI 3.1 spec (neutral between the two, but untested).
- Login, TOTP, sessions, CSRF. The token in the URL fragment is a stand-in.
- Real developer productivity, hot-reload experience and testing tools (Vitest, component tests).
- The HTMX + templ fallback.
- A real agent-side terminal (PTY under the site user, see S1 and S4).
- uPlot axis labels are crude (raw seconds), the chart is not styled for dark mode beyond the series colors.

## How to run

```sh
cd spike/s7-ui
./build.sh                      # npm ci, both SPAs, one Go binary each in bin/
(cd server && go test -race ./...)
NODE_PATH=$(npm root -g) node e2e/run.cjs   # Playwright, screenshots in e2e/out/
bin/s7-react -terminal          # prints a URL with a random token, loopback only
```

`e2e/run.cjs` needs `playwright` resolvable (`NODE_PATH` above) and a Chromium that Playwright can launch.
