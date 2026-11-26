// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { CloneCard, CloneStatusCard } from "./Clone";
import type { Clone } from "./clones";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "shop",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["shop.example.com"],
  tls: { enabled: true, active: true, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const staging: Site = { ...site, name: "staging-shop", domains: ["staging.shop.example.com"] };

const clone = (c: Partial<Clone> = {}): Clone => ({
  source: "shop",
  staging: true,
  state: "done",
  refresh: false,
  filesDone: true,
  config: "done",
  databases: [{ source: "shop_wp", target: "staging-shop_wp", done: true }],
  startedAt: "2026-11-26T10:00:00Z",
  finishedAt: "2026-11-26T10:05:00Z",
  ...c,
});

const profile = (p: string) => ({ body: { profile: p, hardening: {}, defaults: {}, changed: false, choices: { profiles: [] } } });

describe("CloneCard", () => {
  it("creates a staging copy and shows its password once", async () => {
    const calls = mockFetch({
      "GET /api/v1/pools": { body: { pools: [{ name: "default", path: "/srv", sites: 1, available: true }] } },
      "POST /api/v1/sites/shop/clone": { status: 202, body: { site: staging, access: { user: "staging", password: "s3cret-pass" } } },
    });
    const user = userEvent.setup();
    render(<CloneCard site={site} />);

    await user.click(screen.getByRole("button", { name: "Copy site" }));
    expect(screen.getByRole("textbox", { name: "System name of the copy" })).toHaveValue("staging-shop");
    expect(screen.getByRole("textbox", { name: "Domain of the copy" })).toHaveValue("staging.shop.example.com");
    expect(screen.getByText(/send real mail/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create staging copy" }));

    expect(await screen.findByRole("heading", { name: "Save the password of the copy now" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Password" })).toHaveValue("s3cret-pass");
    expect(calls).toContainEqual({
      method: "POST",
      path: "/api/v1/sites/shop/clone",
      body: { name: "staging-shop", domain: "staging.shop.example.com", staging: true },
    });

    await user.click(screen.getByRole("button", { name: "I have saved it" }));
    expect(screen.queryByText("s3cret-pass")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Password" })).toBeNull();
  });

  it("checks the name before asking the server", async () => {
    const calls = mockFetch({ "GET /api/v1/pools": { body: { pools: [] } } });
    const user = userEvent.setup();
    render(<CloneCard site={site} />);

    await user.click(screen.getByRole("button", { name: "Copy site" }));
    await user.clear(screen.getByRole("textbox", { name: "System name of the copy" }));
    await user.type(screen.getByRole("textbox", { name: "System name of the copy" }), "Bad Name");
    await user.click(screen.getByRole("button", { name: "Create staging copy" }));

    expect(screen.getByText(/lowercase letters/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
  });

  it("explains a lack of space", async () => {
    mockFetch({
      "GET /api/v1/pools": { body: { pools: [] } },
      "POST /api/v1/sites/shop/clone": { status: 409, body: { error: { code: "no_space", message: "the pool has 1.0 GiB free, the copy needs 2.2 GiB" } } },
    });
    const user = userEvent.setup();
    render(<CloneCard site={site} />);

    await user.click(screen.getByRole("button", { name: "Copy site" }));
    await user.click(screen.getByRole("checkbox", { name: "Staging copy of shop" }));
    await user.click(screen.getByRole("button", { name: "Create copy" }));

    expect(await screen.findByText("The pool has 1.0 GiB free, the copy needs 2.2 GiB.")).toBeInTheDocument();
  });
});

describe("CloneStatusCard", () => {
  it("refreshes only after the copy's name is typed", async () => {
    let refreshed = 0;
    const calls = mockFetch({
      "GET /api/v1/sites/staging-shop/profile": profile("wordpress"),
      "POST /api/v1/sites/staging-shop/clone/refresh": () => {
        refreshed++;
        return { status: 202, body: clone({ state: "copying", refresh: true }) };
      },
    });
    const user = userEvent.setup();
    render(<CloneStatusCard site={staging} clone={clone()} staff onRefresh={() => undefined} />);

    expect(screen.getByRole("link", { name: "shop" })).toHaveAttribute("href", "#/sites/shop");
    await user.click(screen.getByRole("button", { name: "Refresh from shop" }));
    const go = screen.getByRole("button", { name: "Replace staging-shop with shop" });
    expect(screen.getByText("staging-shop", { selector: "code" })).toHaveClass("ui-confirm-phrase");
    expect(go).toBeDisabled();
    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "shop");
    expect(go).toBeDisabled();
    await user.clear(screen.getByRole("textbox", { name: /to confirm/ }));
    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "staging-shop");
    await user.click(go);

    expect(refreshed).toBe(1);
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    expect(screen.queryByRole("list", { name: "What to change" })).toBeNull();
  });

  it("shows the progress and a checklist for other apps", async () => {
    mockFetch({ "GET /api/v1/sites/staging-shop/profile": profile("") });
    const { rerender } = render(
      <CloneStatusCard site={staging} clone={clone({ state: "copying", filesDone: true, config: "", databases: [{ source: "shop_wp", target: "staging-shop_wp", done: false }] })} staff onRefresh={() => undefined} />,
    );

    expect(screen.getByRole("list", { name: "Copy progress" })).toHaveTextContent("✓ Files");
    expect(screen.getByRole("list", { name: "Copy progress" })).toHaveTextContent("Databases (0 of 1)");
    expect(screen.queryByRole("button", { name: /Refresh/ })).toBeNull();

    rerender(<CloneStatusCard site={staging} clone={clone()} staff onRefresh={() => undefined} />);
    expect(await screen.findByRole("list", { name: "What to change" })).toHaveTextContent("staging-shop_wp");
  });

  it("shows why the last copy failed and hides refresh for users", () => {
    mockFetch({ "GET /api/v1/sites/staging-shop/profile": profile("wordpress") });
    render(<CloneStatusCard site={staging} clone={clone({ state: "failed", error: "configuration: the database is not one of the copied ones" })} staff={false} onRefresh={() => undefined} />);

    expect(screen.getByText(/not one of the copied ones/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Refresh/ })).toBeNull();
  });
});
