// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Device } from "./Device";
import { normalizeCode } from "./device";
import { mockFetch } from "./test-utils";

const login = {
  userCode: "WDJB-MJHT",
  client: "hostashi CLI on laptop",
  ip: "198.51.100.7",
  createdAt: "2026-11-22T10:00:00Z",
  expiresAt: "2026-11-22T10:10:00Z",
};

const scopes = {
  scopes: [
    { name: "sites:read", description: "Read sites", highRisk: false },
    { name: "files:write", description: "Change site files", highRisk: true },
  ],
};

const sites = { sites: [{ name: "shop", pool: "default", state: "active", php: "8.4" }] };

describe("Device", () => {
  it("looks up the code from the link and approves it without the high-risk scopes", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/auth/device/WDJB-MJHT": { body: login },
      "GET /api/v1/scopes": { body: scopes },
      "GET /api/v1/sites": { body: sites },
      "POST /api/v1/auth/device/approve": { status: 204 },
    });
    render(<Device code="wdjbmjht" />);

    expect(await screen.findByText("hostashi CLI on laptop")).toBeInTheDocument();
    expect(screen.getByText("198.51.100.7")).toBeInTheDocument();
    expect(screen.getByText("Approve only a login you started yourself")).toBeInTheDocument();
    expect(await screen.findByRole("checkbox", { name: /sites:read/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /files:write/ })).not.toBeChecked();

    await user.click(screen.getByRole("button", { name: "Approve" }));

    expect(await screen.findByText("Login approved")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      userCode: "WDJB-MJHT",
      approve: true,
      scopes: ["sites:read"],
      sites: null,
      expiresInDays: 90,
    });
  });

  it("limits the token to picked sites and a shorter lifetime", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/auth/device/WDJB-MJHT": { body: login },
      "GET /api/v1/scopes": { body: scopes },
      "GET /api/v1/sites": { body: sites },
      "POST /api/v1/auth/device/approve": { status: 204 },
    });
    render(<Device code="WDJB-MJHT" />);

    await user.click(await screen.findByRole("checkbox", { name: /Every site/ }));
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
    await user.click(await screen.findByRole("checkbox", { name: "shop" }));
    await user.clear(screen.getByLabelText("Valid for (days)"));
    await user.type(screen.getByLabelText("Valid for (days)"), "7");
    await user.click(screen.getByRole("button", { name: "Approve" }));

    await screen.findByText("Login approved");
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ sites: ["shop"], expiresInDays: 7 });
  });

  it("denies a login", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/auth/device/WDJB-MJHT": { body: login },
      "GET /api/v1/scopes": { body: scopes },
      "GET /api/v1/sites": { body: sites },
      "POST /api/v1/auth/device/approve": { status: 204 },
    });
    render(<Device code="WDJB-MJHT" />);

    await user.click(await screen.findByRole("button", { name: "Deny" }));

    expect(await screen.findByText("Login denied. The CLI gets no token.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ approve: false });
  });

  it("asks for the code and explains an unknown one", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/auth/device/BCDF-GHJK": { status: 404, body: { error: { code: "not_found", message: "no such device login" } } },
    });
    render(<Device code="" />);

    await user.type(screen.getByLabelText("Code from the terminal"), "bcdf ghjk");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByText(/No login is waiting for this code/)).toBeInTheDocument();
  });
});

describe("normalizeCode", () => {
  it("upper-cases and puts the dash in", () => {
    expect(normalizeCode("wdjbmjht")).toBe("WDJB-MJHT");
    expect(normalizeCode(" wdjb-mjht ")).toBe("WDJB-MJHT");
    expect(normalizeCode("abc")).toBe("ABC");
  });
});
