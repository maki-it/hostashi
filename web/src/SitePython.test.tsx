// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PythonPage } from "./PythonPage";
import { AppCard } from "./SiteNode";
import { PythonSettingsCard, VenvCard } from "./SitePython";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const releases = [
  { line: "3.12", version: "3.12.12", build: "20261111", end: "2028-10-31" },
  { line: "3.13", version: "3.13.9", build: "20261111", end: "2029-10-31" },
  { line: "3.11", version: "3.11.14", build: "20261111", end: "2027-10-31" },
];
const idle = {
  installed: [
    { line: "3.13", version: "3.13.9", build: "20261111" },
    { line: "3.12", version: "3.12.12", build: "20261001" },
  ],
  last: "",
  state: "idle",
  log: [],
  releases,
};

const site = (py: Partial<NonNullable<Site["python"]>> = {}) =>
  ({
    name: "blog",
    type: "python",
    disabled: false,
    updatedAt: "2026-11-23T10:00:00Z",
    python: { version: "3.12", workdir: "htdocs", startKind: "wsgi", target: "mysite.wsgi:application", args: [], port: 20001, stopped: false, ...py },
  }) as Site;

const running = { state: "active", subState: "running", restarts: 0, usedBytes: 0, since: "" };

describe("PythonPage", () => {
  it("lists the lines newest first and offers a newer build", async () => {
    mockFetch({ "GET /api/v1/python": { body: idle }, "GET /api/v1/sites": { body: { sites: [site()] } } });
    render(<PythonPage today="2026-11-23" />);
    const rows = await screen.findAllByRole("row");
    expect(rows.slice(1).map((r) => r.querySelector("strong")?.textContent)).toEqual(["Python 3.13", "Python 3.12", "Python 3.11"]);
    expect(screen.getByText("Installed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update to build 20261111" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install Python 3.11" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove Python 3.13" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove Python 3.12" })).not.toBeInTheDocument();
  });
});

describe("AppCard for Python", () => {
  it("shows the command and the environment", async () => {
    mockFetch({ "GET /api/v1/sites/blog/app": { body: running } });
    render(<AppCard site={site()} onChange={vi.fn()} />);
    expect(await screen.findByText("Running")).toBeInTheDocument();
    expect(screen.getByText("gunicorn --bind 127.0.0.1:20001 mysite.wsgi:application")).toBeInTheDocument();
    expect(screen.getByText(/3\.12, environment/)).toBeInTheDocument();
  });

  it("waits for the packages", async () => {
    mockFetch({ "GET /api/v1/sites/blog/app": { body: { ...running, state: "inactive", problem: "gunicorn is not installed in .venv" } } });
    render(<AppCard site={site()} onChange={vi.fn()} />);
    expect(await screen.findByText("Waiting for its packages")).toBeInTheDocument();
    expect(screen.getByText("The app is not started yet.")).toBeInTheDocument();
  });
});

describe("PythonSettingsCard", () => {
  it("saves a new start command without a confirmation", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/python": { body: idle },
      "PUT /api/v1/sites/blog/python": { body: { site: site(), status: running } },
    });
    render(<PythonSettingsCard site={site()} onChange={vi.fn().mockResolvedValue(undefined)} admin />);
    await user.click(await screen.findByLabelText(/ASGI with uvicorn/));
    // The target stays when only the server changes.
    expect(screen.getByText("Runs: ~/.venv/bin/uvicorn --host 127.0.0.1 --port PORT mysite.wsgi:application")).toBeInTheDocument();
    await user.clear(screen.getByLabelText("App"));
    await user.type(screen.getByLabelText("App"), "main:app");
    await user.click(screen.getByRole("button", { name: "Save app settings" }));
    expect(await screen.findByText("Saved. The app was restarted with it.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ version: "3.12", workdir: "htdocs", startKind: "asgi", target: "main:app", args: [] });
  });

  it("refuses a bind argument before sending", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "GET /api/v1/python": { body: idle } });
    render(<PythonSettingsCard site={site()} onChange={vi.fn()} admin />);
    await user.type(await screen.findByLabelText("Extra arguments"), "--bind=0.0.0.0:80");
    await user.click(screen.getByRole("button", { name: "Save app settings" }));
    expect(screen.getByText(/--bind is set by Hostashi/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("asks for the site name before a new version rebuilds .venv", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/python": { body: idle },
      "PUT /api/v1/sites/blog/python": { body: { site: site({ version: "3.13" }), status: { ...running, problem: "gunicorn is not installed in .venv" } } },
    });
    render(<PythonSettingsCard site={site()} onChange={vi.fn().mockResolvedValue(undefined)} admin />);
    await user.selectOptions(await screen.findByLabelText("Python version"), "3.13");
    expect(screen.getByText("The environment .venv is rebuilt")).toBeInTheDocument();
    const go = screen.getByRole("button", { name: "Switch to Python 3.13" });
    expect(go).toBeDisabled();
    expect(screen.getByText("blog", { selector: "code" })).toHaveClass("ui-confirm-phrase");
    await user.type(screen.getByLabelText(/to confirm/), "blog");
    await user.click(go);
    expect(await screen.findByText("Saved. The app starts once its packages are installed in .venv.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ version: "3.13", confirm: "blog" });
  });
});

describe("VenvCard", () => {
  it("recreates the environment after the name is typed", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "POST /api/v1/sites/blog/python/venv": { body: running } });
    render(<VenvCard site={site()} onChange={vi.fn().mockResolvedValue(undefined)} />);
    await user.click(screen.getByRole("button", { name: "Recreate environment" }));
    const go = screen.getByRole("button", { name: "Recreate environment" });
    expect(go).toBeDisabled();
    await user.type(screen.getByLabelText(/to confirm/), "blog");
    await user.click(go);
    expect(await screen.findByText(/The environment was created again/)).toBeInTheDocument();
    expect(calls[0]?.body).toEqual({ confirm: "blog" });
  });
});
