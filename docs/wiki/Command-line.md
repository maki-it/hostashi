# Command line

**For:** server admin. Every command runs on the server as root, for example with `sudo`, unless it says otherwise.

Most work is done in the panel. The command line is for what the panel cannot do for you: the first login, getting back in, checks and emergencies.

## Getting in

| Command | What it does |
|---------|--------------|
| `hostashi setup-token` | Prints the one-time token for the first login. |
| `hostashi user recover [-public-url URL] NAME` | Removes the password and second factors of account `NAME`, enables it and prints a new invitation link. See [Recovery](Recovery.md). |

## Using the API from the command line

These commands work as any user and on any machine, not only on the server. They talk to the panel's API with an API token, so your role, the token's scopes and the audit log apply as in the panel.

| Command | What it does |
|---------|--------------|
| `hostashi login [URL]` | Logs in through the browser. It prints a link and a code such as `WDJB-MJHT`. Open the link in a browser where you are logged in to the panel (with a passkey or a password and second factor), check that the panel shows the same code and the name of your computer, pick what the token may do and approve. The command then stores the token in `~/.config/hostashi/credentials.json` (only readable by you). `URL` is the panel's address, for example `https://panel.example.com`; on the server itself it defaults to `http://127.0.0.1:8080`. |
| `hostashi login -token [URL]` | Reads an existing API token from stdin instead, for scripts (see below). |
| `hostashi whoami` | Shows the account, its role and the token's scopes. |
| `hostashi api` | Lists the areas of the API, such as `sites`, `databases` or `backups`. |
| `hostashi api AREA` | Lists the commands of an area with their arguments. |
| `hostashi api AREA COMMAND -h` | Explains a command: the request it sends, the token scope it needs and its fields. |
| `hostashi api AREA COMMAND ARGS` | Runs it and prints the answer as JSON, or a download as it is. |
| `hostashi logout` | Removes the stored token. It stays valid until it expires; revoke it in the panel under **Your account → API tokens** if it may have leaked. |

For example, with the token in a password manager:

```sh
pass show hostashi | hostashi login -token https://panel.example.com
```

Every operation of the API is a command of `hostashi api`, so anything the panel can do through its API, the command line can do as well. Arguments after the command are, in this order:

- the names in the route, such as the site: `hostashi api sites get blog`
- fields as `key=value`; numbers, `true`/`false` and comma-separated lists are converted to the field's type: `hostashi api limits set-site blog preset=small`
- any JSON value as `key:=JSON`: `hostashi api limits set-site blog 'custom:={"memory":1073741824}'`
- the whole body with `-d JSON`, `-d @FILE` or `-d -` for stdin
- a file for an upload as `key=@FILE`: `hostashi api pages create-status name=sorry kind=maintenance file=@sorry.html`

Commands marked "panel only" refuse API tokens, for example creating tokens; use the panel for them (see [API tokens](API-tokens.md)).

`HOSTASHI_URL` and `HOSTASHI_TOKEN` override the stored login, for example in CI jobs. Plain `http://` is only accepted for the server itself (`127.0.0.1`, `::1`, `localhost`), so that a token never crosses a network unencrypted.

Approve only a login you started yourself: whoever started it gets a token for your account. A code is valid for ten minutes.

## Tab completion

The installer sets up tab completion for bash, zsh and fish. In a new shell, `hostashi si` and Tab completes to `hostashi site`, and `hostashi api sites create` and Tab shows the fields of that command. Completion knows the commands, their flags, the areas and commands of `hostashi api` and the keys of `hostashi site limits`. It needs no login and does not ask the panel, so it does not complete names of sites or databases.

For bash, the package `bash-completion` must be installed (it is on most servers). On another machine, or for a shell the installer does not know, load the script yourself:

```sh
source <(hostashi completion bash)        # in ~/.bashrc
source <(hostashi completion zsh)         # in ~/.zshrc, after compinit
hostashi completion fish | source         # in ~/.config/fish/config.fish
```

## Checks

| Command | What it does |
|---------|--------------|
| `hostashi doctor` | Checks the server and every site without changing anything. Exit code `3` if a check failed, `0` otherwise. `-json` prints the result as JSON. |
| `hostashi ping` | Checks that the agent answers. |
| `hostashi version` | Prints the version. |

## Services

