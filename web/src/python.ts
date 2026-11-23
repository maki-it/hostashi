// SPDX-License-Identifier: AGPL-3.0-or-later

// Python (ADR-0061): the CPython lines Hostashi installs from
// python-build-standalone, and the app of a Python site.

import { ApiError, request } from "./api";
import { argsProblem, quoteArg, workdirProblem, type AppStatus } from "./node";
import { siteMessage, type Site } from "./sites";

export type PythonRelease = { line: string; version: string; build: string; end: string };

export type PythonStatus = {
  installed: { line: string; version: string; build: string }[];
  /** The line of the running or last installation, "" for none. */
  last: string;
  state: "idle" | "running" | "succeeded" | "failed";
  log: string[];
  releases: PythonRelease[];
};

export const getPython = () => request<PythonStatus>("GET", "/python");
export const installPython = (line: string) => request<PythonStatus>("POST", "/python/install", { line });
export const removePython = (line: string) => request<PythonStatus>("POST", "/python/remove", { line });

export type PythonStartKind = "" | "wsgi" | "asgi" | "script";

/** The app of a Python site; port and stopped are the panel's. */
export type PythonApp = { version: string; workdir: string; startKind: PythonStartKind; target: string; args: string[]; port: number; stopped: boolean };

export type PythonValue = Pick<PythonApp, "version" | "workdir" | "startKind" | "target" | "args">;

const sitePath = (name: string) => "/sites/" + encodeURIComponent(name);

/** confirm is the site's name; it is needed when the version changes. */
export const setPythonApp = (name: string, a: PythonValue, confirm = "") =>
  request<{ site: Site; status: AppStatus }>("PUT", sitePath(name) + "/python", confirm ? { ...a, confirm } : a);
export const recreateVenv = (name: string) => request<AppStatus>("POST", sitePath(name) + "/python/venv", { confirm: name });

/** Lines sort by their minor version, newest first. */
const byLine = (a: string, b: string) => Number(b.split(".")[1]) - Number(a.split(".")[1]);

/** The installed lines, newest first. */
export const installedPythonLines = (s: PythonStatus | null) => (s?.installed ?? []).map((i) => i.line).sort(byLine);

/** The newest installed line, "" without one. */
export const newestPythonLine = (s: PythonStatus | null) => installedPythonLines(s)[0] ?? "";

export const sortPythonLines = (lines: string[]) => [...lines].sort(byLine);

const targetRe = /^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$/;
const fileRe = /^[A-Za-z0-9._-]+(\/[A-Za-z0-9._-]+)*$/;
const bindArgs = ["-b", "--bind", "--host", "--port", "--fd", "--uds"];

/** targetProblem mirrors agentapi.ValidatePythonStart. */
export function targetProblem(kind: PythonStartKind, target: string): string {
  if (kind === "") return "";
  if (kind === "script") {
    if (!target.endsWith(".py")) return "The start file must end in .py.";
    if (!fileRe.test(target) || target.split("/").some((p) => p === "." || p === ".."))
      return "Use a file below the working directory: letters, digits, . _ - and /, no .. and no leading /.";
    return "";
  }
  if (target.length > 255 || !targetRe.test(target)) return "Enter module.path:callable, for example mysite.wsgi:application or main:app.";
  return "";
}

/** pythonArgsProblem mirrors agentapi.ValidatePythonArgs. */
export function pythonArgsProblem(kind: PythonStartKind, args: string[]): string {
  const p = argsProblem(args);
  if (p) return p;
  if (kind === "") return args.length > 0 ? "Arguments need a start command." : "";
  if (kind === "script") return "";
  for (const a of args)
    for (const o of bindArgs)
      if (a === o || a.startsWith(o + "=") || (o.length === 2 && a.startsWith(o)))
        return `${o} is set by Hostashi: the app listens on 127.0.0.1 and its own port.`;
  return "";
}

/** pythonProblem checks the fields before they are sent. */
export function pythonProblem(v: PythonValue): string {
  if (!v.version) return "No Python version is installed. An administrator installs one under Python versions.";
  return workdirProblem(v.workdir) || targetProblem(v.startKind, v.target) || pythonArgsProblem(v.startKind, v.args);
}

/** The command the app runs, as shown in the panel; the address is Hostashi's. */
export function pythonCommand(a: Pick<PythonApp, "startKind" | "target" | "args">, port?: number): string {
  const args = a.args.map(quoteArg);
  const addr = port ? `127.0.0.1:${port}` : "127.0.0.1:PORT";
  switch (a.startKind) {
    case "wsgi":
      return ["gunicorn", "--bind", addr, ...args, a.target].join(" ");
    case "asgi":
      return ["uvicorn", "--host", "127.0.0.1", "--port", port ? String(port) : "PORT", ...args, a.target].join(" ");
    case "script":
      return ["python", a.target, ...args].join(" ");
  }
  return "";
}

export function pythonMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return "Only administrators can install Python.";
      case "python_install_refused":
        if (err.message.includes("apps still run")) return "Sites still run on this version. Move them to another version first.";
        if (err.message.includes("not installed")) return "This version is not installed.";
        return err.message.includes("running") ? "Another installation is still running. Wait until it is done." : "The installation could not start.";
      case "agent_precondition":
        if (err.message.includes("environment could not be created")) return "The environment .venv could not be created: " + err.message.replace(/^.*could not be created: ?/, "");
        break;
    }
  }
  return siteMessage(err);
}
