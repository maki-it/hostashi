# ADR-0032 SSH and SFTP access per site

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0007 (isolation model), ADR-0015 (per-site resource limits), ADR-0017 (CloudPanel import), ADR-0018 (API), ADR-0021 (fail2ban), ADR-0028 (cron), [`03-features.md`](../03-features.md) row "SSH keys and SFTP per site user (chroot optional)" (P0).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Every site has one Linux user, created by `useradd` with the shell `/usr/sbin/nologin`, a home `<pool>/<site>` owned by the site user, group `www-data`, mode `0750`, and the web root `htdocs` below it [V: `internal/sysuser/sysuser.go`, `internal/sites/fs.go`]. Nobody can log in as a site user today.
- The feature row asks for SSH keys and SFTP per site user, chroot optional, P0 [V: `03-features.md`]. Admins and customers need it to upload files, run `wp-cli`, `composer` or `git` [A].
- The site user can already run any code through PHP [A]. A shell therefore gives the site user no new privilege on the server; the new risks are a new way in (stolen keys, guessed passwords) and processes outside the PHP unit's sandbox [A].
- ADR-0015 (Kim, 2026-11-19): SSH and SFTP sessions count against the site's CPU and memory limits through a drop-in on `user-<uid>.slice` [V: ADR-0015, open question 3]. `internal/siteslice` already renders the limit properties [V].
- ADR-0021: fail2ban with an `sshd` jail is decided, on by default [V: ADR-0021]. The installer does not install it yet [V: `packaging/install.sh` has no fail2ban].
- Hostashi only runs on fresh Debian 13 and Ubuntu 26.04 servers; their `sshd_config` includes `/etc/ssh/sshd_config.d/*.conf` first [V on Ubuntu 24.04, line 12; A for the two targets].
- OpenSSH's `ChrootDirectory` requires the directory and every parent to be owned by root and not writable by anyone else [U]. Site homes belong to the site user [V], so the home cannot be the chroot.

### Checked with OpenSSH 9.6 (Ubuntu 24.04 in the sandbox), 2026-11-19

A `Match Group hostashi-sites` block in `/etc/ssh/sshd_config.d/10-hostashi.conf`, keys in a root-owned file per user, and two test users:

1. The `Match` block in the included file ends with that file: root keeps its own settings and the main file's `Subsystem` line still parses (`sshd -t`, `sshd -T -C user=root` vs `user=<site>`) [V].
2. `AuthorizedKeysFile` pointing at a root-owned file per user (`…/%u`) inside the block: a key the site user put into `~/.ssh/authorized_keys` is refused [V].
3. A key line `restrict,command="internal-sftp" …` works for a user whose shell is `nologin`: SFTP and `scp` work, `ssh user id` gets "This service allows sftp connections only.", a terminal is refused [V].
4. A key line `restrict,pty …` with shell `/bin/bash` gives a shell; a local port forward is refused by the server ("refused local port forward") [V].
5. `PasswordAuthentication no` in the block: a user with a valid password gets only `publickey` offered [V].
6. `PermitUserEnvironment` is not allowed in a `Match` block (`sshd -t` fails); its default is `no` and `restrict` covers it [V].

Not checked: Debian 13 and Ubuntu 26.04 (newer OpenSSH), sessions in `user-<uid>.slice`, fail2ban.

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The distribution's OpenSSH; site users in one group, one static `Match` block; the agent writes a root-owned key file per site; SFTP only or shell per key** | No new daemon. The site user cannot add keys behind the panel's back. Changing keys needs no sshd reload. One server-wide rule set that `sshd -T` can show | No chroot (see below). Shell users run outside the PHP unit's sandbox |
| B. As A, plus SFTP chroot into a root-owned directory per site | SFTP users see only their site | Needs a new layout (root-owned home or a bind mount per site), breaks the CloudPanel layout and the importer, and does not help shell users [A] |
| C. An SFTP server in Go inside Hostashi (`pkg/sftp` server side) | Full control, own chroot in code | A second SSH server on another port, own crypto exposure, no shell, no `scp`/`rsync`. Not boring tech |
| D. A key file in the home (`~/.ssh/authorized_keys`) managed by the panel | Standard layout | The site user (or a compromised PHP site) can add keys without the panel |

## Recommendation

**Option A.**

**sshd configuration (installer, static)**

- The installer adds the system group `hostashi-sites` and writes `/etc/ssh/sshd_config.d/10-hostashi.conf` with one block, then checks it with `sshd -t` before it reloads sshd:

  ```
  Match Group hostashi-sites
      AuthorizedKeysFile /etc/hostashi-ssh/keys/%u
      AuthorizedKeysCommand none
      PasswordAuthentication no
      KbdInteractiveAuthentication no
      AllowTcpForwarding no
      AllowStreamLocalForwarding no
      AllowAgentForwarding no
      X11Forwarding no
      PermitTunnel no
      PermitUserRC no
  ```

