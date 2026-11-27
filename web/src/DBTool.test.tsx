// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Databases } from "./Databases";
import { durationLabel, safeToolURL, withLook } from "./dbtool";
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

  it("adds the colour scheme and the panel's port for Adminer's look", () => {
    const https = { protocol: "https:", port: "8443" };
    expect(withLook(ticketURL, "dark", https)).toBe(ticketURL + "&scheme=dark&panel=8443");
    expect(withLook(ticketURL, "light", { protocol: "https:", port: "" })).toBe(ticketURL + "&scheme=light&panel=443");
    // The system's scheme and anything else add no scheme.
    expect(withLook(ticketURL, undefined, https)).toBe(ticketURL + "&panel=8443");
    expect(withLook(ticketURL, "evil", https)).toBe(ticketURL + "&panel=8443");
    // A panel without https has no address to link back to.
    expect(withLook(ticketURL, "dark", { protocol: "http:", port: "5173" })).toBe(ticketURL + "&scheme=dark");
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

  it("warns when the external server has no TLS", async () => {
    const user = userEvent.setup();
    const local = sites["GET /api/v1/db-servers"].body.servers[0];
    const servers = [
      local,
      { ...local, name: "db1", local: false, default: false, tlsMode: "verify-ca", tlsActive: true },
      { ...local, name: "db2", local: false, default: false, tlsMode: "none" },
    ];
    mockFetch({
      ...sites,
      "GET /api/v1/db-servers": { body: { servers } },
      "GET /api/v1/databases": { body: { databases: [blogWp, { ...blogWp, name: "blog_tls", server: "db1" }, { ...blogWp, name: "blog_plain", server: "db2" }] } },
      ...available,
    });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Open blog_plain in Adminer" }));
    let dialog = screen.getByRole("dialog", { name: "Open blog_plain in Adminer" });
    expect(within(dialog).getByText("db2 is set up without TLS.")).toBeInTheDocument();
    expect(within(dialog).getByText(/cross the network unencrypted/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: "Open blog_tls in Adminer" }));
    dialog = screen.getByRole("dialog", { name: "Open blog_tls in Adminer" });
    expect(within(dialog).queryByText(/without TLS/)).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: "Open blog_wp in Adminer" }));
    dialog = screen.getByRole("dialog", { name: "Open blog_wp in Adminer" });
    expect(within(dialog).queryByText(/without TLS/)).not.toBeInTheDocument();
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
    document.documentElement.dataset.scheme = "dark";
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Open blog_wp in Adminer" }));
    const dialog = screen.getByRole("dialog", { name: "Open blog_wp in Adminer" });
    await user.click(within(dialog).getByRole("radio", { name: "2 hours" }));
    await user.click(within(dialog).getByRole("button", { name: "Open in Adminer" }));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ minutes: 120 });
    expect(open).toHaveBeenCalledWith("about:blank", "_blank");
    expect(tab.opener).toBeNull();
    expect(tab.location.replace).toHaveBeenCalledWith(ticketURL + "&scheme=dark");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    delete document.documentElement.dataset.scheme;
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

