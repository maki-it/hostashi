# Backups

**For:** panel admins.

Hostashi backs up every site's files and every database once a day with restic, encrypted, to a Hetzner Storage Box over SFTP. Restores never touch the live site.

## Set up the target

1. In the Storage Box settings at Hetzner, turn on SSH access.
2. Open **Server → Backups → Target and schedule** and enter the Storage Box host (for example `u123456.your-storagebox.de`), the user and a directory. The directory is created on the first run.
3. The panel shows a **public key**. Add it to the Storage Box's `.ssh/authorized_keys`.
4. Under **Host key**, confirm the Storage Box's SSH host key. Backups start only after that.
5. Choose the time window: the daily backup starts at a random time between the two hours (server time).
6. Choose how many daily, weekly and monthly snapshots to keep.

**Back up now** runs a backup right away; it runs in the background.

## Keep the keys outside the server

The backups are encrypted with a repository password. Restoring on a new server also needs the panel key. Print both once and store them in a password manager:

```sh
sudo hostashi backup key show
```

Without them, the backups are useless if the server is lost. They are never shown in the panel or the API.

## Data check

Once a month Hostashi reads part of the stored data back and checks it, a different part each month. **Monthly data check reads 1 of N parts** sets how much; `0` turns it off. **Status** shows the last backup and the last data check.

## Restore files

1. Under **Snapshots**, choose the site.
2. Pick a snapshot and choose restore.
3. Hostashi writes it into a new folder `restore-<time>` in the site's home. Nothing in the live site changes.
4. Copy what you need from there into place, with [SFTP](SSH-and-SFTP.md) or the [file manager](File-manager.md), and delete the folder afterwards. It counts against the site's disk limit.

## Restore a database

Each database has its own snapshots in the same list. Restoring one writes its dump (`<database>.sql`, or `.dump` for PostgreSQL) into a `restore-<time>` folder of the site. The panel then offers **Import into a new database**: the dump goes into a new database, and the old one stays as it is. Check the new database, then point the site's configuration to it.

You can also import the dump into an existing database yourself, see [Databases](Databases.md).

On the command line, the same is `hostashi backup restore SITE SNAPSHOT`.
