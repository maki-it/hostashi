# ADR-0040 File manager in the panel

**Status:** Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0015 (limits and quota per site), ADR-0032 (SSH/SFTP per site), ADR-0033 (hardening), ADR-0035 (roles), ADR-0037 (UI redesign), spike S4 (file access as the site user), [`03-features.md`](../03-features.md) row "File manager in the browser" (P1).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants a file manager per site **in v1**, including operations on several files and folders at once and downloading several files and folders as one ZIP [V: Kim, thread, 2026-11-20].
- `03-features.md` lists it as P1 with "operations through the agent with path confinement" [V].
- Spike S4 showed the pattern: a helper running as the site user with the site directory as an open descriptor, every path resolved with `openat2` and `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS | RESOLVE_NO_XDEV`; about 1.5 ms per request; bulk transfer over the 64 KiB RPC frames was left open [V: `spike/s4-site-helper/REPORT.md`, memory of PR 17].
- The product already resolves paths this way (`internal/sites/fs.go`, the restore in `internal/backup/restore_run.go`) [V].
- Restore and import run as fixed systemd template units that the agent starts through `systemctl` inside an existing `os/exec` exception (`internal/resticctl`) [V: `internal/resticctl/systemd.go`].
- Site users already reach their files over SFTP (ADR-0032) [V].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Own file manager: React UI in the panel, file operations in a per-site helper unit running as the site user** | Fits the design; no PHP, no extra port; writes are the site user's, so ownership, quota and the site's limits apply as with SFTP [A] | Code we write and test ourselves; streaming must be built |
| B. A PHP file manager (for example Tiny File Manager) on its own port like Adminer (ADR-0038) | Ready-made | Another PHP application with its own login and history of vulnerabilities [U]; looks foreign in the panel |
| C. Operations directly in the root agent | No new unit | Every bug runs as root; quota and ownership need extra care [A] |

## Recommendation

**Option A.**

- **Helper.** `hostashi-files@<site>.service`, started on demand by the agent and stopped after 5 minutes without requests. `User=` is the site user, the unit runs in the site's slice (ADR-0015), so CPU, memory and quota count against the site. Sandbox as a site pool: `ProtectSystem=strict`, `ReadWritePaths=` only the site home (drop-in per instance, as for restore), `PrivateTmp`, `PrivateNetwork=yes`, no capabilities, `NoNewPrivileges`, `MemoryDenyWriteExecute`, `SystemCallFilter=@system-service`. The unit template joins the fixed list of an existing `os/exec` exception; no new exception [A: which package, phpctl or resticctl, is decided in the PR].
- **Paths.** The helper opens the home once and resolves every path beneath it with `openat2` and the four `RESOLVE_*` flags. Symlinks are listed and shown, never followed; device files, sockets and FIFOs are listed, not opened. Names must be valid UTF-8 and without `/` or NUL.
- **Transport.** The panel talks to the helper over a Unix socket created by systemd for the instance (`hostashi-files@<site>.socket`, mode 0660, group `hostashi`), so file data does not pass the agent. The panel checks role and site grant before every request; the helper trusts only that socket.
- **Operations in v1.**
  - list a folder (name, size, modified, mode, type), sorted and filtered in the browser;
  - download a file; download a selection of files and folders as one ZIP, built while streaming (no temporary file, so no quota use);
  - upload files and folders (drag and drop) in 8 MiB chunks, so large files and nginx limits are no problem; an upload goes to a temporary name and is renamed at the end;
  - create folder, create empty file, rename;
  - move, copy, delete, change mode: each for one entry or a selection;
  - edit text files up to 1 MiB in a code editor (see below), with a check that the file did not change since it was opened;
  - reload the folder listing with a button (no automatic polling);
  - extract a ZIP into a folder.
- **Limits.**
  - ZIP extraction: at most 100 000 entries and 10 GiB unpacked, entries with absolute paths or `..` are refused, symlink entries are skipped, existing files are not overwritten unless chosen.
  - Mode changes cannot set setuid, setgid or sticky bits; the owner stays the site user.
  - Deleting a selection asks once with the number of entries; there is no recycle bin in v1.
- **Code editor (Kim, 2026-11-20: a plain text field is too simple).** CodeMirror 6 (`codemirror` 6.0.2, `@codemirror/view` 6.43.15, language packages for PHP, JavaScript, CSS, HTML, JSON, YAML, Markdown and plain text; all MIT [V: npm, 2026-11-20]), loaded only when the editor opens. Line numbers, syntax highlighting, current line, bracket matching, find and replace, line wrapping as a switch, undo history. Shortcuts: Ctrl/Cmd+S saves, Ctrl/Cmd+F finds, Esc closes.
  - **Unsaved changes (Kim, 2026-11-20).** While there are unsaved changes, the footer shows an "Unsaved changes" badge and the primary button reads "Save changes"; without changes the button is inactive. Every way of closing (Close, the X, Esc, a click outside, another tab of the site page) first asks "Discard your changes?" with "Keep editing" as the default, "Save and close" and "Discard changes". Leaving the page or reloading triggers the browser's own warning (`beforeunload`).
  - Dialog footers use text buttons ("Close", "Save changes") on purpose: final actions are labelled, icons are for toolbars and table rows (ADR-0037). Status line with position, encoding, line endings and "unsaved changes". The editor's colours come from the panel's tokens (ADR-0037).
  - **CSP.** CodeMirror injects its styles at run time. It supports a nonce (`EditorView.cspNonce`) [V: `@codemirror/view` type definitions], and modern browsers use constructed style sheets, which `style-src 'self'` may not cover [U]. The first PR checks this in Chromium, Firefox and WebKit; if a nonce is needed, the panel serves `index.html` with a per-response style nonce instead of loosening `style-src`.
- **Roles.** New scopes `files:read` and `files:write`. admin and manager on all sites, `user` on granted sites (ADR-0035). API tokens need the scope and a site list as today.
- **Audit.** Every write (upload, delete, move, mode change, extract, edit) is logged with user, site and paths; downloads are logged as one entry per request.
- **UI.** New tab "Files" on the site page (ADR-0037), between "Domains & HTTPS" and "PHP": breadcrumb path, toolbar (reload, upload, new folder, new file), table with checkboxes, row actions as icon buttons with tooltips, a bar for the selection (download as ZIP, move, copy, mode, delete), an editor dialog for text files (wide, with the editor's own toolbar).

## Consequences

- One more unit and socket template, a helper subcommand in the binary, new API routes under `/api/v1/sites/{name}/files/...`.
- doctor checks that no helper runs longer than its idle time and that its sandbox settings are in place.
- Fuzz tests for path handling and the ZIP reader; container tests for symlink races, quota and mode limits.
- Estimate: 4 to 6 PRs [E].

## Risks to name openly

- **Browser write access to site files.** Anyone with a panel session for a site can change its PHP code. That is the purpose; the second factor (ADR-0035) and the audit log are the guard.
- **Decompression bombs and slow streams.** The limits above and the site's slice limits contain them [A].
- **Not a pentest.**

## Open questions

1. Own file manager with a per-site helper running as the site user (option A)?
2. Is a recycle bin needed in v1, or is "ask once, then delete" enough?
