// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Doctor } from "./Doctor";
import type { DoctorReport } from "./doctor";
import { mockFetch } from "./test-utils";

const report: DoctorReport = {
  status: "fail",
  checkedAt: "2026-11-20T09:30:00Z",
  counts: { ok: 2, warn: 1, fail: 1 },
  checks: [
    { id: "system.os", status: "ok", summary: "Debian GNU/Linux 13 (trixie) is supported" },
    { id: "system.quota", status: "warn", summary: "/home: no project quota, sites get no disk limit", hint: "Run the installer again." },
    { id: "site.disk", site: "shop", status: "ok", summary: "disk space: 1.0 GiB of 10.0 GiB (10 %)" },
    { id: "site.tls", site: "shop", status: "fail", summary: "certificate expired on 2026-11-01", hint: "The renewal did not work." },
  ],
};

describe("Doctor", () => {
  it("groups the checks by server and site and shows hints", async () => {
    mockFetch({ "GET /api/v1/doctor": { body: report } });
    render(<Doctor />);

    expect(await screen.findByText("1 check failed, 1 warning.")).toBeInTheDocument();

    const server = within(screen.getByRole("region", { name: "Server" }));
    expect(server.getByText("Operating system")).toBeInTheDocument();
    expect(server.getByText("Disk limits")).toBeInTheDocument();
    expect(server.getByText("Run the installer again.")).toBeInTheDocument();
    expect(server.queryByText("Certificate")).not.toBeInTheDocument();

    const site = within(screen.getByRole("region", { name: "Site shop" }));
    expect(site.getByText("certificate expired on 2026-11-01")).toBeInTheDocument();
    expect(site.getByText("Failed", { selector: ".badge" })).toBeInTheDocument();
  });

  it("hides the good checks on request and checks again", async () => {
    const user = userEvent.setup();
    let current = report;
    const calls = mockFetch({ "GET /api/v1/doctor": () => ({ body: current }) });
    render(<Doctor />);
    await screen.findByRole("region", { name: "Server" });

    await user.click(screen.getByRole("checkbox", { name: "Show only problems" }));
    expect(screen.queryByText("Operating system")).not.toBeInTheDocument();
    expect(screen.queryByText("Disk use")).not.toBeInTheDocument();
    expect(screen.getByText("Certificate")).toBeInTheDocument();

    current = { status: "ok", checkedAt: "2026-11-20T09:31:00Z", counts: { ok: 1, warn: 0, fail: 0 }, checks: [report.checks[0]!] };
    await user.click(screen.getByRole("button", { name: "Check again" }));
    expect(await screen.findByText("Everything is fine.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Server" })).not.toBeInTheDocument();
    expect(calls.length).toBe(2);
  });

  it("explains an agent that does not answer", async () => {
    mockFetch({ "GET /api/v1/doctor": { status: 503, body: { error: { code: "agent_unavailable", message: "x" } } } });
    render(<Doctor />);
    expect(await screen.findByRole("alert")).toHaveTextContent("The checks could not be run. Try again.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });
});
