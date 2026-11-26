// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { BlockedAddressesCard } from "./BlockedAddresses";
import { blockedAddresses, type Fail2banStatus } from "./fail2ban";
import { mockFetch } from "./test-utils";

const jail = (name: string, bans: Fail2banStatus["jails"][number]["bans"], running = true) => ({
  name,
  running,
  currentlyFailed: 1,
  totalFailed: 12,
  currentlyBanned: bans.length,
  totalBanned: bans.length,
  bans,
});

const soon = (min: number) => new Date(Date.now() + min * 60_000).toISOString();
const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

const status = (): Fail2banStatus => ({
  jails: [
    jail("sshd", [], false),
    jail("hostashi-panel", []),
    jail("hostashi-site-auth", [{ address: "203.0.113.7", since: ago(5), until: soon(55) }]),
    jail("recidive", [
      { address: "203.0.113.7", since: ago(2), until: null },
      { address: "2001:db8::1", since: ago(30), until: soon(600) },
    ]),
  ],
});

describe("blockedAddresses", () => {
  it("lists each address once with all its jails and its last end", () => {
    const rows = blockedAddresses(status());
    expect(rows.map((r) => r.address)).toEqual(["203.0.113.7", "2001:db8::1"]);
    expect(rows[0]!.jails).toEqual(["hostashi-site-auth", "recidive"]);
    expect(rows[0]!.until).toBeNull();
  });
});

describe("BlockedAddressesCard", () => {
  it("shows the jails and unblocks an address in every jail", async () => {
    const user = userEvent.setup();
    let current = status();
    const calls = mockFetch({
      "GET /api/v1/firewall/bans": () => ({ body: current }),
      "DELETE /api/v1/firewall/bans/203.0.113.7": () => {
        current = { jails: current.jails.map((j) => ({ ...j, bans: j.bans.filter((b) => b.address !== "203.0.113.7") })) };
        return { body: { address: "203.0.113.7", jails: ["hostashi-site-auth", "recidive"] } };
      },
    });
    render(<BlockedAddressesCard />);

    const jails = await screen.findByRole("list", { name: "Jails" });
    expect(within(jails).getByText("Password-protected sites")).toBeInTheDocument();
    expect(within(jails).getByText("Not running")).toBeInTheDocument();

    const table = screen.getByRole("table", { name: "Blocked addresses" });
    expect(within(table).getByText("No end")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Unblock 203.0.113.7" }));
    expect(screen.getByText("Unblock 203.0.113.7?")).toBeInTheDocument();
    expect(screen.getByText(/Password-protected sites, Repeat offenders/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Unblock address" }));

    expect(await screen.findByText("203.0.113.7 is unblocked.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
    expect(screen.queryByRole("button", { name: "Unblock 203.0.113.7" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Unblock 2001:db8::1" })).toBeInTheDocument();
  });

  it("says when nothing is blocked", async () => {
    mockFetch({ "GET /api/v1/firewall/bans": { body: { jails: [jail("sshd", [])] } } });
    render(<BlockedAddressesCard />);
    expect(await screen.findByText("No address is blocked.")).toBeInTheDocument();
  });

  it("explains a fail2ban that does not answer", async () => {
    mockFetch({ "GET /api/v1/firewall/bans": { status: 409, body: { error: { code: "fail2ban_unavailable", message: "fail2ban does not answer" } } } });
    render(<BlockedAddressesCard />);
    expect(await screen.findByText(/fail2ban does not answer, so nothing is blocked/)).toBeInTheDocument();
  });
});
