// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ChangePrimaryCard, DomainsCard, PendingDomainsNotice } from "./Domains";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site = (o: Partial<Site> = {}): Site => ({
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["old.example.com", "www.old.example.com"],
  redirectDomains: [],
  pendingDomains: null,
  tls: { enabled: true, active: true, hsts: false, domains: ["old.example.com", "www.old.example.com"], notAfter: null, source: "letsencrypt" },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
  ...o,
});

const pending = {
  domains: ["new.example.com", "www.new.example.com"],
  redirectDomains: ["old.example.com", "www.old.example.com"],
  requestedAt: "2026-11-25T01:00:00Z",
  error: "",
  failedAt: null,
};

describe("domains card", () => {
  it("saves served and redirect domains and says when the change waits", async () => {
    const calls = mockFetch({ "PUT /api/v1/sites/blog/domains": { status: 202, body: site({ pendingDomains: { ...pending, domains: ["old.example.com", "a.example.com"] } }) } });
    render(<DomainsCard site={site()} onChange={() => Promise.resolve()} />);

    const served = screen.getByLabelText("Domains of blog");
    await userEvent.clear(served);
    await userEvent.type(served, "old.example.com{enter}a.example.com");
    await userEvent.type(screen.getByLabelText("Redirect domains"), "older.example.com");
    await userEvent.click(screen.getByRole("button", { name: "Save domains" }));

    expect(calls).toContainEqual({
      method: "PUT",
      path: "/api/v1/sites/blog/domains",
      body: { domains: ["old.example.com", "a.example.com"], redirectDomains: ["older.example.com"], waitForCertificate: true },
    });
    expect(await screen.findByText(/waits for a certificate for old.example.com/)).toBeInTheDocument();
  });

  it("refuses a name in both lists", async () => {
    const calls = mockFetch({});
    render(<DomainsCard site={site()} onChange={() => Promise.resolve()} />);

    await userEvent.type(screen.getByLabelText("Redirect domains"), "www.old.example.com");
    await userEvent.click(screen.getByRole("button", { name: "Save domains" }));

    expect(await screen.findByText(/is a served domain and a redirect domain/)).toBeInTheDocument();
    expect(calls).toHaveLength(0);
  });

  it("has no redirect domains for a redirect site", () => {
    mockFetch({});
    render(<DomainsCard site={site({ type: "redirect" })} onChange={() => Promise.resolve()} />);
    expect(screen.queryByLabelText("Redirect domains")).not.toBeInTheDocument();
  });
});

describe("change primary domain", () => {
  it("previews the plan and sends it", async () => {
    const calls = mockFetch({ "PUT /api/v1/sites/blog/domains": { status: 202, body: site({ pendingDomains: pending }) } });
    let saved: Site | null = null;
    render(
      <ChangePrimaryCard
        site={site()}
        onClose={() => undefined}
        onSaved={(s) => {
          saved = s;
          return Promise.resolve();
        }}
      />,
    );

    expect(screen.getByRole("button", { name: /Change to/ })).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/New primary domain/), "new.example.com");

    const after = screen.getByRole("region", { name: "After the change" });
    expect(after).toHaveTextContent("new.example.com · new on the certificate");
    expect(after).toHaveTextContent("www.new.example.com · new on the certificate");
    expect(after).toHaveTextContent(/old\.example\.com/);
    expect(after).toHaveTextContent(/stays on HTTPS with its current domains/);
    expect(screen.getByText("wp search-replace 'https://old.example.com' 'https://new.example.com' --all-tables")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Change to new.example.com" }));
    expect(calls).toContainEqual({
      method: "PUT",
      path: "/api/v1/sites/blog/domains",
      body: { domains: ["new.example.com", "www.new.example.com"], redirectDomains: ["old.example.com", "www.old.example.com"], waitForCertificate: true },
    });
    expect(saved).not.toBeNull();
  });

  it("drops the old names when asked and checks the input", async () => {
    const calls = mockFetch({ "PUT /api/v1/sites/blog/domains": { body: site() } });
    render(<ChangePrimaryCard site={site()} onClose={() => undefined} onSaved={() => Promise.resolve()} />);

    await userEvent.type(screen.getByLabelText(/New primary domain/), "old.example.com");
    expect(screen.getByText("old.example.com is already the primary domain.")).toBeInTheDocument();

    const input = screen.getByLabelText(/New primary domain/);
    await userEvent.clear(input);
    await userEvent.type(input, "new.example.com");
    await userEvent.click(screen.getByRole("checkbox", { name: /Also/ }));
    await userEvent.click(screen.getByRole("checkbox", { name: /Redirect/ }));
    await userEvent.click(screen.getByRole("button", { name: "Change to new.example.com" }));

    expect(calls).toContainEqual({
      method: "PUT",
      path: "/api/v1/sites/blog/domains",
      body: { domains: ["new.example.com"], redirectDomains: [], waitForCertificate: true },
    });
  });
});

describe("pending notice", () => {
  it("shows the error and cancels", async () => {
    const calls = mockFetch({ "DELETE /api/v1/sites/blog/domains/pending": { body: site() } });
    render(
      <PendingDomainsNotice
        site={site({ pendingDomains: { ...pending, error: "acme: NXDOMAIN new.example.com", failedAt: "2026-11-25T01:01:00Z" }, tls: { ...site().tls, hsts: true } })}
        onChange={() => Promise.resolve()}
      />,
    );

    expect(screen.getByText("acme: NXDOMAIN new.example.com")).toBeInTheDocument();
    expect(screen.getByText(/HSTS is on/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Cancel change" }));
    expect(calls).toContainEqual({ method: "DELETE", path: "/api/v1/sites/blog/domains/pending" });
  });

  it("changes now without waiting", async () => {
    const calls = mockFetch({ "PUT /api/v1/sites/blog/domains": { body: site() } });
    render(<PendingDomainsNotice site={site({ pendingDomains: pending })} onChange={() => Promise.resolve()} />);

    await userEvent.click(screen.getByRole("button", { name: "Change now without HTTPS" }));
    expect(calls).toContainEqual({
      method: "PUT",
      path: "/api/v1/sites/blog/domains",
      body: { domains: pending.domains, redirectDomains: pending.redirectDomains, waitForCertificate: false },
    });
  });

  it("renders nothing without a pending change", () => {
    const { container } = render(<PendingDomainsNotice site={site()} onChange={() => Promise.resolve()} />);
    expect(container).toBeEmptyDOMElement();
  });
});
