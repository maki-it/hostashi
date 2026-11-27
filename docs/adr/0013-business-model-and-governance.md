# ADR-0013 Business model and governance

**Status:** Accepted (Kim, 2026-11-27). **Date drafted:** 2026-11-17. **Decides:** Kim. **Blocks:** ADR-0002, contribution rules, funding of the pentest.

## Context

- Hostashi is in Phase 0, no code yet. The repository `maki-it/hostashi` is **private** [V: GitHub API, 2026-11-17]. Publishing it is an outward-facing step that needs Kim's explicit approval.
- Open question 1 (own need, public open source, commercial product, or a mix) and question 8 (budget for an external pentest and legal review) are **unanswered** [V: `07-decisions.md`]. This draft is therefore conditional.
- The roadmap requires an external pentest before 1.0 and lists the bus factor as a high risk [V: `06-roadmap.md`, `05-security.md`]. Both cost money or people.
- Security is the main selling point [V: `05-security.md`]. A business model that creates a security-relevant paid/free split (for example SSO or audit features only in a paid tier) would undermine that.
- Out of scope for v1: billing, reseller, multi-server [V: `02-vision-scope.md`]. These are the typical "Pro" candidates, which matters for option (c).

## Owner input (2026-11-17)

- Q1: Hostashi is first for Kim's own servers and customers [V: Kim, thread].
- Q2: the team is Kim alone, who masters Go [V: Kim, thread]. Bus factor is 1.
- Q5: single-server v1, multi-server and reseller maybe much later [V: Kim, thread].
- Q8: no budget for an external pentest or legal review [V: Kim, thread].
- Owner of the trademark: Maki IT, Kim's sole proprietorship [V: Kim, thread]. The repository stays in the `maki-it` GitHub organization, there is no separate Hostashi organization [V: Kim, thread, 2026-11-17]. A sole proprietorship is not a separate legal person, so personal liability remains [U: general knowledge, not legal advice].
- Money: no sponsoring or paid services are set up until a tax advisor has been asked [V: Kim agreed in thread].
- Contributions: DCO for now, no CLA, open core stays off the table for the moment [V: Kim, thread]. Confirmed with this ADR's acceptance (Kim, 2026-11-27).

Effect: option (d) fits the start. The missing budget is an uncovered risk, see below.

## Options

### (a) Community project, donations and sponsoring only

- Pro: simplest, no conflict of interest, fits "really open source" positioning.
- Contra: funding is unreliable [A]. The pentest and legal review would depend on sponsors or on Kim paying personally.

### (b) Free software plus paid services (support, installation help, managed hosting offers)

- Pro: all code stays open, revenue comes from time and expertise, no feature gating. Compatible with any license in ADR-0002.
- Contra: services scale with people, not with users. Needs a legal entity and invoicing around it [A: Kim's company could carry this, not confirmed].

### (c) Open core (open base, proprietary Pro modules such as multi-server, reseller, SSO)

- Pro: the classic path to product revenue.
- Contra: needs a CLA so the owner can relicense contributions [U]. Splits the community, doubles maintenance, and the Pro candidates are exactly the features that are non-goals for v1. Risk of the "closed core" criticism that Hostashi positions itself against (CloudPanel is free but not open source, see `01-cloudpanel-research.md`).

### (d) Internal tool of Kim's company, opened later

- Pro: no pressure, real production use first, which is the best test of the product.
- Contra: "opened later" often means never, or opened with accumulated assumptions (internal paths, credentials, naming). Contributors cannot help during the phase where design choices are cheapest.

### (e) Hybrid (recommended): (d) as the start, (a) plus (b) as the public model, explicit no-open-core pledge for v1

Build in the open once the license and name checks are done, use it on Kim's own servers from Alpha, accept sponsoring, and offer paid services around it. Revisit open core only through a new ADR, not by drift.

## Recommendation

**Option (e).** Reasons:

1. It does not force an answer to question 1 now. It works whether the main driver is own use or public adoption.
2. It keeps ADR-0002 simple: no CLA is needed for relicensing, so **DCO** (sign-off) is enough and contributors face less friction [U].
3. It keeps the security story clean: no feature that protects the server sits behind a paywall.
4. It leaves the door open: a later open-core move needs a new ADR and, if the code is contributed under DCO only, would require contributor consent or a rewrite of their parts [U]. That is a deliberate cost, and it is why ADR-0002 should be settled before the first outside contribution.

### Governance proposal (small, explicit, revisable)

- **Maintainer model:** Kim as lead maintainer (BDFL-style) in Phase 0 to 1. Decisions through ADRs, status set by the lead maintainer. A `GOVERNANCE.md` states this, how a second maintainer is added, and what happens if the lead is unavailable.
- **Second maintainer:** recruit before 1.0 (bus factor, see roadmap risk table) [V: risk table].
- **Trademark:** the name is protected by trademark law, not by the code license. Decide who holds the mark (Kim personally, the company, or later an association). Publish a short trademark policy (what forks may call themselves). Depends on the open DPMA/EUIPO/USPTO search from ADR-0011 [V: ADR-0011 lists it as open].
- **Security policy:** `SECURITY.md` with a disclosure path before the first public release [V: `05-security.md` 5.9].
- **Funding the pentest:** budget is unknown (question 8). Plan one of: own budget, sponsor pool, or a grant program. The cost of an external pentest for a product like this is probably in the low-to-mid five-digit euro range [E: no quote obtained; get two quotes in Phase 0].
- **No telemetry by default** and no license-key checks in v1. Keeps the "no lock-in" principle [V: `02-vision-scope.md` 2.5].

## Consequences

- Positive: lowest-friction start, honest positioning, no forced split of the code base.
- Negative: revenue is slow and indirect [A]. Kim carries the early costs (pentest, legal review) unless sponsors appear.
- Needs from Kim before this can be Accepted: answers to questions 1 and 8, a statement whether a company or Kim personally acts as the owner, and whether a later open-core move must stay possible (if yes, ADR-0002 must choose CLA over DCO now).
- Follow-ups if accepted: `GOVERNANCE.md`, `CONTRIBUTING.md` with DCO text, trademark policy, sponsoring setup (only after Kim's approval, this is outward-facing).

## Uncovered risk: no budget (Q8)

The roadmap requires an external pentest before 1.0 [V: `06-roadmap.md`]. Without budget there are three ways: sponsors or a grant pay for it, revenue from Kim's own customers pays for it later, or the release stays labeled Alpha/Beta. Recommendation: the last one is the default. Do not call a release 1.0 without the pentest. Publish the code only after at least a basic legal check of license and trademark, or accept that residual risk knowingly.

## Open questions

None at this level. Still open after acceptance: the tax advisor is asked before any income, and the funding of an external pentest stays open (no budget).

## Acceptance (2026-11-27)

Accepted by Kim on 2026-11-27: option (e), DCO without CLA, no open core in v1, no telemetry and no license-key checks.

State at acceptance, which amends the text above where they differ:

- The repository is public, under AGPL-3.0 for the code and CC-BY-4.0 for the documentation, with DCO (ADR-0002) [V].
- `SECURITY.md` and `CONTRIBUTING.md` exist [V]. `GOVERNANCE.md` and a trademark policy are still to be written [V: not in the repository].
- The external pentest before 1.0 was replaced by an own security review before 1.0 (Kim, 2026-11-21, see `06-roadmap.md` and `05-security.md`). The recommendation "do not call a release 1.0 without the pentest" is superseded by that decision; an external pentest stays a goal for later.
- Sponsoring and paid services are still not set up; that stays an outward-facing step that needs Kim's approval and the tax advisor first.
