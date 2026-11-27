// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { LogRetentionCard, SiteLogs } from "./Logs";
import type { Site } from "./sites";
import { setLanguage } from "./i18n";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const access = [
  '203.0.113.7 - - [21/Nov/2026:16:10:00 +0100] "GET / HTTP/2.0" 200 512 "-" "x"',
  '203.0.113.7 - - [21/Nov/2026:16:10:01 +0100] "GET /wp-login.php HTTP/2.0" 404 99 "-" "x"',
  '203.0.113.7 - - [21/Nov/2026:16:10:02 +0100] "GET /cart HTTP/2.0" 500 12 "-" "x"',
];
const chunk = (lines: string[]) => ({ body: { lines, cursor: "7.100", rotated: false, more: false } });
const retention = { body: { days: 7, minDays: 1, maxDays: 365, defaultDays: 7 } };

describe("SiteLogs", () => {
  it("shows, colours and filters the last lines", async () => {
    const user = userEvent.setup();
    mockFetch({ "GET /api/v1/sites/blog/logs/access?lines=200": chunk(access), "GET /api/v1/settings/logs": retention });
    render(<SiteLogs site={site} admin={true} />);
    const log = await screen.findByRole("log");
    await within(log).findByText(/GET \/cart/);
    expect(within(log).getByText(/GET \/cart/)).toHaveClass("err");
    expect(within(log).getByText(/wp-login/)).toHaveClass("warn");
    expect(screen.getByRole("status")).toHaveTextContent("Live");
    expect(screen.getByText("3 of 3 lines shown.", { exact: false })).toBeInTheDocument();

    await user.type(screen.getByLabelText("Filter"), "WP-LOGIN 404");
    expect(within(log).getAllByText(/HTTP/)).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(screen.getByRole("status")).toHaveTextContent("Paused");
    expect(screen.getByRole("button", { name: "Follow" })).toBeInTheDocument();
  });

  it("switches the log and shows an empty one", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/blog/logs/access?lines=200": chunk(access),
      "GET /api/v1/sites/blog/logs/php?lines=200": chunk([]),
    });
    render(<SiteLogs site={site} admin={false} />);
    await screen.findByText(/GET \/cart/);
    await user.click(screen.getByRole("radio", { name: "PHP errors" }));
    expect(await screen.findByText("The log is empty so far.")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "PHP errors" })).toHaveAttribute("aria-checked", "true");
    // Users see the retention as a hint and never ask for the setting.
    expect(screen.getByText(/deletes old logs after the days an administrator set/)).toBeInTheDocument();
    expect(calls.some((c) => c.path === "/api/v1/settings/logs")).toBe(false);
  });

  it("shows the WordPress install log of a PHP site, empty without an install", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/sites/blog/logs/access?lines=200": chunk(access),
      "GET /api/v1/sites/blog/logs/wordpress?lines=200": chunk(["Success: WordPress downloaded.", "Error: The site you have requested is not installed."]),
    });
    render(<SiteLogs site={site} admin={false} />);
    await screen.findByText(/GET \/cart/);
    await user.click(screen.getByRole("radio", { name: "WordPress install" }));
    const log = screen.getByRole("log");
    expect(await within(log).findByText(/WordPress downloaded/)).toBeInTheDocument();
    expect(within(log).getByText(/not installed/)).toHaveClass("err");
  });

  it("shows no WordPress log for a site without PHP", async () => {
    mockFetch({ "GET /api/v1/sites/blog/logs/access?lines=200": chunk([]) });
    render(<SiteLogs site={{ ...site, php: "" }} admin={false} />);
    expect(await screen.findByText("The log is empty so far.")).toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: "WordPress install" })).not.toBeInTheDocument();
  });

  it("shows an error from the server", async () => {
    mockFetch({ "GET /api/v1/sites/blog/logs/access?lines=200": { status: 404, body: { error: "not_found", message: "no such log" } } });
    render(<SiteLogs site={site} admin={false} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("There is no such site or log.");
    expect(screen.queryByText("The log is empty so far.")).not.toBeInTheDocument();
  });

  it("lets admins change the retention", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/settings/logs": retention,
      "PUT /api/v1/settings/logs": { body: { days: 14, minDays: 1, maxDays: 365, defaultDays: 7 } },
    });
    render(<LogRetentionCard />);
    const days = await screen.findByLabelText("Keep logs for (days)");
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    await user.clear(days);
    await user.type(days, "0");
    expect(await screen.findByText("Enter 1 to 365 days.")).toBeInTheDocument();
    expect(save).toBeDisabled();
    await user.clear(days);
    await user.type(days, "14");
    await user.click(save);
    expect(await screen.findByText("Logs are kept for 14 days from the next rotation on.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ days: 14 });
  });
});

describe("SiteLogs in German", () => {
  it("names the logs and counts lines in German", async () => {
    setLanguage("de");
    mockFetch({ "GET /api/v1/sites/blog/logs/access?lines=200": chunk(access), "GET /api/v1/settings/logs": retention });
    render(<SiteLogs site={site} admin={false} />);
    expect(await screen.findByRole("log", { name: "Log „Zugriffe“ von blog" })).toBeInTheDocument();
    expect(await screen.findByText("3 von 3 Zeilen gezeigt.", { exact: false })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Webserver-Fehler" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pausieren" })).toBeInTheDocument();
  });
});
