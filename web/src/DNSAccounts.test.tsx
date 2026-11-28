// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { CertificateCard } from "./Certificate";
import { DNSAccounts } from "./DNSAccounts";
import { badZone, parseZones, type DNSAccount, type DNSProvider } from "./dns";
import { mockFetch } from "./test-utils";

const providers: DNSProvider[] = [
  { id: "cloudflare", name: "Cloudflare", fields: [{ name: "apiToken", label: "API token", secret: true, optional: false }], hint: "" },
  { id: "hetzner", name: "Hetzner", fields: [{ name: "apiToken", label: "API token (Hetzner Cloud)", secret: true, optional: false }], hint: "" },
  {
    id: "inwx",
    name: "INWX",
    fields: [
      { name: "username", label: "Username", secret: false, optional: false },
      { name: "password", label: "Password", secret: true, optional: false },
      { name: "sharedSecret", label: "Two-factor shared secret", secret: true, optional: true },
    ],
    hint: "",
  },
];

const inwx: DNSAccount = {
  id: 1,
  label: "INWX main",
  provider: "inwx",
  zones: ["example.com", "acme.example.net"],
  settings: { username: "kim" },
  state: "ok",
  checkedAt: "2026-11-28T08:00:00Z",
  createdAt: "2026-11-28T08:00:00Z",
  updatedAt: "2026-11-28T08:00:00Z",
};

const failing: DNSAccount = { ...inwx, id: 2, label: "Cloudflare", provider: "cloudflare", zones: ["shop.example"], settings: {}, state: "error", lastError: "shop.example: 403 forbidden" };

const base = { "GET /api/v1/dns-providers": { body: { providers } } };

describe("DNSAccounts", () => {
  it("lists the accounts with zones and the check state", async () => {
    mockFetch({ ...base, "GET /api/v1/dns-accounts": { body: { accounts: [inwx, failing] } } });
    render(<DNSAccounts query={new URLSearchParams()} />);

    const row = (await screen.findByText("INWX main")).closest("tr")!;
    expect(within(row).getByText("INWX")).toBeInTheDocument();
    expect(within(row).getByText("acme.example.net")).toBeInTheDocument();
    expect(within(row).getByText("Can write")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.getByText("Cloudflare: shop.example: 403 forbidden")).toBeInTheDocument();
    expect(screen.getByText(/Recommended: a separate zone/)).toBeInTheDocument();
  });

  it("adds an INWX account and sends only its fields", async () => {
    window.location.hash = "";
    const user = userEvent.setup();
    const calls = mockFetch({
      ...base,
      "GET /api/v1/dns-accounts": { body: { accounts: [] } },
      "POST /api/v1/dns-accounts": { status: 201, body: inwx },
    });
    render(<DNSAccounts query={new URLSearchParams({ new: "1" })} />);

    await user.selectOptions(await screen.findByLabelText("Provider"), "inwx");
    expect(screen.getByText(/sub-user with DNS rights only/)).toBeInTheDocument();

    // Nothing is sent while a field is missing.
    await user.click(screen.getByRole("button", { name: "Add DNS account" }));
    expect(screen.getByText("Enter a name.")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Name"), "INWX main");
    await user.type(screen.getByLabelText("Zones"), "Example.COM.\nacme.example.net");
    await user.type(screen.getByLabelText("Username"), "kim");
    await user.click(screen.getByRole("button", { name: "Add DNS account" }));
    expect(screen.getByText("Fill in Password.")).toBeInTheDocument();

    expect(screen.getByLabelText("Password")).toHaveAttribute("type", "password");
    await user.type(screen.getByLabelText("Password"), "Secret-1");
    await user.click(screen.getByRole("button", { name: "Add DNS account" }));

    // Back to the list, which shows the result.
    await waitFor(() => expect(window.location.hash).toBe("#/dns"));
    const post = calls.find((c) => c.method === "POST");
    expect(post?.body).toEqual({ label: "INWX main", provider: "inwx", zones: ["example.com", "acme.example.net"], credentials: { username: "kim", password: "Secret-1" } });
  });

  it("changes zones and keeps the credentials", async () => {
    window.location.hash = "";
    const user = userEvent.setup();
    const calls = mockFetch({
      ...base,
      "GET /api/v1/dns-accounts": { body: { accounts: [inwx] } },
      "PUT /api/v1/dns-accounts/1": (b) => ({ body: { ...inwx, zones: (b as { zones: string[] }).zones } }),
    });
    render(<DNSAccounts query={new URLSearchParams({ edit: "1" })} />);

    const zones = await screen.findByLabelText("Zones");
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
    expect(screen.getByText("The stored credentials stay.")).toBeInTheDocument();
    await user.clear(zones);
    await user.type(zones, "example.com");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(window.location.hash).toBe("#/dns"));
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ label: "INWX main", zones: ["example.com"] });
  });

  it("asks before removing and shows a refusal", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...base,
      "GET /api/v1/dns-accounts": { body: { accounts: [inwx] } },
      "DELETE /api/v1/dns-accounts/1": { status: 409, body: { error: { code: "dns_account_in_use", message: "shop would have a domain without a DNS account" } } },
    });
    render(<DNSAccounts query={new URLSearchParams()} />);

    await user.click(await screen.findByRole("button", { name: "Remove INWX main" }));
    await user.click(screen.getByRole("button", { name: "Remove account" }));
    expect(await screen.findByText(/still get their certificate over this account/)).toBeInTheDocument();
  });
});

describe("zones", () => {
  it("parses and checks zone lists", () => {
    expect(parseZones(" Example.com.,acme.example.net\nexample.com ")).toEqual(["example.com", "acme.example.net"]);
    expect(badZone(["example.com", "_acme-challenge.example.com"])).toBe("");
    expect(badZone(["com"])).toBe("com");
    expect(badZone(["*.example.com"])).toBe("*.example.com");
  });
});

describe("challenge choice", () => {
  const tls = { enabled: true, active: true, source: "letsencrypt" as const, certSource: "letsencrypt" as const, challenge: "http" as const };
  const certRoute = { "GET /api/v1/sites/shop/tls/certificate": { body: { certificate: null } } };

  it("lets an admin switch to DNS", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ ...certRoute, "PUT /api/v1/sites/shop/tls/challenge": { body: {} } });
    render(<CertificateCard base="/sites/shop/tls" name="shop" tls={tls} challenge={{ editable: true }} onChange={async () => {}} />);

    await user.click(await screen.findByRole("radio", { name: /DNS: a TXT record/ }));
    expect(await screen.findByText("Let's Encrypt checks the domains over DNS from now on.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ challenge: "dns" });
  });

  it("is read-only for managers and hidden for an own certificate", async () => {
    mockFetch(certRoute);
    const { unmount } = render(<CertificateCard base="/sites/shop/tls" name="shop" tls={tls} challenge={{ editable: false }} onChange={async () => {}} />);
    expect(await screen.findByRole("radio", { name: /DNS: a TXT record/ })).toBeDisabled();
    expect(screen.getByText(/Only admins can choose DNS/)).toBeInTheDocument();
    unmount();

    render(<CertificateCard base="/sites/shop/tls" name="shop" tls={{ ...tls, source: "custom" }} challenge={{ editable: true }} onChange={async () => {}} />);
    expect(await screen.findByText("Source")).toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: /DNS: a TXT record/ })).not.toBeInTheDocument();
  });
});
