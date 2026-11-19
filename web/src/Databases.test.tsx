// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Databases } from "./Databases";
import { nameProblem, pathProblem } from "./databases";
import { mockFetch } from "./test-utils";

const sites = { "GET /api/v1/sites": { body: { sites: [{ name: "blog", state: "active" }, { name: "shop", state: "active" }] } } };
const blogWp = { name: "blog_wp", site: "blog", users: ["blog_wp"], createdAt: "2026-11-19T08:00:00Z" };
const pw = "Zx8!kQ2#mN5$pL7%";

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
        return { status: 201, body: { name: "shop_db", site: "shop", users: ["shop_db"], createdAt: "x", password: pw } };
      },
    });
    render(<Databases />);
    expect(await screen.findByText("No databases yet.")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Site"), "shop");
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
    expect(await screen.findByText("shop_db", { selector: "strong" })).toBeInTheDocument();
  });

  it("creates a database without a user and shows no password", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [] } },
      "POST /api/v1/sites/blog/databases": { status: 201, body: { name: "blog_x", site: "blog", users: [], createdAt: "x" } },
    });
    render(<Databases />);
    await user.type(await screen.findByLabelText(/Database name/), "blog_x");
    await user.click(screen.getByLabelText(/Create a user/));
    await user.click(screen.getByRole("button", { name: "Create database" }));
    await screen.findAllByText("No databases yet.");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "blog_x" });
    expect(screen.queryByRole("region", { name: "Save this password now" })).not.toBeInTheDocument();
  });

  it("checks the name before it asks the server", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ ...sites, "GET /api/v1/databases": { body: { databases: [] } } });
    render(<Databases />);
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
    render(<Databases />);
    await user.type(await screen.findByLabelText(/Database name/), "blog_wp");
    await user.click(screen.getByRole("button", { name: "Create database" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("exists. Choose another name.");
  });

  it("asks for a site first", async () => {
    mockFetch({ "GET /api/v1/sites": { body: { sites: [] } }, "GET /api/v1/databases": { body: { databases: [] } } });
    render(<Databases />);
    expect(await screen.findByText(/Create a site first/)).toBeInTheDocument();
  });

  it("sets a new password only after confirmation", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "PUT /api/v1/databases/blog_wp/users/blog_wp/password": { body: { user: "blog_wp", password: pw } },
    });
    render(<Databases />);
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
    render(<Databases />);
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
    render(<Databases />);
    await user.click(await screen.findByRole("button", { name: "Delete user" }));
    await user.click(screen.getByRole("button", { name: "Delete blog_wp" }));
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
    render(<Databases />);
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    const confirm = screen.getByRole("button", { name: "Delete blog_wp" });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/to delete the database/), "blog_w");
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/to delete the database/), "p");
    await user.click(confirm);
    expect(await screen.findByText("No databases yet.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  });

  it("says when a non-administrator opens the page", async () => {
    mockFetch({
      ...sites,
      "GET /api/v1/databases": { status: 403, body: { error: { code: "forbidden", message: "x" } } },
    });
    render(<Databases />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Only administrators");
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
    render(<Databases />);
    await user.click(await screen.findByRole("button", { name: "Import or export" }));
    const path = screen.getByLabelText(/File in the home of site blog/);
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
    render(<Databases />);
    await user.click(await screen.findByRole("button", { name: "Import or export" }));
    await user.click(screen.getByLabelText("Import a file"));
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
    render(<Databases />);
    await user.click(await screen.findByRole("button", { name: "Import or export" }));
    const path = screen.getByLabelText(/File in the home of site blog/);
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
    render(<Databases />);
    await user.click(await screen.findByRole("button", { name: "Import or export" }));
    await user.click(screen.getByRole("button", { name: "Start export" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Another import or export of this database is running.");
  });
});
