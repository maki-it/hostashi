// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { LimitSettings, Presets } from "./Limits";
import { fieldsOf, formatBytes, GiB, limitsOf, MiB, type Limits, type Preset, type SiteLimits } from "./limits";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const small: Limits = {
  diskBytes: 10 * GiB,
  diskInodes: 500000,
  memoryBytes: 512 * MiB,
  phpWorkers: 5,
  tasks: 128,
  cpuPriority: "normal",
  cpuQuotaPercent: 0,
  databaseBytes: 2 * GiB,
  databaseConnections: 20,
  unlimited: [],
};
const medium: Limits = { ...small, diskBytes: 25 * GiB, diskInodes: 1000000, memoryBytes: GiB, phpWorkers: 10, tasks: 256, cpuPriority: "high" };
const presets: Preset[] = [
  { name: "medium", default: false, limits: medium, createdAt: 1, updatedAt: 1 },
  { name: "small", default: true, limits: small, createdAt: 1, updatedAt: 1 },
];
const onSmall: SiteLimits = {
  set: true,
  preset: "small",
  limits: small,
  appliedAt: 1795600000,
  applyError: "",
  updatedAt: 1795600000,
  usage: { enforced: true, bytes: 9 * GiB, inodes: 1200, bytesPercent: 90, inodesPercent: 0, level: "warning" },
};
const base = "/api/v1/sites/blog/limits";
const list = { "GET /api/v1/limit-presets": { body: { presets, cpus: 4 } } };

// Harness keeps the address query in state, as the app does.
function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Presets query={query} onQuery={setQuery} />;
}

describe("limits helpers", () => {
  it("formats bytes", () => {
    expect(formatBytes(10 * GiB)).toBe("10 GiB");
    expect(formatBytes(1.5 * GiB)).toBe("1.5 GiB");
    expect(formatBytes(512 * MiB)).toBe("512 MiB");
    expect(formatBytes(100)).toBe("100 B");
  });

  it("checks the fields like the server", () => {
    expect(limitsOf(fieldsOf(small))).toEqual({ ...small, unlimited: undefined });
    const f = fieldsOf(small);
    expect(limitsOf({ ...f, workers: "0" })).toMatch(/PHP workers must be between 1 and 500/);
    expect(limitsOf({ ...f, memoryMiB: "32" })).toMatch(/Memory must be between 64/);
    expect(limitsOf({ ...f, tasks: "8" })).toMatch(/Processes must be between 16/);
    expect(limitsOf({ ...f, inodes: "10" })).toMatch(/Files must be between 1,000/);
    expect(limitsOf({ ...f, diskGiB: "1.5" })).toMatch(/whole number/);
    expect(limitsOf({ ...f, cpuCores: "-1" })).toMatch(/CPU cores must be a number/);
    expect(limitsOf({ ...f, cpuCores: "0.125" })).toMatch(/CPU cores must be a number/);
    expect(limitsOf({ ...f, cpuCores: "1001" })).toMatch(/at most 1,000/);
    expect(limitsOf({ ...f, dbGiB: "0" })).toMatch(/Database size must be between 1/);
    expect(limitsOf({ ...f, dbConnections: "10001" })).toMatch(/Database connections must be between 1 and 10,000/);
    expect(limitsOf({ ...f, dbUnlimited: true, dbConnectionsUnlimited: true })).toMatchObject({ databaseBytes: 0, databaseConnections: 0 });
    const half = limitsOf({ ...f, cpuCores: "1,5" });
    expect(typeof half === "object" && half.cpuQuotaPercent).toBe(150);
    expect(fieldsOf({ ...small, cpuQuotaPercent: 250 }).cpuCores).toBe("2.5");
    const open = limitsOf({ ...f, diskUnlimited: true, diskGiB: "x" });
    expect(typeof open === "object" && open.diskBytes).toBe(0);
    expect(limitsOf({ ...f, memoryUnlimited: true }, true)).toMatch(/default preset must limit/);
  });
});

