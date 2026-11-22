// SPDX-License-Identifier: AGPL-3.0-or-later

import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { Migrate } from "./Migrate";
import type { ImportJob, Plan, PlanSite, SourceInfo } from "./cloudpanel";
import { mockFetch } from "./test-utils";

const base = "/api/v1/migrations/cloudpanel";
const readySource: SourceInfo = {
  source: { host: "203.0.113.7", port: 22 },
  publicKey: "ssh-ed25519 AAAA hostashi",
  authorizedKeysLine: 'restrict,command="sftp-server -R" ssh-ed25519 AAAA hostashi',
  hostKey: "SHA256:abc",
  ready: true,
};

const planSite = (name: string, action: PlanSite["action"], reasons: PlanSite["reasons"] = []): PlanSite => ({
  name,
  type: "php",
  domains: [name + ".example"],
  redirectFrom: ["www." + name + ".example"],
  docRoot: "/home/" + name + "/htdocs/" + name + ".example",
  php: "8.2",
  tls: "reissue",
  customSnippets: action === "review" ? 2 : 0,
  cronJobs: 1,
  cronImport: 1,
  action,
  reasons,
  source: "/etc/nginx/sites-enabled/" + name + ".conf",
});

const plan = (sites: PlanSite[]): Plan => ({
  sites,
  summary: {
    sites: sites.length,
    import: sites.filter((s) => s.action === "import").length,
    review: sites.filter((s) => s.action === "review").length,
    skip: sites.filter((s) => s.action === "skip").length,
  },
});

const exists = [
  { code: "site_exists", msg: "a site named shop exists on this server" },
  { code: "domain_taken", msg: "the domain shop.example belongs to a site on this server" },
];

const noCutover = { status: 404, body: { error: { code: "not_found", message: "the site has no cut-over" } } };

// Harness keeps the address query in state, as the app does.
function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Migrate query={query} onQuery={setQuery} />;
}

