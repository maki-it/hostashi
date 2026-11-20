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
npm run e2e               # browser test against a fresh "hostashi serve", see e2e/login.e2e.mjs
```

After `npm run build`, rebuild the Go binary to embed the new files. A
binary built without a frontend build still works and shows a short
notice instead of the interface.

## Components (redesign, ADR-0037)

The base components live in `src/ui/`: tokens (`tokens.css`, theme
"default" in light and dark), component styles (`ui.css`, only tokens, no
raw colours) and React components exported from `src/ui/index.ts`. Icons
come from Phosphor (`@phosphor-icons/react`, weight "regular"). Tokens apply
once `<html data-theme="default">` is set; `data-scheme="light|dark"`
overrides the system scheme.

`npm run dev`, then open `/gallery.html` (add `?scheme=dark` for the dark
scheme) to see every component. The gallery is not part of the build.

The build writes `third-party-licenses.txt` with the license texts of every
bundled npm package. Fonts under OFL-1.1 are allowed only for packages under
`@fontsource/` (`scripts/check-licenses.mjs`).

## Rules

- Everything is served from the panel itself. No CDN, no inline scripts:
  the page runs under a strict Content Security Policy (see
  `internal/ui/ui.go`).
- The session is an HttpOnly cookie. Frontend code never stores tokens.
- Dependencies are pinned to exact versions; Renovate proposes updates.
