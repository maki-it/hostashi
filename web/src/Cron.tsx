// SPDX-License-Identifier: AGPL-3.0-or-later

import { DotsThree, Pause, PencilSimple, Play, Plus, Terminal, Trash, X } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import i18n from "./i18n";
import {
  commandProblem,
  createCron,
  cronMessage,
  cronOutput,
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
  type CronOutput,
} from "./cron";
import type { Site } from "./sites";
import { Badge, Button, Card, CopyButton, Field, Menu, Notice, RowActions, type MenuItem } from "./ui";

// CronSettings lists the cron jobs of a site and adds, changes, pauses,
// runs and deletes them. Jobs run as the site user in its home.
export function CronSettings({ site }: { site: Site }) {
  const { t } = useTranslation();
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
    const timer = setInterval(() => void load(), 5000);
    return () => clearInterval(timer);
  }, [running, load]);

  const changed = async (msg: string) => {
    setDone(msg);
    await load();
  };

  return (
    <Card
      title={t("cron.title")}
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
            {t("cron.add")}
          </Button>
        ) : undefined
      }
    >
      <div className="ui-stack">
        <p className="ui-flush">{t("cron.intro", { user: site.name })}</p>
        {site.disabled && <Notice tone="warn" title={t("cron.siteDisabled")} />}
        {!list ? (
          loadError ? (
            <Notice tone="err" title={loadError} />
          ) : (
            <p aria-busy="true">{t("cron.loading")}</p>
          )
        ) : (
          <>
            {loadError && <Notice tone="err" title={loadError} />}
            {adding && (
              <JobForm
                title={t("cron.newTitle")}
                submitLabel={t("cron.add")}
                onCancel={() => setAdding(false)}
                onSubmit={async (input) => {
                  await createCron(site.name, input);
                  setAdding(false);
                  await changed(input.paused ? t("cron.addedPaused") : t("cron.added"));
                }}
              />
            )}
            {list.jobs.length === 0 ? (
              <p className="ui-hint ui-flush">{t("cron.none")}</p>
            ) : (
              <>
                {!list.statusKnown && <p className="ui-hint ui-flush">{t("cron.statusUnknown")}</p>}
                {/* No scroll box around the table: it would cut off the More menu. */}
                <div>
                  <table className="ui-table ui-table-actions-below" aria-label={t("cron.tableAria", { site: site.name })}>
                    <thead>
                      <tr>
                        <th scope="col">{t("cron.columns.schedule")}</th>
                        <th scope="col">{t("cron.columns.command")}</th>
                        <th scope="col">{t("cron.columns.lastRun")}</th>
                        <th scope="col">{t("cron.columns.nextRun")}</th>
                        <th scope="col">
                          <span className="ui-sr-only">{t("cron.columns.actions")}</span>
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
            {list.jobs.length >= maxCronJobs && <p className="ui-hint ui-flush">{t("cron.max", { max: maxCronJobs })}</p>}
          </>
        )}
      </div>
    </Card>
  );
}

