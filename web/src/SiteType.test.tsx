// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ChangeTypeCard, RemoveAccountCard } from "./SiteType";
import { typeChange, type Site } from "./sites";
import { mockFetch } from "./test-utils";

const site = (extra: Partial<Site> = {}) =>
  ({
    name: "shop",
    pool: "default",
    state: "active",
    type: "php",
    php: "8.4",
    account: true,
    domains: ["shop.example.com"],
    disabled: false,
    node: null,
    app: null,
    proxy: null,
    redirect: null,
    web: { dir: "", spa: false, assetCache: false },
    tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
    maintenance: { enabled: false, page: "", allowIps: [] },
    ...extra,
  }) as Site;

const php = { installed: ["8.3", "8.4"], supported: ["8.3", "8.4"], legacy: [] };

describe("typeChange", () => {
  it("keeps the account between types with one", () => {
    const c = typeChange(site(), { type: "static" });
    expect(c.stops[0]).toContain("PHP 8.4 pool stops");
    expect(c.starts[0]).toContain("files of htdocs");
    expect(c.stays.join(" ")).toContain("Linux account with its files, databases");
  });

  it("keeps the port between Node.js and app", () => {
    const c = typeChange(site({ type: "node", php: "" }), { type: "app", app: { workdir: "htdocs", program: "bin/server", args: [] } });
    expect(c.stops).toEqual(["The Node.js app stops. The new one keeps its port."]);
    expect(c.starts).toEqual(["The program bin/server starts on its own port."]);
  });

  it("says that a redirect without an account gets one", () => {
    const c = typeChange(site({ type: "redirect", php: "", account: false, redirect: { target: "https://example.org", status: 301, keepPath: true } }), {
      type: "php",
      php: "8.4",
    });
    expect(c.starts[0]).toContain("Linux account with its home is created in the pool default");
    expect(c.stops).toEqual(["nginx no longer redirects to https://example.org."]);
  });

  it("says that the account stays for a proxy", () => {
    const c = typeChange(site(), { type: "proxy", proxy: { target: "http://10.0.0.5", targetHost: false, insecure: false } });
    expect(c.stays.join(" ")).toContain("You can remove them afterwards");
  });
});

describe("ChangeTypeCard", () => {
  it("changes a PHP site into an app", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/node": { body: { installed: [], releases: [], installing: [] } },
      "POST /api/v1/sites/shop/type": { body: site({ type: "app", php: "" }) },
    });
    render(<ChangeTypeCard site={site()} php={php} onChange={async () => {}} />);
    await user.click(screen.getByRole("button", { name: "Change type" }));
    expect(screen.queryByRole("radio", { name: /^PHP/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: /^App/ }));
    await user.type(screen.getByLabelText(/^Program/), "bin/server");
    expect(screen.getByText(/The PHP 8.4 pool stops/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Change to app" }));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ type: "app", app: { workdir: "htdocs", program: "bin/server", args: [] } });
    expect(await screen.findByText("shop is an app site now.")).toBeInTheDocument();
  });

  it("checks a redirect target before sending", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "GET /api/v1/node": { body: { installed: [], releases: [], installing: [] } } });
    render(<ChangeTypeCard site={site()} php={php} onChange={async () => {}} />);
    await user.click(screen.getByRole("button", { name: "Change type" }));
    await user.click(screen.getByRole("radio", { name: /^Redirect/ }));
    await user.click(screen.getByRole("button", { name: "Change to redirect" }));
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });
});

describe("RemoveAccountCard", () => {
  it("removes the account after the name is typed", async () => {
    const user = userEvent.setup();
    const s = site({ type: "redirect", php: "", redirect: { target: "https://example.org", status: 301, keepPath: true } });
    const calls = mockFetch({ "DELETE /api/v1/sites/shop/account": { body: { ...s, account: false } } });
    render(<RemoveAccountCard site={s} onChange={async () => {}} />);
    expect(screen.getByText("This site still has a Linux account and files")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove account and files" }));
    const confirm = screen.getByRole("button", { name: "Remove account and files" });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText("Type shop to confirm"), "shop");
    await user.click(confirm);
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  });
});
