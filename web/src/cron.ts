// SPDX-License-Identifier: AGPL-3.0-or-later

// Cron jobs of a site (ADR-0028): each job is a systemd timer that runs one
// command line as the site user, in the site's home and slice. The panel
// database holds the jobs; every change is sent to the agent at once.

import { ApiError, request } from "./api";
import { formatDateTime } from "./format";
import i18n from "./i18n";
import { siteMessage } from "./sites";

export type CronStatus = {
  running: boolean;
  result?: string; // systemd's result of the last run: "success", "exit-code", "timeout", ...
  exitStatus?: number;
  lastStart?: number; // Unix seconds
  lastEnd?: number;
  next?: number;
};

export type CronJob = {
  id: number;
  schedule: string;
  command: string;
  timeoutSec: number; // 0: the default
  nice: number | null; // null: the default
  paused: boolean;
  createdAt: number;
  updatedAt: number;
  status?: CronStatus | undefined; // missing when the agent did not answer
};

export type CronList = { jobs: CronJob[]; statusKnown: boolean };

export type CronInput = { schedule: string; command: string; timeoutSec: number; nice: number | null; paused: boolean };

const cronPath = (site: string, id?: number) =>
  "/sites/" + encodeURIComponent(site) + "/cron" + (id === undefined ? "" : "/" + String(id));

export const listCron = (site: string) => request<CronList>("GET", cronPath(site));
export const createCron = (site: string, j: CronInput) => request<CronJob>("POST", cronPath(site), j);
export const updateCron = (site: string, id: number, j: CronInput) => request<CronJob>("PUT", cronPath(site, id), j);
export const deleteCron = (site: string, id: number) => request<void>("DELETE", cronPath(site, id));
export const runCron = (site: string, id: number) => request<void>("POST", cronPath(site, id) + "/run");

/**
 * CronOutput is what one run of a job wrote: the last run without a run id
 * (ADR-0028 Addendum 1), else one of the last runs (Addendum 2).
 */
export type CronOutput = { ran: boolean; lines: string[]; truncated: boolean };
export const cronOutput = (site: string, id: number, run?: string) =>
  request<CronOutput>("GET", cronPath(site, id) + "/output" + (run ? "?run=" + encodeURIComponent(run) : ""));

/** CronRun is one of the last runs of a job (ADR-0028 Addendum 2). Times are Unix seconds. */
export type CronRun = {
  id: string;
  start: number;
  end?: number; // missing while it runs or when the journal has no end
  running: boolean;
  result?: string; // like CronStatus.result; missing when unknown
  exitStatus?: number;
};
export const cronRuns = (site: string, id: number) => request<{ runs: CronRun[] }>("GET", cronPath(site, id) + "/runs");

/** runResult says in a word or two how a run went, for the list of runs. */
export function runResult(r: CronRun): string {
  if (r.running) return i18n.t("cron.runs.result.running");
  switch (r.result) {
    case undefined:
    case "":
      return i18n.t("cron.runs.result.unknown");
    case "success":
      return i18n.t("cron.runs.result.success");
    case "exit-code":
      return i18n.t("cron.runs.result.exitCode", { code: r.exitStatus ?? i18n.t("cron.last.unknownCode") });
    case "timeout":
      return i18n.t("cron.runs.result.timeout");
    case "signal":
    case "core-dump":
      return i18n.t("cron.runs.result.killed");
    default:
      return i18n.t("cron.runs.result.other", { result: r.result });
  }
}

/** runStatus describes a run like a job's last run, for lastRun and lastRunTone. */
export const runStatus = (r: CronRun): CronStatus => ({
  running: r.running,
  lastStart: r.start,
  ...(r.result === undefined ? {} : { result: r.result }),
  ...(r.exitStatus === undefined ? {} : { exitStatus: r.exitStatus }),
  ...(r.end === undefined ? {} : { lastEnd: r.end }),
});

/** runLabel describes one of the last runs, like lastRun a job's last run. */
export function runLabel(r: CronRun): string {
  if (!r.running && !r.result) return i18n.t("cron.last.unknown", { when: formatTime(r.start) });
  return lastRun(runStatus(r));
}

