// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Sites } from "./Sites";
import { domainsProblem, httpsLabel, parseDomains, siteNameProblem, type Site, type SiteTLS } from "./sites";
import { mockFetch } from "./test-utils";

const noTLS: SiteTLS = { enabled: false, active: false, hsts: false, domains: [], notAfter: null };
const blog: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  domains: ["blog.example.com", "www.blog.example.com"],
  tls: { enabled: true, active: true, hsts: true, domains: ["blog.example.com", "www.blog.example.com"], notAfter: "2027-02-01T00:00:00Z" },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};
const php = { "GET /api/v1/php/versions": { body: { installed: ["8.2", "8.4"], supported: ["7.4", "8.1", "8.2", "8.3", "8.4", "8.5"], legacy: ["7.4", "8.1", "8.2"] } } };

describe("siteNameProblem", () => {
  it.each([
    ["blog", ""],
    ["shop-2_x", ""],
    ["", "Enter a name."],
    ["b", "Use 2 to 32"],
    ["Blog", "Use 2 to 32"],
    ["1blog", "Use 2 to 32"],
    ["-blog", "Use 2 to 32"],
    ["blog.de", "Use 2 to 32"],
    ["../etc", "Use 2 to 32"],
    ["a".repeat(32), ""],
    ["a".repeat(33), "Use 2 to 32"],
    ["root", "reserved"],
    ["www-data", "reserved"],
    ["systemd-x", "reserved"],
  ] as const)("%s", (v, want) => {
    const got = siteNameProblem(v);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });
});

describe("domainsProblem", () => {
  it.each([
    [["example.com"], ""],
    [["xn--mnchen-3ya.de", "www.example.com"], ""],
    [[], "at least one"],
    [["localhost"], "two parts"],
    [["*.example.com"], "not a domain"],
    [["-a.example.com"], "not a domain"],
    [["a-.example.com"], "not a domain"],
    [["exa_mple.com"], "not a domain"],
    [["münchen.de"], "xn--"],
    [["10.0.0.1"], "IP addresses"],
    [["example.com."], "not a domain"],
    [["a".repeat(64) + ".com"], "not a domain"],
    [["example.com", "example.com"], "listed twice"],
    [Array.from({ length: 51 }, (_, i) => `s${i}.example.com`), "at most 50"],
  ] as const)("%j", (v, want) => {
    const got = domainsProblem([...v]);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });

  it("parses lines, spaces and commas and lower-cases", () => {
    expect(parseDomains(" Example.com\nwww.example.com, shop.example.com  \n\n")).toEqual(["example.com", "www.example.com", "shop.example.com"]);
  });
});

describe("httpsLabel", () => {
  it.each([
    [noTLS, "HTTP only"],
    [{ ...noTLS, enabled: true }, "Waiting for certificate"],
    [{ ...noTLS, enabled: true, error: "dns" }, "Certificate failed"],
    [{ ...noTLS, enabled: true, active: true }, "HTTPS"],
  ])("%j", (t, want) => expect(httpsLabel(t)).toBe(want));
});

