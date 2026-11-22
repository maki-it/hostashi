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

export type StartKind = "" | "script" | "npm";

/** The app of a Node.js site; port and stopped are the panel's. */
export type NodeApp = { version: string; workdir: string; startKind: StartKind; start: string; port: number; stopped: boolean };

export type AppStatus = { state: string; subState: string; restarts: number; usedBytes: number; since: string };

export type AppAction = "start" | "stop" | "restart";

const sitePath = (name: string) => "/sites/" + encodeURIComponent(name);

export const setNodeApp = (name: string, a: Pick<NodeApp, "version" | "workdir" | "startKind" | "start">) =>
  request<{ site: Site; status: AppStatus }>("PUT", sitePath(name) + "/node", a);
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

/** The command the app runs, as shown in the panel. */
export function startCommand(a: Pick<NodeApp, "startKind" | "start">): string {
  if (a.startKind === "script") return "node " + a.start;
  if (a.startKind === "npm") return "npm run " + a.start;
  return "";
}

export type AppView = { word: string; tone: "ok" | "warn" | "err" | "off" };

/** appView says in words what the app does. */
export function appView(site: Site, st: AppStatus | null): AppView {
  const a = site.node;
  if (!a?.startKind) return { word: "No start command", tone: "off" };
  if (site.disabled) return { word: "Stopped: site disabled", tone: "off" };
  if (a.stopped) return { word: "Stopped", tone: "off" };
  if (!st) return { word: "Unknown", tone: "off" };
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
        return err.message.includes("running") ? "Another installation is still running. Wait until it is done." : "The installation could not start.";
    }
  }
  return siteMessage(err);
}
