# ADR-0002 Project license

**Status:** Proposed (not accepted). **Date drafted:** 2026-11-17. **Decides:** Kim. **Depends on:** ADR-0013. **Must be settled before:** the first line of public code or the first outside contribution.

> This is a planning aid, not legal advice. License choice has long-term legal effects. Have a lawyer review the final choice, the contribution terms and the handling of third-party code before publication.

## Context

- CloudPanel's repositories contain no source code for the product itself, and `vhost-templates` and `docs` carry no license file, so all rights are reserved [V: `01-recherche-cloudpanel.md`, per ADR-0001]. We build clean-room and copy nothing from them.
- `dploy` is MIT (copyright holder named in the file looks like an upstream project, needs checking) and `clp-wp-varnish-cache` is GPLv3 per its `readme.txt` [V: `01-recherche-cloudpanel.md`].
- Positioning is "really open source" with a security focus [V: `02-vision-scope.md`].
- Planned dependencies include Go, restic, lego (ACME), Nginx, MariaDB, PHP-FPM as **separate programs** [V: `04-architektur.md`]. Their licenses must be checked, but a panel that starts and configures external programs does not link them [A].
- Dual-licensing or open core would need a CLA from the start [V: ADR-0002 original text].

## What the license actually protects (and what it does not)

- **[U]** A copyleft license (GPL/AGPL) obliges whoever **distributes** a modified version to publish its source under the same terms. AGPL adds the same duty for users who interact with a modified version **over a network**.
- **[A]** A control panel is installed by the operator on their own server, so a fork is normally **distributed** (as a package or image). That makes GPL-3.0 already effective against a closed fork. The extra AGPL clause mainly matters for someone running a modified panel as a hosted service that others log in to. That is a narrower case for a single-server panel.
- **[U]** No license stops a competitor from using the code **unmodified**. Neither stops reselling support or hosting. If that is the fear, the real protection is the trademark (ADR-0013), not the license.
- **[U]** Apache-2.0 includes an explicit patent grant and is widely accepted by companies. GPL-3.0 also has a patent grant. Apache-2.0 code can be included in a GPLv3 project, the reverse is not possible.
- **[A]** The GPLv3 WordPress plugin `clp-wp-varnish-cache` is a separate program that runs inside WordPress. Using or recommending it does not by itself force our license. Copying its code into our repository would. Varnish is P2 anyway [V: `03-features.md` 3.9], so this should **not** drive the choice.

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **Apache-2.0** | Maximum adoption, patent grant, no objections from company legal teams, easy for plugins and integrations | Forks can go proprietary. Differentiation vs a closed CloudPanel rests on our own openness only |
| **AGPL-3.0** (or later) | Strongest copyleft, covers hosted forks, signals commitment to staying open | Some companies ban AGPL [U]. May deter contributors and plugin authors. Plugins linked into the process would have to be AGPL [U] |
| **GPL-3.0** (or later) | Covers distributed forks, familiar in the panel scene (HestiaCP is GPL per prior notes [U]), fewer corporate bans than AGPL | Does not cover hosted-only forks. Same plugin-linking caveat |
| **Open core** (permissive or copyleft base plus proprietary modules) | Funding path | Needs CLA, community trust cost, double maintenance. See ADR-0013 (c) |

Also considered and not recommended: source-available licenses (BUSL, SSPL, Elastic-style). They are **not OSI open source** [U] and contradict the "really open source" positioning.

## Recommendation

Decision rule, because it hangs on ADR-0013:

1. If ADR-0013 is accepted as recommended (hybrid, no open core): **AGPL-3.0-or-later**, with **DCO** instead of a CLA. Rationale: the product's promise is openness and trust, a hosted or distributed closed fork would hurt that most, and without a revenue model based on relicensing a CLA is unnecessary friction.
   - If Kim prefers fewer corporate objections over the hosted-fork protection, **GPL-3.0-or-later** is an acceptable fallback. The practical difference for a self-installed panel is small [A].
2. If the goal is maximum adoption or an ecosystem of third-party integrations: **Apache-2.0**. Accept that closed forks are possible and rely on the trademark and on being the best upstream.
3. If open core or dual licensing must stay possible: choose the base license (Apache-2.0 or AGPL-3.0), and **require a CLA from the first outside contribution**. Switching from DCO to CLA later means asking every contributor for consent [U].

Stated plainly, my pick under the current information is **option 1 (AGPL-3.0-or-later plus DCO)**, with Apache-2.0 as the alternative if Kim answers question 1 with "maximum adoption".

## Consequences

- A license choice can be widened later (AGPL to Apache requires all copyright holders to agree) but not cheaply [U]. Decide before publishing code.
- Needed with either choice: `LICENSE` file, SPDX headers or REUSE compliance, a third-party license inventory in CI (Go modules and npm packages for the embedded SPA, fonts, icons), and a rule that no code or text from the CloudPanel reference repos enters the tree (ADR-0001).
- The repo currently has **no** LICENSE file and must not get one until Kim accepts this ADR. Until then, outside contributions should not be accepted [A].
- Documentation and UI texts need a license too. A common choice is CC-BY-4.0 for docs [U], decide together with this ADR.
- Name and logo are not covered by the code license. A trademark policy is part of ADR-0013.

## Risks to name openly

- License incompatibility with a future dependency (for example a GPL-2.0-only library with Apache-2.0 [U]). Mitigation: license check in CI.
- Contributor reluctance with AGPL [A]. Mitigation: clear `CONTRIBUTING.md`, DCO only.
- Wrong license choice discovered after contributions arrived. Mitigation: do not take outside code before this ADR is accepted.
- Legal interpretation of "linking" and "derivative work" for plugins is not settled in a way I can verify here. Do not promise a plugin ecosystem under copyleft without legal advice.

## Open questions

1. Answer to Q1 and the choice in ADR-0013.
2. Must dual licensing or open core stay possible? (CLA vs DCO)
3. Is there budget for a lawyer to review the license choice, contribution terms and trademark? (Q8)
4. License for documentation and UI translations.
