// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { mockFetch } from "./test-utils";
import { SitesByUsage, SiteUsageCard } from "./Usage";
import { cores, count, ofCores, percent, type SitePoint, type SiteTotal } from "./usage";

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
  cores: 4,
});

describe("usage helpers", () => {
  it("formats cores and counts", () => {
    expect(percent(0)).toBe("0 %");
    expect(percent(2.25)).toBe("2.3 %");
    expect(percent(37.5)).toBe("38 %");
    expect(ofCores(150, 4)).toBe(37.5);
    expect(ofCores(50, 0.5)).toBe(100);
    expect(ofCores(50, 0)).toBe(50);
    expect(cores(0)).toBe("0 cores");
    expect(cores(25)).toBe("0.25 cores");
    expect(cores(100)).toBe("1.0 core");
    expect(cores(150)).toBe("1.5 cores");
    expect(count(950)).toBe("950");
    expect(count(25000)).toBe("25 k");
    expect(count(3_400_000)).toBe("3.4 M");
  });
});

describe("SiteUsageCard", () => {
  it("draws the site's charts against the server's cores and its limits, and switches the range", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/shop/usage?range=1h": { body: { site: "shop", range: "1h", step: 15, cpus: 4, cores: 2, points: [point("2026-11-24T09:00:00Z", 20), point("2026-11-24T09:00:15Z", 80)] } },
      "GET /api/v1/sites/shop/usage?range=30d": { body: { site: "shop", range: "30d", step: 3600, cpus: 4, cores: 2, points: [] } },
      "GET /api/v1/sites/shop/limits": { body: { set: true, preset: "small", limits: { memoryBytes: 512 * 1024 ** 2 } } },
    });
    render(<SiteUsageCard site="shop" />);

    expect(await screen.findByRole("figure", { name: "CPU use in percent of the cores the site may use" })).toBeInTheDocument();
    expect(screen.getByText(/100 % is the limit of 2\.0 cores/)).toBeInTheDocument();
    expect(screen.getByRole("figure", { name: "Web traffic" })).toBeInTheDocument();
    expect(screen.getByRole("figure", { name: "Disk reads and writes" })).toBeInTheDocument();
    expect(await screen.findByText("of 512 MiB limit")).toBeInTheDocument();
    expect(screen.getByText(/0\.80 cores of 2\.0 cores busy · 2\.5 requests\/s · 7 processes · 64 MiB file cache/)).toBeInTheDocument();

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
        body: { range: "24h", sort: "cpu", cpus: 4, sites: [total("shop", 80, 100 * 1024 ** 2, 5 * 1024 ** 3), { ...total("blog", 5, 900 * 1024 ** 2, 1024), cores: 0.5 }] },
      },
    });
    render(<SitesByUsage />);

    const table = await screen.findByRole("table", { name: "Sites by usage" });
    const names = () => within(table).getAllByRole("link").map((a) => a.textContent);
    expect(names()).toEqual(["shop", "blog"]);
    expect(within(table).getByRole("link", { name: "shop" })).toHaveAttribute("href", "#/sites/shop/usage");
    expect(within(table).getByText("5.0 GiB")).toBeInTheDocument();
    // 80 % of one core on 4 cores is a fifth; 5 % of a core on a limit of
    // half a core is a tenth of the limit.
    expect(within(table).getByText("20 %")).toBeInTheDocument();
    expect(within(table).getByText("10 %")).toBeInTheDocument();
    expect(within(table).getByText(/of 0\.50 cores \(limit\)/)).toBeInTheDocument();

    await user.click(within(table).getByRole("button", { name: /Memory/ }));
    await user.click(within(table).getByRole("button", { name: /Memory/ }));
    expect(names()).toEqual(["blog", "shop"]);
  });
});
