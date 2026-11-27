// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Pools } from "./Pools";
import { defaultPool, poolNameProblem, poolUsed, type Pool } from "./sites";
import { mockFetch } from "./test-utils";

const GiB = 2 ** 30;
const home: Pool = { name: "default", path: "/home", sites: 3, available: true, quota: "enforced", freeBytes: 20 * GiB, totalBytes: 38 * GiB, default: true };
const vol1: Pool = { name: "vol1", path: "/srv/vol1", sites: 1, available: true, quota: "counting", freeBytes: 150 * GiB, totalBytes: 200 * GiB, default: false };
const gone: Pool = { name: "vol2", path: "/srv/vol2", sites: 0, available: false, default: false };

describe("Pools", () => {
  it("lists the pools with usage, quota and the default", async () => {
    mockFetch({ "GET /api/v1/pools": { body: { pools: [home, vol1, gone] } } });
    render(<Pools />);

    const card = await screen.findByRole("region", { name: "default" });
    expect(within(card).getByText("Default")).toBeInTheDocument();
    expect(within(card).getByText("/home")).toBeInTheDocument();
    expect(within(card).getByText("3 sites")).toBeInTheDocument();
    expect(within(card).getByRole("meter", { name: "Used" })).toHaveAttribute("aria-valuenow", "47");
    expect(within(card).queryByRole("button", { name: "Make default" })).not.toBeInTheDocument();

    const v1 = screen.getByRole("region", { name: "vol1" });
    expect(within(v1).getByText("Usage counted, limits not enforced")).toBeInTheDocument();
    expect(within(v1).getByText(/Disk limits on this pool are not enforced/)).toBeInTheDocument();

    const v2 = screen.getByRole("region", { name: "vol2" });
    expect(within(v2).getByText("Not available")).toBeInTheDocument();
    expect(within(v2).getByRole("button", { name: "Make default" })).toBeDisabled();
    expect(within(v2).queryByRole("meter")).not.toBeInTheDocument();
    expect(screen.getByText(/hostashi pool add \[-default\] NAME PATH/)).toBeInTheDocument();
  });

  it("makes a pool the default", async () => {
    const user = userEvent.setup();
    let pools = [home, vol1];
    const calls = mockFetch({
      "GET /api/v1/pools": () => ({ body: { pools } }),
      "POST /api/v1/pools/vol1/default": () => {
        pools = [
          { ...home, default: false },
          { ...vol1, default: true },
        ];
        return { body: pools[1] };
      },
    });
    render(<Pools />);

    const v1 = await screen.findByRole("region", { name: "vol1" });
    await user.click(within(v1).getByRole("button", { name: "Make default" }));
    expect(await screen.findByText("New sites now land on vol1.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/pools/vol1/default")).toBe(true);
    expect(await within(screen.getByRole("region", { name: "vol1" })).findByText("Default")).toBeInTheDocument();
  });

  it("renames a pool and checks the name first", async () => {
    const user = userEvent.setup();
    let pools = [home, vol1];
    const calls = mockFetch({
      "GET /api/v1/pools": () => ({ body: { pools } }),
      "POST /api/v1/pools/vol1/rename": () => {
        pools = [home, { ...vol1, name: "fast" }];
        return { body: pools[1] };
      },
    });
    render(<Pools />);

    const v1 = await screen.findByRole("region", { name: "vol1" });
    await user.click(within(v1).getByRole("button", { name: "Rename" }));
    const input = within(v1).getByLabelText("New name");
    const save = within(v1).getByRole("button", { name: "Rename pool" });
    expect(save).toBeDisabled();

    await user.clear(input);
    await user.type(input, "default");
    expect(within(v1).getByText("Another pool already has this name.")).toBeInTheDocument();
    expect(save).toBeDisabled();

    await user.clear(input);
    await user.type(input, "Fast");
    expect(within(v1).getByText(/Lowercase letters/)).toBeInTheDocument();

    await user.clear(input);
    await user.type(input, "fast");
    await user.click(save);
    expect(await screen.findByText("The pool vol1 is now called fast.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "fast" });
    expect(await screen.findByRole("region", { name: "fast" })).toBeInTheDocument();
  });

  it("shows the server's refusal", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/pools": { body: { pools: [home, vol1] } },
      "POST /api/v1/pools/vol1/default": { status: 503, body: { error: { code: "agent_unavailable", message: "the agent could not do this, try again" } } },
    });
    render(<Pools />);

    const v1 = await screen.findByRole("region", { name: "vol1" });
    await user.click(within(v1).getByRole("button", { name: "Make default" }));
    expect(await screen.findByText("the agent could not do this, try again")).toBeInTheDocument();
  });
});

describe("pool helpers", () => {
  it("finds the default", () => {
    expect(defaultPool([home, vol1])).toBe("default");
    expect(defaultPool([{ ...home, default: false }, { ...vol1, default: true }])).toBe("vol1");
    expect(defaultPool([])).toBe("default");
  });

  it("checks new names like the agent", () => {
    expect(poolNameProblem("", [home], "vol1")).toBe("Enter a name.");
    expect(poolNameProblem("a_b", [home], "vol1")).toMatch(/Lowercase/);
    expect(poolNameProblem("1a", [home], "vol1")).toMatch(/Lowercase/);
    expect(poolNameProblem("a".repeat(33), [home], "vol1")).toMatch(/Lowercase/);
    expect(poolNameProblem("default", [home], "vol1")).toMatch(/already/);
    expect(poolNameProblem("vol1", [home, vol1], "vol1")).toBe("");
    expect(poolNameProblem("fast-2", [home], "vol1")).toBe("");
  });

  it("computes the used space only with sizes", () => {
    expect(poolUsed(home)).toBe(18 * GiB);
    expect(poolUsed(gone)).toBeNull();
  });
});
