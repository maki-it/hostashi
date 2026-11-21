// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { SiteTab } from "./routes";
import { SitePage } from "./SitePage";
import { Sites } from "./Sites";
import { compareVersions, domainsProblem, httpsLabel, parseDomains, phpChoices, siteNameProblem, siteStatus, type Site, type SiteTLS } from "./sites";
import { mockFetch } from "./test-utils";

const noTLS: SiteTLS = { enabled: false, active: false, hsts: false, domains: [], notAfter: null };
const blog: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com", "www.blog.example.com"],
  tls: { enabled: true, active: true, hsts: true, domains: ["blog.example.com", "www.blog.example.com"], notAfter: "2027-02-01T00:00:00Z" },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};
const php = { "GET /api/v1/php/versions": { body: { installed: ["8.2", "8.4"], supported: ["7.4", "8.1", "8.2", "8.3", "8.4", "8.5"], legacy: ["7.4", "8.1", "8.2"] } },
  // The settings load the cron jobs of the site (Cron.test.tsx tests them).
  "GET /api/v1/sites/blog/cron": { body: { jobs: [], statusKnown: true } },
  // And its SSH keys (SSH.test.tsx tests them).
  "GET /api/v1/sites/blog/ssh-keys": { body: { keys: [] } },
  // And its resource limits (Limits.test.tsx tests them).
  "GET /api/v1/sites/blog/limits": { body: { set: false, preset: "", applyError: "", appliedAt: 0, updatedAt: 0 } },
  "GET /api/v1/limit-presets": { body: { presets: [] } },
};

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

describe("siteStatus and phpChoices", () => {
  it("names the state of a site", () => {
    expect(siteStatus(blog)).toMatchObject({ tone: "ok", word: "Active" });
    expect(siteStatus({ ...blog, disabled: true, maintenance: { ...blog.maintenance, enabled: true } })).toMatchObject({ word: "Disabled" });
    expect(siteStatus({ ...blog, maintenance: { ...blog.maintenance, enabled: true } })).toMatchObject({ tone: "warn", word: "Maintenance" });
    expect(siteStatus({ ...blog, state: "creating" })).toMatchObject({ word: "Being created" });
    expect(siteStatus({ ...blog, state: "deleting" })).toMatchObject({ word: "Being deleted" });
  });

  it("sorts PHP versions newest first, numerically, and keeps the current one", () => {
    expect(compareVersions("8.10", "8.4")).toBeGreaterThan(0);
    const php = { installed: ["8.4", "8.10", "8.2"], supported: [], legacy: [] };
    expect(phpChoices(php)).toEqual(["8.10", "8.4", "8.2"]);
    expect(phpChoices(php, "7.4")).toEqual(["8.10", "8.4", "8.2", "7.4"]);
    expect(phpChoices(null)).toEqual([]);
  });
});

// ListHarness keeps the address query in state, as the app does.
function ListHarness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Sites query={query} onQuery={setQuery} />;
}

const shop: Site = { ...blog, name: "shop", php: "", domains: ["shop.example.com"], tls: { ...noTLS, enabled: true, error: "no DNS record" } };

