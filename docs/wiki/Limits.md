# Limits

**For:** panel admins and managers set limits; users can see them.

Each site with a Linux user has its own limits, so one busy or broken site cannot slow down the others. The site's **Limits** tab shows them and what the site uses.

## What can be limited

| Limit | Meaning |
|-------|---------|
| CPU cores | hard limit, such as `2` or `0.5`. `0` means no hard limit: the site may use every free core. |
| CPU priority | the share of the CPU the site gets when the server is busy |
| Memory | MiB for PHP, cron jobs and other processes of the site |
| Processes | how many processes the site may run at once |
| PHP workers | how many requests PHP handles at once |
| Disk space and files | space and number of files in the site's home |
| Database size | a warning size for the site's local databases (they can grow past it) |
| Database connections | connections at the same time, per database user |

Disk limits need project quota on the file system. If the server has none, the tab says so and disk limits are not enforced.

When a site has used 95 % of a disk limit, the panel warns. When it is full, writing new files fails.

## Presets

A preset is a named set of limits. Under **Server → Limit presets** you create and change them.

- A new site gets the **default** preset.
- On a site's **Limits** tab you choose a preset or **custom values**.
- Changing a preset changes every site that uses it. A site with custom values stays as it is.
- Deleting a preset keeps its values on the sites that used it, as custom values.

Presets also say whether new sites get an [object cache](Object-cache.md) and a page cache.

See also [Database limits](Database-limits.md).
