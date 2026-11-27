# File manager

Every site has a **Files › File manager** tab between "Domains & HTTPS" and "PHP". It shows the site's home folder (`/home/<site>`); the web root is `htdocs`.

## What you can do

- Browse folders. The folder is part of the address, so the browser's back button and bookmarks work.
- Upload files and whole folders with **Upload**, or by dragging them onto the table. Large files go up in parts of 8 MiB; a cut connection resumes.
- Download a file, or a folder or a selection as a ZIP.
- Create folders and empty files with **New**. Rename, move, copy, change the mode and delete one entry from its **⋯** menu, or several at once by ticking them; renaming happens right in the row.
- Scan a selection for viruses while the [virus scan](Virus-scan.md#scan-some-files-or-folders) is on.
- Change the mode (for example `644` or `755`). setuid, setgid and the sticky bit cannot be set.
- Extract a ZIP into a folder. Files that exist already are kept unless you choose to replace them.
- Edit text files up to 1 MiB: click the file's name to open it in the code editor, with syntax highlighting, find and replace (the magnifier or Ctrl+F), a full screen switch and saving with Ctrl+S. A file too large for the editor downloads when you click its name. If someone changed the file since you opened it, saving is refused instead of overwriting their change.

Deleting asks once and lists what it deletes. There is no recycle bin; backups are the way back.

## Limits

| What | Limit |
|------|-------|
| entries shown in one folder | 20,000 (use SFTP for more) |
| file size in the editor | 1 MiB, UTF-8 text |
| ZIP extraction | 100,000 entries and 10 GiB unpacked |
| folder and file names | 255 bytes |

Symlinks are shown with their target but never followed: you cannot open, download or edit through them. Use SFTP for them.

## Who can use it

The file manager needs the permissions `files:read` (browse, download) and `files:write` (everything that changes files). Admins and managers have both for every site; a user has them for the sites assigned to them. API tokens get them only when you select them. Every change and every download is written to the audit log.

## How it works

The panel never touches the site's files itself. It talks to a small helper, `hostashi-files@<site>.service`, which systemd starts when the tab is used. The helper runs as the site's Linux user, inside the site's resource limits, with no network and no access outside the home folder. It stops 5 minutes after the last request. Files it writes belong to the site's user, just as with SFTP.

`hostashi doctor` checks that the helper's units are installed, that no helper runs as root, and that none has run for more than a day.

To give all files of a site back to its user and reset their modes at once, use [Reset file permissions](Reset-file-permissions.md) below the file manager.
