// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { mockFetch } from "./test-utils";
import { SitesByUsage, SiteUsageCard } from "./Usage";
import { cores, count, type SitePoint, type SiteTotal } from "./usage";

const point = (at: string, cpu: number): SitePoint => ({
  at,
  cpu,
  mem: 256 * 1024 ** 2,
  cache: 64 * 1024 ** 2,
  tasks: 7,
  ioRead: 1000,
  ioWrite: 2000,
  httpReq: 2.5,
  httpIn: 500,
  httpOut: 40000,
});

const total = (site: string, cpuAvg: number, memAvg: number, out: number): SiteTotal => ({
  site,
  cpuAvg,
  cpuMax: cpuAvg * 2,
  memAvg,
  memMax: memAvg,
  ioReadBytes: 0,
  ioWriteBytes: 0,
  httpRequests: 1200,
  httpInBytes: 0,
  httpOutBytes: out,
});

describe("usage helpers", () => {
  it("formats cores and counts", () => {
    expect(cores(0)).toBe("0 %");
    expect(cores(2.25)).toBe("2.3 %");
    expect(cores(150)).toBe("150 %");
    expect(count(950)).toBe("950");
    expect(count(25000)).toBe("25 k");
    expect(count(3_400_000)).toBe("3.4 M");
  });
});

describe("SiteUsageCard", () => {
  it("draws the site's charts with its memory limit and switches the range", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/shop/usage?range=1h": { body: { site: "shop", range: "1h", step: 15, points: [point("2026-11-24T09:00:00Z", 20), point("2026-11-24T09:00:15Z", 80)] } },
      "GET /api/v1/sites/shop/usage?range=30d": { body: { site: "shop", range: "30d", step: 3600, points: [] } },
      "GET /api/v1/sites/shop/limits": { body: { set: true, preset: "small", limits: { memoryBytes: 512 * 1024 ** 2 } } },
    });
    render(<SiteUsageCard site="shop" />);

    expect(await screen.findByRole("figure", { name: "CPU use in percent of one core" })).toBeInTheDocument();
    expect(screen.getByRole("figure", { name: "Web traffic" })).toBeInTheDocument();
    expect(screen.getByRole("figure", { name: "Disk reads and writes" })).toBeInTheDocument();
    expect(await screen.findByText("of 512 MiB limit")).toBeInTheDocument();
    expect(screen.getByText(/2\.5 requests\/s · 7 processes · 64 MiB file cache/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "30d" }));
    expect(await screen.findByRole("heading", { name: "Usage, last 30 days" })).toBeInTheDocument();
    expect(calls.map((c) => c.path)).toContain("/api/v1/sites/shop/usage?range=30d");
  });

  it("shows an error", async () => {
    mockFetch({
      "GET /api/v1/sites/shop/usage?range=1h": { status: 500, body: { error: { code: "internal", message: "the samples could not be read" } } },
      "GET /api/v1/sites/shop/limits": { status: 404, body: { error: { code: "not_found", message: "no" } } },
    });
    render(<SiteUsageCard site="shop" />);

    expect(await screen.findByText(/could not be read/)).toBeInTheDocument();
  });
});

describe("SitesByUsage", () => {
  it("ranks the sites and sorts by a column", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/usage/sites?range=24h&sort=cpu": {
        body: { range: "24h", sort: "cpu", sites: [total("shop", 80, 100 * 1024 ** 2, 5 * 1024 ** 3), total("blog", 5, 900 * 1024 ** 2, 1024)] },
      },
    });
    render(<SitesByUsage />);

    const table = await screen.findByRole("table", { name: "Sites by usage" });
    const names = () => within(table).getAllByRole("link").map((a) => a.textContent);
    expect(names()).toEqual(["shop", "blog"]);
    expect(within(table).getByRole("link", { name: "shop" })).toHaveAttribute("href", "#/sites/shop/usage");
    expect(within(table).getByText("5.0 GiB")).toBeInTheDocument();

    await user.click(within(table).getByRole("button", { name: /Memory/ }));
    await user.click(within(table).getByRole("button", { name: /Memory/ }));
    expect(names()).toEqual(["blog", "shop"]);
  });
});
