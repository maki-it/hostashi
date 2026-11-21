// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ObjectCacheSettings } from "./ObjectCache";
import { sizeChoices, wpConfig, type ObjectCache } from "./objcache";
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

const socket = "/run/hostashi-valkey/blog/valkey.sock";
const off: ObjectCache = { enabled: false, memoryMb: 64, minMb: 32, maxMb: 1024, memoryLimitBytes: 512 << 20, socket, status: { state: "", paused: false, usedBytes: 0 } };
const on: ObjectCache = { ...off, enabled: true, status: { state: "active", paused: false, usedBytes: 3 << 20 } };

describe("ObjectCacheSettings", () => {
  it("turns the cache on and shows how to connect", async () => {
    let cur = off;
    const calls = mockFetch({
      "GET /api/v1/sites/blog/object-cache": () => ({ body: cur }),
      "PUT /api/v1/sites/blog/object-cache": () => {
        cur = on;
        return { body: on };
      },
      "POST /api/v1/sites/blog/object-cache/flush": { body: { status: on.status } },
    });
    const user = userEvent.setup();
    render(<ObjectCacheSettings site={site} canSize={true} />);

    const box = await screen.findByRole("checkbox", { name: /Run an object cache/ });
    expect(screen.getByText("Off")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /Socket/ })).not.toBeInTheDocument();

    await user.click(box);
    expect(await screen.findByRole("status")).toHaveTextContent("The object cache runs now.");
    expect(calls).toContainEqual({ method: "PUT", path: "/api/v1/sites/blog/object-cache", body: { enabled: true } });
    expect(await screen.findByRole("textbox", { name: /Socket/ })).toHaveValue(socket);
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("3 MiB used of 64 MiB")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Flush cache" }));
    expect(await screen.findByRole("status")).toHaveTextContent("The object cache is empty now.");
  });

  it("lets admins and managers change the size, within half the memory limit", async () => {
    const calls = mockFetch({
      "GET /api/v1/sites/blog/object-cache": { body: on },
      "PUT /api/v1/sites/blog/object-cache/size": { body: { ...on, memoryMb: 256 } },
    });
    const user = userEvent.setup();
    render(<ObjectCacheSettings site={site} canSize={true} />);

    const select = await screen.findByRole("combobox", { name: "Size" });
    const offered = Array.from((select as HTMLSelectElement).options).map((o) => o.value);
    expect(offered).toEqual(["32", "64", "128", "256"]);
    await user.selectOptions(select, "256");
    await user.click(screen.getByRole("button", { name: "Save size" }));
    expect(calls).toContainEqual({ method: "PUT", path: "/api/v1/sites/blog/object-cache/size", body: { memoryMb: 256 } });
  });

  it("shows the size without a control to users", async () => {
    mockFetch({ "GET /api/v1/sites/blog/object-cache": { body: on } });
    render(<ObjectCacheSettings site={site} canSize={false} />);
    expect(await screen.findByText(/Size: 64 MiB/)).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Size" })).not.toBeInTheDocument();
  });

  it("cannot be turned on while the site is disabled", async () => {
    mockFetch({ "GET /api/v1/sites/blog/object-cache": { body: off } });
    render(<ObjectCacheSettings site={{ ...site, disabled: true }} canSize={true} />);
    expect(await screen.findByRole("checkbox", { name: /Run an object cache/ })).toBeDisabled();
  });

  it("reports a cache that did not start", async () => {
    mockFetch({
      "GET /api/v1/sites/blog/object-cache": { body: off },
      "PUT /api/v1/sites/blog/object-cache": { status: 409, body: { error: { code: "agent_failed_precondition", message: "did not start" } } },
    });
    const user = userEvent.setup();
    render(<ObjectCacheSettings site={site} canSize={true} />);
    await user.click(await screen.findByRole("checkbox", { name: /Run an object cache/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The cache did not start.");
  });
});

describe("sizeChoices", () => {
  it("keeps the current size and drops what does not fit", () => {
    expect(sizeChoices({ memoryMb: 64, minMb: 32, maxMb: 1024, memoryLimitBytes: 0 })).toEqual([32, 64, 128, 256, 512, 1024]);
    expect(sizeChoices({ memoryMb: 512, minMb: 32, maxMb: 1024, memoryLimitBytes: 256 << 20 })).toEqual([32, 64, 128, 512]);
  });

  it("writes the WordPress constants", () => {
    expect(wpConfig(socket)).toBe(`define('WP_REDIS_SCHEME', 'unix');\ndefine('WP_REDIS_PATH', '${socket}');`);
  });
});
