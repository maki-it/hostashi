// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Databases } from "./Databases";
import { durationLabel, safeToolURL } from "./dbtool";
import { mockFetch } from "./test-utils";

const sites = {
  "GET /api/v1/sites": { body: { sites: [{ name: "blog", state: "active" }] } },
  "GET /api/v1/db-servers": {
    body: { servers: [{ name: "localhost", local: true, default: true, state: "ok", databases: 1, tlsMode: "socket", tlsActive: false }] },
  },
};
const blogWp = { name: "blog_wp", site: "blog", server: "localhost", users: ["blog_wp"], createdAt: "2026-11-19T08:00:00Z" };
const ticketURL = "https://panel.example.com:8444/?ticket=0123456789abcdef0123456789abcdef";
const available = { "GET /api/v1/dbtool": { body: { available: true, minutes: [30, 120] } } };

function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Databases query={query} onQuery={setQuery} />;
}

// fakeTab stands in for the tab window.open returns.
function fakeTab() {
  const tab = { opener: {} as unknown, location: { replace: vi.fn() }, close: vi.fn() };
  const open = vi.fn(() => tab as unknown as Window);
  vi.stubGlobal("open", open);
  return { tab, open };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("database tool helpers", () => {
  it("names the durations", () => {
    expect(durationLabel(30)).toBe("30 minutes");
    expect(durationLabel(120)).toBe("2 hours");
    expect(durationLabel(60)).toBe("1 hour");
  });

  it("accepts only the panel's https address with a ticket", () => {
    expect(safeToolURL(ticketURL)).toBe(ticketURL);
    expect(safeToolURL("javascript:alert(1)")).toBeNull();
    expect(safeToolURL("http://panel.example.com:8444/?ticket=0123456789abcdef0123456789abcdef")).toBeNull();
    expect(safeToolURL("https://panel.example.com:8444/?ticket=x")).toBeNull();
    expect(safeToolURL("not a url")).toBeNull();
  });
});

describe("Open in Adminer", () => {
  it("has no button when the tool is not available", async () => {
    mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "GET /api/v1/dbtool": { body: { available: false, minutes: [30, 120] } },
    });
    render(<Harness />);
    expect(await screen.findByRole("link", { name: "blog_wp" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Open blog_wp in Adminer" })).not.toBeInTheDocument();
  });

  it("opens a database on an external server too, with a hint", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...sites,
      "GET /api/v1/databases": { body: { databases: [blogWp, { ...blogWp, name: "blog_ext", server: "db1" }] } },
      ...available,
    });
    render(<Harness />);
    expect(await screen.findByRole("button", { name: "Open blog_wp in Adminer" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open blog_ext in Adminer" }));
    const dialog = screen.getByRole("dialog", { name: "Open blog_ext in Adminer" });
    expect(within(dialog).getByText(/connects from this machine to db1/)).toBeInTheDocument();
  });

  it("opens a database for the chosen time in a new tab", async () => {
    const user = userEvent.setup();
    const { tab, open } = fakeTab();
    const calls = mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "POST /api/v1/databases/blog_wp/tool": { status: 201, body: { url: ticketURL, expires: "2026-11-21T03:00:00Z" } },
    });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Open blog_wp in Adminer" }));
    const dialog = screen.getByRole("dialog", { name: "Open blog_wp in Adminer" });
    await user.click(within(dialog).getByRole("radio", { name: "2 hours" }));
    await user.click(within(dialog).getByRole("button", { name: "Open in Adminer" }));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ minutes: 120 });
    expect(open).toHaveBeenCalledWith("about:blank", "_blank");
    expect(tab.opener).toBeNull();
    expect(tab.location.replace).toHaveBeenCalledWith(ticketURL);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows the link when the browser blocks the tab", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("open", vi.fn(() => null));
    mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "POST /api/v1/databases/blog_wp/tool": { status: 201, body: { url: ticketURL, expires: "2026-11-21T03:00:00Z" } },
    });
    render(<Harness initial="db=blog_wp" />);
    await user.click(await screen.findByRole("button", { name: "Open in Adminer" }));
    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Open in Adminer" }));
    const link = await within(dialog).findByRole("link", { name: "Open Adminer" });
    expect(link).toHaveAttribute("href", ticketURL);
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("closes the tab and says why when opening fails", async () => {
    const user = userEvent.setup();
    const { tab } = fakeTab();
    mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "POST /api/v1/databases/blog_wp/tool": { status: 409, body: { error: { code: "dbtool_unavailable", message: "x" } } },
    });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Open blog_wp in Adminer" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Open in Adminer" }));
    expect(await screen.findByText(/not available/)).toBeInTheDocument();
    expect(tab.close).toHaveBeenCalled();
    expect(tab.location.replace).not.toHaveBeenCalled();
  });

  it("does not follow an address that is not the tool's", async () => {
    const user = userEvent.setup();
    const { tab } = fakeTab();
    mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      "POST /api/v1/databases/blog_wp/tool": { status: 201, body: { url: "javascript:alert(1)", expires: "x" } },
    });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Open blog_wp in Adminer" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Open in Adminer" }));
    expect(await screen.findByText(/Something went wrong/)).toBeInTheDocument();
    expect(tab.location.replace).not.toHaveBeenCalled();
  });
});
