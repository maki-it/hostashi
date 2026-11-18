# Hostashi web interface

React + Vite + TypeScript, as decided in ADR-0003 and checked in spike S7.
The build output goes to `internal/ui/dist/` and is embedded into the
`hostashi` binary, so a server needs no Node.js.

## Commands

```sh
npm ci --ignore-scripts   # install exactly what package-lock.json says
npm run dev               # dev server on http://localhost:5173, /api goes to 127.0.0.1:8080
npm test                  # unit tests (Vitest, jsdom)
npm run typecheck         # TypeScript, strict
npm run build             # typecheck, then write internal/ui/dist/
```

After `npm run build`, rebuild the Go binary to embed the new files. A
binary built without a frontend build still works and shows a short
notice instead of the interface.

## Rules

- Everything is served from the panel itself. No CDN, no inline scripts:
  the page runs under a strict Content Security Policy (see
  `internal/ui/ui.go`).
- The session is an HttpOnly cookie. Frontend code never stores tokens.
- Dependencies are pinned to exact versions; Renovate proposes updates.