describe("LimitSettings", () => {
  it("shows the preset, the values and the disk use with a warning", async () => {
    mockFetch({ ...list, ["GET " + base]: { body: onSmall } });
    render(<LimitSettings site={site} />);
    expect(await screen.findByText("Preset: small")).toBeInTheDocument();
    const facts = within(screen.getByLabelText("Limits of blog"));
    expect(facts.getByText("10 GiB")).toBeInTheDocument();
    expect(facts.getByText("512 MiB")).toBeInTheDocument();
    expect(facts.getByText("Normal priority, no hard limit")).toBeInTheDocument();
    expect(screen.getByText("Filling up")).toBeInTheDocument();
    expect(within(screen.getByLabelText("Use")).getByText(/9 GiB of 10 GiB \(90 %\)/)).toBeInTheDocument();
  });

  it("warns when a disk limit is almost reached", async () => {
    const full = { ...onSmall, usage: { ...onSmall.usage!, bytesPercent: 97, level: "critical" as const } };
    mockFetch({ ...list, ["GET " + base]: { body: full } });
    render(<LimitSettings site={site} />);
    expect(await screen.findByText("Almost full")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(/95 % or more/);
  });

  it("shows the size of the site's databases against the warning", async () => {
    const withDB = { ...onSmall, databaseUsage: { bytes: 1.5 * GiB, databases: 2, percent: 75, level: "ok" as const, measuredAt: 1795600000 } };
    mockFetch({ ...list, ["GET " + base]: { body: withDB } });
    render(<LimitSettings site={site} />);
    const facts = within(await screen.findByLabelText("Use"));
    expect(facts.getByText(/1.5 GiB of 2 GiB \(75 %\)/)).toBeInTheDocument();
    expect(facts.getByText(/in 2 databases/)).toBeInTheDocument();
    const limits = within(screen.getByLabelText("Limits of blog"));
    expect(limits.getByText("warning at 2 GiB")).toBeInTheDocument();
    expect(limits.getByText("20 per database user")).toBeInTheDocument();
  });

  it("warns when the databases reach the warning size, and says nothing stops them", async () => {
    const full = { ...onSmall, databaseUsage: { bytes: 2 * GiB, databases: 1, percent: 100, level: "critical" as const, measuredAt: 1795600000 } };
    mockFetch({ ...list, ["GET " + base]: { body: full } });
    render(<LimitSettings site={site} />);
    expect(await screen.findByText("Above the warning")).toBeInTheDocument();
    expect(screen.getByText(/keep growing and fill the server's disk/)).toBeInTheDocument();
  });

  it("marks unlimited values and says when disk limits are not enforced", async () => {
    const open: SiteLimits = {
      ...onSmall,
      preset: "",
      limits: { ...small, diskBytes: 0, diskInodes: 0, unlimited: ["diskBytes", "diskInodes"] },
      usage: { enforced: false, bytes: 0, inodes: 0, bytesPercent: 0, inodesPercent: 0, level: "ok" },
    };
    mockFetch({ ...list, ["GET " + base]: { body: open } });
    render(<LimitSettings site={site} />);
    expect(await screen.findByText("Custom values", { selector: "p" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("Limits of blog")).getAllByText("Unlimited")).toHaveLength(2);
    expect(screen.getByText(/not enforced on this server/)).toBeInTheDocument();
  });

  it("offers the default preset to a site without limits", async () => {
    const calls = mockFetch({
      ...list,
      ["GET " + base]: { body: { set: false, preset: "", applyError: "", appliedAt: 0, updatedAt: 0 } },
      ["PUT " + base]: { body: onSmall },
    });
    render(<LimitSettings site={site} />);
    expect(await screen.findByText(/no limits yet/)).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Limits" })).toHaveValue("small");
    await userEvent.click(screen.getByRole("button", { name: "Save limits" }));
    expect(await screen.findByText("blog now uses the preset small.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ preset: "small" });
  });

  it("sends custom values, with unlimited as an explicit choice", async () => {
    const calls = mockFetch({ ...list, ["GET " + base]: { body: onSmall }, ["PUT " + base]: { body: onSmall } });
    render(<LimitSettings site={site} />);
    await screen.findByText("Preset: small");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Limits" }), "");
    await userEvent.clear(screen.getByRole("textbox", { name: /PHP workers/ }));
    await userEvent.type(screen.getByRole("textbox", { name: /PHP workers/ }), "8");
    await userEvent.click(screen.getByRole("checkbox", { name: "Unlimited disk space" }));
    expect(screen.getByText(/fill the disk or take the memory/)).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByRole("combobox", { name: /CPU priority/ }), "high");
    await userEvent.click(screen.getByRole("button", { name: "Save limits" }));
    expect(await screen.findByText("Custom limits saved.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
      custom: { ...small, unlimited: undefined, diskBytes: 0, phpWorkers: 8, cpuPriority: "high" },
    });
  });

  it("checks the form before sending", async () => {
    const calls = mockFetch({ ...list, ["GET " + base]: { body: onSmall } });
    render(<LimitSettings site={site} />);
    await screen.findByText("Preset: small");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Limits" }), "");
    await userEvent.clear(screen.getByRole("textbox", { name: /^Memory/ }));
    await userEvent.type(screen.getByRole("textbox", { name: /^Memory/ }), "10");
    await userEvent.click(screen.getByRole("button", { name: "Save limits" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/Memory must be between 64/);
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
  });

  it("shows a failed apply", async () => {
    mockFetch({ ...list, ["GET " + base]: { body: { ...onSmall, appliedAt: 0, applyError: "agent down" } } });
    render(<LimitSettings site={site} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(/could not be applied on the server: agent down/);
  });
});

describe("Presets", () => {
  const rowOf = async (name: string) => {
    const table = await screen.findByRole("table", { name: "Limit presets" });
    return within(within(table).getByRole("link", { name }).closest("tr")!);
  };

  it("lists presets in a narrow table with the default marked, CPU in cores and the row opening the preset", async () => {
    mockFetch({ "GET /api/v1/limit-presets": { body: { presets: [presets[0], { ...presets[1]!, limits: { ...small, cpuQuotaPercent: 150 } }], cpus: 4 } } });
    render(<Harness />);
    const def = await rowOf("small");
    expect(def.getByText("Default")).toBeInTheDocument();
    expect(def.getByText("1.5")).toBeInTheDocument();
    const med = await rowOf("medium");
    expect(med.getByText("25 GiB")).toBeInTheDocument();
    expect(med.getByText("No limit")).toBeInTheDocument();
    expect(med.getByText("10 PHP workers")).toBeInTheDocument();
    expect(med.queryByRole("button")).toBeNull();
    expect(med.getByRole("link", { name: "medium" })).toHaveAttribute("href", "#/limits?edit=medium");
    expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Preset", "Disk space", "Memory", "Processes", "CPU", "Caches"]);
    expect(screen.getByText("This server has 4 CPU cores.")).toBeInTheDocument();
  });

  it("filters and sorts by a column", async () => {
    const user = userEvent.setup();
    mockFetch(list);
    render(<Harness />);
    await rowOf("small");
    const names = () =>
      screen
        .getAllByRole("row")
        .slice(1)
        .map((r) => r.querySelector("a")?.textContent);
    expect(names()).toEqual(["medium", "small"]);
    await user.click(screen.getByRole("button", { name: "Memory" }));
    expect(names()).toEqual(["small", "medium"]);
    await user.type(screen.getByRole("searchbox", { name: "Filter presets" }), "med");
    expect(names()).toEqual(["medium"]);
  });

  it("adds a preset with a CPU limit in cores and refuses an unlimited default", async () => {
    const calls = mockFetch({ ...list, "POST /api/v1/limit-presets": { status: 201, body: presets[0] } });
    render(<Harness initial="new=1" />);
    const form = within(await screen.findByRole("form", { name: "New preset" }));
    expect(form.getByText(/This server has 4 cores\./)).toBeInTheDocument();
    await userEvent.type(form.getByRole("textbox", { name: /Name/ }), "Tiny");
    await userEvent.click(form.getByRole("button", { name: "Add preset" }));
    expect(form.getByRole("alert")).toHaveTextContent(/lower-case/);

    await userEvent.clear(form.getByRole("textbox", { name: /Name/ }));
    await userEvent.type(form.getByRole("textbox", { name: /Name/ }), "tiny");
    await userEvent.click(form.getByRole("checkbox", { name: "Unlimited memory" }));
    await userEvent.click(form.getByRole("checkbox", { name: "Default for new sites" }));
    // A default preset offers no unlimited values: the box went away, but
    // the value is still unlimited and refused.
    await userEvent.click(form.getByRole("button", { name: "Add preset" }));
    expect(form.getByRole("alert")).toHaveTextContent(/default preset must limit/);

    await userEvent.click(form.getByRole("checkbox", { name: "Unlimited memory" }));
    await userEvent.clear(form.getByRole("textbox", { name: /CPU cores/ }));
    await userEvent.type(form.getByRole("textbox", { name: /CPU cores/ }), "0.5");
    await userEvent.selectOptions(form.getByRole("combobox", { name: /Page cache/ }), "512 MB");
    await userEvent.click(form.getByRole("button", { name: "Add preset" }));
    expect(await screen.findByText("Preset tiny added.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({
      name: "tiny",
      default: true,
      limits: { memoryBytes: 512 * MiB, cpuQuotaPercent: 50 },
      caches: { objectCacheMb: 0, pageCacheMb: 512 },
    });
    expect(await screen.findByRole("table", { name: "Limit presets" })).toBeInTheDocument();
  });

  it("changes a preset from the address", async () => {
    const calls = mockFetch({ ...list, "PUT /api/v1/limit-presets/medium": { body: presets[0] } });
    render(<Harness initial="edit=medium" />);
    const form = within(await screen.findByRole("form", { name: "Change preset medium" }));
    expect(form.getByRole("textbox", { name: /Name/ })).toHaveValue("medium");
    await userEvent.click(form.getByRole("button", { name: "Save preset" }));
    expect(await screen.findByText("Preset medium saved. Sites that use it get the new values.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ name: "medium", limits: { phpWorkers: 10 } });
  });

  it("deletes a preset after asking and explains a refused one", async () => {
    const calls = mockFetch({
      ...list,
      "DELETE /api/v1/limit-presets/medium": { status: 409, body: { error: { code: "preset_is_default", message: "" } } },
    });
    render(<Harness initial="edit=medium" />);
    await userEvent.click(await screen.findByRole("button", { name: "Delete preset" }));
    const dialog = within(screen.getByRole("alertdialog", { name: "Delete the preset medium?" }));
    await userEvent.click(dialog.getByRole("button", { name: "Delete preset" }));
    expect(await dialog.findByRole("alert")).toHaveTextContent("Make another preset the default first.");
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("makes a preset the default in its form", async () => {
    const calls = mockFetch({ ...list, "PUT /api/v1/limit-presets/medium": { body: { ...presets[0], default: true } } });
    render(<Harness initial="edit=medium" />);
    const form = within(await screen.findByRole("form", { name: "Change preset medium" }));
    await userEvent.click(form.getByRole("checkbox", { name: "Default for new sites" }));
    await userEvent.click(form.getByRole("button", { name: "Save preset" }));
    expect(await screen.findByText("Preset medium saved. Sites that use it get the new values.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ name: "medium", default: true });
  });

  it("offers no delete for the default preset", async () => {
    mockFetch(list);
    render(<Harness initial="edit=small" />);
    await screen.findByRole("form", { name: "Change preset small" });
    expect(screen.queryByRole("button", { name: "Delete preset" })).toBeNull();
  });
});
