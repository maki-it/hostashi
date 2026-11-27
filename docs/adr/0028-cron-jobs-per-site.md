# ADR-0028 Cron jobs per site

**Status:** Accepted (2026-11-19, by Kim: option A); Addendum 1 Accepted (Kim, 2026-11-27). **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0007 (isolation model), ADR-0015 (per-site resource limits, Proposed), ADR-0017 (CloudPanel import), ADR-0026 (migration transfer), [`03-features.md`](../03-features.md) row "Cron jobs per site" (P0).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- The feature row asks for cron jobs per site with a validator instead of deny lists, systemd timers as an option, and jobs that can be paused and run at once [V: `03-features.md`].
- Hostashi has no cron support yet. The CloudPanel scanner already reads each site user's crontab and reports schedule, command and line, and flags environment lines, `%` in commands and paths into other homes [V: `internal/cloudpanel/scan/cron.go`]. The migration plan marks such sites "review: cron jobs are not carried over yet" [V: `internal/cloudpanel/plan`].
- web2 has 22 sites; how many of them have cron jobs is not known to the project [U]. WordPress sites often run `wp-cron.php` from cron instead of on page loads [U].
- ADR-0015 wants every process of a site, cron included, inside the site's slice so limits apply [V: ADR-0015 section 2]. Classic cron starts jobs outside any per-site cgroup [U].
- The agent already starts and stops systemd units with fixed arguments through the reviewed `os/exec` exceptions `phpctl` and `resticctl` [V: `.golangci.yml`]. The per-site PHP masters are systemd template units with sandboxing [V: `packaging/systemd/hostashi-php@.service`].
- A cron command is a shell command line written by the admin. It runs as the site user, who can already run any code through PHP [A]. So the command itself grants no new privilege; the risks are in how it reaches the system (quoting into a unit file, `%` specifiers, a job that runs as root by mistake) [A].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. One systemd timer and service per job, rendered by the agent; the command lives in a script file, the unit runs it as the site user** | Runs inside the site's slice and sandbox like PHP. No overlapping runs (systemd does not start a running service again). Status, last exit code and logs per job from systemd and the journal. "Run now" is a plain start, "pause" stops the timer | Cron's five fields must be translated into `OnCalendar=`; a few cron forms have no exact match (see below). One more unit pair per job |
| B. Classic cron: write the site user's crontab (`/var/spool/cron/crontabs/<user>`) | Familiar, exact cron semantics, no translation | Jobs run outside the site's cgroup and without sandbox. Overlapping runs, output by mail or lost. Status and "run now" need extra plumbing. Writing the spool file bypasses `crontab`'s own checks [U] |
| C. A scheduler inside the agent that starts transient units (`systemd-run`) | No unit files on disk | The agent becomes a clock; runs are missed while it restarts. More of our own code for what systemd already does |

## Recommendation

**Option A.**

**Jobs**

- A job belongs to one site and has: an id, a schedule, a command, a timeout (default 1 hour, at most 24 hours), and a paused flag. At most 50 jobs per site [E].
- The panel database is the desired state, like sites and pages; every change sends the site's complete job list to the agent (one agent op), which renders and reconciles the units [A: same pattern as `web.apply`].

**Schedule: a validator, not free text**

- Accepted: the five cron fields with numbers, ranges, lists and steps (`*/5`, `1-5`, `0,30`), month and weekday names, and the keywords `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`. Not accepted: `@reboot`, and anything the validator does not understand [A].
- The agent translates the fields into `OnCalendar=` and checks the result with systemd's own parser before it writes the unit [A]. Two cron rules differ: when day-of-month and day-of-week are both restricted, cron runs on either, systemd on both. Such a schedule becomes two `OnCalendar=` lines, which systemd joins with "or" [U]. Steps on ranges (`1-30/5`) are expanded into lists [A].
- Timezone: the server's timezone, as cron [A]. `RandomizedDelaySec=` stays 0 so jobs keep their exact minute; `AccuracySec=1s` [A].
- Missed runs while the server was off are not caught up (`Persistent=false`), as cron [A].

**Command**

