// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { installedPythonLines, pythonArgsProblem, pythonCommand, pythonProblem, targetProblem } from "./python";

describe("python", () => {
  it("sorts lines by their minor version", () => {
    const s = { installed: [{ line: "3.9" }, { line: "3.13" }, { line: "3.11" }].map((i) => ({ ...i, version: "", build: "" })), last: "", state: "idle" as const, log: [], releases: [] };
    expect(installedPythonLines(s)).toEqual(["3.13", "3.11", "3.9"]);
  });

  it("checks the start target", () => {
    expect(targetProblem("wsgi", "mysite.wsgi:application")).toBe("");
    expect(targetProblem("asgi", "main:app")).toBe("");
    expect(targetProblem("asgi", "main")).toMatch(/module.path:callable/);
    expect(targetProblem("script", "app.py")).toBe("");
    expect(targetProblem("script", "app.js")).toMatch(/\.py/);
    expect(targetProblem("script", "../app.py")).toMatch(/below the working directory/);
    expect(targetProblem("", "")).toBe("");
  });

  it("refuses bind arguments for gunicorn and uvicorn only", () => {
    expect(pythonArgsProblem("wsgi", ["--workers", "3"])).toBe("");
    expect(pythonArgsProblem("wsgi", ["--bind=0.0.0.0:80"])).toMatch(/--bind is set by Hostashi/);
    expect(pythonArgsProblem("wsgi", ["-b0.0.0.0:80"])).toMatch(/-b is set by Hostashi/);
    expect(pythonArgsProblem("asgi", ["--port", "80"])).toMatch(/--port/);
    expect(pythonArgsProblem("script", ["--port", "80"])).toBe("");
    expect(pythonArgsProblem("", ["x"])).toMatch(/need a start command/);
  });

  it("needs a version", () => {
    expect(pythonProblem({ version: "", workdir: "htdocs", startKind: "", target: "", args: [] })).toMatch(/No Python version/);
    expect(pythonProblem({ version: "3.13", workdir: "htdocs", startKind: "", target: "", args: [] })).toBe("");
  });

  it("shows the command with Hostashi's address", () => {
    expect(pythonCommand({ startKind: "wsgi", target: "mysite.wsgi:application", args: ["--workers", "3"] }, 20001)).toBe(
      "gunicorn --bind 127.0.0.1:20001 --workers 3 mysite.wsgi:application",
    );
    expect(pythonCommand({ startKind: "asgi", target: "main:app", args: [] })).toBe("uvicorn --host 127.0.0.1 --port PORT main:app");
    expect(pythonCommand({ startKind: "script", target: "app.py", args: ["a b"] })).toBe("python app.py 'a b'");
    expect(pythonCommand({ startKind: "", target: "", args: [] })).toBe("");
  });
});
