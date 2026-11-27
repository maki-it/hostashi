// SPDX-License-Identifier: AGPL-3.0-or-later

import { DotsThree, Pause, PencilSimple, Play, Plus, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  commandProblem,
  createCron,
  cronMessage,
  defaultNice,
  defaultTimeoutMin,
  deleteCron,
  describeSchedule,
  formatTime,
  inputOf,
  lastRun,
  lastRunTone,
  listCron,
  maxCronJobs,
  maxTimeoutMin,
  niceProblem,
  runCron,
  scheduleProblem,
  timeoutProblem,
  updateCron,
  type CronInput,
  type CronJob,
  type CronList,
} from "./cron";
import type { Site } from "./sites";
import { Badge, Button, Card, Field, Menu, Notice, RowActions, type MenuItem } from "./ui";

// CronSettings lists the cron jobs of a site and adds, changes, pauses,
// runs and deletes them. Jobs run as the site user in its home.
export function CronSettings({ site }: { site: Site }) {
  const [list, setList] = useState<CronList | null>(null);
  const [loadError, setLoadError] = useState("");
  const [adding, setAdding] = useState(false);
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      setList(await listCron(site.name));
      setLoadError("");
    } catch (err) {
      setLoadError(cronMessage(err));
    }
  }, [site.name]);

  useEffect(() => {
    void load();
  }, [load]);

  // A started run shows up in the status a moment later: look again while
  // a job runs.
  const running = list?.jobs.some((j) => j.status?.running) ?? false;
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void load(), 5000);
    return () => clearInterval(t);
  }, [running, load]);

  const changed = async (msg: string) => {
    setDone(msg);
    await load();
  };

  return (
    <Card
      title="Cron jobs"
      actions={
        list && !adding && list.jobs.length < maxCronJobs ? (
          <Button
            variant="primary"
            icon={<Plus size={16} aria-hidden="true" />}
            onClick={() => {
              setDone("");
              setAdding(true);
            }}
          >
            Add cron job
          </Button>
        ) : undefined
      }
    >
      <div className="ui-stack">
        <p className="ui-flush">
          Each job runs one command line as the user {site.name} in its home directory, with lower priority than the site. A “%” is passed to
          the shell as written, unlike in a crontab.
        </p>
        {site.disabled && <Notice tone="warn" title="The site is disabled: its cron jobs do not run until it is enabled." />}
        {!list ? (
          loadError ? (
            <Notice tone="err" title={loadError} />
          ) : (
            <p aria-busy="true">Loading cron jobs…</p>
          )
        ) : (
          <>
            {loadError && <Notice tone="err" title={loadError} />}
            {adding && (
              <JobForm
                title="New cron job"
                submitLabel="Add cron job"
                onCancel={() => setAdding(false)}
                onSubmit={async (input) => {
                  await createCron(site.name, input);
                  setAdding(false);
                  await changed(input.paused ? "Cron job added, paused." : "Cron job added.");
                }}
              />
            )}
            {list.jobs.length === 0 ? (
              <p className="ui-hint ui-flush">No cron jobs yet.</p>
            ) : (
              <>
                {!list.statusKnown && <p className="ui-hint ui-flush">Last and next runs are not known right now: the server did not answer.</p>}
                {/* No scroll box around the table: it would cut off the More menu. */}
                <div>
                  <table className="ui-table ui-table-actions-below" aria-label={"Cron jobs of " + site.name}>
                    <thead>
                      <tr>
                        <th scope="col">Schedule</th>
                        <th scope="col">Command</th>
                        <th scope="col">Last run</th>
                        <th scope="col">Next run</th>
                        <th scope="col">
                          <span className="ui-sr-only">Actions</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {list.jobs.map((j) => (
                        <JobRow key={j.id} site={site} job={j} onChange={changed} />
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
            {done && <Notice tone="ok" title={done} />}
            {list.jobs.length >= maxCronJobs && <p className="ui-hint ui-flush">A site has at most {maxCronJobs} cron jobs.</p>}
          </>
        )}
      </div>
    </Card>
  );
}

function JobRow({ site, job, onChange }: { site: Site; job: CronJob; onChange: (msg: string) => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    try {
      await onChange(await f());
    } catch (err) {
      setError(cronMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const words = describeSchedule(job.schedule);
  const st = job.status;
  const limit = job.timeoutSec === 0 ? defaultTimeoutMin : Math.round(job.timeoutSec / 60);

  if (editing) {
    return (
      <tr>
        <td colSpan={5}>
          <JobForm
            title={"Change cron job " + String(job.id)}
            submitLabel="Save cron job"
            initial={job}
            onCancel={() => setEditing(false)}
            onSubmit={async (input) => {
              await updateCron(site.name, job.id, input);
              setEditing(false);
              await onChange("Cron job saved.");
            }}
          />
        </td>
      </tr>
    );
  }

  const run = () =>
    void act(async () => {
      await runCron(site.name, job.id);
      return "The cron job started. Its result shows up under “Last run”.";
    });
  const togglePause = () =>
    void act(async () => {
      await updateCron(site.name, job.id, { ...inputOf(job), paused: !job.paused });
      return job.paused ? "Cron job resumed." : "Cron job paused. It runs only when started by hand.";
    });
  const more: MenuItem[] = [
    { label: "Change", icon: <PencilSimple size={16} aria-hidden="true" />, onSelect: () => setEditing(true) },
    { label: job.paused ? "Resume" : "Pause", icon: job.paused ? <Play size={16} aria-hidden="true" /> : <Pause size={16} aria-hidden="true" />, onSelect: togglePause },
    { label: "Delete", icon: <Trash size={16} aria-hidden="true" />, danger: true, onSelect: () => setConfirming(true) },
  ];

  return (
    <>
      <tr aria-label={"Details of cron job " + String(job.id)}>
        <td>
          <div className="ui-stack-tight">
            <span className="ui-row ui-row-tight">
              <code>{job.schedule}</code>
              {job.paused && <Badge tone="warn">Paused</Badge>}
            </span>
            {words && <span className="ui-hint">{words}</span>}
          </div>
        </td>
        <td data-label="Command">
          <div className="ui-stack-tight">
            <code className="ui-cron-cmd">{job.command}</code>
            <span className="ui-hint">
              Limits: {limit} min, priority {job.nice ?? defaultNice}
            </span>
          </div>
        </td>
        <td data-label="Last run">
          {st && st.result && st.lastStart && !st.running ? <Badge tone={lastRunTone(st)}>{lastRun(st)}</Badge> : st ? lastRun(st) : "Unknown"}
        </td>
        <td data-label="Next run">{job.paused ? "Paused" : site.disabled ? "Site disabled" : st?.next ? formatTime(st.next) : st ? "Not planned" : "Unknown"}</td>
        <td data-actions="">
          <RowActions>
            <Button small icon={<Play size={14} aria-hidden="true" />} disabled={busy || site.disabled || st?.running === true} onClick={run}>
              Run now
            </Button>
            <Menu
              label={"Actions for cron job " + String(job.id)}
              buttonLabel={"More actions for cron job " + String(job.id)}
              buttonClass="ui-btn ui-btn-sm"
              button={
                <>
                  <DotsThree size={14} aria-hidden="true" />
                  More
                </>
              }
              items={more}
            />
          </RowActions>
        </td>
      </tr>
      {(confirming || error) && (
        <tr>
          <td colSpan={5}>
            {confirming && (
              <div className="ui-row" role="alertdialog" aria-label={"Delete cron job " + String(job.id)}>
                <span>
                  Delete the cron job <code>{job.command}</code>?
                </span>
                <Button
                  variant="danger"
                  disabled={busy}
                  onClick={() =>
                    void act(async () => {
                      await deleteCron(site.name, job.id);
                      return "Cron job deleted.";
                    })
                  }
                >
                  Delete cron job
                </Button>
                <Button disabled={busy} onClick={() => setConfirming(false)}>
                  Cancel
                </Button>
              </div>
            )}
            {error && <Notice tone="err" title={error} />}
          </td>
        </tr>
      )}
    </>
  );
}

function JobForm({
  title,
  submitLabel,
  initial,
  onSubmit,
  onCancel,
}: {
  title: string;
  submitLabel: string;
  initial?: CronJob;
  onSubmit: (input: CronInput) => Promise<void>;
  onCancel: () => void;
}) {
  const [schedule, setSchedule] = useState(initial?.schedule ?? "");
  const [command, setCommand] = useState(initial?.command ?? "");
  const [timeout, setTimeoutMin] = useState(
    String(initial && initial.timeoutSec > 0 ? Math.round(initial.timeoutSec / 60) : defaultTimeoutMin),
  );
  const [nice, setNice] = useState(String(initial?.nice ?? defaultNice));
  const [paused, setPaused] = useState(initial?.paused ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const words = describeSchedule(schedule);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const min = Number(timeout);
    const n = Number(nice);
    const problem =
      scheduleProblem(schedule) || commandProblem(command) || (timeout.trim() === "" ? timeoutProblem(NaN) : timeoutProblem(min)) || (nice.trim() === "" ? niceProblem(NaN) : niceProblem(n));
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      await onSubmit({
        schedule: schedule.trim(),
        command,
        // The defaults stay defaults, so a later change of them applies.
        timeoutSec: min === defaultTimeoutMin && (initial?.timeoutSec ?? 0) === 0 ? 0 : min * 60,
        nice: n === defaultNice && (initial?.nice ?? null) === null ? null : n,
        paused,
      });
    } catch (err) {
      setError(cronMessage(err));
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate aria-label={title}>
      <strong>{title}</strong>
      <Field
        label="Schedule"
        hint="Five cron fields (minute hour day-of-month month day-of-week), like */15 * * * *, or @hourly, @daily, @weekly, @monthly."
      >
        <input className="ui-input ui-mono" value={schedule} onChange={(e) => setSchedule(e.target.value)} autoComplete="off" spellCheck={false} />
      </Field>
      {words && <p className="ui-hint ui-flush">Runs {words}, in the server's time zone.</p>}
      <Field label="Command" hint="One line, run by the shell. For example: php htdocs/wp-cron.php">
        <input className="ui-input ui-mono" value={command} onChange={(e) => setCommand(e.target.value)} autoComplete="off" spellCheck={false} />
      </Field>
      <div className="ui-narrow ui-stack">
        <Field label="Time limit in minutes" hint={`A run that takes longer is stopped. 1 to ${maxTimeoutMin}.`}>
          <input className="ui-input" type="number" min={1} max={maxTimeoutMin} value={timeout} onChange={(e) => setTimeoutMin(e.target.value)} />
        </Field>
        <Field label="Priority (nice)" hint="0 to 19; higher numbers leave more CPU to the site.">
          <input className="ui-input" type="number" min={0} max={19} value={nice} onChange={(e) => setNice(e.target.value)} />
        </Field>
      </div>
      <label className="ui-check">
        <input type="checkbox" checked={paused} onChange={(e) => setPaused(e.target.checked)} />
        Paused: runs only when started by hand
      </label>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? "Saving…" : submitLabel}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
