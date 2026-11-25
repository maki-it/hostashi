# Site usage

Hostashi shows how much CPU, memory, disk I/O and web traffic each site uses, so you can see which site loads the server most.

**For:** a site's own usage, everyone who may see the site. The ranking of all sites, admins only.

## Usage of a site

Open the site and the tab **Operations › Usage**. Pick the range at the top right: **1h**, **24h**, **7d** or **30d**.

- **CPU**: 100 % is the cores the site may use. With a CPU limit (see [Limits](Limits.md)) that is the limit, without one all cores of the server. The heading says which.
- **Memory**: memory the site's processes use, without the file cache. With a memory limit the chart goes up to it.
- **Web traffic**: bytes per second that visitors sent to the site and that nginx sent back.
- **Disk I/O**: bytes per second read from and written to disk.
- The line below the charts shows the cores in use now, requests per second, processes and the file cache.

Counted are the site's PHP, Node.js or Python processes, its cron jobs, Git deploys and the SSH and SFTP sessions of its user. Not counted are queries in the shared database server and nginx's own work.

## Sites by usage

The **Overview** shows the card **Sites by usage** below the server load, for admins. Each row is a site over the chosen range: average and peak CPU, average and peak memory, web traffic with the number of requests, and disk I/O.

CPU in each row is measured against the cores that site may use, so a site close to its own limit is on top when you sort by CPU. Click a column to sort by it, and a site's name to open its usage.

## How it is measured

The agent reads each site's cgroup every 15 seconds. Web traffic comes from a small extra nginx log per site, `traffic.log`, with only time, status and sizes: no addresses and no paths. It rotates with the site's logs.

The last hour is kept in memory; 5-minute averages are kept for 48 hours and hourly averages for 30 days, as for the [server dashboard](Services-and-dashboard.md). Samples of a deleted site are deleted with it.

## API and metrics

- `GET /api/v1/sites/NAME/usage?range=1h` (or `24h`, `7d`, `30d`), scope `sites:read`. `cpu` is in percent of one core; `cpus` is the server's cores and `cores` the cores the site may use.
- `GET /api/v1/usage/sites?range=24h&sort=cpu` (or `memory`, `traffic`, `io`), admins, scope `services:read`.
- `/metrics` has `hostashi_site_cpu_seconds_total`, `hostashi_site_memory_bytes`, `hostashi_site_tasks`, `hostashi_site_io_bytes_total`, `hostashi_site_http_bytes_total` and `hostashi_site_http_requests_total`, each with the label `site`.
