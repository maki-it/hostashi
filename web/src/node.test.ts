// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { appView, installedLines, newestLine, startCommand, startProblem, workdirProblem, type NodeStatus } from "./node";
import type { Site } from "./sites";

const status = (majors: string[]): NodeStatus => ({
  installed: majors.map((m) => ({ major: m, version: m + ".0.0" })),
  last: "",
  state: "idle",
  log: [],
  releases: [],
});

const site = (node: Partial<NonNullable<Site["node"]>>, disabled = false) =>
  ({ name: "shop", disabled, node: { version: "24", workdir: "htdocs", startKind: "npm", start: "start", port: 20000, stopped: false, ...node } }) as Site;

describe("node", () => {
  it("lists installed lines newest first", () => {
    expect(installedLines(status(["22", "26", "24"]))).toEqual(["26", "24", "22"]);
    expect(newestLine(status([]))).toBe("");
    expect(newestLine(null)).toBe("");
  });

  it("checks the working directory like the server", () => {
    expect(workdirProblem("htdocs")).toBe("");
    expect(workdirProblem("htdocs/app-1")).toBe("");
    for (const bad of ["", "/etc", "../x", "a/../b", "a b", "a//b", "./x"]) expect(workdirProblem(bad), bad).not.toBe("");
  });

  it("checks the start command like the server", () => {
    expect(startProblem("", "")).toBe("");
    expect(startProblem("script", "server.js")).toBe("");
    expect(startProblem("script", "dist/main.mjs")).toBe("");
    expect(startProblem("npm", "start:prod")).toBe("");
    for (const [k, v] of [
      ["script", "server.sh"],
      ["script", "../x.js"],
      ["script", "/srv/x.js"],
      ["script", "a b.js"],
      ["npm", "start; id"],
      ["npm", "Start"],
      ["npm", ""],
    ] as const)
      expect(startProblem(k, v), v).not.toBe("");
  });

  it("names the command", () => {
    expect(startCommand({ startKind: "script", start: "server.js" })).toBe("node server.js");
    expect(startCommand({ startKind: "npm", start: "start" })).toBe("npm run start");
    expect(startCommand({ startKind: "", start: "" })).toBe("");
  });

  it("says what the app does", () => {
    const st = (state: string, subState = "") => ({ state, subState, restarts: 0, usedBytes: 0, since: "" });
    expect(appView(site({ startKind: "", start: "" }), null).word).toBe("No start command");
    expect(appView(site({}, true), st("inactive")).word).toBe("Stopped: site disabled");
    expect(appView(site({ stopped: true }), st("inactive")).word).toBe("Stopped");
    expect(appView(site({}), st("active"))).toEqual({ word: "Running", tone: "ok" });
    expect(appView(site({}), st("activating", "auto-restart")).word).toBe("Restarting after a crash");
    expect(appView(site({}), st("failed")).tone).toBe("err");
    expect(appView(site({}), st("inactive")).tone).toBe("err");
  });
});