describe("Sites", () => {
  const rowNames = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((r) => within(r).getAllByRole("cell")[0]!.querySelector("a")?.textContent);

  it("lists sites with their state in a table", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [blog, shop] } } });
    render(<ListHarness />);
    const table = await screen.findByRole("table", { name: "Sites" });
    const b = within(within(table).getByRole("link", { name: "blog" }).closest("tr")!);
    expect(b.getByText("8.4")).toBeInTheDocument();
    expect(b.getByText("HTTPS")).toBeInTheDocument();
    expect(b.getByText("Active")).toBeInTheDocument();
    expect(b.getByText("blog.example.com")).toBeInTheDocument();
    expect(b.getByRole("link", { name: "Settings of blog" })).toHaveAttribute("href", "#/sites/blog");
    const s = within(within(table).getByRole("link", { name: "shop" }).closest("tr")!);
    expect(s.getByText("No PHP")).toBeInTheDocument();
    expect(s.getByText("Certificate failed")).toBeInTheDocument();
  });

  it("filters by text and state and sorts by a column", async () => {
    const user = userEvent.setup();
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [shop, blog, { ...blog, name: "wiki", domains: ["wiki.example.org"], disabled: true }] } } });
    render(<ListHarness />);
    await screen.findByRole("table", { name: "Sites" });
    expect(rowNames()).toEqual(["shop", "blog", "wiki"]);

    await user.click(screen.getByRole("button", { name: "Site" }));
    expect(rowNames()).toEqual(["blog", "shop", "wiki"]);
    await user.click(screen.getByRole("button", { name: "Site" }));
    expect(rowNames()).toEqual(["wiki", "shop", "blog"]);

    await user.type(screen.getByRole("searchbox", { name: "Filter sites" }), "example.com");
    expect(rowNames()).toEqual(["shop", "blog"]);
    await user.selectOptions(screen.getByRole("combobox", { name: "State" }), "problem");
    expect(rowNames()).toEqual(["shop"]);
    await user.clear(screen.getByRole("searchbox", { name: "Filter sites" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "State" }), "disabled");
    expect(rowNames()).toEqual(["wiki"]);
    await user.type(screen.getByRole("searchbox", { name: "Filter sites" }), "nothing");
    expect(screen.getByText("No site matches the filter.")).toBeInTheDocument();
  });

  it("reads filter and sort from the address", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [shop, blog] } } });
    render(<ListHarness initial="sort=name&dir=desc&q=o" />);
    await screen.findByRole("table", { name: "Sites" });
    expect(rowNames()).toEqual(["shop", "blog"]);
    expect(screen.getByRole("searchbox", { name: "Filter sites" })).toHaveValue("o");
    expect(screen.getByRole("columnheader", { name: "Site" })).toHaveAttribute("aria-sort", "descending");
  });

  it("links to the form for a new site", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [] } } });
    render(<ListHarness />);
    expect(await screen.findByText("No sites yet.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New site" })).toHaveAttribute("href", "#/sites?new=1");
  });

  it("creates a site with the newest standard PHP and HTTPS", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": { body: { sites: [] } },
      "POST /api/v1/sites": { status: 201, body: { ...blog, tls: { ...noTLS, enabled: true } } },
    });
    render(<ListHarness initial="new=1" />);
    expect(screen.getByRole("heading", { name: "New site" })).toBeInTheDocument();
    await user.type(screen.getByLabelText(/^Name/), "blog");
    await user.type(screen.getByLabelText(/^Domains/), "Blog.example.com\nwww.blog.example.com");
    await waitFor(() => expect(screen.getByLabelText("PHP")).toHaveValue("8.4"));
    await user.click(screen.getByLabelText(/^HSTS/));
    await user.click(screen.getByRole("button", { name: "Create site" }));

    expect(await screen.findByRole("status")).toHaveTextContent("served over HTTP until its certificate is there");
    expect(screen.getByRole("link", { name: "Open blog" })).toHaveAttribute("href", "#/sites/blog");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "blog",
      php: "8.4",
      domains: ["blog.example.com", "www.blog.example.com"],
      tls: true,
      hsts: true,
    });
    expect(screen.getByLabelText(/^Name/)).toHaveValue("");
  });

  it("warns about legacy PHP and sends no HSTS without HTTPS", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": { body: { sites: [] } },
      "POST /api/v1/sites": { status: 201, body: { ...blog, tls: noTLS } },
    });
    render(<ListHarness initial="new=1" />);
    await waitFor(() => expect(screen.getByLabelText("PHP")).toHaveValue("8.4"));
    await user.type(screen.getByLabelText(/^Name/), "blog");
    await user.type(screen.getByLabelText(/^Domains/), "blog.example.com");
    await user.selectOptions(screen.getByLabelText("PHP"), "8.2");
    expect(screen.getByText(/PHP 8.2 gets no or only security fixes/)).toBeInTheDocument();
    await user.click(screen.getByLabelText(/^HTTPS with/));
    expect(screen.queryByLabelText(/^HSTS/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create site" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Site blog created.");
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ php: "8.2", tls: false, hsts: false });
  });

  it("checks the form before sending", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [] } } });
    render(<ListHarness initial="new=1" />);
    await user.click(screen.getByRole("button", { name: "Create site" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Enter a name.");
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
      "POST /api/v1/sites": { status: 409, body: { error: { code: "domain_taken" } } },
    });
    render(<ListHarness initial="new=1" />);
    await user.type(screen.getByLabelText(/^Name/), "blog");
    await user.type(screen.getByLabelText(/^Domains/), "blog.example.com");
    await user.click(screen.getByRole("button", { name: "Create site" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("belongs to another site");
  });

  it("offers no PHP when the versions cannot be read", async () => {
    mockFetch({ "GET /api/v1/sites": { body: { sites: [] } }, "GET /api/v1/php/versions": { status: 503, body: { error: { code: "agent_unavailable" } } } });
    render(<ListHarness initial="new=1" />);
    const select = screen.getByLabelText("PHP");
    await waitFor(() => expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual(["No PHP"]));
  });
});

