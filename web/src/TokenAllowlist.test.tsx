// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { TokenAllowlistCard } from "./TokenAllowlist";
import { mockFetch } from "./test-utils";

describe("TokenAllowlistCard", () => {
  it("adds my address and saves", async () => {
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
    expect(screen.getByText(/The server itself \(127\.0\.0\.1 and ::1\) is always allowed/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add my address" }));
    await user.click(screen.getByRole("button", { name: "Add my address" }));
    expect(screen.getByLabelText("Allowed addresses")).toHaveValue("198.51.100.7");
    await user.type(screen.getByLabelText("Allowed addresses"), "\n2001:db8::/32");

    await user.click(save);
    expect(await screen.findByText("Saved. Tokens now work only from these addresses and the server itself.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ entries: ["198.51.100.7", "2001:db8::/32"] });
    expect(screen.getByLabelText("Allowed addresses")).toHaveValue("198.51.100.7/32\n2001:db8::/32");
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
