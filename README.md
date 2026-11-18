# Hostashi

A modern, **truly open source** hosting control panel for single Linux servers, as an alternative to [CloudPanel](https://www.cloudpanel.io/).

> **Status:** Moving from Phase 0 (planning) to alpha. License (ADR-0002) and stack (ADR-0003) are decided, the spikes live under [`spike/`](spike/). The Go skeleton is in place: one binary with `serve` (no root), `agent` (root, typed operations only) and a typed connection between them. It cannot manage sites yet. Layout and local checks: [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Summary

- Sites for **PHP, Node.js, Python, static and reverse proxy**, each site with its own Linux user and its own runtime.
- **Security by design:** web process without root, small root agent with a fixed set of operations, no shell strings.
- **Declarative:** state lives in a database, configurations are generated, validated, applied and rolled back on failure.
- **Import from CloudPanel**, so people switching over do not have to start from scratch.
- One binary, low resource usage, Ubuntu/Debian on amd64 and arm64.

## Documentation

| # | Document | Content |
|---|----------|---------|
| 1 | [CloudPanel research](docs/01-cloudpanel-research.md) | What CloudPanel can do, what its repos contain, legal situation, weaknesses |
| 2 | [Vision & scope](docs/02-vision-scope.md) | Target audience, positioning, differentiation, non-goals |
| 3 | [Feature matrix](docs/03-features.md) | Features with priority and phase, parity with CloudPanel plus added value |
| 4 | [Architecture](docs/04-architecture.md) | Components, privilege separation, reconciler, data model, installer |
| 5 | [Security](docs/05-security.md) | Threat model and mandatory security requirements |
| 6 | [Roadmap](docs/06-roadmap.md) | Phases, milestones, acceptance criteria, test strategy, risks |
| 7 | [Decisions](docs/07-decisions.md) | ADRs: open decisions with recommendations |

## License

Code: **AGPL-3.0-or-later** ([`LICENSE`](LICENSE)). Documentation: **CC-BY-4.0** ([`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt)). Contributions with DCO sign-off, no CLA (see [`CONTRIBUTING.md`](CONTRIBUTING.md)). The name and the logo are not covered by the code license.

## Next step

Phase 1 (alpha) from the [roadmap](docs/06-roadmap.md): build the first real agent operations, state in SQLite and systemd units on top of the skeleton. Open decisions are listed in [`docs/07-decisions.md`](docs/07-decisions.md).
