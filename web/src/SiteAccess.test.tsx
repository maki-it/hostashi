// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { setLanguage } from "./i18n";
import { fromAccessForm, lockout, toAccessForm, type AccessSettings, type AccessView } from "./access";
import { SiteAccess } from "./SiteAccess";
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
  tls: { enabled: true, active: true, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const off: AccessSettings = { ipMode: "", ips: [], password: false, paths: [], bypass: [] };
const kim = { name: "kim", createdAt: "2026-11-24T08:00:00Z", updatedAt: "2026-11-24T08:00:00Z" };

const view = (a: Partial<AccessSettings>, users = [kim]): AccessView => ({
  ...off,
  ...a,
  users,
  limits: { ips: 256, paths: 20, bypass: 64, users: 50, minPassword: 12, maxPassword: 72 },
  clientAddress: "203.0.113.9",
});
const path = "/api/v1/sites/blog/access";

describe("access form", () => {
  it("round-trips settings", () => {
    for (const a of [
      off,
      { ...off, ipMode: "allow", ips: ["203.0.113.7", "2001:db8::/48"] },
      { ...off, ipMode: "deny", ips: ["198.51.100.0/24"], password: true, paths: ["/wp-admin"], bypass: ["203.0.113.0/24"] },
      { ...off, password: true },
    ] satisfies AccessSettings[]) {
      expect(fromAccessForm(toAccessForm(a), 1)).toEqual({ settings: a });
    }
  });

  it("refuses what the server would refuse", () => {
    expect(fromAccessForm({ ...toAccessForm(off), ipMode: "allow" }, 1).error).toMatch(/at least one address/);
    expect(fromAccessForm({ ...toAccessForm(off), password: true }, 0).error).toMatch(/Add a user first/);
    expect(fromAccessForm({ ...toAccessForm(off), password: true, scope: "paths" }, 1).error).toMatch(/at least one path/);
    expect(fromAccessForm({ ...toAccessForm(off), password: true, scope: "paths", paths: "wp-admin" }, 1).error).toMatch(/must start with/);
  });

  it("warns when an allowlist leaves out the caller", () => {
    const f = (ips: string) => ({ ...toAccessForm(off), ipMode: "allow" as const, ips });
    expect(lockout(f("198.51.100.1"), "203.0.113.9")).toBe(true);
    expect(lockout(f("203.0.113.9"), "203.0.113.9")).toBe(false);
    expect(lockout(f("203.0.113.0/24"), "203.0.113.9")).toBe(false);
    expect(lockout(f("203.0.112.0/24"), "203.0.113.9")).toBe(true);
    expect(lockout({ ...f("198.51.100.1"), ipMode: "deny" }, "203.0.113.9")).toBe(false);
  });
});

describe("SiteAccess", () => {
  it("protects /wp-admin with a password", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view({}) },
      ["PUT " + path]: (body) => ({ body: view(body as AccessSettings) }),
    });
    const user = userEvent.setup();
    render(<SiteAccess site={site} />);

    await user.click(await screen.findByRole("checkbox", { name: /Ask for a user name/ }));
    await user.click(screen.getByRole("radio", { name: /Only for these paths/ }));
    await user.type(screen.getByRole("textbox", { name: "Paths" }), "/wp-admin");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText(/password is asked on 1 path/)).toBeInTheDocument();
    expect(calls).toContainEqual({ method: "PUT", path, body: { ipMode: "", ips: [], password: true, paths: ["/wp-admin"], bypass: [] } });
  });

  it("warns before an allowlist locks the caller out", async () => {
    mockFetch({ ["GET " + path]: { body: view({}) } });
    const user = userEvent.setup();
    render(<SiteAccess site={site} />);

    await user.click(await screen.findByRole("radio", { name: /Only these addresses/ }));
    await user.type(screen.getByRole("textbox", { name: "Addresses" }), "198.51.100.1");
    expect(screen.getByText(/Your own address is not on the list/)).toBeInTheDocument();
    expect(screen.getByText("203.0.113.9")).toBeInTheDocument();
  });

  it("shows a generated password once", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view({}, []) },
      ["POST " + path + "/users"]: { status: 201, body: { name: "office", password: "Abc123Abc123Abc123Abc123" } },
    });
    const user = userEvent.setup();
    render(<SiteAccess site={site} />);

    expect(await screen.findByText("No users yet.")).toBeInTheDocument();
    await user.type(screen.getByRole("textbox", { name: "User name" }), "office");
    await user.click(screen.getByRole("button", { name: "Add user" }));

    expect(await screen.findByRole("heading", { name: "Save this password now" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("Abc123Abc123Abc123Abc123")).toBeInTheDocument();
    expect(calls).toContainEqual({ method: "POST", path: path + "/users", body: { name: "office" } });

    await user.click(screen.getByRole("button", { name: "I have saved it" }));
    expect(screen.queryByDisplayValue("Abc123Abc123Abc123Abc123")).not.toBeInTheDocument();
  });

  it("asks before removing a user and keeps the last one while the password is on", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view({}, [kim, { ...kim, name: "office" }]) },
      ["DELETE " + path + "/users/office"]: { status: 204 },
    });
    const user = userEvent.setup();
    render(<SiteAccess site={site} />);

    await user.click(await screen.findByRole("button", { name: "Remove office" }));
    await user.click(screen.getByRole("button", { name: "Remove office?" }));
    expect(await screen.findByText("User office removed.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("disables removing the last user while the password is on", async () => {
    mockFetch({ ["GET " + path]: { body: view({ password: true }) } });
    render(<SiteAccess site={site} />);
    expect(await screen.findByRole("button", { name: "The last user stays while the password is on" })).toBeDisabled();
  });

  it("explains a snippet conflict", async () => {
    mockFetch({
      ["GET " + path]: { body: view({}) },
      ["PUT " + path]: { status: 422, body: { error: { code: "access_conflict", message: "the server snippet uses allow, which the site's access protection sets" } } },
    });
    const user = userEvent.setup();
    render(<SiteAccess site={site} />);

    await user.click(await screen.findByRole("radio", { name: /Block these addresses/ }));
    await user.type(screen.getByRole("textbox", { name: "Addresses" }), "198.51.100.1");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText(/custom nginx snippet/)).toBeInTheDocument();
  });

  it("speaks German", async () => {
    setLanguage("de");
    const calls = mockFetch({
      ["GET " + path]: { body: view({}) },
      ["PUT " + path]: (body) => ({ body: view(body as AccessSettings) }),
    });
    const user = userEvent.setup();
    render(<SiteAccess site={site} />);

    expect(await screen.findByRole("heading", { name: "Zugriffsschutz" })).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: /Benutzername und Passwort abfragen/ }));
    await user.click(screen.getByRole("radio", { name: /Nur für diese Pfade/ }));
    await user.type(screen.getByRole("textbox", { name: "Pfade" }), "/wp-admin");
    await user.click(screen.getByRole("button", { name: "Speichern" }));

    expect(await screen.findByText("Gespeichert: auf 1 Pfad wird ein Passwort abgefragt.")).toBeInTheDocument();
    expect(calls.length).toBeGreaterThan(0);
  });
});
