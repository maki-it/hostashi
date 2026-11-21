# Parked domains

A parked domain is a domain without a site. The web server answers every request for it with a parking page and status 200, over HTTPS once its certificate is there. Use it for a domain you own but do not use yet, or for an old domain you want to keep answering.

A domain is either parked or belongs to a site, never both.

## Park a domain

**For:** admins and managers.

1. Open **Parked domains** and choose **Park a domain**.
2. Enter the domain, pick a parking page and choose whether it gets HTTPS.

The built-in parking page is used unless you choose one of your own under **Maintenance pages**.

## Make a site of a parked domain

**For:** admins and managers.

1. Open **Parked domains**. In the domain's row, choose the globe button **Make a site**, or open the domain and use **Make a site** there.
2. The **New site** form opens with the domain filled in and HTTPS set as it was for the parked domain. Add more domains, such as the `www.` name, if you want.
3. Choose **Create site**.

The parking page ends when the site is live. The site gets its own certificate; until it is there, the domain is served over HTTP only. If the site cannot be created, the domain stays parked as before, with its page and certificate.

## Delete a site and park its domains

**For:** admins and managers.

This deletes the site with its files, databases, cron jobs and SSH logins, like **Delete**, and parks the domains you choose in the same step, so they keep answering.

1. Open the site. On the **Overview** tab, under **Delete site**, choose **Delete and park domains**.
2. All domains of the site are checked. Uncheck the ones you do not want to park; they are free afterwards. At least one must stay checked.
3. Pick a parking page and choose whether the parked domains get HTTPS.
4. Type the site's name and choose **Delete *site* and park**.

Backups of the site stay until they expire. The parked domains get new certificates; until they are there, they are served over HTTP only.

If deleting stops halfway, for example because the server could not remove the site's files, the domains are parked already and the site shows as being deleted. Use **Delete** on the site again to finish.

## API

- `GET /api/v1/parked-domains`, `GET`/`PUT`/`DELETE /api/v1/parked-domains/{domain}`
- Make a site: `POST /api/v1/sites` with the parked domain in `domains`.
- Delete and park: `POST /api/v1/sites/{name}/park` with `{"domains": ["example.com"], "page": "", "tls": true, "confirm": "<name>"}`. A token needs the scopes `sites:delete` and `sites.maintenance:write`.
