## What and why

<!-- Short description of the change and the reason for it. -->

## Type

- [ ] Feature
- [ ] Bug fix
- [ ] Documentation or wiki
- [ ] ADR (new or changed decision; status stays `Proposed`)
- [ ] CI, packaging or repository tooling
- [ ] Other:

## Checklist

- [ ] Nothing was copied from CloudPanel (templates, docs text, UI text, screenshots, code). See [CONTRIBUTING.md](../CONTRIBUTING.md).
- [ ] The change does not weaken the security principles (no root web process, no shell strings, no general exec). Or the conflict is named above.
- [ ] Tests cover the change, including error cases.
- [ ] Changed API routes: `api/openapi.json` is regenerated.
- [ ] New UI texts are in English and German (`web/src/locales/`).
- [ ] The PR title follows Conventional Commits (`feat:`, `fix:`, `docs:` …) and the PR has a `Kind/*` label.
- [ ] Commit messages and public text are in English, commits are signed off (DCO).

## Related

<!-- ADRs, issues, documents -->
