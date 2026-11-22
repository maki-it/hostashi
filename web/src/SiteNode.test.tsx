// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { NodePage } from "./NodePage";
import { AppCard, AppSettingsCard } from "./SiteNode";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const releases = [
  { major: "22", version: "22.21.1", codename: "Jod", end: "2027-04-30" },
  { major: "24", version: "24.11.1", codename: "Krypton", end: "2028-04-30" },
  { major: "26", version: "26.1.0", codename: "", end: "2029-04-30" },
];
const idle = { installed: [{ major: "24", version: "24.11.1" }, { major: "22", version: "22.20.0" }], last: "", state: "idle", log: [], releases };

const site = (node: Partial<NonNullable<Site["node"]>> = {}, extra: Partial<Site> = {}) =>
  ({
    name: "shop",
    type: "node",
    disabled: false,
    updatedAt: "2026-11-22T10:00:00Z",
    node: { version: "24", workdir: "htdocs", startKind: "script", start: "server.js", port: 20000, stopped: false, ...node },
    ...extra,
  }) as Site;

describe("NodePage", () => {
  it("lists the lines newest first with their state", async () => {
    mockFetch({ "GET /api/v1/node": { body: idle }, "GET /api/v1/sites": { body: { sites: [{ name: "shop", node: { version: "24" } }] } } });
    render(<NodePage today="2026-11-22" />);
    const rows = await screen.findAllByRole("row");
    expect(rows.slice(1).map((r) => r.querySelector("strong")?.textContent)).toEqual(["Node.js 26", "Node.js 24", "Node.js 22"]);
    expect(screen.getByRole("button", { name: "Install Node.js 26" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update to 22.21.1" })).toBeInTheDocument();
    expect(screen.getByText("Installed")).toBeInTheDocument();
    expect(screen.getByText("Fixes end in 159 days. Move its sites to a newer version.")).toBeInTheDocument();
  });

  it("installs a line and follows the log", async () => {
    const user = userEvent.setup();
    let state: unknown = idle;
    const calls = mockFetch({
      "GET /api/v1/node": () => ({ body: state }),
      "GET /api/v1/sites": { body: { sites: [] } },
      "POST /api/v1/node/install": () => {
        state = { ...idle, installed: [...idle.installed, { major: "26", version: "26.1.0" }], last: "26", state: "succeeded", log: ["node-install: done"] };
        return { status: 202, body: { ...idle, last: "26", state: "running" } };
      },
    });
    render(<NodePage today="2026-11-22" poll={10} />);
    await user.click(await screen.findByRole("button", { name: "Install Node.js 26" }));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ major: "26" });
    expect(await screen.findByRole("heading", { name: "Node.js 26 was installed" })).toBeInTheDocument();
    expect(screen.getByLabelText("Installation log")).toHaveTextContent("node-install: done");
  });
});

describe("AppCard", () => {
  it("shows a running app and restarts it", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn(async () => {});
    const calls = mockFetch({
      "GET /api/v1/sites/shop/app": { body: { state: "active", subState: "running", restarts: 2, usedBytes: 50 * 1024 * 1024, since: "" } },
      "POST /api/v1/sites/shop/app/restart": { body: { state: "active", subState: "running", restarts: 0, usedBytes: 0, since: "" } },
    });
    render(<AppCard site={site()} onChange={onChange} />);
    expect(await screen.findByText("Running")).toBeInTheDocument();
    expect(screen.getByText("node server.js")).toBeInTheDocument();
    expect(screen.getByText("127.0.0.1:20000")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Restart" }));
    expect(calls.some((c) => c.method === "POST" && c.path.endsWith("/restart"))).toBe(true);
    expect(await screen.findByText("The app was restarted.")).toBeInTheDocument();
    expect(onChange).toHaveBeenCalled();
  });

  it("cannot start without a start command and points to the log after a crash", async () => {
    mockFetch({ "GET /api/v1/sites/shop/app": { body: { state: "inactive", subState: "dead", restarts: 0, usedBytes: 0, since: "" } } });
    const { unmount } = render(<AppCard site={site({ startKind: "", start: "", stopped: true })} onChange={async () => {}} />);
    expect(await screen.findByText("No start command")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start" })).toBeDisabled();
    unmount();

    mockFetch({ "GET /api/v1/sites/shop/app": { body: { state: "failed", subState: "failed", restarts: 5, usedBytes: 0, since: "" } } });
    render(<AppCard site={site()} onChange={async () => {}} />);
    expect(await screen.findByText("Crashed")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "See the app log" })).toBeInTheDocument();
  });
});

describe("AppSettingsCard", () => {
  it("checks the fields and saves the start command", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/node": { body: idle },
      "PUT /api/v1/sites/shop/node": { body: { site: site(), status: { state: "active", subState: "running", restarts: 0, usedBytes: 0, since: "" } } },
    });
    render(<AppSettingsCard site={site({ startKind: "", start: "" })} onChange={async () => {}} admin />);
    await screen.findByRole("option", { name: /Node.js 24/ });
    expect(screen.getByRole("link", { name: "Install Node.js versions" })).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: /npm script/ }));
    const input = screen.getByRole("textbox", { name: /npm script/ });
    expect(input).toHaveValue("start");
    await user.clear(input);
    await user.type(input, "Bad Name");
    await user.click(screen.getByRole("button", { name: "Save app settings" }));
    expect(screen.getByText(/Enter the name of an npm script/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);

    await user.clear(input);
    await user.type(input, "serve");
    await user.click(screen.getByRole("button", { name: "Save app settings" }));
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ version: "24", workdir: "htdocs", startKind: "npm", start: "serve" });
    expect(await screen.findByText("Saved. The app was restarted with it.")).toBeInTheDocument();
  });
});

describe("NodePage removal", () => {
  it("removes a line no site uses, after asking", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/node": { body: idle },
      "GET /api/v1/sites": { body: { sites: [{ name: "shop", node: { version: "24" } }] } },
      "POST /api/v1/node/remove": { body: { ...idle, installed: [{ major: "24", version: "24.11.1" }] } },
    });
    render(<NodePage today="2026-11-22" />);
    await screen.findByRole("button", { name: "Update to 22.21.1" });
    expect(screen.queryByRole("button", { name: "Remove Node.js 24" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove Node.js 26" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Remove Node.js 22" }));
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await user.click(screen.getByRole("button", { name: "Remove" }));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ major: "22" });
    expect(await screen.findByText("Node.js 22 was removed.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install Node.js 22" })).toBeInTheDocument();
  });

  it("explains a refusal", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/node": { body: idle },
      "GET /api/v1/sites": { body: { sites: [] } },
      "POST /api/v1/node/remove": { status: 409, body: { error: { code: "node_install_refused", message: "nodeinstall: apps still run on this Node.js line: shop" } } },
    });
    render(<NodePage today="2026-11-22" />);
    await user.click(await screen.findByRole("button", { name: "Remove Node.js 24" }));
    await user.click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByText("Sites still run on this version. Move them to another version first.")).toBeInTheDocument();
  });
});
