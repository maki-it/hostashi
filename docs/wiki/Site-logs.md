# Site logs

Each site has three logs: the **access** log of every request, the **web server error** log of nginx, and the **PHP error** log of the site's PHP. You can read them in the panel and follow new lines as they arrive.

**For:** admins and managers for every site, users for their own sites.

## Read and follow a log

1. Open the site and choose the **Operations › Logs** tab.
2. Pick **Access**, **Web server errors** or **PHP errors**, and how many of the last lines to show (100 to 1000).
3. While **Live** shows a green dot, new lines appear every 2 seconds. **Pause** stops this, and **Follow** starts it again. The view only asks for new lines while the browser tab is visible.
4. **Filter** shows only the lines that contain every word you type, for example `404 wp-login`. Upper and lower case do not matter.
5. With **Scroll automatically** on, the view stays at the newest line. Scroll up to read older lines, and the view stays where you are until you scroll back to the bottom.

**Download** saves the lines shown, with the filter applied, as a text file.

Lines with an error, such as a 5xx status or a PHP fatal error, are red. Warnings and 4xx statuses are orange. When the server rotates a log at night, the view marks the spot and goes on with the new file.

A line longer than 4 KiB is cut and ends with `…`. Control characters and invalid UTF-8 are shown as `�`.

## How long logs are kept

Logs are rotated daily and deleted after a number of days, **7 by default**. An admin sets this on the **Operations › Logs** tab of any site under **Log retention**, from 1 to 365 days. The value applies to every site on the server, and to nginx's logs of requests that match no site. Match it to what your privacy policy and data processing agreements promise, because the access log holds visitors' IP addresses.

A shorter time deletes older logs at the next daily rotation. The panel's own logs and the system journal follow the settings of the distribution.

## Files

The logs belong to root, so a site user cannot read or change them over SFTP or SSH. The panel reads them through the agent. See [Ports and files](Ports-and-files.md) for the paths.

## API

- `GET /api/v1/sites/{name}/logs/{kind}` with `kind` set to `access`, `error` or `php`, and `lines` (1 to 1000, default 200). The answer carries `lines` and a `cursor`.
- The same address with `cursor` set to the last answer's cursor returns the lines that came after it. `rotated` is true when the log was rotated in between. `more` is true when there are more lines to fetch right away.
- API tokens need the scope `logs:read`.
- `GET` and `PUT /api/v1/settings/logs` with `{"days": 14}` read and set the retention. Only admins can do this, in a browser session.
