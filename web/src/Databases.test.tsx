// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useEffect, useState } from "react";
import { describe, expect, it } from "vitest";
import { Databases } from "./Databases";
import { nameProblem, pathProblem } from "./databases";
import { parseHash } from "./routes";
import { mockFetch } from "./test-utils";

const localServer = { name: "localhost", local: true, default: true, engine: "MariaDB", state: "ok", databases: 1, tlsMode: "socket", tlsActive: false };
const sites = {
  "GET /api/v1/sites": { body: { sites: [{ name: "blog", state: "active" }, { name: "shop", state: "active" }] } },
  "GET /api/v1/db-servers": { body: { servers: [localServer] } },
};
const blogWp = { name: "blog_wp", site: "blog", server: "localhost", users: ["blog_wp"], createdAt: "2026-11-19T08:00:00Z" };
const pw = "Zx8!kQ2#mN5$pL7%";

// Harness keeps the address query in state, as the app does.
function Harness({ initial = "", role = "admin" }: { initial?: string; role?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Databases role={role} query={query} onQuery={setQuery} />;
}

// HashHarness follows the address like the app, for flows that navigate.
function HashHarness({ initial }: { initial: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  useEffect(() => {
    const on = () => setQuery(parseHash(window.location.hash).query);
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return <Databases role="admin" query={query} onQuery={setQuery} />;
}

describe("nameProblem", () => {
  it.each([
    ["database", "blog_wp", ""],
    ["database", "", "Enter a name."],
    ["database", "Blog", "Use 1 to 64"],
    ["database", "1blog", "Use 1 to 64"],
    ["database", "blog-wp", "Use 1 to 64"],
    ["database", "a".repeat(65), "Use 1 to 64"],
    ["database", "mysql", "reserved"],
    ["database", "test", "reserved"],
    ["database", "hostashi_x", "reserved"],
    ["database", "mariadb_x", ""],
    ["user", "a".repeat(32), ""],
    ["user", "a".repeat(33), "Use 1 to 32"],
    ["user", "root", "reserved"],
    ["user", "mariadb_x", "reserved"],
    ["user", "debian_sys_maint", "reserved"],
  ] as const)("%s %s", (kind, v, want) => {
    const got = nameProblem(kind, v);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });
});

describe("Databases", () => {
  it("creates a database with a user and shows the password once", async () => {
    const user = userEvent.setup();
    let created = false;
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": () => ({ body: { databases: created ? [{ ...blogWp, site: "shop", name: "shop_db", users: ["shop_db"] }] : [] } }),
      "POST /api/v1/sites/shop/databases": () => {
        created = true;
        return { status: 201, body: { name: "shop_db", site: "shop", server: "localhost", users: ["shop_db"], createdAt: "x", password: pw } };
      },
    });
    render(<Harness initial="new=1" />);
    await user.selectOptions(await screen.findByLabelText("Site"), "shop");
    await user.type(screen.getByLabelText(/Database name/), "shop_db");
    expect(screen.getByLabelText("User name")).toHaveValue("shop_db");
    await user.click(screen.getByRole("button", { name: "Create database" }));

    const notice = await screen.findByRole("region", { name: "Save this password now" });
    expect(within(notice).getByLabelText("Password")).toHaveValue(pw);
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "shop_db", user: "shop_db" });

    await user.click(within(notice).getByRole("button", { name: "Copy password" }));
    expect(await navigator.clipboard.readText()).toBe(pw);
    expect(within(notice).getByRole("status")).toHaveTextContent("Copied.");

    await user.click(within(notice).getByRole("button", { name: "I have saved it" }));
    expect(screen.queryByRole("region", { name: "Save this password now" })).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue(pw)).not.toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "shop_db" })).toHaveAttribute("href", "#/databases?db=shop_db");
  });

  it("creates a database without a user and shows no password", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [] } },
      "POST /api/v1/sites/blog/databases": { status: 201, body: { name: "blog_x", site: "blog", users: [], createdAt: "x" } },
    });
    render(<Harness initial="new=1" />);
    await user.type(await screen.findByLabelText(/Database name/), "blog_x");
    await user.click(screen.getByLabelText(/Create a user/));
    await user.click(screen.getByRole("button", { name: "Create database" }));
    await screen.findByRole("table", { name: "Databases" });
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "blog_x" });
    expect(screen.queryByRole("region", { name: "Save this password now" })).not.toBeInTheDocument();
  });

  it("checks the name before it asks the server", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ ...sites, "GET /api/v1/databases": { body: { databases: [] } } });
    render(<Harness initial="new=1" />);
    await user.type(await screen.findByLabelText(/Database name/), "Shop");
    await user.click(screen.getByRole("button", { name: "Create database" }));
    expect(screen.getByRole("alert")).toHaveTextContent("lowercase letters");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
  });

  it("explains a name that is taken", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [] } },
      "POST /api/v1/sites/blog/databases": { status: 409, body: { error: { code: "database_exists", message: "x" } } },
    });
    render(<Harness initial="new=1" />);
    await user.type(await screen.findByLabelText(/Database name/), "blog_wp");
    await user.click(screen.getByRole("button", { name: "Create database" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("exists. Choose another name.");
  });

  it("asks for a site first", async () => {
    mockFetch({ "GET /api/v1/sites": { body: { sites: [] } }, "GET /api/v1/db-servers": sites["GET /api/v1/db-servers"], "GET /api/v1/databases": { body: { databases: [] } } });
    render(<Harness />);
    expect(await screen.findByText(/Create a site first/)).toBeInTheDocument();
  });

  it("sets a new password only after confirmation", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "PUT /api/v1/databases/blog_wp/users/blog_wp/password": { body: { user: "blog_wp", password: pw } },
    });
    render(<Harness initial="db=blog_wp" />);
    await user.click(await screen.findByRole("button", { name: "New password" }));
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Set new password" }));
    const notice = await screen.findByRole("region", { name: "Save this password now" });
    expect(within(notice).getByLabelText("Password")).toHaveValue(pw);
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({});
  });

  it("adds a user and shows its password", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "POST /api/v1/databases/blog_wp/users": { status: 201, body: { user: "blog_ro", database: "blog_wp", password: pw } },
    });
    render(<Harness initial="db=blog_wp" />);
    await user.click(await screen.findByRole("button", { name: "Add user" }));
    await user.type(screen.getByLabelText("New user for blog_wp"), "blog_ro");
    await user.click(screen.getByRole("button", { name: "Add user" }));
    const notice = await screen.findByRole("region", { name: "Save this password now" });
    expect(within(notice).getByText("blog_ro")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ user: "blog_ro" });
  });

  it("deletes a user after confirmation", async () => {
    const user = userEvent.setup();
    let gone = false;
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": () => ({ body: { databases: [gone ? { ...blogWp, users: [] } : blogWp] } }),
      "DELETE /api/v1/databases/blog_wp/users/blog_wp": () => {
        gone = true;
        return { status: 204 };
      },
    });
    render(<Harness initial="db=blog_wp" />);
    await user.click(await screen.findByRole("button", { name: "Delete user" }));
    await user.click(screen.getByRole("button", { name: "Delete user blog_wp" }));
    expect(await screen.findByText("No users.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("deletes a database only when its name is typed", async () => {
    const user = userEvent.setup();
    let gone = false;
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": () => ({ body: { databases: gone ? [] : [blogWp] } }),
      "DELETE /api/v1/databases/blog_wp": () => {
        gone = true;
        return { status: 204 };
      },
    });
    render(<Harness initial="db=blog_wp" />);
    const confirm = await screen.findByRole("button", { name: "Delete blog_wp" });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/to confirm/), "blog_w");
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/to confirm/), "p");
    await user.click(confirm);
    expect(await screen.findByText("No databases yet.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  });

  it("says when a non-administrator opens the page", async () => {
    mockFetch({
      ...sites,
      "GET /api/v1/databases": { status: 403, body: { error: { code: "forbidden", message: "x" } } },
    });
    render(<Harness />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Only administrators");
  });
});

describe("Database list", () => {
  const shopDb = { name: "shop_db", site: "shop", users: [], createdAt: "2026-11-01T08:00:00Z" };
  const rowNames = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((r) => within(r).getAllByRole("cell")[0]!.textContent);

  it("lists databases with site, users and a link to each", async () => {
    mockFetch({ ...sites, "GET /api/v1/databases": { body: { databases: [blogWp, shopDb] } } });
    render(<Harness />);
    const table = await screen.findByRole("table", { name: "Databases" });
    const row = within(within(table).getByRole("link", { name: "blog_wp" }).closest("tr")!);
    expect(row.getByRole("link", { name: "blog" })).toHaveAttribute("href", "#/sites/blog");
    expect(row.getByText("blog_wp", { selector: ".ui-mono:not(a)" })).toBeInTheDocument();
    expect(row.getByRole("link", { name: "Manage blog_wp" })).toHaveAttribute("href", "#/databases?db=blog_wp");
    expect(row.getByText((_, el) => el?.tagName === "TIME")).toHaveAttribute("datetime", blogWp.createdAt);
    expect(within(within(table).getByRole("link", { name: "shop_db" }).closest("tr")!).getByText("No users")).toBeInTheDocument();
  });

  it("filters by text and site and sorts, keeping both in the address", async () => {
    const user = userEvent.setup();
    mockFetch({ ...sites, "GET /api/v1/databases": { body: { databases: [shopDb, blogWp] } } });
    render(<Harness initial="sort=name&dir=asc" />);
    await screen.findByRole("table", { name: "Databases" });
    expect(rowNames()).toEqual(["blog_wp", "shop_db"]);
    await user.click(screen.getByRole("button", { name: "Database" }));
    expect(rowNames()).toEqual(["shop_db", "blog_wp"]);
    await user.selectOptions(screen.getByRole("combobox", { name: "Site" }), "blog");
    expect(rowNames()).toEqual(["blog_wp"]);
    await user.selectOptions(screen.getByRole("combobox", { name: "Site" }), "");
    await user.type(screen.getByRole("searchbox", { name: "Filter databases" }), "nothing");
    expect(screen.getByText("No database matches the filter.")).toBeInTheDocument();
  });

  it("says when a database in the address does not exist", async () => {
    mockFetch({ ...sites, "GET /api/v1/databases": { body: { databases: [blogWp] } } });
    render(<Harness initial="db=gone" />);
    expect(await screen.findByText("There is no database named gone.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "All databases" })).toHaveAttribute("href", "#/databases");
  });
});

describe("pathProblem", () => {
  it.each([
    ["shop.sql", ""],
    ["backups/shop-2026.sql.gz", ""],
    ["", "1 to 255"],
    ["shop.zip", "end in .sql"],
    ["../shop.sql", "inside the site's home"],
    ["/etc/shop.sql", "inside the site's home"],
    [".hidden.sql", "inside the site's home"],
    ["-x.sql", "inside the site's home"],
    ["a//b.sql", "inside the site's home"],
    ["a".repeat(253) + ".sql", "1 to 255"],
  ])("%s", (p, want) => {
    const got = pathProblem(p);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });
});

describe("Database dumps", () => {
  const job = { id: "0123456789abcdef", database: "blog_wp", started: "x" };

  it("exports into a new file and waits for the job", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "POST /api/v1/databases/blog_wp/export": { status: 202, body: { ...job, kind: "export", path: "out.sql.gz", state: "running" } },
      "GET /api/v1/databases/blog_wp/jobs/0123456789abcdef": { body: { ...job, kind: "export", path: "out.sql.gz", state: "done" } },
    });
    render(<Harness initial="db=blog_wp" />);
    const path = await screen.findByLabelText(/File in the home of site blog/);
    expect((path as HTMLInputElement).value).toMatch(/^blog_wp-\d{4}-\d{2}-\d{2}\.sql\.gz$/);
    await user.clear(path);
    await user.type(path, "out.sql.gz");
    await user.click(screen.getByRole("button", { name: "Start export" }));
    expect(await screen.findByText(/Exported blog_wp into/)).toHaveTextContent("out.sql.gz");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ path: "out.sql.gz" });
  });

  it("imports a file and shows a failure", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "POST /api/v1/databases/blog_wp/import": { status: 202, body: { ...job, kind: "import", path: "in.sql", state: "running" } },
      "GET /api/v1/databases/blog_wp/jobs/0123456789abcdef": { body: { ...job, kind: "import", path: "in.sql", state: "failed", error: "SQL error at line 12 (1064)" } },
    });
    render(<Harness initial="db=blog_wp" />);
    await user.click(await screen.findByLabelText("Import a file"));
    expect(screen.getByText(/Import into an empty database/)).toBeInTheDocument();
    const path = screen.getByLabelText(/File in the home of site blog/);
    await user.clear(path);
    await user.type(path, "in.sql");
    await user.click(screen.getByRole("button", { name: "Start import" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Import failed: SQL error at line 12 (1064)");
  });

  it("refuses a path outside the home before asking the server", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ ...sites, "GET /api/v1/databases": { body: { databases: [blogWp] } } });
    render(<Harness initial="db=blog_wp" />);
    const path = await screen.findByLabelText(/File in the home of site blog/);
    await user.clear(path);
    await user.type(path, "../../etc/x.sql");
    await user.click(screen.getByRole("button", { name: "Start export" }));
    expect(screen.getByRole("alert")).toHaveTextContent("inside the site's home");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
  });

  it("explains a running job", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "POST /api/v1/databases/blog_wp/export": {
        status: 409,
        body: { error: { code: "agent_conflict", message: "another import or export of this database is running" } },
      },
    });
    render(<Harness initial="db=blog_wp" />);
    await user.click(await screen.findByRole("button", { name: "Start export" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Another import or export of this database is running.");
  });
});