describe("Sites", () => {
  it("lists sites with their state", async () => {
    mockFetch({
      ...php,
      "GET /api/v1/sites": {
        body: { sites: [blog, { ...blog, name: "shop", php: "", domains: ["shop.example.com"], tls: { ...noTLS, enabled: true, error: "no DNS record" } }] },
      },
    });
    render(<Sites />);
    const b = within(await screen.findByLabelText("Details of blog"));
    expect(b.getByText("PHP 8.4")).toBeInTheDocument();
    expect(b.getByText("HTTPS, HSTS, valid until 2027-02-01")).toBeInTheDocument();
    expect(b.getByText("www.blog.example.com")).toBeInTheDocument();
    const s = within(screen.getByLabelText("Details of shop"));
    expect(s.getByText("No PHP")).toBeInTheDocument();
    expect(s.getByText("Certificate failed")).toBeInTheDocument();
    expect(screen.getByText(/Last attempt failed: no DNS record/)).toBeInTheDocument();
  });

  it("creates a site with the newest standard PHP and HTTPS", async () => {
    const user = userEvent.setup();
    let created = false;
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": () => ({ body: { sites: created ? [{ ...blog, tls: { ...noTLS, enabled: true } }] : [] } }),
      "POST /api/v1/sites": () => {
        created = true;
        return { status: 201, body: { ...blog, tls: { ...noTLS, enabled: true } } };
      },
    });
    render(<Sites />);
    expect(await screen.findByText("No sites yet.")).toBeInTheDocument();
    await user.type(screen.getByLabelText(/^Name/), "blog");
    await user.type(screen.getByLabelText(/^Domains/), "Blog.example.com\nwww.blog.example.com");
    await waitFor(() => expect(screen.getByLabelText("PHP")).toHaveValue("8.4"));
    await user.click(screen.getByLabelText(/^HSTS/));
    await user.click(screen.getByRole("button", { name: "Create site" }));

    expect(await screen.findByRole("status")).toHaveTextContent("served over HTTP until its certificate is there");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "blog",
      php: "8.4",
      domains: ["blog.example.com", "www.blog.example.com"],
      tls: true,
      hsts: true,
    });
    expect(await screen.findByText("blog", { selector: "strong" })).toBeInTheDocument();
    expect(screen.getByLabelText(/^Name/)).toHaveValue("");
  });

  it("warns about legacy PHP and sends no HSTS without HTTPS", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": { body: { sites: [] } },
      "POST /api/v1/sites": { status: 201, body: { ...blog, tls: noTLS } },
    });
    render(<Sites />);
    await user.type(await screen.findByLabelText(/^Name/), "old");
    await user.type(screen.getByLabelText(/^Domains/), "old.example.com");
    await waitFor(() => expect(screen.getByLabelText("PHP")).toHaveValue("8.4"));
    await user.selectOptions(screen.getByLabelText("PHP"), "8.2");
    expect(screen.getByText(/PHP 8.2 gets no or only security fixes/)).toBeInTheDocument();
    await user.click(screen.getByLabelText(/^HSTS/));
    await user.click(screen.getByLabelText(/^HTTPS with/));
    expect(screen.queryByLabelText(/^HSTS/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create site" }));
    await screen.findByRole("status");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "old", php: "8.2", domains: ["old.example.com"], tls: false, hsts: false });
  });

  it("checks the form before sending", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [] } } });
    render(<Sites />);
    await user.type(await screen.findByLabelText(/^Name/), "root");
    await user.click(screen.getByRole("button", { name: "Create site" }));
    expect(screen.getByRole("alert")).toHaveTextContent("reserved");
    await user.clear(screen.getByLabelText(/^Name/));
    await user.type(screen.getByLabelText(/^Name/), "blog");
    await user.type(screen.getByLabelText(/^Domains/), "localhost");
    await user.click(screen.getByRole("button", { name: "Create site" }));
    expect(screen.getByRole("alert")).toHaveTextContent("two parts");
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("explains a server refusal", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...php,
      "GET /api/v1/sites": { body: { sites: [] } },
      "POST /api/v1/sites": { status: 409, body: { error: { code: "domain_taken", message: "x" } } },
    });
    render(<Sites />);
    await user.type(await screen.findByLabelText(/^Name/), "blog");
    await user.type(screen.getByLabelText(/^Domains/), "blog.example.com");
    await user.click(screen.getByRole("button", { name: "Create site" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("belongs to another site");
  });

  it("offers no PHP when the versions cannot be read", async () => {
    mockFetch({
      "GET /api/v1/php/versions": { status: 503, body: { error: { code: "agent_unavailable" } } },
      "GET /api/v1/sites": { body: { sites: [] } },
    });
    render(<Sites />);
    const select = await screen.findByLabelText("PHP");
    expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual(["No PHP"]);
  });

  it("deletes a site only after its name is typed", async () => {
    const user = userEvent.setup();
    let gone = false;
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": () => ({ body: { sites: gone ? [] : [blog] } }),
      "DELETE /api/v1/sites/blog": () => {
        gone = true;
        return { status: 204 };
      },
    });
    render(<Sites />);
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    const confirm = screen.getByRole("button", { name: "Delete blog" });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/Type blog to delete/), "blo");
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/Type blog to delete/), "g");
    await user.click(confirm);
    expect(await screen.findByText("No sites yet.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  });
});
