# Recovery

What to do when you cannot get into the panel any more.

## Locked out: lost password or second factor

**For:** server admin (needs root on the server).

If you lost your password, your authenticator app or your passkeys, an admin can reset your account in the panel. If **you are the last admin**, nobody can do that for you, so Hostashi has a command for it on the server.

1. Log in to the server over SSH and become root.
2. Run:

   ```sh
   hostashi user recover <username>
   ```

3. Hostashi removes the password and every second factor of that account, ends its sessions, enables it if it was disabled, and prints an invitation link:

   ```text
   The account kim (admin) has no password and no second factor now, and its sessions ended.
   Open this link before 2026-11-21 23:40 CET to set a new password:

     https://panel.example.com:8443/#/invite/…
   ```

4. Open the link within **24 hours**, set a new password and add a second factor again.

The link is printed only on the server's terminal. Treat it like a password: anyone who opens it first takes over the account.

### The link says `<panel address>`

The command reads the panel address from `/etc/hostashi/hostashi.env`. If that file has no address, pass it yourself:

```sh
hostashi user recover -public-url https://panel.example.com:8443 <username>
```

### Error messages

| Message | Meaning |
|---------|---------|
| `must run as root` | Run the command as root, for example with `sudo`. |
| `no account "<name>"` | There is no account with that name. Names are the login names, not display names. |
| `database /var/lib/hostashi/hostashi.db: …` | The panel database is not where Hostashi expects it. Pass its path with `-db PATH`. |