// PageHarness switches tabs as the app does with the address.
function PageHarness({ tab = "overview" }: { tab?: SiteTab }) {
  const [t, setT] = useState<SiteTab>(tab);
  return <SitePage name="blog" tab={t} onTab={setT} />;
}

describe("SitePage", () => {
  it("shows the details and the tabs of a site", async () => {
    const user = userEvent.setup();
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [blog] } } });
    render(<PageHarness />);
    expect(await screen.findByRole("heading", { name: "blog", level: 1 })).toBeInTheDocument();
    const d = within(screen.getByLabelText("Details of blog"));
    expect(d.getByText("PHP 8.4")).toBeInTheDocument();
    expect(d.getByText("HTTPS, HSTS, valid until 2027-02-01")).toBeInTheDocument();
    expect(d.getByText("www.blog.example.com")).toBeInTheDocument();
    const tabs = screen.getByRole("tablist", { name: "blog" });
    expect(within(tabs).getAllByRole("tab").map((t) => t.textContent)).toEqual([
      "Overview",
      "Domains & HTTPS",
      "PHP",
      "Cron",
      "SSH & SFTP",
      "Limits",
      "nginx",
    ]);
    await user.click(within(tabs).getByRole("tab", { name: "PHP" }));
    expect(screen.getByRole("tabpanel", { name: "PHP" })).toHaveTextContent("PHP of blog");
  });

  it("explains a failed certificate", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [{ ...blog, tls: { ...noTLS, enabled: true, error: "no DNS record" } }] } } });
    render(<PageHarness />);
    expect(await screen.findByRole("alert")).toHaveTextContent(/Last attempt failed: no DNS record/);
  });

  it("says when the site does not exist", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [] } } });
    render(<PageHarness />);
    expect(await screen.findByRole("alert")).toHaveTextContent("There is no site named blog.");
  });

  it("shows the overview for a tab that does not exist yet", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [blog] } } });
    render(<PageHarness tab="files" />);
    expect(await screen.findByRole("tabpanel", { name: "Overview" })).toBeInTheDocument();
  });

  it("deletes a site only after its name is typed", async () => {
    const user = userEvent.setup();
    window.location.hash = "#/sites/blog";
    const calls = mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [blog] } }, "DELETE /api/v1/sites/blog": { status: 204 } });
    render(<PageHarness />);
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    const confirm = screen.getByRole("button", { name: "Delete blog" });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/Type blog to confirm/), "blo");
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/Type blog to confirm/), "g");
    await user.click(confirm);
    await waitFor(() => expect(window.location.hash).toBe("#/sites"));
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
    window.location.hash = "";
  });

  it("changes domains, HTTPS and HSTS", async () => {
    const user = userEvent.setup();
    let cur: Site = { ...blog, domains: ["blog.example.com"] };
    const put = (f: (b: Record<string, unknown>) => Site) => (b: unknown) => {
      cur = f(b as Record<string, unknown>);
      return { body: cur };
    };
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": () => ({ body: { sites: [cur] } }),
      "PUT /api/v1/sites/blog/domains": put((b) => ({ ...cur, domains: b.domains as string[] })),
      "PUT /api/v1/sites/blog/tls": put((b) => ({ ...cur, tls: { ...cur.tls, enabled: b.enabled as boolean, active: false } })),
      "PUT /api/v1/sites/blog/hsts": put((b) => ({ ...cur, tls: { ...cur.tls, hsts: b.enabled as boolean } })),
    });
    render(<PageHarness tab="domains" />);
    const domains = within(await screen.findByRole("region", { name: "Domains" }));
    await user.type(domains.getByLabelText(/^Domains of blog/), "\nWWW.blog.example.com");
    await user.click(domains.getByRole("button", { name: "Save domains" }));
    expect(await domains.findByRole("status")).toHaveTextContent("served over HTTP until a certificate for the new domains");

    const https = within(screen.getByRole("region", { name: "HTTPS" }));
    await user.click(https.getByLabelText(/^HSTS/));
    expect(await https.findByText(/HSTS is off/)).toBeInTheDocument();
    await user.click(https.getByLabelText(/^HTTPS with/));
    expect(await https.findByText("HTTPS is off. The site is served over HTTP.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").map((c) => [c.path, c.body])).toEqual([
      ["/api/v1/sites/blog/domains", { domains: ["blog.example.com", "www.blog.example.com"] }],
      ["/api/v1/sites/blog/hsts", { enabled: false }],
      ["/api/v1/sites/blog/tls", { enabled: false }],
    ]);
  });

  it("rejects bad domains and shows a refused change", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": { body: { sites: [blog] } },
      "PUT /api/v1/sites/blog/tls": { status: 409, body: { error: { code: "tls_unavailable" } } },
    });
    render(<PageHarness tab="domains" />);
    const domains = within(await screen.findByRole("region", { name: "Domains" }));
    await user.clear(domains.getByLabelText(/^Domains of blog/));
    await user.click(domains.getByRole("button", { name: "Save domains" }));
    expect(domains.getByRole("alert")).toHaveTextContent("at least one domain");
    const https = within(screen.getByRole("region", { name: "HTTPS" }));
    await user.click(https.getByLabelText(/^HTTPS with/));
    expect(await https.findByRole("alert")).toHaveTextContent("Certificates are not available");
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1);
  });

  it("changes the PHP version", async () => {
    const user = userEvent.setup();
    let cur: Site = { ...blog };
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": () => ({ body: { sites: [cur] } }),
      "PUT /api/v1/sites/blog/php": (b: unknown) => {
        cur = { ...cur, php: (b as { php: string }).php };
        return { body: cur };
      },
    });
    render(<PageHarness tab="php" />);
    const box = within(await screen.findByRole("region", { name: "PHP version" }));
    expect(box.getByRole("button", { name: "Save PHP version" })).toBeDisabled();
    await user.selectOptions(box.getByLabelText("PHP of blog"), "");
    await user.click(box.getByRole("button", { name: "Save PHP version" }));
    expect(await box.findByText("PHP is off for this site now.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").map((c) => c.body)).toEqual([{ php: "" }]);
  });

  it("lets PHP of a site start programs, and forbids it again", async () => {
    const user = userEvent.setup();
    let cur: Site = { ...blog };
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": () => ({ body: { sites: [cur] } }),
      "PUT /api/v1/sites/blog/php-programs": (b: unknown) => {
        cur = { ...cur, phpPrograms: (b as { enabled: boolean }).enabled };
        return { body: cur };
      },
    });
    render(<PageHarness tab="php" />);
    const box = within(await screen.findByRole("region", { name: "Programs from PHP" }));
    const programs = box.getByLabelText(/^Allow PHP to start programs/);
    expect(programs).not.toBeChecked();
    await user.click(programs);
    expect(await box.findByText("PHP of this site may start programs now.")).toBeInTheDocument();
    expect(box.getByLabelText(/^Allow PHP to start programs/)).toBeChecked();
    await user.click(box.getByLabelText(/^Allow PHP to start programs/));
    expect(await box.findByText("PHP of this site can no longer start programs.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").map((c) => [c.path, c.body])).toEqual([
      ["/api/v1/sites/blog/php-programs", { enabled: true }],
      ["/api/v1/sites/blog/php-programs", { enabled: false }],
    ]);
  });

  it("has no programs switch for a site without PHP", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [{ ...blog, php: "" }] } } });
    render(<PageHarness tab="php" />);
    await screen.findByRole("region", { name: "PHP version" });
    expect(screen.queryByRole("region", { name: "Programs from PHP" })).not.toBeInTheDocument();
  });

  it("keeps a PHP version that is no longer installed selectable, newest first", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [{ ...blog, php: "7.4" }] } } });
    render(<PageHarness tab="php" />);
    const select = await screen.findByLabelText("PHP of blog");
    await waitFor(() =>
      expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual([
        "No PHP",
        "PHP 8.4",
        "PHP 8.2 (legacy)",
        "PHP 7.4 (legacy) (not installed)",
      ]),
    );
    expect(select).toHaveValue("7.4");
  });

  it("offers no settings while a site is being deleted", async () => {
    mockFetch({ ...php, "GET /api/v1/sites": { body: { sites: [{ ...blog, state: "deleting" }] } } });
    render(<PageHarness tab="php" />);
    expect(await screen.findByText("Being deleted", { selector: "dd" })).toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  it("disables a site after a confirmation and enables it again", async () => {
    const user = userEvent.setup();
    let cur: Site = blog;
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": () => ({ body: { sites: [cur] } }),
      "GET /api/v1/status-pages": { body: { pages: [] } },
      "PUT /api/v1/sites/blog/disabled": (b) => {
        cur = { ...cur, disabled: (b as { disabled: boolean }).disabled };
        return { body: cur };
      },
    });
    render(<PageHarness />);
    const box = within(await screen.findByRole("region", { name: "Availability" }));
    await user.click(box.getByRole("button", { name: "Disable site" }));
    expect(box.getByText(/Files, databases, backups and the certificate stay/)).toBeInTheDocument();
    await user.click(box.getByRole("button", { name: "Cancel" }));
    expect(calls.some((c) => c.method === "PUT")).toBe(false);

    await user.click(box.getByRole("button", { name: "Disable site" }));
    await user.click(box.getByRole("button", { name: "Disable blog" }));
    expect(await box.findByRole("status")).toHaveTextContent("blog is disabled.");
    expect(screen.getByText("Disabled", { selector: ".ui-badge" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("Details of blog")).getByText(/PHP is stopped/)).toBeInTheDocument();

    await user.click(box.getByRole("button", { name: "Enable blog" }));
    expect(await box.findByText("blog is enabled again.")).toBeInTheDocument();
    expect(screen.queryByText("Disabled", { selector: ".ui-badge" })).not.toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").map((c) => c.body)).toEqual([{ disabled: true }, { disabled: false }]);
  });

  it("starts maintenance with a page and allowed addresses", async () => {
    const user = userEvent.setup();
    let cur: Site = blog;
    const calls = mockFetch({
      ...php,
      "GET /api/v1/sites": () => ({ body: { sites: [cur] } }),
      "GET /api/v1/status-pages": {
        body: {
          pages: [
            { id: "builtin-maintenance", name: "Built-in maintenance page", kind: "maintenance", builtin: true, sha256: "", files: [], sites: [] },
            { id: "builtin-parking", name: "Built-in parking page", kind: "parking", builtin: true, sha256: "", files: [], sites: [] },
            { id: "p1", name: "Summer break", kind: "maintenance", builtin: false, sha256: "", files: [], sites: [] },
          ],
        },
      },
      "GET /api/v1/auth/me": { body: { username: "kim", role: "admin", clientIp: "198.51.100.7" } },
      "PUT /api/v1/sites/blog/maintenance": (b) => {
        cur = { ...cur, maintenance: b as Site["maintenance"] };
        return { body: cur };
      },
    });
    render(<PageHarness />);
    const box = within(await screen.findByRole("region", { name: "Maintenance" }));
    const select = await box.findByRole("combobox", { name: "Maintenance page" });
    await waitFor(() => expect(within(select).getAllByRole("option")).toHaveLength(2));
    expect(within(select).queryByText("Built-in parking page")).not.toBeInTheDocument();
    await user.selectOptions(select, "p1");

    const allow = box.getByRole("textbox", { name: /Allowed addresses/ });
    await user.type(allow, "not-an-ip");
    await user.click(box.getByRole("button", { name: "Start maintenance" }));
    expect(box.getByRole("alert")).toHaveTextContent('"not-an-ip" is not an IP address');
    expect(calls.some((c) => c.method === "PUT")).toBe(false);

    await user.clear(allow);
    await user.type(allow, "10.0.0.0/8");
    await user.click(box.getByRole("button", { name: "Add my current IP" }));
    await waitFor(() => expect(allow).toHaveValue("10.0.0.0/8\n198.51.100.7"));
    await user.click(box.getByRole("button", { name: "Start maintenance" }));
    expect(await box.findByText("Maintenance is on. The listed addresses still see the site.")).toBeInTheDocument();
    expect(screen.getByText("Maintenance", { selector: ".ui-badge" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("Details of blog")).getByText(/except 10.0.0.0\/8, 198.51.100.7/)).toBeInTheDocument();

    await user.click(box.getByRole("button", { name: "End maintenance" }));
    expect(await box.findByText("Maintenance is off. Everybody sees the site again.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").map((c) => c.body)).toEqual([
      { enabled: true, page: "p1", allowIps: ["10.0.0.0/8", "198.51.100.7"] },
      { enabled: false, page: "p1", allowIps: ["10.0.0.0/8", "198.51.100.7"] },
    ]);
  });
});
