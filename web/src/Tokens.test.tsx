// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { TokensCard } from "./Tokens";
import { tokenState, type APIToken } from "./tokens";
import { mockFetch } from "./test-utils";

const day = 24 * 60 * 60 * 1000;
const inDays = (n: number) => new Date(Date.now() + n * day).toISOString();

const token = (over: Partial<APIToken>): APIToken => ({
  id: 1,
  name: "Uptime monitoring",
  prefix: "hst_Ab3dEf",
  scopes: ["metrics:read"],
  createdAt: inDays(-10),
  expiresAt: inDays(200),
  neverExpires: false,
  lastUsedAt: null,
  sites: null,
  ...over,
});

const scopes = {
  scopes: [
    { name: "sites:read", description: "Read sites", highRisk: false },
    { name: "files:write", description: "Change site files", highRisk: true },
  ],
};

const sites = { sites: [{ name: "shop", pool: "default", state: "active", php: "8.4" }] };

describe("tokenState", () => {
  const now = Date.parse("2026-11-24T00:00:00Z");
  it("tells active, soon, expired, revoked and never-expiring tokens apart", () => {
    expect(tokenState(token({ expiresAt: "2027-06-01T00:00:00Z" }), now)).toBe("active");
    expect(tokenState(token({ expiresAt: "2026-12-10T00:00:00Z" }), now)).toBe("expires_soon");
    expect(tokenState(token({ expiresAt: "2026-11-23T00:00:00Z" }), now)).toBe("expired");
    expect(tokenState(token({ expiresAt: null, neverExpires: true }), now)).toBe("active");
    expect(tokenState(token({ revokedAt: "2026-11-20T00:00:00Z" }), now)).toBe("revoked");
  });
});

