# Audit log

Hostashi writes every change made in the panel or through the API to an audit log: who did it, when, what it was and what it concerned. Failed logins and rejected API tokens are in it too. Entries cannot be changed or deleted, not even by an admin.

**For:** admins. Managers and users do not see the audit log.

## View the log

1. Open **Audit log** in the Administration part of the menu.
2. The newest entries come first. **Show older entries** loads the next 50.
3. Narrow the list with the filters above the table:
   - **Action**: one action, such as `site.create`, or a whole group, such as `site.* (all)`.
   - **Account**: the exact username. An account that was deleted since keeps its name in the log and shows *(deleted)*.
   - **Target**: part of what the action concerned, usually a site, database or account name.
   - **From** and **To**: days in your browser's time zone, both included.

The filters are part of the address, so you can bookmark or share a filtered view.

The **Details** column shows what the panel recorded with the action, for example the IP address of a login or the settings that changed. It never contains passwords or keys.

## Export

**CSV** and **JSON** at the top right download the log with the filters you set, oldest entry first. CSV opens in a spreadsheet; JSON suits scripts. Each export is itself written to the log, with its format and filters.

In the CSV file a cell that starts with `=`, `+`, `-` or `@` gets a leading `'`, so that a spreadsheet does not run a crafted username as a formula.

## API

The same data is available to API tokens with the scope `audit:read`, only for admins:

- `GET /api/v1/audit` with the optional query values `action`, `actor`, `target`, `ip`, `since`, `until` (RFC 3339), `limit` (1 to 200) and `before` (the `next` value of the previous page).
- `GET /api/v1/audit/actions` lists every action in the log.
- `GET /api/v1/audit/export?format=csv` or `format=json` with the same filters.