describe("Adminer sessions", () => {
  const soon = () => new Date(Date.now() + 3 * 60000).toISOString();
  const later = () => new Date(Date.now() + 25 * 60000).toISOString();
  const id = "hostashi_tool_0123456789abcdef";
  const path = "/api/v1/databases/blog_wp/tool/sessions";

  it("names nothing while no session is open", async () => {
    mockFetch({ ...sites, ...available, "GET /api/v1/databases": { body: { databases: [blogWp] } }, ["GET " + path]: { body: { sessions: [] } } });
    render(<Harness initial="db=blog_wp" />);
    expect(await screen.findByRole("button", { name: "Open in Adminer" })).toBeInTheDocument();
    expect(screen.queryByText("Adminer sessions")).not.toBeInTheDocument();
  });

  it("lists who opened a session, extends and ends it", async () => {
    const user = userEvent.setup();
    const first = { id, user: "kim", opened: new Date().toISOString(), expires: soon() };
    let sessions = [first];
    const calls = mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      ["GET " + path]: () => ({ body: { sessions } }),
      ["POST " + path + "/" + id + "/extend"]: () => {
        sessions = [{ ...first, expires: later() }];
        return { body: sessions[0] };
      },
      ["DELETE " + path + "/" + id]: () => {
        sessions = [];
        return { status: 204 };
      },
    });
    render(<Harness initial="db=blog_wp" />);
    const row = (await screen.findByText("kim")).closest("li") as HTMLElement;
    expect(within(row).getByText("Ends soon")).toBeInTheDocument();
    expect(within(row).getByText("ends in 3 min")).toBeInTheDocument();

    await user.click(within(row).getByRole("button", { name: "Extend by 30 minutes" }));
    expect(calls.some((c) => c.method === "POST" && c.path.endsWith("/extend"))).toBe(true);
    expect(await within(row).findByText("Open")).toBeInTheDocument();
    expect(within(row).getByText("ends in 25 min")).toBeInTheDocument();

    await user.click(within(row).getByRole("button", { name: "End now" }));
    expect(calls.some((c) => c.method === "DELETE" && c.path === path + "/" + id)).toBe(true);
    await vi.waitFor(() => expect(screen.queryByText("Adminer sessions")).not.toBeInTheDocument());
  });

  it("says why a session cannot be extended", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      ["GET " + path]: { body: { sessions: [{ id, user: "", opened: new Date().toISOString(), expires: later() }] } },
      ["POST " + path + "/" + id + "/extend"]: { status: 409, body: { error: { code: "dbtool_session_limit", message: "x" } } },
    });
    render(<Harness initial="db=blog_wp" />);
    expect(await screen.findByText("A removed account")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Extend by 30 minutes" }));
    expect(await screen.findByText(/at most two hours ahead/)).toBeInTheDocument();
  });
});

describe("Adminer's +30 min and Log out", () => {
  const id = "hostashi_tool_0123456789abcdef";
  const path = "/api/v1/databases/blog_wp/tool/sessions";
  const later = new Date(Date.now() + 55 * 60000).toISOString();

  it("extends the session with one click", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      ["GET " + path]: { body: { sessions: [] } },
      ["POST " + path + "/" + id + "/extend"]: { body: { id, user: "kim", opened: new Date().toISOString(), expires: later } },
    });
    render(<Harness initial={`db=blog_wp&tool=extend&session=${id}`} />);
    const dialog = await screen.findByRole("dialog", { name: "Extend the Adminer session on blog_wp" });
    await user.click(within(dialog).getByRole("button", { name: "Extend by 30 minutes" }));
    expect(await within(dialog).findByText("Extended by 30 minutes.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    await user.click(within(dialog).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("ends the session after Adminer's logout", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      ["GET " + path]: { body: { sessions: [] } },
      ["DELETE " + path + "/" + id]: { status: 204 },
    });
    render(<Harness initial={`db=blog_wp&tool=end&session=${id}`} />);
    const dialog = await screen.findByRole("dialog", { name: "End the Adminer session on blog_wp" });
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(within(dialog).getByRole("button", { name: "End session" }));
    expect(await within(dialog).findByText("Session ended.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE" && c.path === path + "/" + id)).toBe(true);
  });

  it("says when the session has ended already", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...sites,
      ...available,
      "GET /api/v1/databases": { body: { databases: [blogWp] } },
      ["GET " + path]: { body: { sessions: [] } },
      ["DELETE " + path + "/" + id]: { status: 404, body: { error: { code: "not_found", message: "the session has ended" } } },
    });
    render(<Harness initial={`db=blog_wp&tool=end&session=${id}`} />);
    await user.click(await screen.findByRole("button", { name: "End session" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("This session has ended already.");
  });

  it("ignores a malformed request", async () => {
    mockFetch({ ...sites, ...available, "GET /api/v1/databases": { body: { databases: [blogWp] } }, ["GET " + path]: { body: { sessions: [] } } });
    render(<Harness initial="db=blog_wp&tool=end&session=root" />);
    expect(await screen.findByRole("button", { name: "Open in Adminer" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
