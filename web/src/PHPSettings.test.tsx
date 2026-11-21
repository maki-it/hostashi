// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { PHPSettingsCard } from "./PHPSettings";
import { fromForm, MiB, toForm, type PHPSettings, type PHPSettingsView } from "./phpsettings";
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

const defaults: Required<PHPSettings> = {
  memoryLimit: 256 * MiB,
  uploadMaxFilesize: 64 * MiB,
  postMaxSize: 64 * MiB,
  maxExecutionTime: 60,
  maxInputTime: 60,
  maxInputVars: 3000,
  maxFileUploads: 20,
  timezone: "UTC",
  displayErrors: false,
  errorReporting: "production",
  sessionGcMaxlifetime: 1440,
  shortOpenTag: false,
};

const bounds: PHPSettingsView["bounds"] = {
  memoryLimit: { min: 32 * MiB, max: 4096 * MiB },
  uploadMaxFilesize: { min: MiB, max: 2048 * MiB },
  postMaxSize: { min: MiB, max: 2048 * MiB },
  maxExecutionTime: { min: 10, max: 3600 },
  maxInputTime: { min: 10, max: 3600 },
  maxInputVars: { min: 1000, max: 100000 },
  maxFileUploads: { min: 1, max: 500 },
  sessionGcMaxlifetime: { min: 60, max: 2592000 },
};

const view = (settings: PHPSettings): PHPSettingsView => ({
  settings,
  effective: { ...defaults, ...settings },
  defaults,
  bounds,
  errorReportingPresets: ["production", "development", "errors"],
});

describe("phpsettings form", () => {
  it("round-trips settings and leaves defaults out", () => {
    const s: PHPSettings = { memoryLimit: 512 * MiB, maxExecutionTime: 300, timezone: "Europe/Berlin", displayErrors: true };
    const f = toForm(s);
    expect(f.memoryLimit).toBe("512");
    expect(f.uploadMaxFilesize).toBe("");
    expect(f.displayErrors).toBe("on");
    expect(f.shortOpenTag).toBe("");
    expect(fromForm(f, bounds)).toEqual({ settings: s, errors: {} });
  });

  it("refuses values out of range and non-numbers", () => {
    const f = { ...toForm({}), memoryLimit: "-1", maxInputVars: "12", maxFileUploads: "2.5" };
    const { errors } = fromForm(f, bounds);
    expect(Object.keys(errors).sort()).toEqual(["maxFileUploads", "maxInputVars", "memoryLimit"]);
    expect(errors.memoryLimit).toMatch(/32 to 4096/);
  });
});

describe("PHPSettingsCard", () => {
  it("shows defaults as placeholders and saves only set fields", async () => {
    const calls = mockFetch({
      "GET /api/v1/sites/blog/php/settings": { body: view({}) },
      "PUT /api/v1/sites/blog/php/settings": { body: view({ memoryLimit: 512 * MiB, timezone: "Europe/Berlin" }) },
    });
    const user = userEvent.setup();
    render(<PHPSettingsCard site={site} />);

    const mem = await screen.findByRole("textbox", { name: "Memory limit (MiB)" });
    expect(mem).toHaveAttribute("placeholder", "256");
    expect(mem).toHaveValue("");
    expect(screen.getByRole("button", { name: "Reset all to defaults" })).toBeDisabled();

    await user.type(mem, "512");
    await user.type(screen.getByRole("combobox", { name: "Time zone" }), "Europe/Berlin");
    await user.click(screen.getByRole("button", { name: "Save PHP settings" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Saved.");
    expect(calls).toContainEqual({
      method: "PUT",
      path: "/api/v1/sites/blog/php/settings",
      body: { memoryLimit: 512 * MiB, timezone: "Europe/Berlin" },
    });
    expect(screen.getByRole("button", { name: "Reset all to defaults" })).toBeEnabled();
  });

  it("checks bounds before sending", async () => {
    const calls = mockFetch({ "GET /api/v1/sites/blog/php/settings": { body: view({}) } });
    const user = userEvent.setup();
    render(<PHPSettingsCard site={site} />);

    await user.type(await screen.findByRole("textbox", { name: "Execution time (s)" }), "5");
    await user.click(screen.getByRole("button", { name: "Save PHP settings" }));

    expect(await screen.findByText("Some values are out of range. Check the marked fields.")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Execution time (s)" })).toHaveAttribute("aria-invalid", "true");
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
  });

  it("marks the field the server refused", async () => {
    mockFetch({
      "GET /api/v1/sites/blog/php/settings": { body: view({}) },
      "PUT /api/v1/sites/blog/php/settings": {
        status: 422,
        body: { error: { code: "php_setting_rejected", message: "php setting date.timezone: unknown time zone" }, key: "date.timezone" },
      },
    });
    const user = userEvent.setup();
    render(<PHPSettingsCard site={site} />);

    await user.type(await screen.findByRole("combobox", { name: "Time zone" }), "Mars/Olympus");
    await user.click(screen.getByRole("button", { name: "Save PHP settings" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("A value was refused: date.timezone: unknown time zone.");
    expect(screen.getByRole("combobox", { name: "Time zone" })).toHaveAttribute("aria-invalid", "true");
  });

  it("resets everything to the defaults", async () => {
    const calls = mockFetch({
      "GET /api/v1/sites/blog/php/settings": { body: view({ maxInputVars: 5000, shortOpenTag: true }) },
      "PUT /api/v1/sites/blog/php/settings": { body: view({}) },
    });
    const user = userEvent.setup();
    render(<PHPSettingsCard site={site} />);

    expect(await screen.findByRole("textbox", { name: "Input variables" })).toHaveValue("5000");
    expect(screen.getByRole("combobox", { name: "Short open tag" })).toHaveValue("on");

    await user.click(screen.getByRole("button", { name: "Reset all to defaults" }));
    expect(await screen.findByRole("status")).toHaveTextContent("back to the defaults");
    expect(calls).toContainEqual({ method: "PUT", path: "/api/v1/sites/blog/php/settings", body: {} });
    expect(screen.getByRole("textbox", { name: "Input variables" })).toHaveValue("");
  });
});
