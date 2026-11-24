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

## Redirect to or from www

Most sites should answer under one name only, either `example.com` or `www.example.com`, and send visitors on the other name there. Choose this in the **www redirect** card on the same tab:

- **No redirect:** every domain shows the site. This is the default.
- **Redirect to the name without www:** `www.example.com` sends visitors to `example.com`.
- **Redirect to the www name:** `example.com` sends visitors to `www.example.com`.

The redirect is a permanent one (301) and keeps the path and query, so `www.example.com/shop?page=2` goes to `example.com/shop?page=2`. With HTTPS on, it goes straight to HTTPS in one step, and HSTS and the security headers are sent on the redirect too.

Both names must be domains of the site. If one is missing, the card offers a button such as **Add `www.example.com`**; it adds the name like the domain list above does, so create its DNS record first. With HTTPS on, the site is served over HTTP until the new certificate also names it. Hostashi never adds the other name on its own, because a name without a DNS record would stop the certificate for the whole site.

The setting works on every pair of a domain and its `www` name, so a site with `example.com`, `www.example.com`, `example.de` and `www.example.de` redirects both pairs. Other domains of the site, such as `shop.example.com`, are not touched. Redirect sites do not have this card: they send every name to their target anyway.

![The www redirect of a site](images/site-www.png)

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
