# ADR-0037 Panel UI redesign: layout, look and themes

**Status:** Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0003 (stack: React, Vite, TypeScript), ADR-0023 (panel HTTPS and CSP), ADR-0035 (roles decide which pages a user sees), ADR-0002 (licenses, dependency allowlist).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim finds the current UI very ugly and wants a redesign before Hostashi is used in production [V: Kim, 2026-11-19].
- Today the panel is one column of at most 24 rem on small pages and a wider column on the others, with 12 links in one top row. Each site is a long card with every setting stacked below it; forms for new items sit above their lists [V: `web/src/App.tsx`, `web/src/styles.css`, screenshots under `/mnt/project-files/frontend/`].
- Styling is one hand-written stylesheet (438 lines) with a few CSS variables and a dark scheme through `prefers-color-scheme`. There is no UI library, router or icon set [V: `web/package.json`, `web/src/styles.css`].
- The panel's CSP is `style-src 'self'; font-src 'self'`: no inline `<style>` or `style=""` attributes in HTML, no external fonts [V: `internal/ui/ui.go`].
- The npm license check allows MIT, BSD, ISC, Apache-2.0, MPL-2.0 and 0BSD for bundled packages [V: `.github/workflows/licenses.yml`, PR #109].
- A clickable mockup with three looks on the same layout (A "Klar": neutral, B "Maki": Maki IT colours and fonts, C "Konsole": dark first, compact) was reviewed by Kim on 2026-11-20 [V: design canvas in the project thread]. Kim chose **new layout and new look**, and **A** for now; Kim also asked whether all three could be offered as themes in the user settings [V: Kim, thread, 2026-11-20].

## Options

### Layout

| Option | Pro | Contra |
|--------|-----|--------|
| **L1. Sidebar with grouped navigation, list pages as tables, one detail page per site with tabs** | Scales to many sites and pages; settings of a site are found in one place; common admin pattern [A] | Every page is touched; routing needs paths with parameters (`#/sites/<name>/<tab>`) |
| L2. Keep the top row and long cards, only restyle | Least work | Does not fix the long pages and the crowded navigation, which are the main problem [A] |

### Look and themes

| Option | Pro | Contra |
|--------|-----|--------|
| **T1. Theme A now; the styles use only semantic CSS variables, so B and C can be added later as themes without touching components** | One palette pair to check now; the door stays open | Themes B and C come later |
| T2. All three themes in v1, chosen per user | What Kim asked about | Six palettes to check for contrast with every new component; B ties the public product to the Maki IT brand by default [A] |
| T3. Only A, no theme mechanism | Simplest | Adding themes later means touching every component [A] |

### Building blocks

| Option | Pro | Contra |
|--------|-----|--------|
| **B1. Own small component set in `web/src/ui/` (Button, Card, Table, Badge, Tabs, Field, Notice, Sidebar) on plain CSS with tokens; icons from Lucide (`lucide-react`, ISC); font Inter self-hosted (`@fontsource-variable/inter`, OFL-1.1)** | No styling framework; works with the strict CSP; few dependencies; components are small and testable [A] | We maintain the components ourselves; OFL-1.1 must be added to the license allowlist for fonts |
| B2. A component library (for example Mantine or MUI) | Many ready components | Large dependency, own look to fight, some need inline styles or runtime CSS that collide with the CSP [U] |
| B3. Tailwind CSS | Fast styling | A build tool and a utility vocabulary for one person to learn; markup gets long; no gain over tokens for a small app [A] |

## Recommendation

**L1 + T1 + B1.**

- **Layout.**
  - A fixed sidebar of 248 px with the logo and host name, then groups: *Overview*; *Hosting* (Sites, Databases, Parked domains, Maintenance pages); *Server* (Health, PHP versions, Backups, Firewall, Limit presets); *Administration* (Users, CloudPanel import). Pages a role may not see are not shown (ADR-0035, as today). The current user and logout sit at the bottom.
  - A top bar with breadcrumbs and a search field (search comes later; the field is not shown until it works).
  - Every list page: title, short description, one primary action top right, then a table. Forms for new items open in their own view or a dialog, not above the list.
  - Each site gets its own page `#/sites/<name>` with a header (name, primary domain, state, actions) and tabs: Overview, Domains & HTTPS, PHP, Databases, Cron, SSH & SFTP, Limits, nginx, Backups. The tab is part of the address (`#/sites/<name>/php`), so links and the back button work.
  - Errors that need action (a failed certificate) show as a notice at the top of the affected page and on the overview under "Needs attention".
  - Below 700 px the sidebar becomes a menu above the content; tables scroll inside their box.
- **Routing.** Keep the hash routing, extended by a small parser for `#/sites/<name>/<tab>` with tests. No router library [A: the app has about 15 routes].
- **Theme A.** Neutral greys, one blue accent (`#1d4bb8` light, `#9db8ff` dark), Inter, radius 8 px, a light shadow on cards in the light scheme only. Status colours with a background tint, always with a word (never colour alone). Light and dark as in the mockup.
- **Tokens.** All colours, radii, spacing and fonts are CSS variables on `:root[data-theme]`. Components use only semantic names (`--surface`, `--fg`, `--accent`, `--ok`, …), never raw values. A test fails when a component stylesheet contains a hex colour.
- **Colour scheme.** Light, dark or "follow system", stored per user on the server (`users.color_scheme`, `PATCH /api/v1/me`), so it follows the user across browsers. Until the user has set it, the panel follows the system.
- **Themes B and C later.** The theme name is stored next to the colour scheme from the start (`users.theme`, only `default` allowed for now). Adding B or C is a new token file, a contrast check and a value in the allowlist. Whether and when they come is a separate decision.
- **Accessibility.** WCAG 2.2 AA contrast in both schemes (4.5:1 text, 3:1 controls and focus rings), keyboard use for every control, visible focus, real buttons and links, `aria-current` in the sidebar, tabs with the `tablist` pattern.
- **Order of work (one PR each, screenshots updated in each):**
  1. Tokens, Inter, Lucide, base components with tests; license allowlist gets OFL-1.1 for font packages.
  2. App shell: sidebar, top bar, routing with site paths; existing pages move into it unchanged.
  3. Sites table and the site page with tabs; the settings move from the long card into the tabs.
  4. The other pages one by one (Overview, Databases, Backups, Health, PHP, Firewall, Limits, Pages, Parked, Users, Import).
  5. Colour scheme per user (API, store, settings page).
  6. Remove the old stylesheet.

## Consequences

- Every page component changes. The existing component tests stay and are adjusted; the e2e login test keeps running [A].
- Two new npm dependencies (`lucide-react`, `@fontsource-variable/inter`) and OFL-1.1 in the npm allowlist, restricted to font packages if the check allows it [A].
- One new column pair in `users` and one new API field; the API is versioned (ADR-0018), so this is an addition.
- Screenshots in `/mnt/project-files/frontend/` are replaced.
- Estimate: 6 to 10 PRs [E].

## Risks to name openly

- **Scope creep.** A redesign invites new features. Rule: a PR in this series moves and restyles; new behaviour gets its own PR.
- **Regressions in forms.** Moving settings into tabs can break validation or saving; each moved form keeps its tests [A].
- **Fonts and licenses.** Inter is under the SIL Open Font License 1.1, which allows bundling with AGPL software; REUSE needs the license text in `LICENSES/` [U: compatibility is common practice, no legal review].
- **Not a usability study.** The layout follows common patterns and Kim's review, not tests with users.

## Open questions

1. Layout L1 (sidebar, tables, site page with tabs) with theme A, components of our own, Lucide icons and Inter self-hosted?
2. Themes B and C: decide later, after v1? Recommendation: yes, the mechanism is prepared.
