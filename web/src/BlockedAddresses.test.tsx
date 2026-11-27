// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { BlockedAddressesCard, filterOf } from "./BlockedAddresses";
import { bansPath, type BlockedAddress, type Fail2banJail, type Fail2banStatus } from "./fail2ban";
import { Firewall } from "./Firewall";
import { mockFetch } from "./test-utils";

const jail = (name: string, banned = 0, running = true): Fail2banJail => ({
  name,
  running,
  currentlyFailed: 1,
  totalFailed: 1200,
  currentlyBanned: banned,
  totalBanned: banned,
});

const soon = (min: number) => new Date(Date.now() + min * 60_000).toISOString();
const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

const jails = () => [jail("sshd", 0, false), jail("hostashi-panel"), jail("hostashi-site-auth", 1), jail("recidive", 2)];

const page = (addresses: BlockedAddress[], extra: Partial<Fail2banStatus> = {}): Fail2banStatus => ({
  jails: jails(),
  addresses,
  total: addresses.length,
  page: 1,
  perPage: 50,
  truncated: false,
  ...extra,
});

const two = (): BlockedAddress[] => [
  { address: "203.0.113.7", jails: ["hostashi-site-auth", "recidive"], since: ago(5), until: null },
  { address: "2001:db8::1", jails: ["recidive"], since: ago(30), until: soon(600) },
];

// Routed keeps the query like the address bar does.
function Routed({ start = "" }: { start?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(start));
  return <BlockedAddressesCard query={query} onQuery={setQuery} />;
}

describe("bansPath and filterOf", () => {
  it("sends only what is set", () => {
    expect(bansPath({ jail: "", address: " ", page: 1 })).toBe("/firewall/bans");
    expect(bansPath({ jail: "recidive", address: "203.0.113.0/24", page: 3 })).toBe("/firewall/bans?jail=recidive&address=203.0.113.0%2F24&page=3");
  });

  it("ignores a foreign jail and a bad page in the address", () => {
    expect(filterOf(new URLSearchParams("jail=apache&page=-2&address=10."))).toEqual({ jail: "", address: "10.", page: 1 });
    expect(filterOf(new URLSearchParams("jail=sshd&page=4"))).toEqual({ jail: "sshd", address: "", page: 4 });
  });
});