// inputOf turns a job back into what PUT expects, so a change keeps the
// fields it does not touch.
export const inputOf = (j: CronJob): CronInput => ({
  schedule: j.schedule,
  command: j.command,
  timeoutSec: j.timeoutSec,
  nice: j.nice,
  paused: j.paused,
});

// Mirrors of internal/cron so a form can say what is wrong before a
// request. The server checks again.
export const maxCronJobs = 50;
export const maxCommandBytes = 4096;
export const defaultTimeoutMin = 60;
export const maxTimeoutMin = 24 * 60;
export const defaultNice = 10;

const keywords = ["@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@yearly", "@annually"];
const months = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];
const weekdays = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

type FieldKey = "minute" | "hour" | "dayOfMonth" | "month" | "dayOfWeek";
type Field = { key: FieldKey; min: number; max: number; names?: string[] };

const fieldName = (f: Field) => i18n.t(`cron.fields.${f.key}`);

const fields: Field[] = [
  { key: "minute", min: 0, max: 59 },
  { key: "hour", min: 0, max: 23 },
  { key: "dayOfMonth", min: 1, max: 31 },
  { key: "month", min: 1, max: 12, names: months },
  { key: "dayOfWeek", min: 0, max: 7, names: weekdays },
];

function valueProblem(s: string, f: Field): string {
  if (s === "" || s.length > 3) return i18n.t("cron.problems.valueInvalid", { field: fieldName(f), value: s });
  if (f.names?.includes(s.toLowerCase())) return "";
  if (!/^[0-9]+$/.test(s)) return i18n.t("cron.problems.valueInvalid", { field: fieldName(f), value: s });
  const v = Number(s);
  if (v < f.min || v > f.max) return i18n.t("cron.problems.outside", { field: fieldName(f), value: v, min: f.min, max: f.max });
  return "";
}

const valueOf = (s: string, f: Field) => (f.names?.includes(s.toLowerCase()) ? f.min + f.names.indexOf(s.toLowerCase()) : Number(s));

function fieldProblem(p: string, f: Field): string {
  if (p === "*") return "";
  for (const item of p.split(",")) {
    const slash = item.indexOf("/");
    const rng = slash < 0 ? item : item.slice(0, slash);
    if (slash >= 0) {
      const step = item.slice(slash + 1);
      if (!/^[0-9]+$/.test(step) || Number(step) < 1 || Number(step) > f.max - f.min + 1) return i18n.t("cron.problems.stepInvalid", { field: fieldName(f), value: step });
    }
    if (rng === "*") continue;
    const dash = rng.indexOf("-");
    if (dash >= 0) {
      const a = rng.slice(0, dash);
      const b = rng.slice(dash + 1);
      const problem = valueProblem(a, f) || valueProblem(b, f);
      if (problem) return problem;
      if (valueOf(a, f) > valueOf(b, f)) return i18n.t("cron.problems.backwards", { field: fieldName(f), value: rng });
      continue;
    }
    const problem = valueProblem(rng, f);
    if (problem) return problem;
  }
  return "";
}

export function scheduleProblem(v: string): string {
  const s = v.trim();
  if (s === "") return i18n.t("cron.problems.empty");
  if (s.length > 200) return i18n.t("cron.problems.tooLong");
  if (s.startsWith("@")) {
    if (s.toLowerCase() === "@reboot") return i18n.t("cron.problems.reboot");
    return keywords.includes(s.toLowerCase()) ? "" : i18n.t("cron.problems.keyword", { value: s });
  }
  const parts = s.split(/\s+/);
  if (parts.length !== 5) return i18n.t("cron.problems.fiveFields");
  for (let i = 0; i < 5; i++) {
    const problem = fieldProblem(parts[i] ?? "", fields[i] as Field);
    if (problem) return problem;
  }
  return "";
}