describe("TokensCard", () => {
  it("lists working tokens and hides revoked and expired ones until asked", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/tokens": {
        body: {
          tokens: [
            token({ id: 3, name: "Deploy", expiresAt: null, neverExpires: true, sites: ["shop"], lastUsedAt: inDays(-1), lastUsedIp: "203.0.113.9" }),
            token({ id: 2, name: "Old laptop", revokedAt: inDays(-2) }),
            token({ id: 1, name: "Expired one", expiresAt: inDays(-1) }),
          ],
        },
      },
    });
    render(<TokensCard />);

    const table = await screen.findByRole("table", { name: "API tokens" });
    expect(within(table).getByText("Deploy")).toBeInTheDocument();
    expect(within(table).getByText("Never")).toBeInTheDocument();
    expect(within(table).getByText("203.0.113.9")).toBeInTheDocument();
    expect(within(table).queryByText("Old laptop")).not.toBeInTheDocument();

    await user.click(screen.getByRole("checkbox", { name: /Show revoked and expired tokens \(2\)/ }));
    expect(within(table).getByText("Old laptop")).toBeInTheDocument();
    expect(within(table).getByText("Revoked")).toBeInTheDocument();
    expect(within(table).getByText("Expired")).toBeInTheDocument();
    // A revoked token has nothing left to revoke.
    expect(screen.queryByRole("button", { name: "Revoke Old laptop" })).not.toBeInTheDocument();
  });

  it("creates a token, shows the secret once and sends only what was picked", async () => {
    const user = userEvent.setup();
    let listed: APIToken[] = [];
    const calls = mockFetch({
      "GET /api/v1/tokens": () => ({ body: { tokens: listed } }),
      "GET /api/v1/scopes": { body: scopes },
      "GET /api/v1/sites": { body: sites },
      "POST /api/v1/tokens": () => {
        listed = [token({ id: 7, name: "CI" })];
        return { status: 201, body: { ...token({ id: 7, name: "CI" }), token: "hst_secretsecret", warnings: [] } };
      },
    });
    render(<TokensCard />);

    expect(await screen.findByText("No tokens yet. Create one with New token.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "New token" }));
    const create = screen.getByRole("button", { name: "Create token" });
    expect(create).toBeDisabled();

    await user.type(screen.getByRole("textbox", { name: /Name/ }), "CI");
    expect(await screen.findByRole("checkbox", { name: /sites:read/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /files:write/ })).not.toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: /Every site/ }));
    expect(create).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "shop" }));
    const days = screen.getByRole("spinbutton", { name: /Valid for/ });
    await user.clear(days);
    await user.type(days, "30");
    await user.click(create);

    expect(await screen.findByRole("heading", { name: "Save this token now" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("hst_secretsecret")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "CI",
      scopes: ["sites:read"],
      sites: ["shop"],
      expiresInDays: 30,
      unlimited: false,
    });
    expect(await screen.findByText("CI")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "I have saved it" }));
    expect(screen.queryByDisplayValue("hst_secretsecret")).not.toBeInTheDocument();
  });

  it("asks for a token that never expires with a warning and no lifetime", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/tokens": { body: { tokens: [] } },
      "GET /api/v1/scopes": { body: scopes },
      "GET /api/v1/sites": { body: sites },
      "POST /api/v1/tokens": {
        status: 201,
        body: { ...token({ expiresAt: null, neverExpires: true }), token: "hst_x", warnings: ["never_expires: …", "high_risk_scope: files:write"] },
      },
    });
    render(<TokensCard />);
    await user.click(await screen.findByRole("button", { name: "New token" }));
    await user.type(screen.getByRole("textbox", { name: /Name/ }), "Monitoring");
    await user.click(await screen.findByRole("checkbox", { name: /files:write/ }));
    await user.click(screen.getByRole("checkbox", { name: /Never expires/ }));
    expect(screen.getByText("This token works until you revoke it")).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: /Valid for/ })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Create token" }));

    expect(await screen.findByText("This token has high-risk scopes")).toBeInTheDocument();
    expect(screen.getByText("This token never expires. Revoke it when the job that uses it ends.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ expiresInDays: 0, unlimited: true });
  });

  it("revokes a token only after the second click", async () => {
    const user = userEvent.setup();
    let listed = [token({ id: 4, name: "Laptop" })];
    const calls = mockFetch({
      "GET /api/v1/tokens": () => ({ body: { tokens: listed } }),
      "DELETE /api/v1/tokens/4": () => {
        listed = [token({ id: 4, name: "Laptop", revokedAt: new Date().toISOString() })];
        return { status: 204 };
      },
    });
    render(<TokensCard />);

    await user.click(await screen.findByRole("button", { name: "Revoke Laptop" }));
    expect(screen.getByText("Revoke the token “Laptop”?")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(screen.getByRole("button", { name: "Revoke token" }));

    expect(await screen.findByText("No working tokens.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  });

  it("picks scopes in one go", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/tokens": { body: { tokens: [] } },
      "GET /api/v1/scopes": { body: scopes },
      "GET /api/v1/sites": { body: sites },
    });
    render(<TokensCard />);
    await user.click(await screen.findByRole("button", { name: "New token" }));
    await screen.findByRole("checkbox", { name: /sites:read/ });

    await user.click(screen.getByRole("button", { name: "None" }));
    expect(screen.getByRole("checkbox", { name: /sites:read/ })).not.toBeChecked();
    await user.type(screen.getByRole("textbox", { name: /Name/ }), "x");
    expect(screen.getByRole("button", { name: "Create token" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Only reading" }));
    expect(screen.getByRole("checkbox", { name: /sites:read/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /files:write/ })).not.toBeChecked();
  });

  it("explains a full token list", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/tokens": { body: { tokens: [] } },
      "GET /api/v1/scopes": { body: scopes },
      "GET /api/v1/sites": { body: sites },
      "POST /api/v1/tokens": { status: 409, body: { error: { code: "too_many_tokens", message: "too many API tokens, revoke one first" } } },
    });
    render(<TokensCard />);
    await user.click(await screen.findByRole("button", { name: "New token" }));
    await user.type(screen.getByRole("textbox", { name: /Name/ }), "One more");
    await screen.findByRole("checkbox", { name: /sites:read/ });
    await user.click(screen.getByRole("button", { name: "Create token" }));
    expect(await screen.findByText(/You have 100 working API tokens/)).toBeInTheDocument();
  });
});
