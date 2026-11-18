# ADR-0023 HTTPS for the panel

**Status:** Proposed. **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0003 (stack, lego), ADR-0004 (web/agent privilege split), ADR-0005 and ADR-0020 (nginx), ADR-0018 (API under the panel address, IP allowlist), ADR-0021 (fail2ban), [`05-sicherheit.md`](../05-sicherheit.md) 5.3.

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- The panel listens on loopback only (`127.0.0.1:8080`), because it has no TLS. `CheckListen` and the installer refuse other addresses [V: `internal/serve/serve.go`, `packaging/install.sh`]. Today an admin reaches it through an SSH tunnel or their own proxy.
- `05-sicherheit.md` 5.3 says the panel port is not 443 by default and has its own TLS. Binding to localhost behind nginx is listed as an option [V].
- Sites already get certificates: the agent creates the key (root only) and a CSR, the panel obtains the chain over ACME HTTP-01 with lego, and nginx serves it with HTTP/2, HTTP/3 and optional HSTS [V: PR 55, PR 61, `internal/certs`, `internal/acme`, `internal/web`].
- Passkeys are bound to the host name in `-public-url`, so the panel needs a stable name. The installer accepts `https://<name>[:port]` [V: `internal/auth/passkeys.go`, `install.sh`].
- The panel process runs as `hostashi` without root and with a strict systemd sandbox [V: `packaging/systemd/hostashi.service`].
- Let's Encrypt issues certificates for host names over HTTP-01 on port 80. Short-lived certificates for IP addresses exist, but they are a newer offer with their own profile [U].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. nginx in front of the panel on its own port (default 8443)** | Reuses the tested certificate path: the key stays with root, and the panel never holds a TLS key. HTTP/2, HTTP/3, HSTS and the TLS settings come from the same templates. The panel keeps listening on loopback, so `CheckListen` stays as it is. nginx validates every generation before it goes live [V: `internal/web`] | The panel depends on nginx. If nginx is down, the panel is only reachable through an SSH tunnel to `127.0.0.1:8080` [A]. The panel's client IP then comes from nginx, so the panel must trust a forwarded header from loopback only (see consequences) |
| B. The panel terminates TLS itself on 8443 | Independent of nginx: the panel stays reachable when nginx is broken | The `hostashi` user must be able to read its private key, which breaks "TLS keys stay with root" [A]. A second TLS stack to configure and keep in line with the nginx settings. HTTP/3 would need an extra library [U] |
| C. No panel TLS in v1, SSH tunnel only | Nothing to build | Not usable for the customers and colleagues of Maki IT. Passkeys need HTTPS for any name other than localhost [U] |

## Recommendation

**Option A.** Kim chose option A with port 8443 on 2026-11-18. The status stays Proposed until Kim accepts the ADR.

- nginx gets a panel server on its own port, `8443` by default (installer flag `--panel-port`), for the host name from `--public-url`. It proxies to `127.0.0.1:8080`.
- The certificate for the panel name is obtained like a site certificate. The HTTP-01 challenge is answered on port 80 for the panel name too. The key is stored under `/var/lib/hostashi-agent/tls/_panel/` [A: a name that is not a valid site user, so it cannot clash].
- Until the certificate is there, port 8443 refuses the TLS handshake, like an unknown site name. The panel stays reachable through an SSH tunnel.
- HSTS is on for the panel by default, and HTTP/3 is on when nginx has it [A].
- No public DNS name (only an IP): the panel stays on loopback and SSH tunnel, as today. IP certificates may come later [A].

## Consequences

- The panel must take the client IP from `X-Forwarded-For` (or `X-Real-IP`) only when the TCP peer is loopback, and only the value that nginx sets. Otherwise rate limits, the audit log and fail2ban (ADR-0021) would see `127.0.0.1` for everyone [A]. Today the panel ignores forwarded headers [V: `internal/serve/auth_api.go`, `clientIP`].
- The IP allowlist of ADR-0018 can be enforced in nginx (`allow`/`deny`) as well as in the panel [A].
- The panel's `-listen` stays loopback, and `CheckListen` does not change.
- The installer opens nothing in a firewall, because Hostashi does not manage one yet. The docs must say that TCP 8443 and UDP 8443 (HTTP/3) need to be open [A].
- The panel's own health check must not depend on nginx, so `/api/v1/health` stays reachable on `127.0.0.1:8080` [A].

## Open questions for Kim

1. Should HSTS be on for the panel by default? Recommendation: yes. The panel is only ever served over HTTPS, and an HTTP fallback would only help an attacker [A].
