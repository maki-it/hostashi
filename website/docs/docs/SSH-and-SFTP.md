# SSH and SFTP

**For:** everybody who can open a site, including users.

Each site with files has one login: its Linux user, named like the site. You log in with an **SSH key**; passwords are not accepted. A site can have several keys, for example one per person or per deploy tool.

## Add a key

1. On your computer, create a key if you do not have one yet:

   ```sh
   ssh-keygen -t ed25519
   ```

2. Open the site's **Security › SSH & SFTP** tab and add the contents of the `.pub` file, for example `~/.ssh/id_ed25519.pub`. Ed25519, ECDSA or RSA with at least 3072 bits are accepted.
3. Give the key a label, so you know later whose key it is.
4. Choose the access:
   - **SFTP only** (default): file transfer with SFTP and `scp`, no terminal.
   - **Shell**: a terminal as well, for tools such as `wp`, `composer` or `git`.

The key works within seconds.

## Log in

```sh
sftp <site>@<server>
ssh <site>@<server>      # only with a Shell key
```

`<server>` is the server's host name or address, port 22. On the first login, compare the fingerprint your program shows with the **Host key fingerprints** on the tab. If they differ, do not continue.

The site's web root is `htdocs` in the home folder you land in.

## What a site user can see

Sites cannot read each other's files, and a site user does not see other users' processes. SFTP is not locked into the home folder, but everything outside it that belongs to other sites is closed.

Shell sessions run as the site user, outside the limits of the site's PHP processes.

## WP-CLI

On a PHP site, the `wp` command runs [WP-CLI](https://wp-cli.org/) with the site's PHP version, for example:

```sh
cd htdocs
wp plugin list
wp core update
```

It is the WP-CLI release that Hostashi installs WordPress with. The server gets it with the first WordPress install from the panel, see [Install WordPress](Application-profiles.md#install-wordpress); until then `wp` says so. A server admin can also install it with `systemctl start hostashi-wpcli-install.service`. `wp` refuses to run as root, because WP-CLI runs the site's own code; as root, use `runuser -u <site> -- wp …`.

## Remove a key

Delete it in the list. Sessions that are open keep running until they end. While a site is disabled, nobody can log in; the keys are kept.

## Too many failed logins

fail2ban blocks an address for a while after repeated failed SSH logins. Wait, or ask the server admin to unblock it.
