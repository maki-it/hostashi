// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { argsProblem, parseArgs, programCommand, programProblem, quoteArg } from "./node";
import { ProgramSettingsCard } from "./SiteApp";
import { AppCard } from "./SiteNode";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site = (app: Partial<NonNullable<Site["app"]>> = {}, extra: Partial<Site> = {}) =>
  ({
    name: "svc",
    type: "app",
    disabled: false,
    updatedAt: "2026-11-22T10:00:00Z",
    node: null,
    app: { workdir: "htdocs", program: "bin/server", args: ["--name", "Maki IT"], port: 20002, stopped: false, ...app },
    ...extra,
  }) as Site;

describe("program helpers", () => {
  it("checks the program and its arguments like the server", () => {
    expect(programProblem("bin/server")).toBe("");
    expect(programProblem("")).toBe("");
    expect(programProblem("/bin/sh")).not.toBe("");
    expect(programProblem("../x")).not.toBe("");
    expect(programProblem("my app")).not.toBe("");
    expect(argsProblem(Array(17).fill("x"))).toBe("Use at most 16 arguments.");
    expect(argsProblem(["ü".repeat(129)])).toBe("Argument 1 is longer than 256 bytes.");
    expect(parseArgs("--a\r\n\nb c\n")).toEqual(["--a", "b c"]);
  });

  it("shows the command line quoted for reading", () => {
    expect(quoteArg("--port=8080")).toBe("--port=8080");
    expect(quoteArg("it's")).toBe("'it'\\''s'");
    expect(programCommand({ program: "bin/s", args: ["-n", "a b", "$HOME"] })).toBe("~/bin/s -n 'a b' '$HOME'");
  });
});

describe("AppCard for an app site", () => {
  it("says why the program does not start and offers Start", async () => {
    mockFetch({
      "GET /api/v1/sites/svc/app": { body: { state: "inactive", subState: "dead", restarts: 0, usedBytes: 0, since: "", problem: "the program file bin/server does not exist; upload it, then start the app" } },
    });
    render(<AppCard site={site()} onChange={async () => {}} poll={100000} />);
    expect(await screen.findByText("Waiting for the program")).toBeInTheDocument();
    expect(screen.getByText("The program file bin/server does not exist; upload it, then start the app.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start" })).toBeEnabled();
    expect(screen.queryByText("Node.js")).not.toBeInTheDocument();
    expect(screen.getByText("~/bin/server --name 'Maki IT'")).toBeInTheDocument();
  });

  it("shows a running program", async () => {
    mockFetch({ "GET /api/v1/sites/svc/app": { body: { state: "active", subState: "running", restarts: 0, usedBytes: 1048576, since: "" } } });
    render(<AppCard site={site()} onChange={async () => {}} poll={100000} />);
    expect(await screen.findByText("Running")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Restart" })).toBeInTheDocument();
  });
});

describe("ProgramSettingsCard", () => {
  it("sends the program with one argument per line", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "PUT /api/v1/sites/svc/app": { body: { site: site(), status: { state: "active", subState: "running", restarts: 0, usedBytes: 0, since: "" } } },
    });
    render(<ProgramSettingsCard site={site()} onChange={async () => {}} />);
    const args = screen.getByLabelText(/^Arguments/);
    await user.clear(args);
    await user.type(args, "--config{Enter}my config.toml");
    await user.click(screen.getByRole("button", { name: "Save app settings" }));
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ workdir: "htdocs", program: "bin/server", args: ["--config", "my config.toml"] });
    expect(await screen.findByText("Saved. The app was restarted with it.")).toBeInTheDocument();
  });

  it("refuses a program outside the home before sending", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({});
    render(<ProgramSettingsCard site={site()} onChange={async () => {}} />);
    const prog = screen.getByLabelText(/^Program/);
    await user.clear(prog);
    await user.type(prog, "/usr/bin/env");
    await user.click(screen.getByRole("button", { name: "Save app settings" }));
    expect(screen.getByText(/Use a file below the home/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
  });
});