const db1 = {
  name: "db1",
  local: false,
  default: false,
  engine: "MySQL",
  version: "8.4.7",
  state: "ok",
  databases: 0,
  tlsMode: "verify-ca",
  tlsActive: true,
  checkedAt: "2026-11-21T08:00:00Z",
  certExpires: "2027-02-01T00:00:00Z",
  host: "10.0.0.5",
  port: 3307,
  adminUser: "hostashi_admin",
};

describe("Database servers", () => {
  it("creates a database on an external server and shows its host", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/db-servers": { body: { servers: [localServer, { ...db1, default: true }].map((s, i) => (i === 0 ? { ...s, default: false } : s)) } },
      "GET /api/v1/databases": { body: { databases: [] } },
      "POST /api/v1/sites/blog/databases": {
        status: 201,
        body: { name: "blog_db", site: "blog", server: "db1", users: ["blog_db"], createdAt: "x", password: pw },
      },
    });
    render(<Harness initial="new=1" role="user" />);
    expect(await screen.findByLabelText("Server")).toHaveValue("db1");
    await user.type(screen.getByLabelText(/Database name/), "blog_db");
    await user.click(screen.getByRole("button", { name: "Create database" }));

    const notice = await screen.findByRole("region", { name: "Save this password now" });
    expect(within(notice).getByText("10.0.0.5:3307")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "blog_db", user: "blog_db", server: "db1" });
  });

  it("shows the server column and offers import and export for external databases", async () => {
    mockFetch({
      ...sites,
      "GET /api/v1/db-servers": { body: { servers: [localServer, db1] } },
      "GET /api/v1/databases": { body: { databases: [blogWp, { ...blogWp, name: "blog_ext", server: "db1" }] } },
    });
    const { unmount } = render(<Harness role="user" />);
    expect(await screen.findByRole("columnheader", { name: /Server/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Servers" })).not.toBeInTheDocument();
    unmount();

    render(<Harness initial="db=blog_ext" role="user" />);
    expect(await screen.findByText(/temporary database user that the panel removes after the job/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start export" })).toBeInTheDocument();
  });

  it("lists the servers for admins and checks, sets the default and removes one", async () => {
    const user = userEvent.setup();
    let def = "localhost";
    let gone = false;
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "GET /api/v1/db-servers": () => ({
        body: { servers: [{ ...localServer, default: def === "localhost" }, ...(gone ? [] : [{ ...db1, default: def === "db1" }])] },
      }),
      "POST /api/v1/db-servers/db1/check": { body: db1 },
      "PUT /api/v1/db-servers/db1/default": () => {
        def = "db1";
        return { status: 204 };
      },
      "DELETE /api/v1/db-servers/db1": () => {
        gone = true;
        return { status: 204 };
      },
    });
    render(<Harness initial="view=servers" />);
    const table = await screen.findByRole("table", { name: "Database servers" });
    expect(within(table).getByText("10.0.0.5:3307")).toBeInTheDocument();
    expect(within(table).getByText("MySQL 8.4.7")).toBeInTheDocument();
    expect(within(table).getByText("TLS, own CA")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Check db1" }));
    expect(await screen.findByText("db1 is reachable (MySQL 8.4.7).")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Make db1 the default" }));
    expect(await screen.findByText("New databases now go to db1.")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Make localhost the default" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Remove db1" }));
    await user.click(screen.getByRole("button", { name: "Remove server" }));
    expect(await screen.findByText(/Removed db1/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method !== "GET").map((c) => c.method + " " + c.path)).toEqual([
      "POST /api/v1/db-servers/db1/check",
      "PUT /api/v1/db-servers/db1/default",
      "DELETE /api/v1/db-servers/db1",
    ]);
  });

  it("adds a server with its own CA and shows why a check failed", async () => {
    const user = userEvent.setup();
    let fail = true;
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [] } },
      "POST /api/v1/db-servers": () => {
        if (fail) {
          fail = false;
          return {
            status: 422,
            body: { error: { code: "db_server_check_failed", message: "the administrator lacks a privilege or the password is wrong: access denied (1045)" } },
          };
        }
        return { status: 201, body: db1 };
      },
    });
    render(<Harness initial="view=servers&new=1" />);
    await user.type(await screen.findByLabelText("Name"), "db1");
    await user.type(screen.getByLabelText(/^Host/), "10.0.0.5");
    await user.clear(screen.getByLabelText("Port"));
    await user.type(screen.getByLabelText("Port"), "3307");
    await user.type(screen.getByLabelText(/^Administrator$/), "hostashi_admin");
    await user.type(screen.getByLabelText("Administrator password"), "secret");
    await user.click(screen.getByLabelText(/my own CA/));
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(await screen.findByText("Paste the CA certificate in PEM format.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    await user.type(screen.getByLabelText("CA certificate"), "-----BEGIN CERTIFICATE-----x");
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(await screen.findByText(/The check failed: the administrator lacks a privilege/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      engine: "mariadb",
      name: "db1",
      host: "10.0.0.5",
      port: 3307,
      adminUser: "hostashi_admin",
      adminPassword: "secret",
      tlsMode: "verify-ca",
      caCertificate: "-----BEGIN CERTIFICATE-----x",
    });
  });

  it("adds a PostgreSQL server with its port and shows the warning", async () => {
    const user = userEvent.setup();
    const pg1 = { ...db1, name: "pg1", engine: "PostgreSQL", version: "18.1", port: 5432, default: false };
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/db-servers": { body: { servers: [localServer, pg1] } },
      "GET /api/v1/databases": { body: { databases: [] } },
      "POST /api/v1/db-servers": { status: 201, body: { ...pg1, warnings: ["the administrator is a PostgreSQL superuser; use a dedicated role"] } },
    });
    render(<HashHarness initial="view=servers&new=1" />);
    await user.click(await screen.findByLabelText("PostgreSQL"));
    expect(screen.getByLabelText("Port")).toHaveValue(5432);
    expect(screen.getByText(/pg_hba.conf/)).toBeInTheDocument();
    expect(screen.getByText(/CREATEDB and CREATEROLE/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Name"), "pg1");
    await user.type(screen.getByLabelText(/^Host/), "10.0.0.6");
    await user.type(screen.getByLabelText(/^Administrator$/), "postgres");
    await user.type(screen.getByLabelText("Administrator password"), "secret");
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(await screen.findByText("Added pg1 (PostgreSQL 18.1).")).toBeInTheDocument();
    expect(screen.getByText("The administrator is a PostgreSQL superuser; use a dedicated role.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ engine: "postgresql", port: 5432, adminUser: "postgres" });
  });

  it("moves an untouched port with the engine", async () => {
    const user = userEvent.setup();
    mockFetch({ ...sites, "GET /api/v1/db-servers": { body: { servers: [localServer] } }, "GET /api/v1/databases": { body: { databases: [] } } });
    render(<Harness initial="view=servers&new=1" />);
    await user.click(await screen.findByLabelText("PostgreSQL"));
    await user.click(screen.getByLabelText("MariaDB or MySQL"));
    expect(screen.getByLabelText("Port")).toHaveValue(3306);
    await user.clear(screen.getByLabelText("Port"));
    await user.type(screen.getByLabelText("Port"), "6432");
    await user.click(screen.getByLabelText("PostgreSQL"));
    expect(screen.getByLabelText("Port")).toHaveValue(6432);
  });

  it("names PostgreSQL databases and offers Adminer for them too", async () => {
    const pg1 = { ...db1, name: "pg1", engine: "PostgreSQL", version: "18.1", host: "pg.example.net", port: 5432, tlsMode: "verify" };
    mockFetch({
      ...sites,
      "GET /api/v1/dbtool": { body: { available: true, minutes: [30, 120] } },
      "GET /api/v1/db-servers": { body: { servers: [localServer, pg1] } },
      "GET /api/v1/databases": {
        body: {
          databases: [
            { name: "shop_pg", site: "shop", server: "pg1", users: [], createdAt: "2026-11-21T10:00:00Z" },
            { name: "shop_wp", site: "shop", server: "localhost", users: [], createdAt: "2026-11-21T10:00:00Z" },
          ],
        },
      },
    });
    render(<Harness initial="" />);
    const table = await screen.findByRole("table", { name: "Databases" });
    expect(within(table).getByText("PostgreSQL")).toBeInTheDocument();
    expect(await within(table).findByRole("button", { name: "Open shop_wp in Adminer" })).toBeInTheDocument();
    expect(within(table).getByRole("button", { name: "Open shop_pg in Adminer" })).toBeInTheDocument();
  });

  it("keeps users out of the server list", async () => {
    mockFetch({ ...sites, "GET /api/v1/databases": { body: { databases: [] } } });
    render(<Harness initial="view=servers" role="user" />);
    expect(await screen.findByRole("heading", { name: "Databases" })).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "Database servers" })).not.toBeInTheDocument();
  });
});
