// SPDX-License-Identifier: AGPL-3.0-or-later

// Cron jobs of a site (ADR-0028): each job is a systemd timer that runs one
// command line as the site user, in the site's home and slice. The panel
// database holds the jobs; every change is sent to the agent at once.

import { ApiError, request } from "./api";
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

/** CronOutput is what the last run of a job wrote (ADR-0028 Addendum 1). */
export type CronOutput = { ran: boolean; lines: string[]; truncated: boolean };
export const cronOutput = (site: string, id: number) => request<CronOutput>("GET", cronPath(site, id) + "/output");

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

type Field = { name: string; min: number; max: number; names?: string[] };

const fields: Field[] = [
  { name: "minute", min: 0, max: 59 },
  { name: "hour", min: 0, max: 23 },
  { name: "day of month", min: 1, max: 31 },
  { name: "month", min: 1, max: 12, names: months },
  { name: "day of week", min: 0, max: 7, names: weekdays },
];

function valueProblem(s: string, f: Field): string {
  if (s === "" || s.length > 3) return `The ${f.name} "${s}" is not valid.`;
  if (f.names?.includes(s.toLowerCase())) return "";
  if (!/^[0-9]+$/.test(s)) return `The ${f.name} "${s}" is not valid.`;
  const v = Number(s);
  if (v < f.min || v > f.max) return `The ${f.name} ${v} is outside ${f.min}-${f.max}.`;
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
      if (!/^[0-9]+$/.test(step) || Number(step) < 1 || Number(step) > f.max - f.min + 1) return `The ${f.name} step "${step}" is not valid.`;
    }
    if (rng === "*") continue;
    const dash = rng.indexOf("-");
    if (dash >= 0) {
      const a = rng.slice(0, dash);
      const b = rng.slice(dash + 1);
      const problem = valueProblem(a, f) || valueProblem(b, f);
      if (problem) return problem;
      if (valueOf(a, f) > valueOf(b, f)) return `The ${f.name} range "${rng}" runs backwards.`;
      continue;
    }
    const problem = valueProblem(rng, f);
    if (problem) return problem;
  }
  return "";
}

export function scheduleProblem(v: string): string {
  const s = v.trim();
  if (s === "") return "Enter a schedule.";
  if (s.length > 200) return "The schedule is longer than 200 characters.";
  if (s.startsWith("@")) {
    if (s.toLowerCase() === "@reboot") return "@reboot is not supported. Use a time, like @daily.";
    return keywords.includes(s.toLowerCase()) ? "" : `"${s}" is not a known keyword. Use @hourly, @daily, @weekly, @monthly or @yearly.`;
  }
  const parts = s.split(/\s+/);
  if (parts.length !== 5) return "Use five fields (minute hour day-of-month month day-of-week), like */5 * * * *, or a keyword like @daily.";
  for (let i = 0; i < 5; i++) {
    const problem = fieldProblem(parts[i] ?? "", fields[i] as Field);
    if (problem) return problem;
  }
  return "";
}

export function commandProblem(c: string): string {
  if (c.trim() === "") return "Enter a command.";
  if (new TextEncoder().encode(c).length > maxCommandBytes) return `The command is longer than ${maxCommandBytes} bytes.`;
  // One line: no control characters other than tab, no line or paragraph separators.
  if (/[\u0000-\u0008\u000a-\u001f\u007f-\u009f\u2028\u2029]/.test(c)) return "Write the command on one line.";
  return "";
}

export function timeoutProblem(min: number): string {
  if (!Number.isInteger(min) || min < 1 || min > maxTimeoutMin) return `The time limit must be whole minutes from 1 to ${maxTimeoutMin}.`;
  return "";
}

export function niceProblem(n: number): string {
  if (!Number.isInteger(n) || n < 0 || n > 19) return "The priority must be a whole number from 0 to 19.";
  return "";
}

// describeSchedule says a few common schedules in words; others stay as
// written.
export function describeSchedule(s: string): string {
  const t = s.trim().toLowerCase();
  const named: Record<string, string> = {
    "@hourly": "every hour",
    "@daily": "every day at 00:00",
    "@midnight": "every day at 00:00",
    "@weekly": "every Sunday at 00:00",
    "@monthly": "on the 1st of every month at 00:00",
    "@yearly": "every 1 January at 00:00",
    "@annually": "every 1 January at 00:00",
    "* * * * *": "every minute",
  };
  if (named[t]) return named[t];
  let m = /^\*\/([0-9]+) \* \* \* \*$/.exec(t);
  if (m) return `every ${m[1]} minutes`;
  m = /^([0-9]{1,2}) \* \* \* \*$/.exec(t);
  if (m) return `every hour at minute ${m[1]}`;
  m = /^([0-9]{1,2}) ([0-9]{1,2}) \* \* \*$/.exec(t);
  if (m) return `every day at ${(m[2] ?? "").padStart(2, "0")}:${(m[1] ?? "").padStart(2, "0")}`;
  return "";
}

// lastRun says how the last run went.
export function lastRun(st: CronStatus | undefined): string {
  if (!st) return "";
  if (st.running) return "Running now";
  if (!st.result || !st.lastStart) return "Not run yet";
  const when = formatTime(st.lastStart);
  switch (st.result) {
    case "success":
      return `Succeeded ${when}`;
    case "exit-code":
      return `Failed ${when} with exit code ${st.exitStatus ?? "unknown"}`;
    case "timeout":
      return `Stopped ${when}: it ran longer than its time limit`;
    case "signal":
    case "core-dump":
      return `Killed ${when}`;
    default:
      return `Failed ${when} (${st.result})`;
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

export const formatTime = (unix: number) =>
  new Date(unix * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });

// cronMessage turns an API error into a sentence.
export function cronMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "cron_unavailable":
        return "Cron jobs are not available on this panel.";
      case "too_many_cron_jobs":
        return `A site has at most ${maxCronJobs} cron jobs. Delete one first.`;
      case "agent_precondition":
        return "The site is disabled. Enable it to run its cron jobs.";
      case "site_busy":
        return "The site is not active right now. Reload the page in a moment.";
      case "forbidden":
        return "Only administrators can manage cron jobs.";
      case "not_found":
        if (err.message.includes("cron")) return "This cron job does not exist any more. Reload the page.";
        break;
    }
  }
  return siteMessage(err);
}
