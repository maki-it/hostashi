# Command line

**For:** server admin. Every command runs on the server as root, for example with `sudo`, unless it says otherwise.

Most work is done in the panel. The command line is for what the panel cannot do for you: the first login, getting back in, checks and emergencies.

## Getting in

| Command | What it does |
|---------|--------------|
| `hostashi setup-token` | Prints the one-time token for the first login. |
| `hostashi user recover [-public-url URL] NAME` | Removes the password and second factors of account `NAME`, enables it and prints a new invitation link. See [Recovery](Recovery.md). |

## Checks

| Command | What it does |
|---------|--------------|
| `hostashi doctor` | Checks the server and every site without changing anything. Exit code `3` if a check failed, `0` otherwise. `-json` prints the result as JSON. |
| `hostashi ping` | Checks that the agent answers. |
| `hostashi version` | Prints the version. |

## Sites

| Command | What it does |
|---------|--------------|
| `hostashi site list` | Lists the site accounts. |
| `hostashi site limits NAME` | Shows the CPU, memory and process limits of a site. |
| `hostashi site limits NAME memory=BYTES tasks=N cpu-weight=N cpu-quota=PERCENT` | Sets them. A value you leave out means no limit. |

## Databases

| Command | What it does |
|---------|--------------|
| `hostashi db list` | Lists the databases. |
| `hostashi db import DATABASE FILE` | Loads a dump into a database. `FILE` is relative to the site's home and ends in `.sql` or `.sql.gz`. |
| `hostashi db export DATABASE FILE` | Writes a dump of a database into the site's home. |
| `hostashi db user password USER` | Sets a new password for a database user. Reads it from standard input; an empty line generates one. |

## Backups

| Command | What it does |
|---------|--------------|
| `hostashi backup start` | Starts a backup now. |
| `hostashi backup status` | Shows the state of the last backup. |
| `hostashi backup restore SITE SNAPSHOT` | Restores a snapshot into a new folder `restore-<time>` in the site's home. Nothing existing is overwritten. |
| `hostashi backup restore-status ID` | Shows the state of a restore. |
| `hostashi backup key show` | Prints the backup repository password and the panel key. Store them outside the server: without them, backups cannot be restored on a new server. |

## Moving from CloudPanel

| Command | What it does |
|---------|--------------|
| `hostashi cloudpanel scan` | Run on the **old** CloudPanel server. Shows which sites can be moved, changes nothing. |

The rest of the move is done in the panel. A full guide follows.

## All commands

`hostashi help` prints every command, including the ones the panel and the system services use internally.
