# Moving from CloudPanel

**For:** panel admins.

Hostashi can take over the sites of a CloudPanel server: their domains, files, databases, PHP settings, cron jobs, SSH keys and checked nginx directives. The move goes from the old server to a **new** server with Hostashi; the old server keeps running until every site is switched, and is turned off afterwards.

Open **Administration → CloudPanel import**. The page walks through these steps.

## 1. Connect the old server

1. Under **Old server**, enter its address and SSH port. Prefer the IP address: DNS names move during the migration.
2. The panel shows an **`authorized_keys` line**. Add it to root's `~/.ssh/authorized_keys` on the old server. The key is locked: it can only read files over SFTP and reach the old server's database port, nothing else.
3. Under **Host key of the old server**, run the **fingerprint command** on the old server and compare. Confirm the matching key. Nothing is copied before that.

## 2. Give database access

To copy databases, Hostashi needs a read-only database user on the old server. Under **Database access**, the panel shows the **SQL for the old server**; run it there as the database root. Files are copied without it.

A new password replaces the old one. If you make a new one, run the new SQL on the old server too.

## 3. Scan the old server

1. Download the `hostashi` archive for the old server's architecture (`linux_amd64` or `linux_arm64`) from the [releases](https://github.com/maki-it/hostashi/releases), check it against `SHA256SUMS` and unpack it on the old server.
2. Run the **scan command** shown on the page as root:

   ```sh
   ./hostashi cloudpanel scan -json -full > hostashi-scan.json
   ```

   It only reads the CloudPanel configuration and changes nothing.
3. Upload `hostashi-scan.json` under **Scan reports**.

The report lists every site and what can be taken over. **Show plan** shows what Hostashi would create for a site and which entries it skips.

## 4. Move each site

Do this site by site.

1. **Create the site.** The site gets the old domains, its PHP version and settings, cron jobs and SSH keys (as SFTP only; switch a key to Shell afterwards if needed). It has no certificate yet; HTTPS follows after the DNS switch.
2. **Files and databases.** Start the import. The old files land in `cloudpanel-import` in the site's home, and the web root stays as it is. Map each old database to a database of the site (create those on the [Databases](Databases.md) page first). The dumps are loaded into the databases shortly after.
3. **Custom nginx.** Directives from the old site are shown once for checking. Check every line, then save them; they are active right away.
4. **Final sync.** Shortly before the DNS switch, run the import again. Then choose **Use imported files as web root**.
5. **DNS switch.** The panel shows the records to set for every domain: point them to this server and remove all other `A` and `AAAA` records. It checks what the domains resolve to and, once they point here, gets the certificate and turns on HTTPS. Lower long DNS TTLs to 300 seconds a day before, so the switch is quick.

## Node.js and reverse proxy sites

- A **Node.js** site becomes a Node.js site. It gets the newest Node.js line installed on this server (install one on the Node.js page first), or another line you choose. Its app gets a new port in `PORT` and stays stopped until you set the start command on the site's **Web** tab, after the final sync. The old process manager, for example pm2, is not taken over.
- A **reverse proxy** site becomes a reverse proxy site to the same target. It has no Linux account here, so there are no files, databases, cron jobs or SSH keys to move: skip step 2. A target on the old server itself, such as `127.0.0.1`, cannot be taken over. An `https` target's certificate is checked; turn the check off in the site's settings if the target uses a self-signed certificate.
- Python sites are not taken over yet.

## 5. Clean up

When every site is switched:

- **Forget old server** in the panel, and remove the `authorized_keys` line and the read-only database user on the old server.
- Turn the old server off.

CloudPanel's own panel users and its backups are not taken over: invite the people again ([Users and roles](Users-and-roles.md)) and set up [backups](Backups.md) on the new server.
