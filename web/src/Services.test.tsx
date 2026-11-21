// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ServerDashboard } from "./Dashboard";
import { Services } from "./Services";
import { bytes, eventText, rate, stateWord, type HostPoint, type ServiceRow } from "./services";
import { mockFetch } from "./test-utils";

const rows: ServiceRow[] = [
  { id: "nginx", unit: "nginx.service", installed: true, active: "active", sub: "running", activeSince: "2026-11-21T08:00:00Z", memoryBytes: 52428800, memoryKnown: true, actions: ["start", "restart", "reload"], restarts24h: 0 },
  { id: "mariadb", unit: "mariadb.service", installed: true, active: "active", actions: ["start", "stop", "restart"], confirm: ["stop"], restarts24h: 2 },
  { id: "panel", unit: "hostashi-panel.service", installed: true, active: "active", actions: [], restarts24h: 0 },
  { id: "php", site: "blog", unit: "hostashi-php-8.4@blog.service", installed: true, active: "failed", result: "exit-code", actions: ["start", "restart", "reload"], restarts24h: 0 },
];

const events = [{ at: "2026-11-21T09:00:00Z", service: "mariadb", kind: "crash-restart", restarts: 2 }];

describe("services helpers", () => {
  it("words states, events and sizes", () => {
    expect(stateWord({ installed: false })).toEqual({ word: "Not installed", tone: "off" });
    expect(stateWord({ installed: true, active: "failed" }).tone).toBe("err");
    expect(eventText({ at: "", service: "nginx", kind: "action", detail: "reload", user: "kim" })).toBe("Reload by kim");
    expect(eventText({ at: "", service: "nginx", kind: "crash-restart", restarts: 3 })).toBe("Restarted by systemd 3 times after a crash");
    expect(bytes(52428800)).toBe("50 MiB");
    expect(bytes(1536)).toBe("1.5 KiB");
    expect(rate(125000)).toBe("1.0 Mbit/s");
  });
});

describe("Services", () => {
  it("lists services with state, restarts and only the allowed actions", async () => {
    mockFetch({ "GET /api/v1/services": { body: { services: rows } }, "GET /api/v1/services/events?limit=50": { body: { events } } });
    render(<Services />);

    const table = await screen.findByRole("table", { name: "Services" });
    const [, nginx, mariadb, panel, php] = within(table).getAllByRole("row");
    expect(nginx).toHaveTextContent("Web server (nginx)");
    expect(nginx).toHaveTextContent("50 MiB");
    // Start is pointless while it runs.
    expect(within(nginx!).getByRole("button", { name: "Start Web server (nginx)" })).toBeDisabled();
    expect(within(nginx!).getByRole("button", { name: "Reload Web server (nginx)" })).toBeEnabled();
    expect(within(nginx!).queryByRole("button", { name: /Stop/ })).not.toBeInTheDocument();
    expect(mariadb).toHaveTextContent("2");
    expect(panel).toHaveTextContent("Managed by Hostashi");
    expect(php).toHaveTextContent("Failed");
    expect(php).toHaveTextContent("Site blog");
    expect(screen.getByText("Restarted by systemd 2 times after a crash", { exact: false })).toBeInTheDocument();
  });

  it("asks to type the action before stopping MariaDB", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/services": { body: { services: rows } },
      "GET /api/v1/services/events?limit=50": { body: { events: [] } },
      "POST /api/v1/services/mariadb/stop": { body: rows[1] },
    });
    render(<Services />);

    await user.click(await screen.findByRole("button", { name: "Stop MariaDB" }));
    const dialog = screen.getByRole("alertdialog", { name: "Stop MariaDB?" });
    const go = within(dialog).getByRole("button", { name: "Stop MariaDB" });
    expect(go).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Type stop mariadb to confirm"), "stop mariadb");
    await user.click(go);

    expect(await screen.findByText("Stop: MariaDB done.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ confirm: "stop mariadb" });
  });

  it("runs a reload at once and shows a refusal", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/services": { body: { services: rows } },
      "GET /api/v1/services/events?limit=50": { body: { events: [] } },
      "POST /api/v1/services/nginx/reload": { status: 409, body: { error: { code: "failed_precondition", message: "nginx -t rejected the configuration" } } },
    });
    render(<Services />);

    await user.click(await screen.findByRole("button", { name: "Reload Web server (nginx)" }));
    expect(await screen.findByText(/nginx -t rejected/)).toBeInTheDocument();
  });

  it("shows only a site's services on its page", async () => {
    mockFetch({ "GET /api/v1/sites/blog/services": { body: { services: [rows[3]] } } });
    render(<Services site="blog" />);
    const table = await screen.findByRole("table", { name: "Services" });
    expect(within(table).getAllByRole("row")).toHaveLength(2);
    expect(screen.queryByText(/systemd restarts a crashed service/)).not.toBeInTheDocument();
  });
});

const point = (at: string, busy: number): HostPoint => ({
  at,
  cpuBusy: busy,
  cpuIowait: 1,
  cpuSteal: 0,
  load1: 0.42,
  memUsed: 2 * 1024 ** 3,
  memTotal: 8 * 1024 ** 3,
  swapUsed: 0,
  swapTotal: 0,
  disks: [{ name: "/", used: 36 * 1024 ** 3, total: 38 * 1024 ** 3 }],
  net: [{ name: "eth0", rx: 1000, tx: 2000 }],
});

describe("ServerDashboard", () => {
  it("draws the charts and switches the range", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/dashboard?range=1h": { body: { range: "1h", step: 15, points: [point("2026-11-21T09:00:00Z", 20), point("2026-11-21T09:00:15Z", 40)] } },
      "GET /api/v1/dashboard?range=7d": { body: { range: "7d", step: 3600, points: [] } },
    });
    render(<ServerDashboard />);

    expect(await screen.findByRole("figure", { name: "CPU use in percent" })).toBeInTheDocument();
    expect(screen.getByRole("meter", { name: "/" })).toHaveAttribute("aria-valuenow", "95");
    expect(screen.getByText("Load average (1 min): 0.42")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "7d" }));
    expect(await screen.findByRole("heading", { name: "Server load, last 7 days" })).toBeInTheDocument();
    expect(screen.getAllByText("No data yet. The panel takes a sample every 15 seconds.")).toHaveLength(3);
    expect(calls.map((c) => c.path)).toContain("/api/v1/dashboard?range=7d");
  });
});
