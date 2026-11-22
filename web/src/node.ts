// SPDX-License-Identifier: AGPL-3.0-or-later

// Node.js (ADR-0054): the LTS lines Hostashi installs from nodejs.org, and
// the app of a Node.js site.

import { ApiError, request } from "./api";
import { siteMessage, type Site } from "./sites";

export type NodeRelease = { major: string; version: string; codename: string; end: string };

export type NodeStatus = {
  installed: { major: string; version: string }[];
  /** The line of the running or last installation, "" for none. */
  last: string;
  state: "idle" | "running" | "succeeded" | "failed";
  log: string[];
  releases: NodeRelease[];
};

export const getNode = () => request<NodeStatus>("GET", "/node");
export const installNode = (major: string) => request<NodeStatus>("POST", "/node/install", { major });
export const removeNode = (major: string) => request<NodeStatus>("POST", "/node/remove", { major });

export type StartKind = "" | "script" | "npm";

/** The app of a Node.js site; port and stopped are the panel's. */
export type NodeApp = { version: string; workdir: string; startKind: StartKind; start: string; port: number; stopped: boolean };

/** The program of an app site (ADR-0054 addendum 2); port and stopped are the panel's. */
export type ProgramApp = { workdir: string; program: string; args: string[]; port: number; stopped: boolean };

/** problem says why the program of an app site is not started, "" otherwise. */
export type AppStatus = { state: string; subState: string; restarts: number; usedBytes: number; since: string; problem?: string };

export type AppAction = "start" | "stop" | "restart";

const sitePath = (name: string) => "/sites/" + encodeURIComponent(name);

export const setNodeApp = (name: string, a: Pick<NodeApp, "version" | "workdir" | "startKind" | "start">) =>
  request<{ site: Site; status: AppStatus }>("PUT", sitePath(name) + "/node", a);
export const setProgramApp = (name: string, a: Pick<ProgramApp, "workdir" | "program" | "args">) =>
  request<{ site: Site; status: AppStatus }>("PUT", sitePath(name) + "/app", a);
export const getAppStatus = (name: string) => request<AppStatus>("GET", sitePath(name) + "/app");
export const appAction = (name: string, action: AppAction) => request<AppStatus>("POST", sitePath(name) + "/app/" + action);

/** The lines that can run an app, newest first. */
export const installedLines = (s: NodeStatus | null) =>
  (s?.installed ?? []).map((i) => i.major).sort((a, b) => Number(b) - Number(a));

/** The newest installed line, "" without one. */
export const newestLine = (s: NodeStatus | null) => installedLines(s)[0] ?? "";

const workdirRe = /^[A-Za-z0-9._-]+(\/[A-Za-z0-9._-]+)*$/;

/** workdirProblem mirrors agentapi.ValidateAppWorkdir: a folder below the home. */
export function workdirProblem(dir: string): string {
  if (!dir) return "Enter the working directory, for example htdocs.";
  if (dir.length > 255) return "The folder name is too long.";
  if (!workdirRe.test(dir) || dir.split("/").some((p) => p === "." || p === ".."))
    return "Use a folder below the home: letters, digits, . _ - and /, no .. and no leading /.";
  return "";
}

/** startProblem mirrors agentapi.ValidateAppStart. */
export function startProblem(kind: StartKind, start: string): string {
  if (kind === "") return "";
  if (kind === "npm") {
    if (!/^[a-z0-9:_-]{1,64}$/.test(start)) return "Enter the name of an npm script, for example start: lowercase letters, digits and : _ -.";
    return "";
  }
  if (!/\.(js|mjs|cjs)$/.test(start)) return "The start file must end in .js, .mjs or .cjs.";
  if (!workdirRe.test(start) || start.split("/").some((p) => p === "." || p === ".."))
    return "Use a file below the working directory: letters, digits, . _ - and /, no .. and no leading /.";
  return "";
}

/** programProblem mirrors agentapi.ValidateAppStart for a program: a file below the home. */
export function programProblem(program: string): string {
  if (program === "") return "";
  if (program.length > 255 || program.split("/").length > 4) return "Use a file at most 4 folders below the home.";
  if (!workdirRe.test(program) || program.split("/").some((p) => p === "." || p === ".."))
    return "Use a file below the home, for example bin/server: letters, digits, . _ - and /, no .. and no leading /.";
  return "";
}

/** The most arguments of a program, and the longest one in bytes (agentapi). */
export const maxArgs = 16;
export const maxArgBytes = 256;

/** parseArgs takes one argument per line; empty lines are left out. */
export const parseArgs = (text: string): string[] => text.split("\n").map((l) => l.replace(/\r$/, "")).filter((l) => l !== "");

/** argsProblem mirrors agentapi.ValidateAppArgs. */
export function argsProblem(args: string[]): string {
  if (args.length > maxArgs) return `Use at most ${maxArgs} arguments.`;
  const enc = new TextEncoder();
  const long = args.findIndex((a) => enc.encode(a).length > maxArgBytes);
  if (long >= 0) return `Argument ${long + 1} is longer than ${maxArgBytes} bytes.`;
  return "";
}

