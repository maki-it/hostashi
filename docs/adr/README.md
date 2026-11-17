# ADR drafts

Full drafts for the decisions that need the project owner's input. The overview table and the German summaries live in [`../07-decisions.md`](../07-decisions.md).

| ADR | Topic | Status |
|-----|-------|--------|
| [0002](0002-license.md) | Project license | Proposed |
| [0003](0003-language-and-stack.md) | Language and stack | Proposed |
| [0010](0010-target-audience-and-v1-scope.md) | Target audience and v1 scope | Proposed |
| [0013](0013-business-model-and-governance.md) | Business model and governance | Proposed |

Only the project owner sets a status to `Accepted`.

## Evidence tags

Every non-trivial claim carries one tag so that facts, guesses and numbers stay separable:

- **[V] Verified**: read in this repository or checked against a source during drafting.
- **[A] Assumption**: plausible, but not checked. Needs confirmation by the owner or a spike.
- **[E] Estimate**: a number or duration without a data basis. Recalibrate after Phase 0.
- **[U] Unverified general knowledge**: stated from memory (licenses, third-party projects, legal mechanics). No web or legal check was done. Verify before relying on it.

Nothing here is legal advice. License, trademark and contract questions should be reviewed by a lawyer before publication.

## Decision order and dependencies

```text
Q1 purpose ──► ADR-0013 business model ──► ADR-0002 license (and CLA vs DCO)
Q5 scope   ──► ADR-0010 audience
Q2 team    ──► ADR-0003 stack
```

Recommended order: ADR-0013 and ADR-0010 first, then ADR-0002, then ADR-0003. The stack decision is independent of the license, so it can run in parallel once the team's skills are known.
