# Database limits

Every site has two values for its local databases, next to its disk space and memory: a size at which Hostashi warns, and how many connections each of its database users may open at the same time. They come from the site's limit preset and can be changed per site.

**For:** administrators, who edit the presets, and managers, who change the limits of a site. Everyone who can open a site sees its values.

## The values

| Field | What it does | small | medium | large |
|-------|--------------|-------|--------|-------|
| Database size | Warning threshold for all local databases of the site together, in GiB | 2 GiB | 5 GiB | 10 GiB |
| Database connections | Connections at the same time, per database user (`MAX_USER_CONNECTIONS`) | 20 | 40 | 80 |

The preset values are estimates for typical WordPress sites. *Unlimited* switches a value off.

## Change them

- **One site:** open the site, go to the **Limits** tab and choose another preset, or *Custom* and enter the values under **Database**.
- **A preset:** open **Limit presets** in the menu and edit the preset. Every site that follows the preset gets the new values.

## Database size

The size is the data and the indexes of the site's tables, as MariaDB reports them. For InnoDB tables this is an estimate, so it can differ from the files on disk by some percent. Hostashi measures it at most every five minutes. The **Databases** tab shows the size of each database, the **Limits** tab the sum against the threshold.

The size is **only a warning**. Nothing stops a database from growing past it, because a hard stop would break the site at once: no logins, no orders. Hostashi warns in three places:

- the **Limits** tab of the site, from 80 percent,
- the **Health** page, check *Database size*: a warning from 80 percent and an error from 95 percent,
- an email from 90 percent, in the category *Disk space* of the [email notifications](Email-notifications.md), at most once a day.

When the warning comes, look for data the site no longer needs (old logs, sessions, post revisions, cached data in tables), or raise the threshold if the server's disk has room. Databases live in `/var/lib/mysql` and do not count against the site's disk space.

## Database connections

A login above the limit fails with MariaDB error 1226 (*User has exceeded the 'max_user_connections' resource*). The limit keeps one site's runaway script from taking every connection of the server. It counts per database user, so a site with two users can open twice as many connections.

If a site legitimately needs more, raise the value on its **Limits** tab. Hostashi applies the new value to every database user of the site at once; connections that are already open stay open.

The temporary users of the web database tool (Adminer) and of import and export jobs keep their own fixed limits.

## External database servers

Databases on [external database servers](https://github.com/maki-it/hostashi/blob/main/docs/adr/0041-external-database-servers.md) are not measured and get no connection limit: Hostashi does not manage the storage or the connections of a server it does not run.