- The root login and the admin's own settings stay untouched [V: check 1].
- New site users get the group at `useradd` (`--groups hostashi-sites`) [A].

**Keys (agent)**

- The panel database holds each site's public keys with a label and an access level (SFTP only, or shell). It is the desired state; every change sends the site's complete list to the agent in one op (`ssh.apply`), like `web.apply` and cron [A].
- The agent writes `/etc/hostashi-ssh/keys/<site>` atomically (directory `root:root 0755`, file `root:root 0644`), one line per key, and never copies options from the input [A]:
  - SFTP only: `restrict,command="internal-sftp" <type> <key> <label>` [V: check 3]
  - shell: `restrict,pty <type> <key> <label>` [V: check 4]
- No key file, no login. A disabled site gets an empty file, and deleting a site removes it [A].
- Accepted key types: `ssh-ed25519`, `sk-ssh-ed25519@openssh.com`, `ecdsa-sha2-nistp256/384/521`, `sk-ecdsa-sha2-nistp256@openssh.com`, and `ssh-rsa` with at least 3072 bits. The panel parses the key with `golang.org/x/crypto/ssh` (already a dependency), rejects options, certificates and other types, and stores the canonical form and the SHA256 fingerprint. The agent parses it again. At most 50 keys per site [E].

**Shell**

- The login shell follows the keys: `/bin/bash` while the site has at least one shell key, `nologin` otherwise. The agent changes it with `usermod --shell` in `internal/sysuser`, the existing `useradd`/`userdel` exception, with fixed arguments [A]. This extends that exception by one binary; no new package.
- Shell sessions run in `user-<uid>.slice`, which gets the site's CPU and memory limits through a drop-in (ADR-0015) [A: `UsePAM yes` and `pam_systemd`, not checked]. SFTP-only sessions run there as well [U].
- The CLI of the site's PHP version is reachable as `php` in a shell: out of scope for the first step [A].

**No chroot in v1**

- SFTP-only and shell users see the server as the site's PHP code does: other homes are closed (`0750`, group `www-data`), the panel socket is `hostashi:www-data 0660` [V], logs under `/var/log/nginx/sites` are root's [V]. They can read world-readable system files and list other users' processes [A]. That is what PHP can do today, so SSH adds no new read path, but it is stated as residual risk in `05-security.md`.
- A chroot (option B) can follow as its own ADR if customers need it.

**Brute force**

- Passwords are off for site users, so only the key stage is exposed [V: check 5]. The fail2ban `sshd` jail from ADR-0021 covers all users; it must be installed before SSH per site ships.

**API, UI, CLI**

- `/api/v1/sites/{name}/ssh-keys` (list, add, change label and access, delete), scopes `ssh:read` and `ssh:write` (high risk, because a key with shell access is a login) [A: ADR-0018 conventions]. The view lists fingerprint, label, access, date added; the frontend thread builds it.
- The panel shows the login line (`sftp <site>@<host>`) and the server's host key fingerprints, which the agent reads from `/etc/ssh/ssh_host_*_key.pub` [A].

**Import**

- The CloudPanel scanner can later report each site user's `~/.ssh/authorized_keys` (public keys only). The importer carries them over as SFTP-only keys for review; passwords of CloudPanel SSH users are never carried over [A].

## Consequences

- Site users can log in with keys only; customers who use passwords in FileZilla must switch to a key [A].
- One more binary (`usermod`) in the `sysuser` exception; the `depguard` rule stays as it is [A].
- The installer touches sshd: a broken drop-in could lock the admin out. Mitigation: `sshd -t` before reload, the drop-in contains only a `Match Group` block, never global settings [A].
- Tests: unit tests for key parsing and rendering (options, newlines, NUL, oversized, certificates, weak RSA), a root test for the key file and `usermod`, and a container test with sshd in CI on Debian 13 and Ubuntu 26.04 (SFTP-only refuses commands, shell key works, home key refused, password refused) [A].
- Order of work: installer block and group, agent `ssh.apply`, panel and API, frontend, slice drop-in together with the limits thread, import.

## Open questions

1. Shell access at all in v1, or SFTP only? Recommended: both, chosen per key, SFTP only by default.
2. Keys only, or also passwords for site users? Recommended: keys only.
3. One login per site (the site user with several keys), or extra SSH users per site as in CloudPanel? Recommended: one login per site.
4. No chroot in v1 (option A), or chroot for SFTP (option B)? Recommended: no chroot.
