// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { PanelRealIPCard, RealIPCard } from "./RealIP";
import {
  fromRealIPForm,
  splitRanges,
  toRealIPForm,
  type RealIPView,
  type WebRealIP,
} from "./realip";
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
  tls: {
    enabled: true,
    active: true,
    hsts: false,
    domains: [],
    notAfter: null,
  },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const view = (realIp: WebRealIP): RealIPView => ({
  realIp,
  headers: [
    "X-Forwarded-For",
    "X-Real-IP",
    "CF-Connecting-IP",
    "True-Client-IP",
  ],
  cloudflare: ["173.245.48.0/20", "2400:cb00::/32"],
  cloudflareDate: "2026-11-22",
  maxRanges: 256,
});
const path = "/api/v1/sites/blog/real-ip";

describe("real IP form", () => {
  it("round-trips settings", () => {
    for (const r of [
      {},
      { cloudflare: true },
      { ranges: ["10.0.0.2", "198.51.100.0/24"], header: "X-Real-IP" },
      { cloudflare: true, ranges: ["10.0.0.2"] },
    ] satisfies WebRealIP[]) {
      expect(fromRealIPForm(toRealIPForm(r))).toEqual({ realIp: r });
    }
  });

  it("reads ranges from lines, spaces and commas", () => {
    expect(splitRanges(" 10.0.0.2\n\n198.51.100.0/24, 2001:db8::1 ")).toEqual([
      "10.0.0.2",
      "198.51.100.0/24",
      "2001:db8::1",
    ]);
  });

  it("refuses a switched-on form without a proxy", () => {
    expect(
      fromRealIPForm({ ...toRealIPForm({}), enabled: true }).error,
    ).toMatch(/Cloudflare or enter/);
    expect(
      fromRealIPForm({ ...toRealIPForm({ cloudflare: true }), enabled: false }),
    ).toEqual({ realIp: {} });
  });
});

describe("RealIPCard", () => {
  it("lets an admin trust Cloudflare and a load balancer", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view({}) },
      ["PUT " + path]: (body) => ({ body: view(body as WebRealIP) }),
    });
    const user = userEvent.setup();
    render(<RealIPCard site={site} admin />);

    await user.click(
      await screen.findByRole("checkbox", { name: /behind a proxy or CDN/ }),
    );
    await user.click(screen.getByRole("checkbox", { name: /Cloudflare/ }));
    await user.type(
      screen.getByRole("textbox", { name: "Own proxies" }),
      "10.0.0.2",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(
      await screen.findByText(/address from the proxy now/),
    ).toBeInTheDocument();
    expect(calls).toContainEqual({
      method: "PUT",
      path,
      body: { cloudflare: true, ranges: ["10.0.0.2"] },
    });
  });

  it("keeps own addresses read-only for other roles", async () => {
    const calls = mockFetch({
      ["GET " + path]: {
        body: view({ cloudflare: true, ranges: ["10.0.0.2"] }),
      },
      ["PUT " + path]: (body) => ({ body: view(body as WebRealIP) }),
    });
    const user = userEvent.setup();
    render(<RealIPCard site={site} admin={false} />);

    const box = await screen.findByRole("textbox", { name: "Own proxies" });
    expect(box).toHaveAttribute("readonly");
    await user.click(
      screen.getByRole("button", { name: "Remove own addresses" }),
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(calls).toContainEqual({
      method: "PUT",
      path,
      body: { cloudflare: true },
    });
  });

  it("shows a refused range", async () => {
    mockFetch({
      ["GET " + path]: { body: view({}) },
      ["PUT " + path]: {
        status: 422,
        body: {
          error: {
            code: "real_ip_rejected",
            message:
              "real IP rejected: range 10.0.0.0/24 contains 10.0.0.3, an address of this server; trust the proxy's own address instead",
          },
        },
      },
    });
    const user = userEvent.setup();
    render(<RealIPCard site={site} admin />);

    await user.click(
      await screen.findByRole("checkbox", { name: /behind a proxy or CDN/ }),
    );
    await user.type(
      screen.getByRole("textbox", { name: "Own proxies" }),
      "10.0.0.0/24",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Refused: range 10.0.0.0/24 contains 10.0.0.3",
    );
  });
});

describe("PanelRealIPCard", () => {
  const panelPath = "/api/v1/settings/panel-real-ip";

  it("lets an admin put the panel behind Cloudflare", async () => {
    const calls = mockFetch({
      ["GET " + panelPath]: { body: view({}) },
      ["PUT " + panelPath]: (body) => ({ body: view(body as WebRealIP) }),
    });
    const user = userEvent.setup();
    render(<PanelRealIPCard />);

    await user.click(
      await screen.findByRole("checkbox", { name: /panel is behind a proxy/ }),
    );
    await user.click(screen.getByRole("checkbox", { name: /Cloudflare/ }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(
      await screen.findByText(/address from the proxy now/),
    ).toBeInTheDocument();
    expect(calls).toContainEqual({
      method: "PUT",
      path: panelPath,
      body: { cloudflare: true },
    });
  });
});
