// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Cutover, answerLabel, cutoverPollMs } from "./Cutover";
import type { Cutover as CutoverView } from "./cloudpanel";
import { mockFetch } from "./test-utils";

const path = "/api/v1/migrations/cloudpanel/sites/shop/cutover";

const waiting = (over: Partial<CutoverView> = {}): CutoverView => ({
  site: "shop",
  state: "waiting",
  startedAt: "2026-11-20T08:00:00Z",
  deadline: "2026-11-22T08:00:00Z",
  checkedAt: "2026-11-20T08:01:00Z",
  server: ["203.0.113.10", "2001:db8::10"],
  answers: [
    { domain: "shop.example", addrs: ["203.0.113.10", "2001:db8::10"], here: true, ttl: 300 },
    { domain: "www.shop.example", addrs: ["198.51.100.2"], here: false, ttl: 3600 },
  ],
  ...over,
});

afterEach(() => {
  vi.useRealTimers();
});

describe("Cutover", () => {
  it("starts the DNS check and shows the records to set", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      [`GET ${path}`]: { status: 404, body: { error: { code: "not_found", message: "the site has no cut-over" } } },
      [`POST ${path}`]: { status: 202, body: waiting() },
    });
    render(<Cutover site="shop" />);
    const box = within(await screen.findByRole("group", { name: "DNS switch of shop" }));
    expect(await box.findByText(/point the domains of shop at this server/)).toBeInTheDocument();
    expect(box.queryByRole("alert")).not.toBeInTheDocument();

    await user.click(box.getByRole("button", { name: "Start DNS check" }));
    expect(calls).toContainEqual({ method: "POST", path, body: undefined });

    const records = within(await box.findByRole("group", { name: "Records for shop" }));
    expect(records.getByText("A").nextElementSibling).toHaveTextContent("203.0.113.10");
    expect(records.getByText("AAAA").nextElementSibling).toHaveTextContent("2001:db8::10");
    expect(box.getByText("Waiting for DNS")).toBeInTheDocument();

    const domains = within(box.getByLabelText("Domains of shop"));
    expect(domains.getByText("shop.example").nextElementSibling).toHaveTextContent("Here");
    expect(domains.getByText("shop.example").nextElementSibling).toHaveTextContent("TTL 5 min");
    const www = domains.getByText("www.shop.example").nextElementSibling;
    expect(www).toHaveTextContent("Points elsewhere: 198.51.100.2");
    expect(www).toHaveTextContent("TTL 1 h");
    expect(www).toHaveTextContent("Lower to 300 s");
    expect(box.getByText(/Lower their TTL to 300 seconds/)).toBeInTheDocument();
    expect(box.queryByRole("button", { name: "Start DNS check" })).not.toBeInTheDocument();
  });

  it("cancels a waiting check and offers to start again", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      [`GET ${path}`]: { body: waiting() },
      [`DELETE ${path}`]: { body: waiting({ state: "canceled", doneAt: "2026-11-20T09:00:00Z" }) },
    });
    render(<Cutover site="shop" />);
    const box = within(await screen.findByRole("group", { name: "DNS switch of shop" }));
    await user.click(await box.findByRole("button", { name: "Cancel DNS check" }));
    expect(calls).toContainEqual({ method: "DELETE", path, body: undefined });
    expect(await box.findByText("Canceled")).toBeInTheDocument();
    expect(box.queryByRole("group", { name: "Records for shop" })).not.toBeInTheDocument();
    expect(box.queryByText(/Lower their TTL/)).not.toBeInTheDocument();
    expect(box.getByRole("button", { name: "Start DNS check again" })).toBeInTheDocument();
  });

  it("shows a finished switch without actions", async () => {
    mockFetch({
      [`GET ${path}`]: {
        body: waiting({
          state: "switched",
          doneAt: "2026-11-20T09:00:00Z",
          answers: [{ domain: "shop.example", addrs: ["203.0.113.10"], here: true }],
        }),
      },
    });
    render(<Cutover site="shop" />);
    const box = within(await screen.findByRole("group", { name: "DNS switch of shop" }));
    expect(await box.findByText("Done, HTTPS is on")).toBeInTheDocument();
    expect(box.queryByText(/TTL/)).not.toBeInTheDocument();
    expect(box.queryByRole("button")).not.toBeInTheDocument();
  });

  it("shows why a check failed", async () => {
    mockFetch({
      [`GET ${path}`]: {
        body: waiting({ state: "failed", error: "the domains did not point to this server within 48 hours" }),
      },
    });
    render(<Cutover site="shop" />);
    const box = within(await screen.findByRole("group", { name: "DNS switch of shop" }));
    expect(await box.findByRole("alert")).toHaveTextContent("The domains did not point to this server within 48 hours.");
    expect(box.getByRole("button", { name: "Start DNS check again" })).toBeInTheDocument();
  });

  it("explains a refused start", async () => {
    const user = userEvent.setup();
    mockFetch({
      [`GET ${path}`]: { status: 404, body: { error: { code: "not_found", message: "the site has no cut-over" } } },
      [`POST ${path}`]: { status: 409, body: { error: { code: "site_state", message: "the site does not exist or is not active" } } },
    });
    render(<Cutover site="shop" />);
    const box = within(await screen.findByRole("group", { name: "DNS switch of shop" }));
    await user.click(await box.findByRole("button", { name: "Start DNS check" }));
    expect(await box.findByRole("alert")).toHaveTextContent("Enable it first");
  });

  it("says when the server has no public address yet", async () => {
    const { checkedAt: _, ...fresh } = waiting({ server: [], answers: [], error: "this server has no public address" });
    mockFetch({ [`GET ${path}`]: { body: fresh } });
    render(<Cutover site="shop" />);
    const box = within(await screen.findByRole("group", { name: "DNS switch of shop" }));
    expect(await box.findByText(/public addresses are not known yet/)).toBeInTheDocument();
    expect(box.getByText("Not yet")).toBeInTheDocument();
    expect(box.getByRole("alert")).toHaveTextContent("This server has no public address.");
  });

  it("checks again while it waits", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    const calls = mockFetch({
      [`GET ${path}`]: () => {
        n++;
        return { body: n === 1 ? waiting() : waiting({ state: "switched", doneAt: "2026-11-20T09:00:00Z" }) };
      },
    });
    render(<Cutover site="shop" />);
    const box = within(await screen.findByRole("group", { name: "DNS switch of shop" }));
    expect(await box.findByText("Waiting for DNS")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(cutoverPollMs);
    });
    expect(await box.findByText("Done, HTTPS is on")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(cutoverPollMs * 3);
    });
    expect(calls.filter((c) => c.method === "GET")).toHaveLength(2);
  });
});

describe("answerLabel", () => {
  it("prefers the lookup error", () => {
    expect(answerLabel({ domain: "a.example", addrs: [], here: false, error: "no such host" })).toBe("Lookup failed: no such host");
    expect(answerLabel({ domain: "a.example", addrs: [], here: false })).toBe("No address");
  });
});
