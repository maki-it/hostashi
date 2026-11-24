// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { TokenAllowlistCard } from "./TokenAllowlist";
import { allowsLoopback } from "./tokens";
import { mockFetch } from "./test-utils";

describe("allowsLoopback", () => {
  it("knows which lists let the server's own command line through", () => {
    expect(allowsLoopback([])).toBe(true);
    expect(allowsLoopback(["127.0.0.1/32"])).toBe(true);
    expect(allowsLoopback(["127.0.0.0/8", "2001:db8::/32"])).toBe(true);
    expect(allowsLoopback(["0.0.0.0/0"])).toBe(true);
    expect(allowsLoopback(["203.0.113.0/24"])).toBe(false);
    expect(allowsLoopback(["::1/128"])).toBe(false);
    expect(allowsLoopback(["127.0.0.2/32"])).toBe(false);
    expect(allowsLoopback(["not an address"])).toBe(false);
  });
});

describe("TokenAllowlistCard", () => {
  it("adds my address and the server, warns until the server is on the list, and saves", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/settings/api-ip-allowlist": { body: { entries: [] } },
      "PUT /api/v1/settings/api-ip-allowlist": (body) => ({
        body: { entries: (body as { entries: string[] }).entries.map((e) => (e.includes("/") ? e : e + (e.includes(":") ? "/128" : "/32"))) },
      }),
    });
    render(<TokenAllowlistCard clientIp="198.51.100.7" />);

    const save = await screen.findByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    expect(screen.queryByText("The hostashi command on this server stops working")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add my address" }));
    expect(screen.getByLabelText("Allowed addresses")).toHaveValue("198.51.100.7");
    expect(screen.getByText("The hostashi command on this server stops working")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add this server" }));
    expect(screen.getByLabelText("Allowed addresses")).toHaveValue("198.51.100.7\n127.0.0.1\n::1");
    expect(screen.queryByText("The hostashi command on this server stops working")).not.toBeInTheDocument();

    await user.click(save);
    expect(await screen.findByText("Saved. Tokens now work only from these addresses.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ entries: ["198.51.100.7", "127.0.0.1", "::1"] });
    expect(screen.getByLabelText("Allowed addresses")).toHaveValue("198.51.100.7/32\n127.0.0.1/32\n::1/128");
    expect(save).toBeDisabled();
  });

  it("shows what the server refuses", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/settings/api-ip-allowlist": { body: { entries: [] } },
      "PUT /api/v1/settings/api-ip-allowlist": {
        status: 422,
        body: { error: { code: "invalid_argument", message: 'auth: invalid IP allowlist: "office" is not an address or network' } },
      },
    });
    render(<TokenAllowlistCard />);
    await user.type(await screen.findByLabelText("Allowed addresses"), "office");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText('"office" is not an address or network')).toBeInTheDocument();
  });
});
