// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
const list = { "GET /api/v1/limit-presets": { body: { presets } } };

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
    expect(limitsOf({ ...f, cpuQuota: "-1" })).toMatch(/whole number/);
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
    expect(within(screen.getByLabelText("Disk use")).getByText(/9 GiB of 10 GiB \(90 %\)/)).toBeInTheDocument();
  });

  it("warns when a disk limit is almost reached", async () => {
    const full = { ...onSmall, usage: { ...onSmall.usage!, bytesPercent: 97, level: "critical" as const } };
    mockFetch({ ...list, ["GET " + base]: { body: full } });
    render(<LimitSettings site={site} />);
    expect(await screen.findByText("Almost full")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(/95 % or more/);
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
  it("lists presets with the default marked and no delete for it", async () => {
    mockFetch(list);
    render(<Presets />);
    const rows = within(await screen.findByRole("list", { name: "Limit presets" })).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(within(rows[1]!).getByText("Default")).toBeInTheDocument();
    expect(within(rows[1]!).queryByRole("button", { name: "Delete" })).toBeNull();
    expect(within(rows[0]!).getByRole("button", { name: "Delete" })).toBeInTheDocument();
    expect(within(rows[0]!).getByText("25 GiB")).toBeInTheDocument();
  });

  it("adds a preset and refuses an unlimited default", async () => {
    const calls = mockFetch({ ...list, "POST /api/v1/limit-presets": { status: 201, body: presets[0] } });
    render(<Presets />);
    await userEvent.click(await screen.findByRole("button", { name: "Add preset" }));
    const form = within(screen.getByRole("form", { name: "New preset" }));
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
    await userEvent.click(form.getByRole("button", { name: "Add preset" }));
    expect(await screen.findByText("Preset tiny added.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ name: "tiny", default: true, limits: { memoryBytes: 512 * MiB } });
  });

  it("deletes a preset after asking and explains a refused one", async () => {
    const calls = mockFetch({
      ...list,
      "DELETE /api/v1/limit-presets/medium": { status: 409, body: { error: { code: "preset_is_default", message: "" } } },
    });
    render(<Presets />);
    const row = within((await screen.findAllByRole("listitem"))[0]!);
    await userEvent.click(row.getByRole("button", { name: "Delete" }));
    await userEvent.click(row.getByRole("button", { name: "Delete preset" }));
    expect(await row.findByRole("alert")).toHaveTextContent("Make another preset the default first.");
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("makes a preset the default", async () => {
    const calls = mockFetch({ ...list, "PUT /api/v1/limit-presets/medium": { body: { ...presets[0], default: true } } });
    render(<Presets />);
    const row = within((await screen.findAllByRole("listitem"))[0]!);
    await userEvent.click(row.getByRole("button", { name: "Make default" }));
    expect(await screen.findByText("New sites now get the preset medium.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ name: "medium", default: true });
  });
});
