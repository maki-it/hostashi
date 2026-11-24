# ADR-0071 Panel UI in English and German

**Status:** Proposed. **Date drafted:** 2026-11-25. **Decides:** Kim. **Related:** ADR-0003 (stack: React, Vite, TypeScript), ADR-0012 (language of code and documentation), ADR-0018 (API), ADR-0035 (users and roles), ADR-0037 (UI redesign), ADR-0043 (user documentation), ADR-0044 (outgoing email), ADR-0058 (OpenAPI and CLI), [03-features](../03-features.md) row "Multilingual UI (start: EN, DE)" (P1, in 1.0 scope, note "Weblate or similar").

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- 1.0 contains a multilingual UI, starting with English and German [V: `03-features.md` 3.0].
- The UI has 119 `.tsx` files with about 29,600 lines; all text is English and written inline in JSX, there is no translation layer [V: `web/src`, 2026-11-25]. Tests find elements by their English text [V: Testing Library in `web/package.json`].
- Numbers and dates are formatted in several ways: `toLocaleString("en")`, `"en-US"`, `"en-GB"` and the browser default [V: `web/src/limits.ts`, `system.ts`, `cron.ts`, `SSH.tsx`].
- API errors carry a stable `code` and an English `message`; about 140 distinct codes exist [V: `internal/serve/auth_api.go`, count of `writeError` codes].
- Mails are plain English text from six templates in one file (`internal/mail/templates.go`) [V]. The wiki, the CLI and the OpenAPI document are English [V: ADR-0043, ADR-0058].
- Users already have a profile with settings (email, security, notifications) where a language setting can live [V: `web/src/Account.tsx`, `Notifications.tsx`].
- Candidate libraries, versions and licenses checked on npm on 2026-11-25 [V]:

| Library | Version | License | Runtime dependencies |
|---------|---------|---------|----------------------|
| `i18next` + `react-i18next` | 27.2.7 + 18.0.3 | MIT | `html-parse-stringify` (MIT), which needs `void-elements` (MIT) |
| `@lingui/core` + `@lingui/react` (+ `@lingui/cli`, macro plugin) | 6.12.0 | MIT | Lingui's own packages |
| `react-intl` (FormatJS) | 12.2.1 | BSD-3-Clause | `@formatjs/intl`, `intl-messageformat`, `@formatjs/icu-messageformat-parser` |

All licenses are on the allowlist (MIT, BSD, ISC, Apache-2.0, MPL-2.0, 0BSD) [V: project rules].

## Options

**1. Library**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. `i18next` + `react-i18next`, JSON catalogs per language, typed keys through i18next's TypeScript resource types** | Most widely used React solution [U]; no build plugin; JSON is supported by Weblate and most tools [U]; plurals through `Intl.PluralRules` | Larger bundle than Lingui [U]; key-based catalogs need discipline for naming |
| B. Lingui with macros and PO catalogs | Small runtime, source text as key, PO files fit Weblate well [U] | Needs a macro plugin in the Vite build (one more build piece to keep current) |
| C. `react-intl` with ICU messages | Close to the `Intl` standard, strong plural and select rules | More verbose in JSX; extraction through a separate CLI |
| D. Own small `t()` with a typed TypeScript dictionary | No dependency, compile error for a missing key | Own code for plurals, interpolation and extraction; against "boring tech" |

**2. What gets translated**

| Option | Pro | Contra |
|--------|-----|--------|
| A. The UI only, API errors shown with their English message | Smallest | German screens with English errors |
| **B. The UI, API errors translated in the UI by their `code` (English `message` as fallback), and the mails in the recipient's language** | Every screen and mail a user sees is in their language; the API, the CLI and logs stay English and stable | Mail templates and error codes have to be kept in two languages |
| C. B plus the wiki | Complete | The wiki is large; two versions drift [A] |

## Recommendation

1A and 2B.

- Languages `en` (default and fallback) and `de`. Each user has a language setting (`auto`, `en`, `de`); `auto` follows the browser (`navigator.languages`). The login and setup pages, before an account is known, follow the browser too. Stored in the users table and shown on the account page.
- Catalogs as JSON files in `web/src/locales/<lang>/<area>.json`, loaded with the app bundle (two languages are small enough [E: well below 200 KB uncompressed]). Keys are grouped by page (`sites.create.title`). TypeScript types from the English catalog, so a wrong key fails `tsc`.
- A CI check (`npm test`) fails when a key exists in one language but not the other, and a second test reports JSX text outside `t()` (the web build has no ESLint today [V], so this stays a small test instead of a new linter).
- Formatting through one module with `Intl.NumberFormat`, `Intl.DateTimeFormat` and `Intl.RelativeTimeFormat` in the user's language; the existing fixed locales are replaced. Sizes stay in binary units (KiB, MiB).
- API errors: the UI looks up `errors.<code>`, falls back to the server's English `message`. The server stays English; no `Accept-Language` handling in the API.
- Mails: the six templates get a German version; the recipient's language decides, `auto` means English, because there is no browser at send time. Subjects and footers are translated too; the format stays plain text.
- Not translated in 1.0: wiki, CLI, OpenAPI document, logs, audit log entries (their kinds are codes; the UI shows labels for them), the database tool (Adminer has its own languages [U]).
- The German text uses "du" or neutral wording (see question 3); terms follow one glossary in `web/src/locales/README.md` (for example "Site", "Storage-Pool", "Datenbank").
- The translation is done in the repository by Kim and Claude; Weblate or a similar service is possible later because of the JSON format, without a decision now.
- Migration: one PR for the setup (library, language setting, formatting module, CI checks), then one PR per area (sites, databases, backups, settings, …) that moves the texts and adds the German catalog, with a screenshot pair per area. Tests keep using English by setting the language in the test setup.

## Consequences

- New runtime dependencies: `i18next`, `react-i18next` and `html-parse-stringify` with `void-elements`, all MIT; kept at their newest versions like the other web dependencies.
- Every new UI text needs a key and a German translation in the same PR; the CI check enforces it.
- The bundle grows by the library and the German catalog [E: tens of KB gzipped].
- Screenshots in the wiki stay English (`npm run wiki-shots` sets `en`).
- Effort [E]: setup 1 PR, about 10 to 12 area PRs, mail templates 1 PR. The area PRs are mechanical but long; each needs a visual check in both languages.

## Open questions for Kim

1. Library: `i18next` + `react-i18next` (1A), Lingui (1B), `react-intl` (1C) or an own `t()` (1D)? Recommendation: 1A, the most common and needs no build plugin.
2. Scope: UI plus API errors by code plus mails in the recipient's language (2B), or the UI only (2A)? Recommendation: 2B; mails are six short templates and customers read them.
3. German form of address: "du" or "Sie"? Recommendation: "du" with neutral wording where possible ("Speichern", "Domain eingeben"); it is shorter and matches how the project talks, but "Sie" is the safer choice if most users are business customers.
