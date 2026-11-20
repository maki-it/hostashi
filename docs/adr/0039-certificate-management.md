# ADR-0039 Certificate management: own certificates, renew and delete

**Status:** Accepted (Kim, 2026-11-20). **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0023 (panel HTTPS), ADR-0033 (hardening), ADR-0035 (roles), ADR-0037 (UI redesign), [`03-features.md`](../03-features.md) row "Import custom certificates" (P0).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants to upload certificates of his own, renew Let's Encrypt certificates by hand, and delete certificates [V: Kim, thread, 2026-11-20].
- Today a site has one switch: HTTPS on or off (`PUT /api/v1/sites/{name}/tls`). With HTTPS on, the panel gets a certificate from Let's Encrypt over HTTP-01 and renews it 30 days before expiry [V: `internal/serve/sites_api.go`, `internal/hosting/tls.go` `RenewBefore`].
- Keys never leave root: the agent creates the key, the panel only gets a CSR and hands back the chain. Files live below the agent's state in `<site>/key.pem` and `<site>/cert.pem`, root 0700 [V: `internal/certs/certs.go`].
- "Import custom certificates" is P0 in `03-features.md` and not built [V].

## Options

### How an own certificate gets in

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Both ways: upload certificate, chain and key; or let Hostashi create the key and a CSR, and upload only the certificate** | Upload covers certificates bought elsewhere or copied from an old server; the CSR way keeps the key on the server as today [A] | Two flows in the UI |
| B. Upload only | One flow | The private key always passes the browser and the unprivileged panel process |
| C. CSR only | The key never leaves root | Does not cover existing certificates, which is the common case when moving servers [A] |

## Recommendation

**Option A.**

- **Sources per site.** `letsencrypt` (today's behaviour), `custom` (uploaded or from a CSR). One active certificate per site. Parked domains (ADR-0027) and the panel address (ADR-0023, ADR-0042) get the same choice in v1 (Kim, 2026-11-20): parked domains in their table row, the panel address on the Health page under "Panel". For the panel, a custom certificate must cover the panel host name; deleting it falls back to Let's Encrypt, never to no certificate.
- **Upload.** PEM certificate, optional chain, PEM key (RSA ≥ 2048, ECDSA P-256/P-384, Ed25519 not used by browsers). The agent checks before anything goes live:
  - the key matches the certificate;
  - the chain builds up to a root in the system trust store, or the admin confirms "not publicly trusted" (for internal CAs);
  - the certificate is valid now and for at least 1 more day;
  - it covers every domain of the site (wildcards count); missing names are listed and block the switch;
  - size limits (64 KiB per field).
  The key goes straight into the agent's request and is written root 0600 like the LE keys; the panel does not keep it, logs never contain it [A].
- **CSR.** "Create key and CSR": the agent creates the key as today and returns the CSR with the site's domains; the admin uploads the issued certificate later. The same checks run.
- **Renew now.** A button for `letsencrypt`: runs the normal ACME order at once, independent of the 30-day rule. Rate limit: at most once per 10 minutes per site, because Let's Encrypt limits duplicate certificates [U: 5 per week for the same names].
- **Delete.** Removes the certificate files. Only possible when HTTPS is off for the site, or when another certificate takes over in the same step (switching from custom back to Let's Encrypt keeps the old one live until the new one is installed). A site therefore never serves HTTPS without a certificate.
- **Expiry.** Custom certificates are never renewed automatically. The overview shows them under "Needs attention" 30 and 7 days before expiry; doctor and `/metrics` report the expiry (ADR-0022 already exports it for LE [A]).
- **UI.** Tab "Domains & HTTPS" of the site page (ADR-0037): source, issuer, names, valid until; actions "Renew now", "Upload certificate", "Create CSR", "Delete" as icon buttons with tooltips.
- **Roles.** Like the HTTPS switch today (`sites.tls:write`): admin and manager on all sites, `user` on granted sites. Upload with "not publicly trusted" is admin and manager only [A].
- **Audit.** Upload, renew, delete and source changes are logged with fingerprint, never with key material.

## Consequences

- New agent operations `tls.upload`, `tls.delete`, a source field in the site's TLS state, new API routes under `/api/v1/sites/{name}/tls/...`.
- The renewal loop skips `custom`.
- Tests: mismatching key, missing names, expired, chain errors, delete while HTTPS is on.

## Risks to name openly

- **Key in transit.** An uploaded key passes the browser and the panel process in memory. The CSR way avoids that; the UI recommends it.
- **Forgotten renewals.** Custom certificates expire if nobody acts; the warnings are the only guard.

## Open questions

1. ~~Both ways (upload and CSR)?~~ **Decided (Kim, 2026-11-20): both ways.**
2. ~~Parked domains and the panel address: also in v1, or sites first?~~ **Decided (Kim, 2026-11-20): also in v1.**
