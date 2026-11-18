# Contributing to Hostashi

Thanks for your interest. Hostashi is in **Phase 0 (planning)**: there is no product code yet. The license ([ADR-0002](docs/adr/0002-license.md)) and the stack ([ADR-0003](docs/adr/0003-language-and-stack.md)) are decided. Until the first alpha, contributions are mostly discussion, review of the planning documents, and research. Please ask in an issue before you start on code.

## Clean-room rule

CloudPanel is a **functional reference only**. Contributions must not contain anything taken from it:

- no vhost templates, configuration snippets or scripts,
- no documentation text, UI text or screenshots,
- no code from `cloudpanel-ce` or related repositories, unless its license explicitly allows it (`dploy` is MIT, `clp-wp-varnish-cache` is GPLv3; both need a maintainer decision first).

Write your own text and templates from primary sources (Nginx, PHP, MariaDB documentation and the applications themselves). Describing what a feature does is fine, copying how CloudPanel words or implements it is not. The name "CloudPanel" may only be used descriptively ("alternative to CloudPanel"). See ADR-0001.

If you are unsure whether something crosses the line, ask in an issue before you submit it.

## Security first

Design proposals must not weaken these principles without naming the conflict:

- The web process runs without root. A small agent executes only typed operations.
- No shell strings and no general exec interface.

See [`docs/05-sicherheit.md`](docs/05-sicherheit.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Decisions are made through ADRs

New or changed decisions are proposed as an ADR in [`docs/07-decisions.md`](docs/07-decisions.md): context, options, recommendation, consequences. Proposals start as `Proposed`. Only the maintainer sets `Accepted`. Later changes get a new ADR number instead of rewriting an old one.

Separate what is verified (sources, tests, code) from assumptions and estimates.

## Scope

Mail server, DNS server, billing, reseller features and multi-server are non-goals for v1 ([`docs/02-vision-scope.md`](docs/02-vision-scope.md)). Proposals touching them need to say so.

## Language

- Code, comments, commit messages, issues and public documentation: **English** (ADR-0012).
- The internal planning documents in `docs/` are currently German and will be translated before publication.

## Commits and pull requests

- Commit messages in English, imperative mood, short subject (about 72 characters), for example `Add threat model for agent RPC`.
- One logical change per pull request. Fill in the PR template.
- Documentation changes must pass the CI checks (Markdown lint and link check). To run the lint locally: `npx markdownlint-cli2 "**/*.md"`.
- Do not commit secrets. `.env` files are ignored.

## License and sign-off (DCO)

- Code is licensed under **AGPL-3.0-or-later** (see [`LICENSE`](LICENSE)). Documentation and translations are licensed under **CC-BY-4.0** (see [`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt)).
- There is no CLA. Instead, every commit must carry a `Signed-off-by:` line, which certifies the [Developer Certificate of Origin 1.1](https://developercertificate.org/). Use `git commit -s`.
- Do not contribute code or text you do not have the right to license under these terms.

## Conduct

Be respectful and constructive. A formal Code of Conduct will be added before the repository is opened to contributions.
