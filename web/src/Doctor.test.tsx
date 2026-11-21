// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
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

// Harness keeps the address query in state, as the app does.
function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Doctor query={query} onQuery={setQuery} />;
}

const rowTexts = () =>
  within(screen.getByRole("table", { name: "Checks" }))
    .getAllByRole("row")
    .slice(1)
    .map((r) => r.textContent ?? "");

describe("Doctor", () => {
  it("lists the checks problems first, with where and hints", async () => {
    mockFetch({ "GET /api/v1/doctor": { body: report } });
    render(<Harness />);

    expect(await screen.findByText("1 check failed, 1 warning.")).toBeInTheDocument();
    const rows = rowTexts();
    expect(rows[0]).toContain("Certificate");
    expect(rows[0]).toContain("Site shop");
    expect(rows[0]).toContain("Failed");
    expect(rows[1]).toContain("Disk limits");
    expect(rows[1]).toContain("Run the installer again.");
    expect(rows[1]).toContain("Server");
    expect(rows.at(-1)).toContain("OK");
    expect(screen.getAllByRole("link", { name: "Site shop" })[0]).toHaveAttribute("href", "#/sites/shop");
  });

  it("filters by the address and checks again", async () => {
    const user = userEvent.setup();
    let current = report;
    const calls = mockFetch({ "GET /api/v1/doctor": () => ({ body: current }) });
    render(<Harness initial="show=problems" />);
    await screen.findByRole("table", { name: "Checks" });
    expect(screen.queryByText("Operating system")).not.toBeInTheDocument();
    expect(screen.getByText("Certificate")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Filter checks"), "shop");
    expect(rowTexts()).toHaveLength(1);

    await user.selectOptions(screen.getByLabelText("Show"), "");
    expect(rowTexts()).toHaveLength(2);

    current = { status: "ok", checkedAt: "2026-11-20T09:31:00Z", counts: { ok: 1, warn: 0, fail: 0 }, checks: [report.checks[0]!] };
    await user.click(screen.getByRole("button", { name: "Check again" }));
    expect(await screen.findByText("Everything is fine.")).toBeInTheDocument();
    expect(calls.filter((c) => c.path === "/api/v1/doctor").length).toBe(2);
  });

  it("explains an agent that does not answer", async () => {
    mockFetch({ "GET /api/v1/doctor": { status: 503, body: { error: { code: "agent_unavailable", message: "x" } } } });
    render(<Harness />);
    expect(await screen.findByRole("alert")).toHaveTextContent("The checks could not be run. Try again.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });
});
