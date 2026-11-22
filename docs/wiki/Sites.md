# Sites

**For:** panel admins and managers. Users see and change only the sites they were given.

A site is everything that answers for a set of domains: the web server configuration, the certificate and, for most types, its own Linux user with a home folder, its own PHP or Node.js processes, cron jobs, SSH access and limits. Sites cannot read each other's files.

## Site types

| Type | For | Linux user and files |
|------|-----|----------------------|
| **PHP** | WordPress, Laravel, TYPO3 and other PHP software | yes |
| **Static** | HTML, CSS and JavaScript, for example the build of a static site generator or a single-page app | yes |
| **Node.js** | an app such as Next.js, Nuxt or Express that runs all the time; requests go to its port | yes |
| **Reverse proxy** | passing every request to another web server, for example an app in your network | no |
| **Redirect** | sending every address to another one, for example an old domain to the new one | no |

A site without a Linux user has no files, cron jobs, SSH access or limits.

## Create a site

1. Open **Hosting → Sites** and choose **New site**.
2. **Name:** 2 to 32 characters, lowercase letters, digits, `_` and `-`. For sites with files the name is also the Linux user and the home folder, `/home/<name>`.
3. **Domains:** one per line. The first one is the primary domain.
4. **Type:** see the table above. For PHP choose the version; only installed versions are listed (more under **Server → PHP versions**).
5. **HTTPS with a Let's Encrypt certificate:** leave it on. The domains must already point to this server, see [Domains and HTTPS](Domains-and-HTTPS.md).
6. If the server has more than one storage pool, choose where the site's home lives. A site stays on its pool.

The site is live right away. Until the certificate is there, it answers over HTTP.

Upload your files with SFTP ([SSH and SFTP](SSH-and-SFTP.md)) or the [file manager](File-manager.md). For PHP and static sites, the web root is `/home/<name>/htdocs`; another folder can be set on the site's **Web** tab.

## The tabs of a site

| Tab | What you do there |
|-----|-------------------|
| Overview | state, availability (enable, disable), delete |
| Domains & HTTPS | domains, certificate, HSTS, [security headers](Security-headers.md), [real visitor address](Real-visitor-address.md) |
| Web | site type, web root folder, single-page app fallback, asset caching |
| Files | the [file manager](File-manager.md) |
| PHP | PHP version and [settings](PHP-settings.md) |
| Cron | scheduled jobs |
| SSH & SFTP | keys for logging in as the site's user |
| Limits | CPU, memory, processes and disk |
| Logs | the site's [web server and PHP logs](Site-logs.md) |
| nginx | own nginx directives (admins only) |

Tabs that do not apply to a site's type are not shown.

## Disable a site

On **Overview → Availability**, **Disable** shows a maintenance page to every visitor and stops the site's PHP. Files, databases and settings stay. **Enable** brings it back.

## Delete a site

On **Overview → Delete site**. This deletes the site **with its files and databases** and cannot be undone. Backups stay until they expire.

To keep the domains answering, you can park them in the same step: choose the domains to park and a parking page. Domains you leave out are free afterwards. See [Parked domains](Parked-domains.md).