export function commandProblem(c: string): string {
  if (c.trim() === "") return i18n.t("cron.problems.commandEmpty");
  if (new TextEncoder().encode(c).length > maxCommandBytes) return i18n.t("cron.problems.commandLong", { max: maxCommandBytes });
  // One line: no control characters other than tab, no line or paragraph separators.
  if (/[\u0000-\u0008\u000a-\u001f\u007f-\u009f\u2028\u2029]/.test(c)) return i18n.t("cron.problems.oneLine");
  return "";
}

export function timeoutProblem(min: number): string {
  if (!Number.isInteger(min) || min < 1 || min > maxTimeoutMin) return i18n.t("cron.problems.timeout", { max: maxTimeoutMin });
  return "";
}

export function niceProblem(n: number): string {
  if (!Number.isInteger(n) || n < 0 || n > 19) return i18n.t("cron.problems.nice");
  return "";
}

// describeSchedule says a few common schedules in words; others stay as
// written.
export function describeSchedule(s: string): string {
  const t = s.trim().toLowerCase();
  const named: Record<string, "hourly" | "daily" | "weekly" | "monthly" | "yearly" | "minute"> = {
    "@hourly": "hourly",
    "@daily": "daily",
    "@midnight": "daily",
    "@weekly": "weekly",
    "@monthly": "monthly",
    "@yearly": "yearly",
    "@annually": "yearly",
    "* * * * *": "minute",
  };
  const key = named[t];
  if (key) return i18n.t(`cron.describe.${key}`);
  let m = /^\*\/([0-9]+) \* \* \* \*$/.exec(t);
  if (m) return i18n.t("cron.describe.everyMinutes", { n: m[1] });
  m = /^([0-9]{1,2}) \* \* \* \*$/.exec(t);
  if (m) return i18n.t("cron.describe.hourAt", { minute: m[1] });
  m = /^([0-9]{1,2}) ([0-9]{1,2}) \* \* \*$/.exec(t);
  if (m) return i18n.t("cron.describe.dayAt", { time: `${(m[2] ?? "").padStart(2, "0")}:${(m[1] ?? "").padStart(2, "0")}` });
  return "";
}

// lastRun says how the last run went.
export function lastRun(st: CronStatus | undefined): string {
  if (!st) return "";
  if (st.running) return i18n.t("cron.last.running");
  if (!st.result || !st.lastStart) return i18n.t("cron.last.notRun");
  const when = formatTime(st.lastStart);
  switch (st.result) {
    case "success":
      return i18n.t("cron.last.success", { when });
    case "exit-code":
      return i18n.t("cron.last.exitCode", { when, code: st.exitStatus ?? i18n.t("cron.last.unknownCode") });
    case "timeout":
      return i18n.t("cron.last.timeout", { when });
    case "signal":
    case "core-dump":
      return i18n.t("cron.last.killed", { when });
    default:
      return i18n.t("cron.last.other", { when, result: st.result });
  }
}

// lastRunTone is the badge colour of lastRun: red for every failure (B6
// of the UX review), so a failing job stands out in the list.
export function lastRunTone(st: CronStatus | undefined): "ok" | "warn" | "err" | "off" {
  if (!st || !st.result || !st.lastStart) return "off";
  if (st.running) return "warn";
  return st.result === "success" ? "ok" : "err";
}

/** cronFailing reports a job whose last run failed. */
export const cronFailing = (st: CronStatus | undefined) => lastRunTone(st) === "err";

/** formatTime writes a Unix time in the panel's language: "27 Nov 2026, 14:05" or "27.11.2026, 14:05". */
export const formatTime = (unix: number) => formatDateTime(new Date(unix * 1000));

// cronMessage turns an API error into a sentence.
export function cronMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "run_not_found":
        return i18n.t("cron.errors.run_not_found");
      case "cron_unavailable":
      case "agent_precondition":
      case "site_busy":
      case "forbidden":
        return i18n.t(`cron.errors.${err.code}`);
      case "too_many_cron_jobs":
        return i18n.t("cron.errors.too_many_cron_jobs", { max: maxCronJobs });
      case "not_found":
        if (err.message.includes("cron")) return i18n.t("cron.errors.gone");
        break;
    }
  }
  return siteMessage(err);
}
