# <img src="images/hostashi-logo.svg" alt="Hostashi" height="48">

Hostashi is an open-source control panel for a single Linux server. It runs PHP, Node.js, Python, static, reverse proxy and redirect sites with nginx and Let's Encrypt certificates, manages MariaDB databases (also on external MariaDB, MySQL or PostgreSQL servers), takes backups with restic, and can move sites over from CloudPanel. It is a product of [Maki IT](https://maki-it.de/).

> This wiki is generated from [`website/docs/docs/`](https://github.com/maki-it/hostashi/tree/main/website/docs/docs) in the repository. Changes go through a pull request; edits made here are overwritten.

## Who this is for

| You are … | You need | Start with |
|-----------|----------|------------|
| **Server admin** | root on the server | [Install](Install.md) |
| **Panel admin** | the role `admin` in the panel | [First steps](First-steps.md) |
| **Manager** or **user** | an invitation to the panel | [Sites](Sites.md) |

## What Hostashi does not do

Hostashi 1.0 manages one server. It is not a mail server, not a DNS server, has no billing and no reseller accounts.

## Help

- Locked out of the panel? See [Recovery](Recovery.md).
- Found a bug? Open an issue in the [repository](https://github.com/maki-it/hostashi/issues).
- Security problem? Follow [SECURITY.md](https://github.com/maki-it/hostashi/blob/main/SECURITY.md) and do not open a public issue.
