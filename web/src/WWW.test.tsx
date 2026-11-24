// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";
import { WWWCard, wwwExample } from "./WWW";

const site = (o: Partial<Site>): Site => ({
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
  ...o,
});

const pair = { bare: "blog.example.com", www: "www.blog.example.com" };

describe("www redirect", () => {
  it("names the example pair", () => {
    expect(wwwExample(site({}))).toEqual(pair);
    expect(wwwExample(site({ domains: ["www.blog.example.com"] }))).toEqual(pair);
    expect(wwwExample(site({ domains: ["shop.example.com"], www: { mode: "off", pairs: [pair], missing: [] } }))).toEqual(pair);
    // Older answers without www still render.
    expect(wwwExample(site({ domains: [] }))).toEqual({ bare: "example.com", www: "www.example.com" });
  });

  it("offers to add the missing name and keeps the modes off without a pair", async () => {
    const onDomains = vi.fn();
    const calls = mockFetch({ "PUT /api/v1/sites/blog/domains": { body: site({}) } });
    render(
      <WWWCard
        site={site({ www: { mode: "off", pairs: [], missing: ["www.blog.example.com"] } })}
        onChange={() => Promise.resolve()}
        onDomains={onDomains}
      />,
    );

    expect(screen.getByRole("radio", { name: /to the www name/ })).toBeDisabled();
    expect(screen.getByRole("radio", { name: /No redirect/ })).toBeChecked();

    await userEvent.click(screen.getByRole("button", { name: /Add www.blog.example.com/ }));
    expect(calls).toContainEqual({ method: "PUT", path: "/api/v1/sites/blog/domains", body: { domains: ["blog.example.com", "www.blog.example.com"] } });
    expect(onDomains).toHaveBeenCalledWith(["blog.example.com", "www.blog.example.com"]);
    expect(await screen.findByText(/served over HTTP until its certificate also names www.blog.example.com/)).toBeInTheDocument();
  });

  it("sets the mode and lists the live redirects", async () => {
    const calls = mockFetch({ "PUT /api/v1/sites/blog/www": { body: site({}) } });
    render(
      <WWWCard
        site={site({ domains: ["blog.example.com", "www.blog.example.com"], www: { mode: "to-non-www", pairs: [pair], missing: [] } })}
        onChange={() => Promise.resolve()}
        onDomains={() => undefined}
      />,
    );

    expect(screen.getByText("www.blog.example.com → https://blog.example.com")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: /to the www name/ }));
    expect(calls).toContainEqual({ method: "PUT", path: "/api/v1/sites/blog/www", body: { mode: "to-www" } });
    expect(await screen.findByText(/names without www now redirect/)).toBeInTheDocument();
  });

  it("shows the server's refusal", async () => {
    mockFetch({ "PUT /api/v1/sites/blog/www": { status: 422, body: { error: "invalid_argument", message: "add www.blog.example.com as a domain first" } } });
    render(
      <WWWCard
        site={site({ domains: ["blog.example.com", "www.blog.example.com"], www: { mode: "off", pairs: [pair], missing: [] } })}
        onChange={() => Promise.resolve()}
        onDomains={() => undefined}
      />,
    );

    await userEvent.click(screen.getByRole("radio", { name: /without www/ }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });
});