function JobRow({ site, job, onChange }: { site: Site; job: CronJob; onChange: (msg: string) => Promise<void> }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [showOutput, setShowOutput] = useState(false);

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
            title={t("cron.changeTitle", { id: job.id })}
            submitLabel={t("cron.save")}
            initial={job}
            onCancel={() => setEditing(false)}
            onSubmit={async (input) => {
              await updateCron(site.name, job.id, input);
              setEditing(false);
              await onChange(t("cron.saved"));
            }}
          />
        </td>
      </tr>
    );
  }

  const run = () =>
    void act(async () => {
      await runCron(site.name, job.id);
      return t("cron.started");
    });
  const togglePause = () =>
    void act(async () => {
      await updateCron(site.name, job.id, { ...inputOf(job), paused: !job.paused });
      return job.paused ? t("cron.resumed") : t("cron.pausedDone");
    });
  const hasRun = Boolean(st?.lastStart);
  const more: MenuItem[] = [
    ...(hasRun
      ? [{ label: showOutput ? t("cron.menu.hideOutput") : t("cron.menu.showOutput"), icon: <Terminal size={16} aria-hidden="true" />, onSelect: () => setShowOutput(!showOutput) }]
      : []),
    { label: t("cron.menu.change"), icon: <PencilSimple size={16} aria-hidden="true" />, onSelect: () => setEditing(true) },
    { label: job.paused ? t("cron.menu.resume") : t("cron.menu.pause"), icon: job.paused ? <Play size={16} aria-hidden="true" /> : <Pause size={16} aria-hidden="true" />, onSelect: togglePause },
    { label: t("cron.menu.delete"), icon: <Trash size={16} aria-hidden="true" />, danger: true, onSelect: () => setConfirming(true) },
  ];

  return (
    <>
      <tr aria-label={t("cron.detailsAria", { id: job.id })}>
        <td>
          <div className="ui-stack-tight">
            <span className="ui-row ui-row-tight">
              <code>{job.schedule}</code>
              {job.paused && <Badge tone="warn">{t("cron.paused")}</Badge>}
            </span>
            {words && <span className="ui-hint">{words}</span>}
          </div>
        </td>
        <td data-label={t("cron.columns.command")}>
          <div className="ui-stack-tight">
            <code className="ui-cron-cmd">{job.command}</code>
            <span className="ui-hint">{t("cron.limits", { minutes: limit, nice: job.nice ?? defaultNice })}</span>
          </div>
        </td>
        <td data-label={t("cron.columns.lastRun")}>
          {st && st.result && st.lastStart && !st.running ? (
            <button
              type="button"
              className="ui-cron-last"
              aria-expanded={showOutput}
              title={showOutput ? t("cron.hideRun") : t("cron.showRun")}
              onClick={() => setShowOutput(!showOutput)}
            >
              <Badge tone={lastRunTone(st)}>{lastRun(st)}</Badge>
            </button>
          ) : st ? (
            lastRun(st)
          ) : (
            t("cron.unknown")
          )}
        </td>
        <td data-label={t("cron.columns.nextRun")}>
          {job.paused ? t("cron.paused") : site.disabled ? t("cron.nextSiteDisabled") : st?.next ? formatTime(st.next) : st ? t("cron.notPlanned") : t("cron.unknown")}
        </td>
        <td data-actions="">
          <RowActions>
            <Button small icon={<Play size={14} aria-hidden="true" />} disabled={busy || site.disabled || st?.running === true} onClick={run}>
              {t("cron.runNow")}
            </Button>
            <Menu
              label={t("cron.actionsLabel", { id: job.id })}
              buttonLabel={t("cron.moreLabel", { id: job.id })}
              buttonClass="ui-btn ui-btn-sm"
              button={
                <>
                  <DotsThree size={14} aria-hidden="true" />
                  {t("cron.more")}
                </>
              }
              items={more}
            />
          </RowActions>
        </td>
      </tr>
      {showOutput && (
        <tr>
          <td colSpan={5}>
            <JobOutput site={site} job={job} onClose={() => setShowOutput(false)} />
          </td>
        </tr>
      )}
      {(confirming || error) && (
        <tr>
          <td colSpan={5}>
            {confirming && (
              <div className="ui-row" role="alertdialog" aria-label={t("cron.deleteAria", { id: job.id })}>
                <span>
                  <Trans i18nKey="cron.deleteConfirm" values={{ command: job.command }} components={{ code: <code /> }} />
                </span>
                <Button
                  variant="danger"
                  disabled={busy}
                  onClick={() =>
                    void act(async () => {
                      await deleteCron(site.name, job.id);
                      return t("cron.deleted");
                    })
                  }
                >
                  {t("cron.deleteButton")}
                </Button>
                <Button disabled={busy} onClick={() => setConfirming(false)}>
                  {t("common.actions.cancel")}
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

// JobOutput shows what the last run of a job wrote, as the journal kept
// it, below the job's row: how the run went, then the lines with numbers,
// scrolled to the end where errors usually are. It loads again when a new
// run has ended.
function JobOutput({ site, job, onClose }: { site: Site; job: CronJob; onClose: () => void }) {
  const { t } = useTranslation();
  const [out, setOut] = useState<CronOutput | null>(null);
  const [error, setError] = useState("");
  const [wrap, setWrap] = useState(true);
  const st = job.status;
  const runKey = `${st?.lastStart ?? 0}-${st?.running ? 1 : 0}`;
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    setError("");
    cronOutput(site.name, job.id).then(
      (o) => live && setOut(o),
      (err: unknown) => live && setError(cronMessage(err)),
    );
    return () => {
      live = false;
    };
  }, [site.name, job.id, runKey]);

  useEffect(() => {
    const el = box.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [out]);

  const lines = out?.lines ?? [];
  const text = lines.join("\n");
  const label = t("cron.output.aria", { id: job.id });
  const tone = lastRunTone(st);
  const took = st?.lastStart && st.lastEnd && st.lastEnd >= st.lastStart ? runDuration(st.lastEnd - st.lastStart) : "";

  return (
    <section className={"ui-cron-output ui-cron-output-" + tone} aria-label={label}>
      <header className="ui-cron-output-head">
        <div className="ui-stack-tight">
          <strong>{t("cron.output.title")}</strong>
          <span className="ui-hint">
            {[
              st?.running ? t("cron.last.running") : lastRun(st),
              took && t("cron.output.took", { time: took }),
              out?.ran && lines.length > 0 && (out.truncated ? t("cron.output.linesCut", { count: lines.length }) : t("cron.output.lines", { count: lines.length })),
            ]
              .filter(Boolean)
              .join(" · ")}
          </span>
        </div>
        <span className="ui-row ui-row-tight">
          {lines.length > 0 && (
            <Button small aria-pressed={wrap} onClick={() => setWrap(!wrap)}>
              {t("cron.output.wrap")}
            </Button>
          )}
          {text && <CopyButton text={text} label={t("cron.output.copy")} />}
          <Button small icon={<X size={14} aria-hidden="true" />} onClick={onClose}>
            {t("cron.output.close")}
          </Button>
        </span>
      </header>
      {error ? (
        <Notice tone="err" title={error} />
      ) : !out ? (
        <p aria-busy="true" className="ui-cron-output-empty">
          {t("cron.output.loading")}
        </p>
      ) : !out.ran ? (
        <p className="ui-cron-output-empty">{t("cron.output.notRan")}</p>
      ) : lines.length === 0 ? (
        <p className="ui-cron-output-empty">{t("cron.output.empty")}</p>
      ) : (
        <div ref={box} className={"ui-cron-output-lines" + (wrap ? "" : " ui-cron-output-nowrap")} role="log" aria-label={label} tabIndex={0}>
          {out.truncated && <div className="ui-cron-output-cut">{t("cron.output.cut")}</div>}
          <ol>
            {lines.map((l, i) => (
              <li key={i} className={lineClass(l)}>
                {l || "\u00a0"}
              </li>
            ))}
          </ol>
        </div>
      )}
      <p className="ui-hint ui-flush">{t("cron.output.footer")}</p>
    </section>
  );
}

// lineClass marks lines that read like errors or warnings, so they stand
// out in a long output. Only a hint: the text is shown as written.
function lineClass(l: string): string | undefined {
  if (/\b(warning|deprecated)\b/i.test(l)) return "ui-cron-line-warn";
  if (/\b(error|fatal|failed|exception|denied|traceback)\b/i.test(l)) return "ui-cron-line-err";
  return undefined;
}

function runDuration(s: number): string {
  const t = i18n.t;
  if (s < 60) return t("cron.duration.s", { s });
  if (s < 3600) return s % 60 === 0 ? t("cron.duration.min", { min: s / 60 }) : t("cron.duration.minS", { min: Math.floor(s / 60), s: s % 60 });
  const m = Math.round(s / 60);
  return m % 60 === 0 ? t("cron.duration.h", { h: m / 60 }) : t("cron.duration.hMin", { h: Math.floor(m / 60), min: m % 60 });
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
  const { t } = useTranslation();
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
        label={t("cron.form.schedule")}
        hint={t("cron.form.scheduleHint")}
      >
        <input className="ui-input ui-mono" value={schedule} onChange={(e) => setSchedule(e.target.value)} autoComplete="off" spellCheck={false} />
      </Field>
      {words && <p className="ui-hint ui-flush">{t("cron.form.runs", { words })}</p>}
      <Field label={t("cron.form.command")} hint={t("cron.form.commandHint")}>
        <input className="ui-input ui-mono" value={command} onChange={(e) => setCommand(e.target.value)} autoComplete="off" spellCheck={false} />
      </Field>
      <div className="ui-narrow ui-stack">
        <Field label={t("cron.form.timeLimit")} hint={t("cron.form.timeLimitHint", { max: maxTimeoutMin })}>
          <input className="ui-input" type="number" min={1} max={maxTimeoutMin} value={timeout} onChange={(e) => setTimeoutMin(e.target.value)} />
        </Field>
        <Field label={t("cron.form.priority")} hint={t("cron.form.priorityHint")}>
          <input className="ui-input" type="number" min={0} max={19} value={nice} onChange={(e) => setNice(e.target.value)} />
        </Field>
      </div>
      <label className="ui-check">
        <input type="checkbox" checked={paused} onChange={(e) => setPaused(e.target.checked)} />
        {t("cron.form.paused")}
      </label>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? t("common.status.saving") : submitLabel}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          {t("common.actions.cancel")}
        </Button>
      </div>
    </form>
  );
}
