# Email notifications

Hostashi can send invitations, security notes and notifications by email. It does not run a mail server: it hands every message to an SMTP relay you already have, such as your own mail server or a sending service.

Email is off until an admin sets up a relay. Without it everything works as before: the panel shows invitation links to copy, and problems show up on the overview and the Health page.

## Set up the relay

**For:** admins.

1. Open **Email** in the Administration part of the menu.
2. Enter the relay:
   - **Host** and **Port** of the SMTP server.
   - **Encryption**: *STARTTLS* (usually port 587) or *TLS* (usually port 465). Hostashi never sends mail without encryption and always checks the server's certificate. For a relay with a certificate from your own CA, paste that CA under **CA certificate (PEM)**.
   - **Username** and **Password**, if the relay wants a login. Hostashi sends them only inside the encrypted connection and keeps the password encrypted with the server key. The page never shows it again; leave the field empty to keep it.
   - **From address** and **From name**: the sender of every email, for example `panel@example.com` and `Hostashi`.
   - **Reply-to**, optional.
3. Save, then enter your own address under **Send to** and send a test email. The page shows the relay's answer, for example `535 5.7.8 authentication failed`, if something is wrong.

Examples of relay settings (check your provider's documentation for the current values):

| Relay | Host | Port | Encryption |
|---|---|---|---|
| Your own mail server | `mail.example.com` | 587 | STARTTLS |
| Amazon SES (eu-central-1) | `email-smtp.eu-central-1.amazonaws.com` | 587 | STARTTLS |
| Brevo | `smtp-relay.brevo.com` | 587 | STARTTLS |
| Postmark | `smtp.postmarkapp.com` | 587 | STARTTLS |

**Deliverability is the relay's job.** Set up SPF and DKIM for the sender domain at your provider, otherwise the emails may end up in spam. Hostashi does not sign messages itself.

Links in emails point to the panel's own address (the `--panel-domain` of the installer), never to whatever address a request came in on.

## Your email address

**For:** every account.

On **Your account → Email**, enter your address. Hostashi sends a link to it; the address is used only after you open that link (within 24 hours). Changing the address sends a note to the old one. When an admin invites you with an address, accepting the invitation confirms it.

## What Hostashi sends

**Account emails** go to your own confirmed address and cannot be switched off:

- an invitation or a reset link from an admin,
- a second factor added or removed, a passkey added or removed, a new API token,
- your email address changed.

**Notifications** are about the server and sites. Switch them on or off on **Your account → Notifications**. They are on by default for admins and off for everyone else. You only get notifications about what your role can see: backups and server checks only go to admins; managers get those of every site, users those of their own sites.

| Notification | When |
|---|---|
| Backups | A backup failed or did not save everything. Once per run. |
| Certificates | A renewal failed (at most once a day), or a certificate expires in 14 or in 3 days. |
| Server checks | Once a day, when the checks on the Health page find errors. Also once per new Hostashi version, when the server does not install it by itself (`--auto-update`). |
| PHP versions | Once a week per site, when a site uses a PHP version that loses security fixes within 60 days or already has. |
| Disk space | A disk of the server or a site's disk limit is 90 percent full or more. At most once a day. |

At most 10 emails per recipient and 100 per server go out each hour. Above that, notifications wait for the next hour; account emails are always sent.

## When email does not arrive

- The Health page and the overview show **Email** with a warning when messages failed in the last 24 hours, with the relay's last answer.
- Hostashi tries again after 1, 5, 15 and 30 minutes and then every hour or two, for up to 24 hours. A permanent rejection (a `5xx` answer, for example an unknown recipient) is not retried.
- Send a test email on the **Email** page to see the relay's answer right away.
- Invitation and reset links are always shown on the **Users** page as well, so you can copy them by hand.

Email is not a secure channel: anyone with access to a mailbox can open an invitation link in it. Links work once and only for 24 hours, and admins and managers still need a second factor.