- The command is stored as given, at most 4 KiB, one line, no NUL [E]. It is written to a root-owned script file `/var/lib/hostashi-cron/<site>/<id>.sh` (directory `root:<site>` `0750`, file `0640`, so only the site user reads it; not below the agent's `0700` state directory) that the unit runs with `/bin/sh`; the unit's `ExecStart=` names only that file, so nothing the admin typed passes through systemd's unit syntax or `%` specifiers [A].
- `%` keeps its shell meaning; cron's "percent becomes newline" is not copied. The importer flags such commands for review (it already does [V: `cron_percent` finding]).
- No environment from the panel in the first step. `HOME`, `USER`, `PATH` and `SHELL` are set by the unit [A]. Environment lines from CloudPanel crontabs stay a review finding [V].

**Unit**

- `hostashi-cron-<site>-<id>.service`: `User=` and `Group=` the site user, `Slice=` the site's slice (`hostashi-site-<user>.slice`, `-` escaped as `\x2d`) and `Nice=10` unless the job sets another (ADR-0015), `WorkingDirectory=` the home, `NoNewPrivileges=yes`, `PrivateTmp=yes`, `ProtectSystem=strict` with `ReadWritePaths=` the home, `ProtectHome=` hiding other homes, `TimeoutStartSec=` the job's timeout, `Type=oneshot` [A: same hardening family as the PHP unit].
- `hostashi-cron-<site>-<id>.timer` with the translated `OnCalendar=` lines. Paused means the timer is stopped and disabled; the service can still be run once [A].
- The agent refuses a site user of uid 0 and a site that is disabled; disabling a site stops its timers [A].
- Output goes to the journal with the unit name; the panel shows the last run (start, end, exit status) and the last lines of output through the agent [A]. Mail is not sent.

**API, UI, CLI**

- `/api/v1/sites/{name}/cron` (list, create, change, delete), `.../cron/{id}/run`, scopes `cron:read` and `cron:write` [A: ADR-0018 conventions]. The view belongs to the frontend thread.

**Import**

- The migration plan counts the scanned jobs that pass the validator (`cronImport`, at most 50, never the commands themselves); creating the site creates them paused, and activating the import (ADR-0026) does not start them. The admin starts them after the DNS switch, so a job never runs on both servers at once [V: `internal/cloudpanel/plan/cron.go`, `internal/serve/migrations_api.go`]. Jobs that are not accepted (`@reboot`, unknown fields, more than 50) get the review reason `cron_not_carried` and are set up by hand.

## Consequences

- No new `os/exec` exception: `internal/phpctl` drives the cron units with fixed verbs (daemon-reload, enable/disable `--now` for timers, start `--no-block` for services, show) and unit names that match `hostashi-cron-<site>-<id>.(service|timer)` [V: `internal/phpctl/cron.go`].
- A disabled site's timers stop after `web.apply` and start again when it is enabled; deleting a site removes its jobs [V: `internal/agent/cron.go`].
- Unit files appear under `/etc/systemd/system`, named `hostashi-cron-*`, and are removed with the job or the site [A].
- A schedule that cron accepts but the validator does not (for example `@reboot`) must be rewritten by the admin [A].
- Needs a spike-sized test: translation table with `systemd-analyze calendar` on Debian 13 and Ubuntu 26.04 in CI [A].

## Open questions

None. Decided (Kim, 2026-11-19): imported jobs start paused; Kim starts them after the DNS switch.

## Addendum 1: output of the last run in the panel

**Status:** Accepted (Kim, 2026-11-27). **Decides:** Kim.

### Context

- The recommendation above says the panel shows "the last lines of output through the agent". Only start, end, exit status and the next run are shown today [V: `internal/cronjobs/cronjobs.go`, `web/src/Cron.tsx`].
- A job's standard output and error go to the journal: the service unit sets no `StandardOutput=`, so systemd's default `journal` applies, with `SyslogIdentifier=hostashi-cron-<site>-<id>` [V: `internal/cron/unit.go`].
- Reading the journal needs either `journalctl` or a journal file reader. Neither is allowed today: `internal/phpctl` may run `systemctl` only for the cron units [V: `.golangci.yml`, `internal/phpctl/cron.go`].
- Kim chose the journal over a file per job (`StandardOutput=truncate:`), because journald limits size and rate itself and a file could fill `/var` [V: Kim, decision card in the thread, 2026-11-27].

### Proposal

1. New typed agent operation `cron.output` with `site` and job `id`. The agent checks that the job exists in its state, as `cron.run` does.
2. The agent reads `InvocationID` of `hostashi-cron-<site>-<id>.service` with the existing `systemctl show` exception. An empty value means the job has not run since boot or since its units were written; the answer is then "no output".
3. New narrow `os/exec` exception in `internal/phpctl/cron.go`: `/usr/bin/journalctl` by absolute path with exactly these arguments: `--no-pager --quiet --output=json --output-fields=MESSAGE --lines=200 _SYSTEMD_UNIT=<unit> _SYSTEMD_INVOCATION_ID=<id>`. The unit name must match the cron unit pattern and end in `.service`, the invocation id must be 32 lowercase hex digits. Both matches together select the lines the job's processes wrote in that run, not systemd's own "Started"/"Finished" lines [A: journald stores `_SYSTEMD_INVOCATION_ID` with stream lines since systemd 233; to be checked by the container test].
4. Limits: at most the last 200 lines, each cut to 2 KiB, and at most 64 KiB in total; the answer says whether anything was cut. A message that is not valid UTF-8 (journald stores it as a byte array) is shown with replacement characters. `journalctl` output is read up to 1 MiB, then parsing stops.
5. API: `GET /api/v1/sites/{name}/cron/{id}/output`, scope `cron:read`, with the run's start, end, result, the lines and `truncated`. Reading is not written to the audit log, like the site logs (ADR-0053).
6. UI: the "Last run" badge opens a dialog with the output in a monospace block and a copy button; the "More" menu gets "Show output" as well.

No database change: nothing is stored by the panel.

### Options considered

| Option | Pro | Contra |
|---|---|---|
| **A. `journalctl` with fixed arguments (recommended, chosen)** | Uses the journal that already has the output; journald caps size and rate; no change to the units | One more binary in the exec exception |
| B. `StandardOutput=truncate:` into a file per job | No new exec | A job printing gigabytes fills `/var` outside the site quota; a new file per job to clean up |
| C. Read journal files in Go | No exec | No maintained pure-Go reader; the format is not a stable API [U] |

### Consequences

- `.golangci.yml` names `journalctl` in the `internal/phpctl` comment; the package stays the only place for it.
- Who may see a job may see its output. A job that prints secrets shows them there; the same people can already read the command and the site's files [A].
- Output is available as long as journald keeps it. With volatile storage it is gone after a reboot; Debian 13 and Ubuntu 26.04 keep the journal on disk by default [A].
- journald rate limiting (by default 10,000 messages in 30 s per service) can drop lines of a very chatty job [U: journald.conf defaults].

