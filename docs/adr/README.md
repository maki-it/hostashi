# ADR drafts

Full drafts for the decisions that need the project owner's input. The overview table and the German summaries live in [`../07-decisions.md`](../07-decisions.md).

| ADR | Topic | Status |
|-----|-------|--------|
| [0002](0002-license.md) | Project license | Accepted |
| [0003](0003-language-and-stack.md) | Language and stack | Accepted |
| [0005](0005-web-server.md) | Web server | Accepted |
| [0008](0008-backups-with-restic.md) | Backups with restic | Accepted |
| [0010](0010-target-audience-and-v1-scope.md) | Target audience and v1 scope | Proposed |
| [0013](0013-business-model-and-governance.md) | Business model and governance | Proposed |
| [0015](0015-per-site-resource-limits.md) | Per-site resource limits | Accepted |
| [0016](0016-per-site-storage-location.md) | Per-site storage location (storage pools) | Proposed |
| [0017](0017-cloudpanel-import-and-presets.md) | CloudPanel import as migration, and Hostashi presets | Accepted |
| [0018](0018-public-rest-api.md) | Public REST API design | Accepted |
| [0019](0019-php-package-source-and-versions.md) | PHP package source and multiple PHP versions | Accepted |
| [0020](0020-nginx-package-source-and-brotli.md) | nginx package source and brotli | Accepted |
| [0021](0021-brute-force-and-intrusion-protection.md) | Brute-force and intrusion protection | Accepted |
| [0022](0022-metrics-and-tracing.md) | Metrics, logs and tracing | Accepted |
| [0023](0023-panel-https.md) | HTTPS for the panel | Accepted |
| [0024](0024-mariadb-databases-per-site.md) | MariaDB databases per site | Accepted |
| [0025](0025-database-import-export.md) | Database import and export | Accepted |
| [0026](0026-cloudpanel-migration-transfer.md) | Data transfer for the CloudPanel migration | Accepted |
| [0027](0027-maintenance-and-parking-pages.md) | Maintenance and parking pages | Accepted |
| [0028](0028-cron-jobs-per-site.md) | Cron jobs per site | Accepted |
| [0029](0029-custom-nginx-snippets.md) | Custom nginx snippets per site | Accepted |
| [0030](0030-loading-dumps-with-definers.md) | Loading imported dumps with definers | Accepted |
| [0031](0031-dns-cut-over-per-site.md) | DNS cut-over per site in the CloudPanel migration | Accepted |
| [0032](0032-ssh-and-sftp-per-site.md) | SSH and SFTP access per site | Accepted |
| [0033](0033-system-hardening-baseline.md) | System hardening baseline before v1 | Proposed |
| [0034](0034-firewall-rules-in-panel.md) | Firewall rules in the panel | Accepted |
| [0035](0035-users-and-roles.md) | Users and roles | Accepted |
| [0036](0036-php-versions-from-the-panel.md) | Installing PHP versions from the panel | Accepted |
| [0037](0037-panel-ui-redesign.md) | Panel UI redesign: layout, look and themes | Proposed |

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