These commands go through the panel's API like [`hostashi api`](#using-the-api-from-the-command-line), also as root on the server: log in once with `hostashi login` first. Your role and the token's scopes (`services:…`, `system:…`) decide what works, and every change is in the audit log. Naming the action on the command line counts as the confirmation the panel asks for.

| Command | What it does |
|---------|--------------|
| `hostashi service list` | Lists the services with their state and automatic restarts. `-json` prints JSON. |
| `hostashi service ID [SITE] ACTION` | Runs an action, for example `hostashi service nginx reload` or `hostashi service php blog restart`. Only the actions of the [service catalog](Services-and-dashboard.md#services) work. |
| `hostashi system info` | Shows the hostname, time zone, clock, uptime and a pending reboot as JSON. |
| `hostashi system timezones` | Lists the time zones the server knows. |
| `hostashi system timezone ZONE` | Sets the time zone, for example `hostashi system timezone Europe/Berlin`. |
| `hostashi system hostname NAME` | Sets the hostname and updates `/etc/hosts` (see [System settings](System-settings.md)). |
| `hostashi system reboot HOSTNAME` | Reboots the server; the hostname confirms it. |

## Sites

`site create`, `site delete`, `site list` and `site limits` go through the panel's API and need `hostashi login`. `site files` talks to the agent and needs root on the server.

| Command | What it does |
|---------|--------------|
| `hostashi site list` | Lists the sites with their type, pool, state and domains. |
| `hostashi site create NAME DOMAIN[,DOMAIN...] [POOL]` | Creates a static site with its domains, for example `hostashi site create blog blog.example.com,www.blog.example.com`. Change it afterwards in the panel or with `hostashi api`. |
| `hostashi site delete NAME` | Deletes a site with its files, databases and virtual host. |
| `hostashi site files NAME` | Starts the file manager of a site and prints its socket. The panel does this by itself; the command is for checks. |
| `hostashi site limits NAME` | Shows the preset and the limits of a site. |
| `hostashi site limits NAME preset=PRESET` | Gives the site a limit preset. |
| `hostashi site limits NAME KEY=VALUE ...` | Changes single limits, for example `hostashi site limits blog memory=536870912 cpu-priority=high`. Values you leave out stay as they are; 0 means no limit. |

`site limits` goes through the panel's API, like in **Site → Limits**: it needs `hostashi login` and a token with `limits:read` (and `limits:write` to change). The keys are `memory` and `disk` (bytes), `inodes`, `tasks`, `php-workers`, `cpu-priority` (`low`, `normal` or `high`), `cpu-quota` (percent of one CPU), `db-size` (bytes, only warns) and `db-connections`.

## Storage pools

A storage pool is an extra volume for site homes, for example a provider volume for large sites. Hostashi never formats or mounts a volume. You prepare it first:

> **Before you format:** check the device twice.
> Formatting erases everything on the volume, and `mkfs` does not know whether you still need it. Before step 1, run `lsblk -f` and `cat /etc/fstab` and make sure the device is the new, empty volume: it has no mount point, no label or UUID that `/etc/fstab` still uses, and is not where `/home` lives. Hypervisors and cloud consoles may attach a volume under a name that an earlier disk had (`/dev/sdb`), so go by size and serial (`lsblk -o NAME,SIZE,SERIAL,LABEL,UUID,MOUNTPOINTS`), not by name.
>
> If the volume replaces an old one, remove or comment out the old line in `/etc/fstab` and run `systemctl daemon-reload`. A line whose device is gone makes systemd wait 90 seconds for it at every login below that mount point, and without `nofail` the next boot stops in emergency mode. `hostashi doctor` reports such lines (`system.fstab`).

1. Create an ext4 or XFS file system on the volume. For disk limits per site, create it with project quota: `mkfs.ext4 -O quota,project -E quotatype=prjquota /dev/...` (an existing, unmounted ext4 volume: `tune2fs -O project,quota -Q prjquota /dev/...`). XFS needs nothing at this step.
2. Add it to `/etc/fstab` by UUID (from `lsblk -f`, not `/dev/sdX`) with `nosuid,nodev,nofail` and, for disk limits, `prjquota`, for example `UUID=... /srv/vol1 ext4 nosuid,nodev,nofail,prjquota 0 2`, then run `systemctl daemon-reload` and mount it.
3. Leave it empty (`lost+found` is fine).

Without project quota a pool works, but its sites get no disk limit. The agent switches on the enforcement of the limits on every pool that counts usage, at its start and when a pool is added, so no `quotaon` is needed. `hostashi pool list` and `hostashi doctor` show the quota state of each pool: `enforced`, `counting` (limits are not enforced, doctor reports a failure) or `off`.

| Command | What it does |
|---------|--------------|
| `hostashi pool add [-default] NAME PATH` | Adds the mounted volume at PATH as pool NAME. It refuses, with the reason, a path that is not a mount point, not ext4 or XFS, not mounted with `nosuid,nodev`, not in `/etc/fstab`, not empty, inside a system directory or overlapping another pool. With `-default`, new sites land on it when they name no pool. |
| `hostashi pool list` | Lists the pools with their path, number of sites and quota state; the default pool ends with `default`. |
| `hostashi pool rename NAME NEWNAME` | Gives a pool a new name. Its path, its sites and their files stay where they are. Goes through the panel's API (needs `hostashi login`). |
| `hostashi pool default NAME` | Makes a pool the default for new sites that name no pool. Goes through the panel's API (needs `hostashi login`). |
| `hostashi pool remove NAME` | Removes a pool without sites that is not the default. Then you can unmount the volume. |

The panel shows the same under **Server → Storage pools**, where admins can also rename a pool and make it the default. Adding and removing a pool stay on the command line, because they need a mounted volume on the server.

`hostashi doctor` fails a pool whose volume is not mounted or whose marker is missing, and warns when a pool's volume is mounted without `nosuid,nodev` or has no `/etc/fstab` entry. It also checks every block device in `/etc/fstab`: a device that does not exist fails without `nofail` and warns with it. While a volume is missing, its sites do not start and cannot be changed; mount it and restart `hostashi-agent`.

A site gets its pool when it is created and stays there. Its databases stay in MariaDB's own directory.

## Databases

These commands go through the panel's API, like **Site → Databases**, and need `hostashi login` with the `databases:…` scopes.

| Command | What it does |
|---------|--------------|
| `hostashi db list` | Lists the databases with their site and users. |
| `hostashi db create SITE NAME` | Creates a database for a site. |
| `hostashi db delete NAME` | Deletes a database and its users. |
| `hostashi db user create DATABASE USER` | Creates a database user. Reads the password from standard input; an empty line generates one and prints it once. |
| `hostashi db import DATABASE FILE` | Loads a dump into a database. `FILE` is relative to the site's home and ends in `.sql` or `.sql.gz`. |
| `hostashi db export DATABASE FILE` | Writes a dump of a database into the site's home. |
| `hostashi db user password USER` | Sets a new password for a database user. Reads it from standard input; an empty line generates one and prints it once. |
| `hostashi db user delete USER` | Removes a database user. |

## Backups

These commands go through the panel's API and need `hostashi login`, except `backup key show`, which only root on the server can run.

| Command | What it does |
|---------|--------------|
| `hostashi backup get` | Shows the backup settings, the public key and the state as JSON. |
| `hostashi backup set` | Sets the target, retention and time window. Reads the settings as JSON from standard input. |
| `hostashi backup hostkey` | Confirms the host key of the backup server. Reads one public key line from standard input. |
| `hostashi backup start` | Starts a backup now. |
| `hostashi backup status` | Shows the state of the last backup. |
| `hostashi backup restore SITE SNAPSHOT` | Restores a snapshot into a new folder `restore-<time>` in the site's home. Nothing existing is overwritten. |
| `hostashi backup restore-status ID` | Shows the state of a restore. |
| `hostashi backup deleted [SITE]` | Lists deleted sites that still have snapshots, or the snapshots of one. A site name that a newer site took again shows the snapshots from before that site was created. Administrators only. |
| `hostashi backup restore-deleted SITE SNAPSHOT TARGET` | Restores a snapshot of a deleted site into a new folder `restore-<time>` in the home of the existing site `TARGET`. Administrators only. |
| `hostashi backup key show` | Prints the backup repository password and the panel key. Store them outside the server: without them, backups cannot be restored on a new server. |

## Moving from CloudPanel

| Command | What it does |
|---------|--------------|
| `hostashi cloudpanel scan` | Run on the **old** CloudPanel server. Shows which sites can be moved, changes nothing. |

The rest of the move is done in the panel. A full guide follows.

## All commands

`hostashi help` prints every command, including the ones the panel and the system services use internally.

The package also installs a manual page with the same list, so `man hostashi` works on the server. It is made from the same text as `hostashi help` and changes with it.
