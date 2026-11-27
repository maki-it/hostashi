# Panel languages

The panel speaks English and German ([ADR-0071](../../../docs/adr/0071-panel-ui-in-english-and-german.md)).
English is the default and the fallback; every account picks `auto`
(follow the browser), `en` or `de` on its account page. The login and setup
pages follow the browser.

## Files

- `en/<area>.json` and `de/<area>.json`: one catalog file per area, such as
  `shell.json` or `sites.json`. The file name is the first part of the key:
  `shell.nav.pages.sites` is `nav.pages.sites` in `shell.json`.
- `en/index.ts` and `de/index.ts` put the area files together. A new area
  file goes into both. The English catalog is the type of the German one,
  so `tsc` fails for a German area that misses a key, and `t()` only takes
  keys that exist.
- `pending.json` lists the pages whose texts are still written in the JSX.
  An area PR moves the texts of its pages to the catalog and removes them
  from the list.

## Rules

- Every text a user reads goes through `t()` (or `<Trans>` for text with
  markup). `locales/jsx-text.test.ts` fails for text between tags, string
  literals in `{…}` between tags and literals in text attributes (`label`,
  `title`, `placeholder`, `aria-label`, `hint` and the like) outside the
  pending pages. Code examples such as `bin/server` or `example.com` are
  left alone.
- A new key gets its English and German text in the same PR.
  `locales/locales.test.ts` fails when a key is missing in one language,
  when a text is empty, or when the translation drops a `{{placeholder}}`
  or a `<b>` tag.
- Keys are grouped by page or part, then by what they are:
  `sites.create.title`, `sites.create.submit`, `backups.restore.confirm`.
  Use the same key only for the same meaning, not for the same English
  words.
- Plurals use i18next's suffixes: `files_one`, `files_other` with
  `t("…files", { count })`. German uses the same two forms.
- API errors: `apiErrorText(err)` from `i18n.ts` looks up `errors.<code>`
  in `errors.json` and falls back to the server's English message. Add a
  code there when a page shows it.
- Numbers, dates and sizes go through `format.ts` (`formatNumber`,
  `formatCount`, `formatDate`, `formatDateTime`, `formatRelative`,
  `formatBytes`, `compare`), never `toLocaleString` with a fixed or missing
  locale. Sizes stay in binary units (KiB, MiB, GiB) in both languages.
- Tests run in English: `test-setup.ts` sets the language before each test.
  A test that checks German calls `setLanguage("de")` itself.

## German

- Address the user with "du", lower case, or write neutrally where that
  reads naturally ("Passwort ändern" rather than "Ändere dein Passwort" on
  a button).
- Buttons and menu items are verbs or nouns without "bitte": "Speichern",
  "Site anlegen", "Löschen".
- Keep sentences short; German grows about a third longer than English, so
  check the layout on a phone.
- Product, protocol and software names stay as they are: Hostashi, nginx,
  PHP, Node.js, Python, MariaDB, PostgreSQL, Valkey, restic, Let's
  Encrypt, SSH, SFTP, Git, Cron, WordPress, CloudPanel.
- Quotes: „…“ in running text; code, paths and values stay in code style.

## Glossary

The same English term is always translated the same way.

| English | German | Note |
|---------|--------|------|
| account | Konto | |
| administrator, admin (role) | Admin | role name in lists: Admin |
| manager (role) | Manager | |
| user (role, page) | Benutzer | |
| API token | API-Token | das Token |
| app profile | App-Profil | |
| allowlist | Allowlist | die Allowlist |
| audit log | Audit-Log | |
| backup | Backup | das Backup; restore: wiederherstellen |
| blocked address | gesperrte Adresse | fail2ban; unblock: entsperren |
| certificate | Zertifikat | |
| check (Health) | Prüfung | |
| colour scheme | Farbschema | |
| cron job | Cron-Job | |
| database | Datenbank | |
| database server | Datenbankserver | |
| database tool session | Adminer-Sitzung | the Adminer sessions card |
| deploy (Git) | Deploy | der Deploy; verb: deployen |
| deploy key | Deploy-Key | |
| dump (import/export file) | Dump | dump file: Dump-Datei |
| DNS switch | DNS-Umstellung | CloudPanel import |
| domain | Domain | die Domain |
| email | E-Mail | mail as a message: Mail |
| disk | Datenträger | |
| false positive | Fehlalarm | virus scan |
| file manager | Dateimanager | |
| file permissions, mode | Dateirechte, Rechte | chmod: Rechte ändern |
| firewall | Firewall | die Firewall |
| flagged (file) | auffällig | virus scan |
| hardening | Härtung | |
| health | Zustand | the Health page |
| home (of a site) | Home-Verzeichnis | |
| host key | Host-Key | |
| invitation | Einladung | invitation link: Einladungslink |
| jail (fail2ban) | Jail | das Jail |
| limit preset | Limit-Vorlage | |
| log in / log out | anmelden / abmelden | |
| maintenance page | Wartungsseite | |
| memory (RAM) | Arbeitsspeicher | |
| one-time code | Einmalcode | |
| overview | Übersicht | |
| parked domain | geparkte Domain | |
| passkey | Passkey | der Passkey |
| password | Passwort | |
| quarantine | Quarantäne | |
| real-time scan | Echtzeit-Scan | |
| reboot | Neustart | neu starten |
| recovery code | Wiederherstellungscode | |
| redirect | Weiterleitung | |
| release (Git deploy) | Release | das Release |
| relay (SMTP) | Relay | das SMTP-Relay |
| reverse proxy | Reverse Proxy | |
| runtime | Runtime | the Runtimes page: Runtimes |
| scan report | Scan-Bericht | CloudPanel import |
| second factor | zweiter Faktor | TOTP or a passkey |
| security headers | Sicherheits-Header | |
| service | Dienst | |
| settings | Einstellungen | |
| setup token | Setup-Token | |
| shared path | geteilter Pfad | Git deploy |
| site | Site | die Site; site user: Site-Benutzer |
| staging | Staging | staging copy: Staging-Kopie |
| storage pool | Storage-Pool | |
| two-factor | Zwei-Faktor | two-factor authentication: Zwei-Faktor-Anmeldung |
| usage | Auslastung | the Usage tab |
| virus scan | Virenscan | |
| visitor address | Besucheradresse | the Real IP tab |
| web root | Web-Root | das Web-Root |