describe("BlockedAddressesCard", () => {
  it("shows the jails and unblocks an address in every jail", async () => {
    const user = userEvent.setup();
    let current = page(two());
    const calls = mockFetch({
      "GET /api/v1/firewall/bans": () => ({ body: current }),
      "DELETE /api/v1/firewall/bans/203.0.113.7": () => {
        current = page(two().slice(1));
        return { body: { address: "203.0.113.7", jails: ["hostashi-site-auth", "recidive"] } };
      },
    });
    render(<Routed />);

    const list = await screen.findByRole("list", { name: "Jails" });
    expect(within(list).getByText("Password-protected sites")).toBeInTheDocument();
    expect(within(list).getByText("Not running")).toBeInTheDocument();
    expect(within(list).getAllByText(/1,200 since start/).length).toBeGreaterThan(0);

    const table = screen.getByRole("table", { name: "Blocked addresses" });
    expect(within(table).getByText("No end")).toBeInTheDocument();
    expect(screen.getByText("1–2 of 2 addresses")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Next" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Unblock 203.0.113.7" }));
    expect(screen.getByText("Unblock 203.0.113.7?")).toBeInTheDocument();
    expect(screen.getByText(/Password-protected sites, Repeat offenders/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Unblock address" }));

    expect(await screen.findByText("203.0.113.7 is unblocked.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
    expect(await screen.findByText("1–1 of 1 address")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Unblock 203.0.113.7" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Unblock 2001:db8::1" })).toBeInTheDocument();
  });

  it("filters by block type and address on the server", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/firewall/bans": { body: page(two()) },
      "GET /api/v1/firewall/bans?jail=recidive": { body: page(two()) },
      "GET /api/v1/firewall/bans?jail=recidive&address=2001%3Adb8%3A%3A%2F32": { body: page(two().slice(1)) },
      "GET /api/v1/firewall/bans?jail=recidive&address=10.": { body: page([]) },
    });
    render(<Routed />);

    await user.selectOptions(await screen.findByRole("combobox", { name: "Block type" }), "Repeat offenders");
    await screen.findByRole("button", { name: "Clear filters" });
    expect(calls.at(-1)!.path).toBe("/api/v1/firewall/bans?jail=recidive");

    await user.type(screen.getByRole("searchbox", { name: "IP address" }), "2001:db8::/32");
    await screen.findByText("1–1 of 1 address");
    expect(calls.at(-1)!.path).toBe("/api/v1/firewall/bans?jail=recidive&address=2001%3Adb8%3A%3A%2F32");
    // Typing waits for a pause instead of asking for every key.
    expect(calls.filter((c) => c.path.includes("address=")).length).toBe(1);

    await user.clear(screen.getByRole("searchbox", { name: "IP address" }));
    await user.type(screen.getByRole("searchbox", { name: "IP address" }), "10.");
    expect(await screen.findByText("No blocked address matches the filters.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(await screen.findByText("1–2 of 2 addresses")).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "IP address" })).toHaveValue("");
  });

  it("pages through thousands of addresses", async () => {
    const user = userEvent.setup();
    const rows = (from: number): BlockedAddress[] =>
      Array.from({ length: 50 }, (_, i) => ({ address: "198.18.0." + (from + i), jails: ["hostashi-panel"], since: ago(1), until: soon(10) }));
    mockFetch({
      "GET /api/v1/firewall/bans": { body: page(rows(1), { total: 3214 }) },
      "GET /api/v1/firewall/bans?page=2": { body: page(rows(51), { total: 3214, page: 2 }) },
    });
    render(<Routed />);

    expect(await screen.findByText("1–50 of 3,214 addresses")).toBeInTheDocument();
    expect(screen.getByText("Page 1 of 65")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("51–100 of 3,214 addresses")).toBeInTheDocument();
    expect(screen.getByText("198.18.0.51")).toBeInTheDocument();
    expect(screen.getAllByRole("row")).toHaveLength(51);
  });

  it("warns when fail2ban lists more than the panel reads", async () => {
    mockFetch({ "GET /api/v1/firewall/bans": { body: page(two(), { truncated: true }) } });
    render(<Routed />);
    expect(await screen.findByText(/blocks more addresses than the panel reads/)).toBeInTheDocument();
  });

  it("says when nothing is blocked", async () => {
    mockFetch({ "GET /api/v1/firewall/bans": { body: page([]) } });
    render(<Routed />);
    expect(await screen.findByText("No address is blocked.")).toBeInTheDocument();
  });

  it("explains a fail2ban that does not answer", async () => {
    mockFetch({ "GET /api/v1/firewall/bans": { status: 409, body: { error: { code: "fail2ban_unavailable", message: "fail2ban does not answer" } } } });
    render(<Routed />);
    expect(await screen.findByText(/fail2ban does not answer, so nothing is blocked/)).toBeInTheDocument();
  });
});

describe("Firewall tabs", () => {
  function Page() {
    const [query, setQuery] = useState(new URLSearchParams());
    return <Firewall query={query} onQuery={setQuery} />;
  }

  it("shows the rules first and the blocked addresses on their own tab", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/firewall": {
        body: { enabled: false, state: { open: [], sshAllow: [], panelAllow: [] }, pending: null, rolledBack: null, clientIp: "198.51.100.7", confirmWithin: 120 },
      },
      "GET /api/v1/settings/api-ip-allowlist": { body: { entries: [] } },
      "GET /api/v1/firewall/bans": { body: page(two()) },
    });
    render(<Page />);

    expect(await screen.findByRole("tab", { name: "Firewall", selected: true })).toBeInTheDocument();
    expect(await screen.findByText(/firewall is off/)).toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith("/api/v1/firewall/bans"))).toBe(false);

    await user.click(screen.getByRole("tab", { name: "Blocked addresses" }));
    expect(await screen.findByRole("table", { name: "Blocked addresses" })).toBeInTheDocument();
    expect(screen.getByText(/firewall is off/)).not.toBeVisible();

    await act(async () => {
      await user.click(screen.getByRole("tab", { name: "Firewall" }));
    });
    expect(screen.queryByRole("table", { name: "Blocked addresses" })).not.toBeInTheDocument();
    expect(screen.getByText(/firewall is off/)).toBeVisible();
  });
});
