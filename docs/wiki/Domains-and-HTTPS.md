# Domains and HTTPS

**For:** panel admins and managers. Users cannot change domains or certificates, because a domain is a claim on a name that other customers might need.

## Point the domain to the server

Before you add a domain to a site, create DNS records at your DNS provider:

| Record | Name | Value |
|--------|------|-------|
| `A` | `example.com` and `www.example.com` | the server's IPv4 address |
| `AAAA` | the same names | the server's IPv6 address, if it has one |

Hostashi does not run DNS. Let's Encrypt checks each name over HTTP on port 80, so the records must resolve to this server and port 80 must be open.

## Change the domains of a site

On the site's **Domains & HTTPS** tab, edit the list: one domain per line, the first one is the primary domain. After saving, the site answers for the new names at once, over HTTP until the certificate covers them.

A domain can belong to one site or parked domain only, and never be the panel's own host name.

![The Domains tab of a site](images/site-domains.png)

## HTTPS

With **HTTPS with a Let's Encrypt certificate** on, Hostashi gets a certificate for all domains of the site, renews it on its own and redirects HTTP to HTTPS. Sites are served with HTTP/2 and HTTP/3.

If the certificate cannot be issued, the site page says why, for example a domain that does not point to the server. Hostashi tries again within an hour. Fix the DNS record and wait, or use **Renew now** on the certificate card.

### HSTS

**HSTS: browsers use only HTTPS for this site** tells browsers to never use plain HTTP for the domain again. Turn it on only once HTTPS works: browsers remember it for a year, even if you turn it off later.

### Own certificate

If you get certificates from another CA, use the certificate card on the same tab:

- **Upload certificate:** paste or upload the certificate, the chain (optional) and the private key (PEM, no passphrase; RSA with at least 2048 bits, or ECDSA P-256 or P-384). The key passes through your browser and the panel.
- **Create CSR:** Hostashi creates the key on the server and shows a certificate signing request. Give it to your CA and upload the certificate you get back. The key never leaves the server.

Hostashi checks that the key matches, the chain is complete, the certificate is valid and covers every domain of the site before it goes live. Own certificates are **not** renewed automatically: the panel warns 30 and 7 days before they expire.