/** quoteArg shows an argument the way a shell user reads it; only for the panel. */
export const quoteArg = (a: string) => (/^[A-Za-z0-9._\/:=,@+-]+$/.test(a) ? a : "'" + a.replaceAll("'", "'\\''") + "'");

/** The command line of a program, as shown in the panel. */
export const programCommand = (a: Pick<ProgramApp, "program" | "args">) => ["~/" + a.program, ...a.args.map(quoteArg)].join(" ");

/** The command the app runs, as shown in the panel. */
export function startCommand(a: Pick<NodeApp, "startKind" | "start">): string {
  if (a.startKind === "script") return "node " + a.start;
  if (a.startKind === "npm") return "npm run " + a.start;
  return "";
}

export type AppView = { word: string; tone: "ok" | "warn" | "err" | "off" };

/** appView says in words what the app of a Node.js, app or Python site does. */
export function appView(site: Site, st: AppStatus | null): AppView {
  const set = site.node ? site.node.startKind !== "" : site.python ? site.python.startKind !== "" : !!site.app?.program;
  if (!set) return { word: site.app ? "No program" : "No start command", tone: "off" };
  if (site.disabled) return { word: "Stopped: site disabled", tone: "off" };
  if (site.node?.stopped || site.app?.stopped || site.python?.stopped) return { word: "Stopped", tone: "off" };
  if (!st) return { word: "Unknown", tone: "off" };
  if (st.problem) return { word: site.python ? "Waiting for its packages" : "Waiting for the program", tone: "warn" };
  switch (st.state) {
    case "active":
      return { word: "Running", tone: "ok" };
    case "activating":
    case "reloading":
      return { word: st.subState === "auto-restart" ? "Restarting after a crash" : "Starting", tone: "warn" };
    case "failed":
      return { word: "Crashed", tone: "err" };
    default:
      return { word: "Not running", tone: "err" };
  }
}

export function nodeMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return "Only administrators can install Node.js.";
      case "node_install_refused":
        if (err.message.includes("apps still run")) return "Sites still run on this version. Move them to another version first.";
        if (err.message.includes("not installed")) return "This version is not installed.";
        return err.message.includes("running") ? "Another installation is still running. Wait until it is done." : "The installation could not start.";
    }
  }
  return siteMessage(err);
}

/** An environment variable of an app (ADR-0054). */
export type EnvVar = { name: string; value: string };

export const getAppEnvNames = async (name: string) => (await request<{ names: string[] }>("GET", sitePath(name) + "/app/env")).names;
export const getAppEnv = async (name: string) => (await request<{ variables: EnvVar[] }>("GET", sitePath(name) + "/app/env/values")).variables;
export const setAppEnv = (name: string, variables: EnvVar[]) =>
  request<{ names: string[]; status: AppStatus }>("PUT", sitePath(name) + "/app/env", { variables });

const envNameRe = /^[A-Z_][A-Z0-9_]{0,63}$/;
/** Names Hostashi sets for every app (agentapi.reservedAppEnv). */
export const reservedEnv = ["HOST", "PORT", "HOME", "USER", "LOGNAME", "PATH"];
export const maxEnv = 100;
export const maxEnvValue = 4096;

/** formatEnv writes variables as the NAME=value lines the editor shows. */
export const formatEnv = (vars: EnvVar[]) => vars.map((v) => v.name + "=" + v.value).join("\n");

/**
 * parseEnv reads NAME=value lines, mirroring agentapi.ValidateAppEnv.
 * Empty lines and lines starting with # are skipped; a value is taken as
 * written, quotes included.
 */
export function parseEnv(text: string): { vars: EnvVar[]; problem: string } {
  const vars: EnvVar[] = [];
  const seen = new Set<string>();
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const line = (lines[i] ?? "").replace(/\r$/, "");
    if (line.trim() === "" || line.trimStart().startsWith("#")) continue;
    const at = "Line " + (i + 1) + ": ";
    const eq = line.indexOf("=");
    if (eq < 0) return { vars, problem: at + "write NAME=value." };
    const name = line.slice(0, eq).trim();
    const value = line.slice(eq + 1);
    if (!envNameRe.test(name)) return { vars, problem: at + "use A-Z, 0-9 and _ in the name, no digit first, at most 64 characters." };
    if (reservedEnv.includes(name)) return { vars, problem: at + name + " is set by Hostashi." };
    if (seen.has(name)) return { vars, problem: at + name + " is set twice." };
    if (new TextEncoder().encode(value).length > maxEnvValue) return { vars, problem: at + "the value is longer than 4096 bytes." };
    seen.add(name);
    vars.push({ name, value });
  }
  if (vars.length > maxEnv) return { vars, problem: "At most " + maxEnv + " variables." };
  return { vars, problem: "" };
}
