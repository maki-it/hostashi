# ADR-0010 Target audience and v1 scope

**Status:** Proposed (not accepted). **Date drafted:** 2026-11-17. **Decides:** Kim. **Depends on:** open questions 1 and 5. **Feeds:** ADR-0009 (platform matrix), ADR-0013.

## Context

- Proposal in the planning docs: a single-server panel for developers and agencies who run several client sites on one or a few servers of their own, which is CloudPanel's niche [V: `02-vision-scope.md` 2.2].
- Non-goals for v1: own mail server, own DNS server, Windows/BSD/RHEL family, shared-hosting mass operation, billing, domain registration, Kubernetes [V: `02-vision-scope.md` 2.4]. Reseller, multi-server are "later" [V: same, 2.2].
- CloudPanel itself has no mail, DNS, multi-server or reseller functions [V: `01-recherche-cloudpanel.md` 1.2], so staying in the niche means feature parity is reachable without those.
- Features marked P2 and phase 2+: multi-server, LXC mode, Docker-Compose apps, web terminal [V: `03-features.md`]. The architecture is meant to be prepared for multi-server but not to build it.
- A market analysis against HestiaCP, ISPConfig, 1Panel, Coolify and others has **not** been done [V: `01-recherche-cloudpanel.md` 1.5]. Claims about their audiences below are [U].

## Owner input (2026-11-17)

- Q1: Hostashi is first for Kim's own servers and customers [V: Kim, thread].
- Q2: the team is Kim alone, who masters Go [V: Kim, thread]. Bus factor is 1.
- Q5: single-server v1, multi-server and reseller maybe much later [V: Kim, thread].
- Q8: no budget for an external pentest or legal review [V: Kim, thread].

Effect: option A is confirmed by the owner. The first user is Kim's own business.

## Options

| Option | Who it serves | Pro | Contra |
|--------|---------------|-----|--------|
| **A. Single-server for developers and agencies** | Admins with one or a few own servers, several customer sites | Clear niche, matches CloudPanel parity, smallest attack surface, achievable roadmap | Smaller revenue potential, "few servers" users will ask for multi-server soon [A] |
| **B. A plus reseller functions from the start** | Small hosters with packages, quotas, white label | Larger market | Quotas, plans, customer isolation and billing hooks multiply scope and attack surface, touches non-goals |
| **C. Multi-server first** | Teams with fleets | Differentiates from CloudPanel | Needs controller/agent trust model, PostgreSQL decision, remote credentials. Largest security surface, delays the single-server value [A] |
| **D. Shared hosting at scale (cPanel-like)** | Hosting companies | Big market | Very different product (mail, DNS, billing, abuse handling). Outside the stated vision |

## Recommendation

**Option A**, with these guardrails so that "later" stays possible without being built:

1. **Persona v1:** one admin (or a small team) running 1 to roughly 20 sites per server on a VPS or root server, mostly PHP, Node, Python, static and reverse proxy, wanting isolation between sites and safe defaults [A: the site count is a sizing guess, not a measured target].
2. **Client access is not reseller.** The existing roles (Admin, Site Manager, User with site grants) let an agency give a client access to their own sites. That is in scope [V: `03-features.md` 3.6]. Plans, quotas as products, white-label and billing are out.
3. **"Few servers" means independent panels.** Each server runs its own Hostashi. A central view is a later controller feature. Keep IDs and the API multi-server-ready (no hardcoded single-server assumptions in the data model) [A, matches `03-features.md` "Architektur vorbereiten, nicht bauen"].
4. **Scope changes only by ADR.** Mail, DNS server, billing, reseller, multi-server get a new ADR before any implementation, as the vision already demands [V: roadmap risk table, scope creep].
5. **Integrations that stay in scope** because they serve the single-server operator: DNS-01 providers (INWX, Cloudflare, Hetzner DNS) and S3/SFTP/Storage Box backup targets [V: `06-roadmap.md` Phase 2, `03-features.md`]. Whether Hetzner Cloud/Robot and invoicing integrations are mandatory is open question 7 and stays P2 until answered.

### How to check the assumption

- Phase 0 market analysis (HestiaCP, ISPConfig, Virtualmin, 1Panel, Coolify, Dokploy, CapRover, plus commercial panels) with stars, activity, license, audience, weaknesses [V: roadmap task].
- A small set of interviews with agency admins about their server count and pain points [E: 5 to 10 conversations over 2 weeks, no data basis].
- Evaluate CloudPanel's public issues for feature demand [V: `01-recherche-cloudpanel.md` notes issues were not evaluated].

## Consequences

- Smaller, shippable roadmap: Alpha PHP site with HTTPS and DB, Beta parity, 1.0 with pentest [V: `06-roadmap.md`].
- Clear "no" for requests that need cross-customer billing or mass accounts. Documented in a public "Scope" page.
- Platform matrix stays small (ADR-0009): Ubuntu 24.04 and Debian 13, plus older LTS, amd64 and arm64 [V: ADR-0009].
- Risk: if the real demand is hosting resellers, v1 will miss the market. Mitigation: validate before Beta, not after 1.0.
- Risk: users with 3 to 5 servers see no central management and leave. Mitigation: API and YAML export/apply as the automation path [V: `03-features.md` 3.10].

## Open questions

1. Expected number of servers and sites in Kim's own use, as a sizing check.
2. Are Hetzner, INWX and invoicing integrations mandatory or nice to have? (Q7)