describe("Migrate", () => {
  it("connects the old server and confirms its host key", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      [`GET ${base}/source`]: { body: { source: null, hostKey: "", ready: false } },
      [`PUT ${base}/source`]: (b) => ({ body: { ...readySource, source: b, hostKey: "", ready: false } }),
      [`POST ${base}/source/hostkey/scan`]: { body: { key: "ssh-ed25519 KEY", fingerprint: "SHA256:abc" } },
      [`PUT ${base}/source/hostkey`]: { body: readySource },
      [`GET ${base}/reports`]: { body: { reports: [] } },
    });
    render(<Migrate />);
    const card = within(await screen.findByRole("region", { name: "Old server" }));
    expect(await screen.findByText("No report is uploaded.")).toBeInTheDocument();

    await user.type(card.getByRole("textbox", { name: /^Address/ }), "web2 .example");
    await user.click(card.getByRole("button", { name: "Connect old server" }));
    expect(card.getByRole("alert")).toHaveTextContent("Use an IP address");
    expect(calls.some((c) => c.method === "PUT")).toBe(false);

    await user.clear(card.getByRole("textbox", { name: /^Address/ }));
    await user.type(card.getByRole("textbox", { name: /^Address/ }), "203.0.113.7");
    await user.click(card.getByRole("button", { name: "Connect old server" }));
    expect(await card.findByLabelText("authorized_keys line")).toHaveTextContent('command="sftp-server -R"');
    expect(calls).toContainEqual({ method: "PUT", path: `${base}/source`, body: { host: "203.0.113.7", port: 22 } });

    const hk = within(screen.getByRole("region", { name: "Host key of the old server" }));
    await user.click(hk.getByRole("button", { name: "Fetch the host key" }));
    expect(await hk.findByText("SHA256:abc")).toBeInTheDocument();
    await user.click(hk.getByRole("button", { name: "Trust this key" }));
    expect(await hk.findByText(/^Trusted:/)).toBeInTheDocument();
    expect(calls).toContainEqual({ method: "PUT", path: `${base}/source/hostkey`, body: { key: "ssh-ed25519 KEY" } });
    expect(screen.getByText(/The connection is ready/)).toBeInTheDocument();
  });

  it("shows the SQL for the dump user once", async () => {
    const user = userEvent.setup();
    mockFetch({
      [`GET ${base}/source`]: { body: readySource },
      [`POST ${base}/source/database`]: {
        body: { user: "hostashi_import", password: "pw123", sql: "CREATE OR REPLACE USER 'hostashi_import'@'127.0.0.1' IDENTIFIED BY 'pw123';" },
      },
      [`GET ${base}/reports`]: { body: { reports: [] } },
    });
    render(<Migrate />);
    await user.click(await screen.findByRole("button", { name: "Create the database user" }));
    const notice = within(await screen.findByRole("region", { name: "Run this SQL on the old server now" }));
    expect(notice.getByLabelText("SQL for the old server")).toHaveTextContent("IDENTIFIED BY 'pw123'");
    await user.click(notice.getByRole("button", { name: "I have run it" }));
    expect(screen.queryByText(/pw123/)).not.toBeInTheDocument();
    const card = within(screen.getByRole("region", { name: "Database access" }));
    expect(card.getByText("hostashi_import")).toBeInTheDocument();

    // A second password needs a confirmation: the SQL must run again.
    await user.click(card.getByRole("button", { name: "Make a new password" }));
    expect(card.getByText(/A new password replaces the old one/)).toBeInTheDocument();
  });

  it("creates a reviewed site from the plan and imports it", async () => {
    const user = userEvent.setup();
    let sites = [
      { ...planSite("shop", "review", [{ code: "custom_snippets", msg: "2 nginx directives need a one-time review" }]), pageCache: "php" },
      planSite("old", "skip", [{ code: "type_unsupported", msg: 'site type "nodejs" is not supported by Hostashi yet' }]),
    ];
    let polls = 0;
    const job = (state: ImportJob["state"]): ImportJob => ({
      id: "imp1",
      site: "shop",
      remoteUser: "shop",
      docRoot: "/home/shop/htdocs/shop.example",
      dir: "cloudpanel-import",
      state,
      created: "2026-11-19T10:00:00Z",
      files: state === "done" ? 120 : 0,
      bytes: state === "done" ? 3 * 1024 * 1024 : 0,
      unchanged: 0,
      deleted: 0,
      skipped: 0,
      databases: [{ from: "shop_live", to: "shop" }],
      ...(state === "done" ? { finished: "2026-11-19T10:01:00Z", dumps: ["cloudpanel-import-db/shop-20261119T100100Z.sql"] } : {}),
    });
    const report = { id: "r1", sites: 2, createdAt: "2026-11-19T09:00:00Z" };
    let reports: (typeof report)[] = [];
    const calls = mockFetch({
      [`GET ${base}/source`]: { body: readySource },
      [`GET ${base}/reports`]: () => ({ body: { reports } }),
      [`POST ${base}/reports`]: () => {
        reports = [report];
        return { status: 201, body: report };
      },
      [`GET ${base}/reports/r1`]: () => ({ body: { ...report, plan: plan(sites) } }),
      "GET /api/v1/php/versions": { body: { installed: ["8.3", "8.4"], supported: ["8.3", "8.4", "8.5"], legacy: ["8.2"] } },
      [`POST ${base}/reports/r1/sites/shop`]: () => {
        sites = [{ ...sites[0]!, action: "skip", reasons: exists }, sites[1]!];
        return { status: 201, body: sites[0] };
      },
      "GET /api/v1/databases": { body: { databases: [{ name: "shop", site: "shop", users: [], createdAt: "" }] } },
      [`GET ${base}/imports?site=shop`]: { body: { imports: [] } },
      [`GET ${base}/sites/shop/cutover`]: noCutover,
      [`POST ${base}/reports/r1/sites/shop/import`]: { status: 202, body: job("queued") },
      [`POST ${base}/sites/shop/activate`]: { body: { site: "shop", previous: "htdocs-before-import-20261119T100500Z" } },
      [`GET ${base}/imports/imp1/loads`]: {
        body: {
          loads: [
            {
              database: "shop",
              path: "cloudpanel-import-db/shop-20261119T100100Z.sql",
              job: { id: "j1", state: "failed", error: "line 12: ERROR 1227", started: "2026-11-19T10:01:05Z" },
            },
          ],
        },
      },
      [`POST ${base}/imports/imp1/load`]: {
        status: 202,
        body: {
          loads: [
            {
              database: "shop",
              path: "cloudpanel-import-db/shop-20261119T100100Z.sql",
              job: { id: "j2", state: "done", started: "2026-11-19T10:02:00Z", finished: "2026-11-19T10:02:30Z" },
            },
          ],
        },
      },
      [`GET ${base}/imports/imp1`]: () => ({ body: job(polls++ === 0 ? "running" : "done") }),
    });
    render(<Harness initial="report=r1" />);

    expect(await screen.findByText("2 sites: 0 ready, 1 to review, 1 skipped.")).toBeInTheDocument();
    const table = within(screen.getByRole("table", { name: "Plan" }));
    const rows = table.getAllByRole("row").slice(1);
    // Sites to review come first, skipped ones last with their reason.
    expect(within(rows[0]!).getByRole("link", { name: "shop" })).toHaveAttribute("href", "#/import?report=r1&site=shop");
    expect(within(rows[0]!).getByText("2 nginx directives need a one-time review.")).toBeInTheDocument();
    expect(within(rows[1]!).getByText('Site type "nodejs" is not supported by Hostashi yet.')).toBeInTheDocument();
    await user.selectOptions(screen.getByRole("combobox", { name: "Status" }), "skip");
    expect(table.getAllByRole("row")).toHaveLength(2);
    expect(table.queryByRole("link", { name: "shop" })).not.toBeInTheDocument();

    cleanup();
    render(<Harness initial="report=r1&site=shop" />);
    expect(await screen.findByRole("list", { name: "Reasons for shop" })).toHaveTextContent("2 nginx directives need a one-time review.");

    // A review needs the confirmation; PHP 8.2 is not installed here.
    const create = within(screen.getByRole("region", { name: "Create the site" }));
    await user.click(create.getByRole("button", { name: "Create shop" }));
    expect(create.getByRole("alert")).toHaveTextContent("Confirm that you checked");
    expect(calls.some((c) => c.path === `${base}/reports/r1/sites/shop`)).toBe(false);
    const select = await create.findByRole("combobox", { name: "PHP version for shop" });
    expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual(["8.2 (not installed)", "8.3", "8.4"]);
    await user.selectOptions(select, "8.4");
    await user.click(create.getByRole("checkbox", { name: /I checked the points above for shop/ }));
    // The proposed page cache is on unless the admin unticks it.
    const cache = create.getByRole("checkbox", { name: "Switch on the page cache (Other PHP application preset)" });
    expect(cache).toBeChecked();
    await user.click(cache);
    await user.click(create.getByRole("button", { name: "Create shop" }));
    expect(calls).toContainEqual({ method: "POST", path: `${base}/reports/r1/sites/shop`, body: { reviewed: true, php: "8.4", pageCache: false } });

    // Created: the plan now skips it because it is here; import it.
    const files = within(await screen.findByRole("region", { name: "Files and databases" }));
    expect(screen.queryByRole("region", { name: "Create the site" })).not.toBeInTheDocument();
    const area = files.getByRole("textbox", { name: /Databases to copy for shop/ });
    expect(await files.findByText(/This site has: shop\./)).toBeInTheDocument();
    await user.type(area, "shop_live");
    await user.click(files.getByRole("button", { name: "Import shop" }));
    expect(files.getByRole("alert")).toHaveTextContent("Line 1: write the old database");
    await user.clear(area);
    await user.type(area, "shop_live shop");
    await user.click(files.getByRole("button", { name: "Import shop" }));
    expect(calls).toContainEqual({
      method: "POST",
      path: `${base}/reports/r1/sites/shop/import`,
      body: { databases: [{ from: "shop_live", to: "shop" }] },
    });
    const status = within(await files.findByRole("region", { name: "Import of shop" }));
    expect(await status.findByText(/^Copying/)).toBeInTheDocument();
    await waitFor(() => expect(status.getByText(/^Done/)).toBeInTheDocument(), { timeout: 5000 });
    expect(status.getByText("120 files, 3.0 MiB")).toBeInTheDocument();
    const loads = within(status.getByRole("group", { name: "Database loads" }));
    expect(await loads.findByText("Failed")).toBeInTheDocument();
    expect(loads.getByText(/line 12: ERROR 1227/)).toBeInTheDocument();
    await user.click(loads.getByRole("button", { name: "Load the dumps again" }));
    expect(await loads.findByText("Loaded")).toBeInTheDocument();
    expect(loads.queryByRole("button", { name: "Load the dumps again" })).not.toBeInTheDocument();
    expect(calls).toContainEqual({ method: "POST", path: `${base}/imports/imp1/load`, body: undefined });
    expect(status.getByText("cloudpanel-import-db/shop-20261119T100100Z.sql")).toBeInTheDocument();
    expect(files.getByRole("button", { name: "Import shop again" })).toBeInTheDocument();

    // After the final sync the imported files become the web root.
    await user.click(status.getByRole("button", { name: "Use imported files as web root" }));
    await user.click(status.getByRole("button", { name: "Use imported files for shop" }));
    expect(screen.getByText("1 cron job was created paused. Start it after the DNS switch.")).toBeInTheDocument();
    expect(await status.findByText(/kept as htdocs-before-import-20261119T100500Z/)).toBeInTheDocument();
    expect(calls).toContainEqual({ method: "POST", path: `${base}/sites/shop/activate`, body: undefined });
  });

  it("uploads a report and opens its plan", async () => {
    const user = userEvent.setup();
    const report = { id: "r1", sites: 2, createdAt: "2026-11-19T09:00:00Z" };
    const calls = mockFetch({
      [`GET ${base}/source`]: { body: readySource },
      [`GET ${base}/reports`]: { body: { reports: [] } },
      [`POST ${base}/reports`]: { status: 201, body: report },
    });
    window.location.hash = "";
    render(<Harness />);
    const card = within(await screen.findByRole("region", { name: "Scan reports" }));
    await user.click(card.getByRole("button", { name: "Upload report" }));
    expect(card.getByRole("alert")).toHaveTextContent("Choose the report file.");

    const text = '{"format":"hostashi-cloudpanel-scan/1","sites":[]}';
    await user.upload(card.getByLabelText("Report file"), new File([text], "hostashi-scan.json", { type: "application/json" }));
    await user.click(card.getByRole("button", { name: "Upload report" }));
    await waitFor(() => expect(window.location.hash).toBe("#/import?report=r1"));
    expect(calls).toContainEqual({ method: "POST", path: `${base}/reports`, body: JSON.parse(text) });
  });

  it("shows the last import of a created site", async () => {
    const report = { id: "r1", sites: 1, createdAt: "2026-11-19T09:00:00Z" };
    const done: ImportJob = {
      id: "imp9",
      site: "shop",
      remoteUser: "shop",
      docRoot: "/home/shop/htdocs/shop.example",
      dir: "cloudpanel-import",
      state: "failed",
      created: "2026-11-19T10:00:00Z",
      finished: "2026-11-19T10:00:05Z",
      files: 0,
      bytes: 0,
      unchanged: 0,
      deleted: 0,
      skipped: 0,
      error: "the old server refused the key",
    };
    mockFetch({
      [`GET ${base}/source`]: { body: readySource },
      [`GET ${base}/reports`]: { body: { reports: [report] } },
      [`GET ${base}/reports/r1`]: { body: { ...report, plan: plan([{ ...planSite("shop", "skip"), reasons: exists }]) } },
      "GET /api/v1/php/versions": { body: { installed: ["8.3"], supported: ["8.3"], legacy: [] } },
      "GET /api/v1/databases": { body: { databases: [] } },
      [`GET ${base}/imports?site=shop`]: { body: { imports: [{ ...done, skippedPaths: undefined }] } },
      [`GET ${base}/sites/shop/cutover`]: noCutover,
      [`GET ${base}/imports/imp9`]: { body: done },
    });
    render(<Harness initial="report=r1&site=shop" />);
    const status = within(await screen.findByRole("region", { name: "Import of shop" }));
    expect(await status.findByText(/^Failed/)).toBeInTheDocument();
    expect(status.getByRole("alert")).toHaveTextContent("the old server refused the key");
    expect(await screen.findByRole("button", { name: "Import shop again" })).toBeInTheDocument();
    // A failed import cannot be made the web root.
    expect(status.queryByRole("button", { name: "Use imported files as web root" })).not.toBeInTheDocument();
  });

  it("creates an imported site on a chosen storage pool", async () => {
    const user = userEvent.setup();
    const report = { id: "r1", sites: 1, createdAt: "2026-11-19T09:00:00Z" };
    const GiB = 2 ** 30;
    const calls = mockFetch({
      [`GET ${base}/source`]: { body: readySource },
      [`GET ${base}/reports`]: { body: { reports: [report] } },
      [`GET ${base}/reports/r1`]: { body: { ...report, plan: plan([{ ...planSite("shop", "import"), php: "8.3", homeBytes: 20 * GiB }]) } },
      "GET /api/v1/php/versions": { body: { installed: ["8.3"], supported: ["8.3"], legacy: [] } },
      "GET /api/v1/pools": {
        body: {
          pools: [
            { name: "default", path: "/home", sites: 22, available: true, quota: "enforced", freeBytes: 14 * GiB, totalBytes: 38 * GiB },
            { name: "vol1", path: "/srv/vol1", sites: 0, available: true, quota: "enforced", freeBytes: 180 * GiB, totalBytes: 200 * GiB },
          ],
        },
      },
      [`POST ${base}/reports/r1/sites/shop`]: { status: 201, body: planSite("shop", "skip") },
      "GET /api/v1/databases": { body: { databases: [] } },
      [`GET ${base}/imports?site=shop`]: { body: { imports: [] } },
      [`GET ${base}/sites/shop/cutover`]: noCutover,
    });
    render(<Harness initial="report=r1&site=shop" />);
    const select = await screen.findByLabelText(/^Storage pool for shop/);
    expect(screen.getByText("The old home uses 20.0 GiB.")).toBeInTheDocument();
    // 14 GiB free on the default pool: too small for the old home.
    expect(screen.getByText(/Pool default has less free space/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create shop" })).toBeDisabled();
    await user.selectOptions(select, "vol1");
    await user.click(screen.getByRole("button", { name: "Create shop" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ pool: "vol1" }));
  });

  it("creates a Node.js site with another Node.js line", async () => {
    const user = userEvent.setup();
    const report = { id: "r1", sites: 1, createdAt: "2026-11-19T09:00:00Z" };
    const api: PlanSite = { ...planSite("api", "review", [{ code: "node_start", msg: "the app stays stopped" }]), type: "nodejs", php: "", node: "24", customSnippets: 0 };
    const calls = mockFetch({
      [`GET ${base}/source`]: { body: readySource },
      [`GET ${base}/reports`]: { body: { reports: [report] } },
      [`GET ${base}/reports/r1`]: { body: { ...report, plan: plan([api]) } },
      "GET /api/v1/php/versions": { body: { installed: ["8.3"], supported: ["8.3"], legacy: [] } },
      "GET /api/v1/pools": { body: { pools: [] } },
      "GET /api/v1/node": {
        body: {
          installed: [{ major: "22", version: "22.23.5" }, { major: "24", version: "24.22.3" }],
          last: "",
          state: "idle",
          log: [],
          releases: ["22", "24", "26"].map((major) => ({ major, version: major + ".0.0", codename: "", end: "2029-04-30" })),
        },
      },
      [`POST ${base}/reports/r1/sites/api`]: { status: 201, body: { ...api, action: "skip", reasons: exists } },
      "GET /api/v1/databases": { body: { databases: [] } },
      [`GET ${base}/imports?site=api`]: { body: { imports: [] } },
      [`GET ${base}/sites/api/cutover`]: noCutover,
    });
    render(<Harness initial="report=r1&site=api" />);
    expect(await screen.findByText("Node.js")).toBeInTheDocument();
    const select = await screen.findByLabelText(/^Node.js for api/);
    expect(within(select).getByRole("option", { name: "Node.js 26 (not installed)" })).toBeInTheDocument();
    await user.selectOptions(select, "22");
    await user.click(screen.getByLabelText(/I checked the points above/));
    await user.click(screen.getByRole("button", { name: "Create api" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ reviewed: true, node: "22" }));
  });

  it("shows a created reverse proxy site without a file import", async () => {
    const report = { id: "r1", sites: 1, createdAt: "2026-11-19T09:00:00Z" };
    const portal: PlanSite = { ...planSite("portal", "skip", exists), type: "proxy", php: "", proxy: "http://10.0.0.5:8080", customSnippets: 1 };
    mockFetch({
      [`GET ${base}/source`]: { body: readySource },
      [`GET ${base}/reports`]: { body: { reports: [report] } },
      [`GET ${base}/reports/r1`]: { body: { ...report, plan: plan([portal]) } },
      "GET /api/v1/php/versions": { body: { installed: ["8.3"], supported: ["8.3"], legacy: [] } },
      [`GET ${base}/sites/portal/cutover`]: noCutover,
    });
    render(<Harness initial="report=r1&site=portal" />);
    expect(await screen.findByText("Reverse proxy")).toBeInTheDocument();
    expect(screen.getByText("http://10.0.0.5:8080")).toBeInTheDocument();
    expect(screen.getByText(/no files or databases to copy/)).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Files and databases" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Custom nginx" })).not.toBeInTheDocument();
    expect(screen.queryByText("Old files")).not.toBeInTheDocument();
  });

  it("reviews the custom nginx of a created site and saves it as snippets", async () => {
    const user = userEvent.setup();
    const report = { id: "r1", sites: 1, createdAt: "2026-11-19T09:00:00Z" };
    const calls = mockFetch({
      [`GET ${base}/source`]: { body: readySource },
      [`GET ${base}/reports`]: { body: { reports: [report] } },
      [`GET ${base}/reports/r1`]: { body: { ...report, plan: plan([{ ...planSite("shop", "skip"), customSnippets: 3, reasons: exists }]) } },
      "GET /api/v1/php/versions": { body: { installed: ["8.3"], supported: ["8.3"], legacy: [] } },
      "GET /api/v1/databases": { body: { databases: [] } },
      [`GET ${base}/imports?site=shop`]: { body: { imports: [] } },
      [`GET ${base}/sites/shop/cutover`]: noCutover,
      [`GET ${base}/reports/r1/sites/shop/snippets-draft`]: {
        body: {
          server: "add_header X-Frame-Options SAMEORIGIN;\n",
          root: "",
          items: [
            { context: "server: add_header", source: "/etc/nginx/sites-enabled/shop.conf", line: 10, slot: "server", action: "kept" },
            { context: "server: pagespeed", source: "/etc/nginx/sites-enabled/shop.conf", line: 12, action: "dropped", reason: "the PageSpeed module is not shipped (ADR-0017)" },
          ],
        },
      },
      "PUT /api/v1/sites/shop/snippets": (b) => ({ body: b }),
    });
    render(<Harness initial="report=r1&site=shop" />);
    await user.click(await screen.findByRole("button", { name: "Review custom nginx" }));

    const items = within(await screen.findByRole("list", { name: "Directives of shop" }));
    expect(items.getByText(/Left out\. The PageSpeed module is not shipped/)).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: /Inside the server block/ })).toHaveValue("add_header X-Frame-Options SAMEORIGIN;\n");

    await user.click(screen.getByRole("button", { name: "Save as snippets of shop" }));
    expect(await screen.findByText("Snippets saved and active on shop.")).toBeInTheDocument();
    expect(calls.at(-1)).toEqual({ method: "PUT", path: "/api/v1/sites/shop/snippets", body: { server: "add_header X-Frame-Options SAMEORIGIN;\n", root: "" } });
  });

  it("explains a rejected report", async () => {
    const user = userEvent.setup();
    mockFetch({
      [`GET ${base}/source`]: { body: { source: null, hostKey: "", ready: false } },
      [`GET ${base}/reports`]: { body: { reports: [] } },
      [`POST ${base}/reports`]: { status: 422, body: { error: { code: "invalid_report", message: "unknown format" } } },
    });
    render(<Migrate />);
    const card = within(await screen.findByRole("region", { name: "Scan reports" }));
    await user.upload(card.getByLabelText("Report file"), new File(["{}"], "x.json", { type: "application/json" }));
    await user.click(card.getByRole("button", { name: "Upload report" }));
    expect(await card.findByRole("alert")).toHaveTextContent("This is not a valid scan report: unknown format.");
  });
});
