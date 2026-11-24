# First steps

**For:** the first panel admin, right after the [install](Install.md).

## 1. Create the admin account

Open the link that the installer printed at the end, for example `https://panel.example.com:8443/#/setup/3f9c…`. On a new server the panel shows **First-time setup** with the setup token filled in. Choose a user name and a password. Without the link, open the panel address and enter the token by hand.

Lost the token? On the server, `sudo hostashi setup-token` prints it again. It is valid for 24 hours after the install.

## 2. Add a second factor

Admins and managers must have a second factor. Until you add one, the panel lets you do nothing else.

- **Authenticator app (TOTP):** scan the QR code with an app such as Aegis, 2FAS or your password manager, and enter the current code. The panel then shows **recovery codes**. Each one works once, in place of a code from the app. Store them outside the server, for example in your password manager.
- **Passkey:** add one under **Your account → Sign-in and security**. A passkey replaces the password and the code: you log in with your device's fingerprint, face or PIN. Passkeys need the panel to run under its host name (`--panel-domain`).

If you ever lose both, see [Recovery](Recovery.md).

## 3. Look at the server's health

Open **Server → Health**. It runs the same checks as `hostashi doctor` and shows what needs attention, for example a disk quota that waits for its reboot.

## 4. Set up backups

Open **Server → Backups** and enter where backups go, for example a Hetzner Storage Box. Then print the backup keys once on the server and store them outside it:

```sh
sudo hostashi backup key show
```

Without these keys, backups cannot be restored on a new server.

## 5. Create your first site

Go to **Hosting → Sites → New site**. See [Sites](Sites.md).

## 6. Invite others

Under **Administration → Users** you invite more people with the role `admin`, `manager` or `user`. Each gets a link to set their own password. See [Users and roles](Users-and-roles.md).
