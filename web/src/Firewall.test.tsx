// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Firewall } from "./Firewall";
import type { Firewall as FirewallStatus, FirewallState } from "./firewall";
import { mockFetch } from "./test-utils";

const ID = "0123456789abcdef0123456789abcdef";
const inTwoMinutes = () => new Date(Date.now() + 120_000).toISOString();

const status = (state: Partial<FirewallState> = {}, extra: Partial<FirewallStatus> = {}): FirewallStatus => ({
  enabled: true,
  state: { open: [], sshAllow: [], panelAllow: [], ...state },
  pending: null,
  rolledBack: null,
  clientIp: "198.51.100.7",
  confirmWithin: 120,
  ...extra,
});

// The API token card loads its own data.
const allow = {
  "GET /api/v1/settings/api-ip-allowlist": { body: { entries: [] } },
};

describe("Firewall", () => {
  it("explains a server installed without the firewall", async () => {
    mockFetch({ ...allow, "GET /api/v1/firewall": { body: status({}, { enabled: false }) } });
    render(<Firewall query={new URLSearchParams()} onQuery={() => undefined} />);
    expect(await screen.findByText(/installed with --no-firewall/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply rules" })).not.toBeInTheDocument();
  });

  it("opens a port and keeps it when the panel still answers", async () => {
    const user = userEvent.setup();
    let current = status();
    const calls = mockFetch({ ...allow,
      "GET /api/v1/firewall": () => ({ body: current }),
      "PUT /api/v1/firewall": (body) => {
        const { state } = body as { state: FirewallState };
        current = status(state, { pending: { deadline: inTwoMinutes() } });
        return { body: { ...current, pending: { id: ID, deadline: current.pending!.deadline } } };
      },
      "POST /api/v1/firewall/confirm": () => {
        current = status(current.state);
        return { body: current };
      },
    });
    render(<Firewall query={new URLSearchParams()} onQuery={() => undefined} />);
    await screen.findByText("No other port is open.");

    await user.click(screen.getByRole("button", { name: "Add port" }));
    const port = within(screen.getByRole("listitem", { name: "Port 1" }));
    await user.selectOptions(port.getByLabelText("Protocol"), "udp");
    await user.type(port.getByLabelText("Port or range"), "6000-6010");
    await user.type(port.getByLabelText("Only from (optional)"), "192.0.2.0/24, 2001:db8::/32");
    await user.type(port.getByLabelText("Note"), "game server");
    await user.click(screen.getByRole("button", { name: "Apply rules" }));

    expect(await screen.findByText("The new rules stay.")).toBeInTheDocument();
    const put = calls.find((c) => c.method === "PUT");
    expect(put?.body).toEqual({
      state: {
        open: [{ proto: "udp", from: 6000, to: 6010, sources: ["192.0.2.0/24", "2001:db8::/32"], note: "game server" }],
        sshAllow: [],
        panelAllow: [],
      },
      otherWay: false,
    });
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ id: ID });
    expect(within(screen.getByRole("listitem", { name: "Port 1" })).getByLabelText("Port or range")).toHaveValue("6000-6010");
  });

  it("refuses a port it cannot read", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ ...allow, "GET /api/v1/firewall": { body: status() } });
    render(<Firewall query={new URLSearchParams()} onQuery={() => undefined} />);
    await user.click(await screen.findByRole("button", { name: "Add port" }));
    await user.type(screen.getByLabelText("Port or range"), "http");
    await user.click(screen.getByRole("button", { name: "Apply rules" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Port 1: enter a port such as 3000");
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
  });

  it("asks before an allowlist without the admin's address, and waits for the SSH test", async () => {
    const user = userEvent.setup();
    let current = status();
    const calls = mockFetch({ ...allow,
      "GET /api/v1/firewall": () => ({ body: current }),
      "PUT /api/v1/firewall": (body) => {
        const { state, otherWay } = body as { state: FirewallState; otherWay: boolean };
        if (!otherWay) {
          return {
            status: 409,
            body: { error: { code: "lockout_ssh", message: "the SSH allowlist does not contain your address 198.51.100.7" } },
          };
        }
        current = status(state, { pending: { deadline: inTwoMinutes() } });
        return { body: { ...current, pending: { id: ID, deadline: current.pending!.deadline } } };
      },
      "POST /api/v1/firewall/confirm": () => {
        current = status(current.state);
        return { body: current };
      },
    });
    render(<Firewall query={new URLSearchParams()} onQuery={() => undefined} />);

    await user.type(await screen.findByLabelText("SSH"), "203.0.113.0/24");
    await user.click(screen.getByRole("button", { name: "Apply rules" }));
    expect(await screen.findByText(/does not contain your address 198.51.100.7/)).toBeInTheDocument();

    await user.click(screen.getByRole("checkbox", { name: /I reach the server another way/ }));
    await user.click(screen.getByRole("button", { name: "Apply rules" }));

    const wait = within(await screen.findByRole("region", { name: "Waiting for confirmation" }));
    expect(wait.getByText(/Open a new terminal now/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
    expect(screen.getByRole("button", { name: "Apply rules" })).toBeDisabled();

    await user.click(wait.getByRole("button", { name: "SSH works, keep the rules" }));
    expect(await screen.findByText("The new rules stay.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").map((c) => (c.body as { otherWay: boolean }).otherWay)).toEqual([false, true]);
    expect(screen.getByLabelText("SSH")).toHaveValue("203.0.113.0/24");
  });

  it("adds the admin's address", async () => {
    const user = userEvent.setup();
    mockFetch({ ...allow, "GET /api/v1/firewall": { body: status({ panelAllow: ["192.0.2.1"] }) } });
    render(<Firewall query={new URLSearchParams()} onQuery={() => undefined} />);
    await user.click(await screen.findByRole("button", { name: "Add my address to the panel" }));
    expect(screen.getByLabelText("Panel")).toHaveValue("192.0.2.1\n198.51.100.7");
    await user.click(screen.getByRole("button", { name: "Add my address to the panel" }));
    expect(screen.getByLabelText("Panel")).toHaveValue("192.0.2.1\n198.51.100.7");
  });

  it("reports a rollback and a change another browser waits for", async () => {
    mockFetch({ ...allow,
      "GET /api/v1/firewall": {
        body: status({}, { rolledBack: "2026-11-20T10:00:00Z", pending: { deadline: inTwoMinutes() } }),
      },
    });
    render(<Firewall query={new URLSearchParams()} onQuery={() => undefined} />);
    expect(await screen.findByText(/was not confirmed in time and was rolled back/)).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "A change is waiting" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply rules" })).toBeDisabled();
  });

  it("says when the change was rolled back before the confirmation", async () => {
    const user = userEvent.setup();
    mockFetch({ ...allow,
      "GET /api/v1/firewall": { body: status() },
      "PUT /api/v1/firewall": { body: status({}, { pending: { id: ID, deadline: inTwoMinutes() } }) },
      "POST /api/v1/firewall/confirm": { status: 404, body: { error: { code: "not_pending", message: "x" } } },
    });
    render(<Firewall query={new URLSearchParams()} onQuery={() => undefined} />);
    const apply = await screen.findByRole("button", { name: "Apply rules" });
    expect(apply).toBeDisabled();
    expect(screen.getByText("No changes")).toBeInTheDocument();
    await user.type(screen.getByRole("textbox", { name: "Panel" }), "203.0.113.9");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    await user.click(apply);
    expect(await screen.findByRole("alert")).toHaveTextContent("it was rolled back");
  });
});
