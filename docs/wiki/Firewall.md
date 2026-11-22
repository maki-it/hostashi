# Firewall

**For:** panel admins.

Hostashi runs its own firewall (nftables). It lets in SSH, the websites and the panel, and drops everything else. Outgoing traffic is not limited. If your provider has a firewall in front of the server as well, that one still applies: open a port there too.

Open **Server → Firewall**.

## What is always open

Ports 80 and 443 (TCP, and UDP for HTTP/3) are always open for the sites and cannot be closed in the panel. SSH (22) and the panel port (8443) are open to everyone until you set an allowlist.

## Allowlists for SSH and the panel

Under **Allowlists**, enter the addresses or networks (for example `203.0.113.7` or `198.51.100.0/24`) that may reach SSH or the panel. Everyone else is dropped. The page shows **your address as the panel sees it**, so you do not lock yourself out.

If the panel runs on port 443 under its own host name, its allowlist is checked by nginx instead (see [Install](Install.md#panel-without-a-port)).

![The Firewall page](images/firewall.png)

## Open more ports

Under **Opened ports**, add a port or a range (`3000` or `6000-6010`), the protocol and, optionally, the addresses it is open to.

## Every change rolls back unless you confirm it

A wrong rule could cut you off, so each change is tested first:

- **Panel access changed:** your browser checks that the panel still answers and confirms on its own.
- **SSH allowlist changed:** the panel asks you to open a **new** terminal and log in over SSH. Keep the session you have open until that works, then choose **SSH works, keep the rules**.

Without a confirmation the rules roll back after the time shown on the page. The page then says that the last change was rolled back.

## Blocked after failed logins

Independent of these rules, fail2ban blocks addresses for a while after repeated failed SSH or panel logins.

## Without the Hostashi firewall

A server installed with `--no-firewall` has no Hostashi firewall; the page says so. Use this only behind a provider firewall. To add more ports at install time instead, use `--firewall-allow PORT/PROTO`.
