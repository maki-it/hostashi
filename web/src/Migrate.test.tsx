// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
    expect(await hk.findByText(/^Trusted/)).toBeInTheDocument();
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

  it("uploads a report, creates reviewed sites and imports them", async () => {
    const user = userEvent.setup();
    let sites = [
      planSite("shop", "review", [{ code: "custom_snippets", msg: "2 nginx directives need a one-time review" }]),
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
      [`POST ${base}/reports/r1/sites/shop/import`]: { status: 202, body: job("queued") },
      [`GET ${base}/imports/imp1`]: () => ({ body: job(polls++ === 0 ? "running" : "done") }),
    });
    render(<Migrate />);

    const reportsCard = within(await screen.findByRole("region", { name: "Scan reports" }));
    await user.click(reportsCard.getByRole("button", { name: "Upload report" }));
    expect(reportsCard.getByRole("alert")).toHaveTextContent("Choose the report file.");

    const text = '{"format":"hostashi-cloudpanel-scan/1","sites":[]}';
    await user.upload(reportsCard.getByLabelText("Report file"), new File([text], "hostashi-scan.json", { type: "application/json" }));
    await user.click(reportsCard.getByRole("button", { name: "Upload report" }));

    const planCard = within(await screen.findByRole("region", { name: "Plan" }));
    expect(planCard.getByText(/2 sites: 0 ready, 1 to review, 1 skipped/)).toBeInTheDocument();
    expect(calls).toContainEqual({ method: "POST", path: `${base}/reports`, body: JSON.parse(text) });

    // The skipped site only explains why.
    expect(planCard.getByText('Site type "nodejs" is not supported by Hostashi yet.')).toBeInTheDocument();
    expect(planCard.queryByRole("button", { name: "Create old" })).not.toBeInTheDocument();

    // A review needs the confirmation; PHP 8.2 is not installed here.
    expect(planCard.getByText("2 nginx directives need a one-time review.")).toBeInTheDocument();
    await user.click(planCard.getByRole("button", { name: "Create shop" }));
    expect(planCard.getByRole("alert")).toHaveTextContent("Confirm that you checked");
    expect(calls.some((c) => c.path === `${base}/reports/r1/sites/shop`)).toBe(false);
    const select = await planCard.findByRole("combobox", { name: "PHP version for shop" });
    expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual(["8.2 (not installed)", "8.3", "8.4"]);
    await user.selectOptions(select, "8.4");
    await user.click(planCard.getByRole("checkbox", { name: /I checked the points above for shop/ }));
    await user.click(planCard.getByRole("button", { name: "Create shop" }));
    expect(calls).toContainEqual({ method: "POST", path: `${base}/reports/r1/sites/shop`, body: { reviewed: true, php: "8.4" } });

    // Created: the plan now skips it because it is here; import it.
    const area = await planCard.findByRole("textbox", { name: /Databases to copy for shop/ });
    expect(await planCard.findByText(/This site has: shop\./)).toBeInTheDocument();
    await user.type(area, "shop_live");
    await user.click(planCard.getByRole("button", { name: "Import shop" }));
    expect(planCard.getByRole("alert")).toHaveTextContent("Line 1: write the old database");
    await user.clear(area);
    await user.type(area, "shop_live shop");
    await user.click(planCard.getByRole("button", { name: "Import shop" }));
    expect(calls).toContainEqual({
      method: "POST",
      path: `${base}/reports/r1/sites/shop/import`,
      body: { databases: [{ from: "shop_live", to: "shop" }] },
    });
    const status = within(await planCard.findByRole("region", { name: "Import of shop" }));
    expect(await status.findByText(/^Copying/)).toBeInTheDocument();
    await waitFor(() => expect(status.getByText(/^Done/)).toBeInTheDocument(), { timeout: 5000 });
    expect(status.getByText("120 files, 3.0 MiB")).toBeInTheDocument();
    expect(status.getByText("cloudpanel-import-db/shop-20261119T100100Z.sql")).toBeInTheDocument();
    expect(planCard.getByRole("button", { name: "Import shop again" })).toBeInTheDocument();
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
