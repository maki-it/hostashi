// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { mockFetch } from "./test-utils";

const noSetup = { "GET /api/v1/setup": { body: { required: false } } };
const loggedOut = { "GET /api/v1/auth/me": { status: 401, body: { error: { code: "unauthenticated", message: "not logged in" } } } };
const admin = { username: "kim", role: "admin" };

function err(status: number, code: string, message = code) {
  return { status, body: { error: { code, message } } };
}

async function fillLogin(user: ReturnType<typeof userEvent.setup>, username = "kim", password = "correct horse") {
  await user.type(await screen.findByLabelText("Username"), username);
  await user.type(screen.getByLabelText("Password"), password);
  await user.click(screen.getByRole("button", { name: "Log in" }));
}

describe("App start", () => {
  it("shows home when a session exists", async () => {
    mockFetch({ ...noSetup, "GET /api/v1/auth/me": { body: { ...admin, auth: "session" } } });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
  });

  it("measures the sticky top bar for the parts that stick below it", async () => {
    // The callback of the observer that watches the top bar; other parts
    // of the page have observers of their own.
    let measured: (() => void) | undefined;
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(private cb: () => void) {}
        observe(el: Element) {
          if (el.classList.contains("app-top")) measured = this.cb;
        }
        disconnect() {}
      },
    );
    mockFetch({ ...noSetup, "GET /api/v1/auth/me": { body: { ...admin, auth: "session" } } });
    const { unmount } = render(<App />);
    await screen.findByRole("heading", { name: "Overview", level: 1 });
    const top = document.querySelector<HTMLElement>(".app-top")!;
    Object.defineProperty(top, "offsetHeight", { configurable: true, value: 81 });
    measured?.();
    expect(document.documentElement.style.getPropertyValue("--app-top-h")).toBe("81px");
    unmount();
    expect(document.documentElement.style.getPropertyValue("--app-top-h")).toBe("");
    vi.unstubAllGlobals();
  });

  it("shows neither the login brand nor text while the session check runs", async () => {
    mockFetch({ ...noSetup, "GET /api/v1/auth/me": { body: { ...admin, auth: "session" } } });
    const { container } = render(<App />);
    const boot = container.querySelector("main.ui-boot");
    expect(boot).toHaveAttribute("aria-busy", "true");
    expect(boot).toHaveTextContent("");
    expect(container.querySelector(".ui-auth-brand")).toBeNull();
    expect(await screen.findByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
    expect(container.querySelector("main.ui-boot")).toBeNull();
  });

  it("opens the backup page from the navigation", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session" } },
      "GET /api/v1/backups": {
        body: {
          settings: { version: 1, retention: { hourly: 0, daily: 7, weekly: 4, monthly: 6, yearly: 0 }, intervalHours: 24, windowStart: 1, windowEnd: 5, readParts: 12 },
          publicKey: "",
          hostKey: "",
          ready: false,
          status: { running: false, ok: false, items: [], maintenanceOk: true },
        },
      },
    });
    render(<App />);
    await user.click(await screen.findByRole("link", { name: "Backups" }));
    expect(await screen.findByRole("heading", { name: "Target and schedule" })).toBeInTheDocument();
    window.location.hash = "";
  });

  it("opens the site page from the navigation", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session" } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/php/versions": { body: { installed: ["8.4"], supported: ["8.4"], legacy: [] } },
    });
    render(<App />);
    await user.click(await screen.findByRole("link", { name: "Sites" }));
    expect(await screen.findByText("No sites yet.")).toBeInTheDocument();
    window.location.hash = "";
  });

  it("opens the database page from the navigation", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session" } },
      "GET /api/v1/databases": { body: { databases: [] } },
      "GET /api/v1/db-servers": { body: { servers: [] } },
      "GET /api/v1/sites": { body: { sites: [] } },
    });
    render(<App />);
    await user.click(await screen.findByRole("link", { name: "Databases" }));
    expect(await screen.findByText("Create a site first. Every database belongs to a site.")).toBeInTheDocument();
    window.location.hash = "";
  });

  it("shows the login form without a session", async () => {
    mockFetch({ ...noSetup, ...loggedOut });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeInTheDocument();
    // ADR-0037: no recovery command on the login page, only who to ask.
    expect(screen.getByText("Forgot your password? Ask an administrator of this panel.")).toBeInTheDocument();
    expect(screen.queryByText(/hostashi user recover/)).not.toBeInTheDocument();
  });

  it("offers a retry when the server is not reachable", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("Failed to fetch"))));
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent("not reachable");
    mockFetch({ ...noSetup, ...loggedOut });
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeInTheDocument();
  });
});

describe("Setup", () => {
  it("creates the administrator and goes to login", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/setup": { body: { required: true } },
      "POST /api/v1/setup": { status: 201, body: admin },
    });
    render(<App />);
    await user.type(await screen.findByLabelText("Setup token"), "  tok123  ");
    await user.type(screen.getByLabelText("Username"), "kim");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.type(screen.getByLabelText("Repeat password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Administrator account created");
    expect(screen.getByRole("heading", { name: "Log in" })).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ token: "tok123", username: "kim", password: "correct horse battery" });
  });

  it("takes the token from the installer's link and removes it from the address", async () => {
    const user = userEvent.setup();
    const tok = "0123456789abcdef0123456789abcdef0123456789abcdef";
    window.location.hash = `#/setup/${tok}`;
    const calls = mockFetch({
      "GET /api/v1/setup": { body: { required: true } },
      "POST /api/v1/setup": { status: 201, body: admin },
    });
    render(<App />);
    expect(await screen.findByLabelText("Setup token")).toHaveValue(tok);
    expect(window.location.hash).toBe("");
    expect(screen.getByText(/token from the installer's link is filled in/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Username"), "kim");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.type(screen.getByLabelText("Repeat password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Administrator account created");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ token: tok, username: "kim", password: "correct horse battery" });
  });

  it("ignores a setup link with a malformed token", async () => {
    window.location.hash = "#/setup/not-a-token";
    mockFetch({ "GET /api/v1/setup": { body: { required: true } } });
    render(<App />);
    expect(await screen.findByLabelText("Setup token")).toHaveValue("");
    window.location.hash = "";
  });

  it("refuses different passwords without calling the server", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "GET /api/v1/setup": { body: { required: true } } });
    render(<App />);
    await user.type(await screen.findByLabelText("Setup token"), "t");
    await user.type(screen.getByLabelText("Username"), "kim");
    await user.type(screen.getByLabelText("Password"), "aaaaaaaaaaaa");
    await user.type(screen.getByLabelText("Repeat password"), "bbbbbbbbbbbb");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(screen.getByRole("alert")).toHaveTextContent("do not match");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
  });

  it("shows server errors", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/setup": { body: { required: true } },
      "POST /api/v1/setup": err(422, "invalid_argument", "password must be at least 12 characters"),
    });
    render(<App />);
    await user.type(await screen.findByLabelText("Setup token"), "t");
    await user.type(screen.getByLabelText("Username"), "kim");
    await user.type(screen.getByLabelText("Password"), "short");
    await user.type(screen.getByLabelText("Repeat password"), "short");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Password must be at least 12 characters.");
  });
});

describe("Login", () => {
  it("logs in with a password and logs out again", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...noSetup,
      ...loggedOut,
      "POST /api/v1/auth/login": { body: admin },
      "POST /api/v1/auth/logout": { status: 204 },
    });
    render(<App />);
    await fillLogin(user);
    expect(await screen.findByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/v1/auth/login")?.body).toEqual({ username: "kim", password: "correct horse", totp: "" });
    await user.click(screen.getByRole("button", { name: "Log out" }));
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeInTheDocument();
  });

  it("draws a line under the brand once the nav scrolls", async () => {
    const user = userEvent.setup();
    mockFetch({ ...noSetup, ...loggedOut, "POST /api/v1/auth/login": { body: admin } });
    render(<App />);
    await fillLogin(user);
    const nav = await screen.findByRole("navigation", { name: "Main" });
    const side = nav.closest("aside");
    expect(side).toHaveAttribute("data-scrolled", "false");
    nav.scrollTop = 40;
    fireEvent.scroll(nav);
    expect(side).toHaveAttribute("data-scrolled", "true");
    nav.scrollTop = 0;
    fireEvent.scroll(nav);
    expect(side).toHaveAttribute("data-scrolled", "false");
  });

  it("shows wrong credentials", async () => {
    const user = userEvent.setup();
    mockFetch({ ...noSetup, ...loggedOut, "POST /api/v1/auth/login": err(401, "invalid_credentials") });
    render(<App />);
    await fillLogin(user);
    expect(await screen.findByRole("alert")).toHaveTextContent("Wrong username or password.");
  });

  it("shows the rate limit", async () => {
    const user = userEvent.setup();
    mockFetch({ ...noSetup, ...loggedOut, "POST /api/v1/auth/login": err(429, "rate_limited") });
    render(<App />);
    await fillLogin(user);
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts");
  });

  it("asks for the one-time code when the account has TOTP", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...noSetup,
      ...loggedOut,
      "POST /api/v1/auth/login": (body) => {
        const code = (body as { totp: string }).totp;
        if (code === "") return err(401, "totp_required");
        if (code === "123456") return { body: admin };
        return err(401, "invalid_credentials");
      },
    });
    render(<App />);
    await fillLogin(user);
    const input = await screen.findByLabelText(/One-time code/);
    await user.type(input, "999999");
    await user.click(screen.getByRole("button", { name: "Verify" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Wrong code");
    expect(screen.getByLabelText(/One-time code/)).toHaveValue("");
    await user.type(screen.getByLabelText(/One-time code/), " 123456 ");
    await user.click(screen.getByRole("button", { name: "Verify" }));
    expect(await screen.findByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
    const logins = calls.filter((c) => c.path === "/api/v1/auth/login").map((c) => (c.body as { totp: string }).totp);
    expect(logins).toEqual(["", "999999", "123456"]);
  });

  it("goes back from the code step to the password", async () => {
    const user = userEvent.setup();
    mockFetch({ ...noSetup, ...loggedOut, "POST /api/v1/auth/login": err(401, "totp_required") });
    render(<App />);
    await fillLogin(user);
    await user.click(await screen.findByRole("button", { name: "Back" }));
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(screen.getByLabelText("Username")).toHaveValue("kim");
  });

  it("disables the button until both fields are filled", async () => {
    mockFetch({ ...noSetup, ...loggedOut });
    render(<App />);
    expect(await screen.findByRole("button", { name: "Log in" })).toBeDisabled();
  });
});

describe("Passkey login", () => {
  it("hides the button when the browser has no WebAuthn", async () => {
    mockFetch({ ...noSetup, ...loggedOut });
    render(<App />);
    await screen.findByRole("heading", { name: "Log in" });
    expect(screen.queryByRole("button", { name: /passkey/ })).not.toBeInTheDocument();
  });

  it("runs the ceremony and logs in", async () => {
    const user = userEvent.setup();
    class FakeCredential {
      id = "cred";
      type = "public-key";
      rawId = new Uint8Array([1, 2]).buffer;
      response = {
        authenticatorData: new Uint8Array([3]).buffer,
        clientDataJSON: new Uint8Array([4]).buffer,
        signature: new Uint8Array([5]).buffer,
        userHandle: new Uint8Array([6]).buffer,
      };
    }
    vi.stubGlobal("PublicKeyCredential", FakeCredential);
    const get = vi.fn(async () => new FakeCredential());
    Object.defineProperty(navigator, "credentials", { value: { get }, configurable: true });

    const calls = mockFetch({
      ...noSetup,
      ...loggedOut,
      "POST /api/v1/auth/passkey/login/begin": { body: { ceremonyId: "c1", options: { publicKey: { challenge: "AQID", userVerification: "required" } } } },
      "POST /api/v1/auth/passkey/login/finish": { body: admin },
    });
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Log in with a passkey" }));
    expect(await screen.findByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
    expect(get).toHaveBeenCalledOnce();
    expect(calls.find((c) => c.path.endsWith("/finish"))?.body).toEqual({
      ceremonyId: "c1",
      credential: { id: "cred", rawId: "AQI", type: "public-key", response: { authenticatorData: "Aw", clientDataJSON: "BA", signature: "BQ", userHandle: "Bg" } },
    });
    Reflect.deleteProperty(navigator, "credentials");
  });

  it("explains when passkeys are off on the server", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("PublicKeyCredential", class {});
    Object.defineProperty(navigator, "credentials", { value: { get: vi.fn() }, configurable: true });
    mockFetch({ ...noSetup, ...loggedOut, "POST /api/v1/auth/passkey/login/begin": err(501, "passkeys_unavailable") });
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Log in with a passkey" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Passkeys are not set up on this server");
    Reflect.deleteProperty(navigator, "credentials");
  });

  it("explains a cancelled prompt", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("PublicKeyCredential", class {});
    const get = vi.fn(async () => Promise.reject(new DOMException("cancelled", "NotAllowedError")));
    Object.defineProperty(navigator, "credentials", { value: { get }, configurable: true });
    mockFetch({
      ...noSetup,
      ...loggedOut,
      "POST /api/v1/auth/passkey/login/begin": { body: { ceremonyId: "c1", options: { publicKey: { challenge: "AQID" } } } },
    });
    render(<App />);
    await user.click(await screen.findByRole("button", { name: "Log in with a passkey" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("closed or timed out");
    Reflect.deleteProperty(navigator, "credentials");
  });
});

describe("Second factor", () => {
  it("makes an admin without one set up TOTP before anything else", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session", secondFactor: false, secondFactorRequired: true } },
      "POST /api/v1/auth/totp/enroll": { body: { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/Hostashi:kim?secret=JBSWY3DPEHPK3PXP" } },
      "POST /api/v1/auth/totp/confirm": (body) =>
        (body as { code: string }).code === "123456" ? { body: { recoveryCodes: ["aaaa-bbbb", "cccc-dddd"] } } : err(401, "invalid_credentials"),
    });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Set up a second factor" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Main" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Set up an authenticator app" }));
    expect(await screen.findByRole("img", { name: "QR code for your authenticator app" })).toBeInTheDocument();
    expect(screen.getByLabelText("Authenticator key")).toHaveTextContent("JBSW Y3DP EHPK 3PXP");
    expect(screen.getByRole("link", { name: "Open in an authenticator app" })).toHaveAttribute(
      "href",
      "otpauth://totp/Hostashi:kim?secret=JBSWY3DPEHPK3PXP",
    );
    await user.type(screen.getByLabelText("One-time code"), "000000");
    await user.click(screen.getByRole("button", { name: "Turn on" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Wrong code");

    await user.type(screen.getByLabelText("One-time code"), "123456");
    await user.click(screen.getByRole("button", { name: "Turn on" }));
    expect(await screen.findByRole("list", { name: "Recovery codes" })).toHaveTextContent("aaaa-bbbb");
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByRole("navigation", { name: "Main" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Set up a second factor" })).not.toBeInTheDocument();
    expect(calls.filter((c) => c.path === "/api/v1/auth/totp/confirm")).toHaveLength(2);
  });

  it("lets a user without the demand in", async () => {
    mockFetch({ ...noSetup, "GET /api/v1/auth/me": { body: { username: "uma", role: "user", secondFactor: false, secondFactorRequired: false } } });
    render(<App />);
    expect(await screen.findByRole("navigation", { name: "Main" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Set up a second factor" })).not.toBeInTheDocument();
  });

  it("tells a passkey-only account to use the passkey", async () => {
    const user = userEvent.setup();
    mockFetch({ ...noSetup, ...loggedOut, "POST /api/v1/auth/login": err(401, "passkey_required") });
    render(<App />);
    await fillLogin(user);
    expect(await screen.findByRole("alert")).toHaveTextContent("This account logs in with a passkey.");
  });
});

describe("Roles", () => {
  it("shows a user only the pages of the role", async () => {
    mockFetch({ ...noSetup, "GET /api/v1/auth/me": { body: { username: "uma", role: "user", secondFactor: false, secondFactorRequired: false } } });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(nav).getAllByRole("link").map((a) => a.textContent)).toEqual(["Overview", "Sites", "Databases"]);
  });

  it("shows the panel version to admins only", async () => {
    for (const role of ["user", "manager"]) {
      const calls = mockFetch({
        ...noSetup,
        "GET /api/v1/auth/me": { body: { username: "uma", role, secondFactor: true, secondFactorRequired: false } },
        "GET /api/v1/health": { body: { status: "ok", version: "0.4.0" } },
      });
      const { unmount } = render(<App />);
      await screen.findByRole("navigation", { name: "Main" });
      expect(screen.queryByText(/Hostashi v/)).not.toBeInTheDocument();
      expect(calls.some((c) => c.path === "/api/v1/health" || c.path === "/api/v1/doctor" || c.path === "/api/v1/services")).toBe(false);
      unmount();
    }
  });

  it("shows admins the users page", async () => {
    mockFetch({ ...noSetup, "GET /api/v1/auth/me": { body: { ...admin, secondFactor: true, secondFactorRequired: true } } });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(nav).getByRole("link", { name: "Users" })).toBeInTheDocument();
  });

  it("does not open a page the role cannot use", async () => {
    window.location.hash = "#/firewall";
    mockFetch({ ...noSetup, "GET /api/v1/auth/me": { body: { username: "uma", role: "user" } } });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Overview", level: 1 })).toBeInTheDocument();
    window.location.hash = "";
  });
});

describe("Shell", () => {
  const session = { ...noSetup, "GET /api/v1/auth/me": { body: { ...admin, auth: "session" } } };

  it("groups the navigation and shows the page in the breadcrumbs", async () => {
    window.location.hash = "#/users";
    mockFetch({
      ...session,
      "GET /api/v1/users": { body: { users: [] } },
      "GET /api/v1/invitations": { body: { invitations: [] } },
      "GET /api/v1/health": { body: { status: "ok", version: "0.4.0" } },
      "GET /api/v1/doctor": {
        body: { status: "fail", checkedAt: "", counts: {}, checks: [{ id: "system.quota", status: "warn", summary: "" }, { id: "service.nginx", status: "fail", summary: "" }] },
      },
      "GET /api/v1/services": { body: { services: [{ id: "nginx", unit: "nginx.service", installed: true, active: "failed", actions: [], restarts24h: 0 }] } },
    });
    render(<App />);
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(screen.getByRole("link", { name: "Hostashi, overview" })).toHaveAttribute("href", "#/");
    const version = await screen.findByRole("link", { name: "Hostashi v0.4.0" });
    expect(version).toHaveAttribute("href", "https://github.com/maki-it/hostashi/releases/tag/v0.4.0");
    expect(version).toHaveAttribute("target", "_blank");
    expect(version).toHaveAttribute("rel", "noopener noreferrer");
    expect(await within(nav).findByRole("link", { name: "Health, 2 problems" })).toBeInTheDocument();
    expect(within(nav).getByRole("link", { name: "Services, 1 problem" })).toBeInTheDocument();
    expect(within(nav).getAllByRole("heading").map((h) => h.textContent)).toEqual(["Hosting", "Server", "Administration"]);
    expect(within(nav).getByRole("link", { name: "Users" })).toHaveAttribute("aria-current", "page");
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByText("Administration")).toBeInTheDocument();
    expect(within(crumbs).getByText("Users")).toHaveAttribute("aria-current", "page");
    window.location.hash = "";
  });

  it("opens the search from the top bar and with Ctrl K", async () => {
    window.location.hash = "#/users";
    mockFetch({
      ...session,
      "GET /api/v1/users": { body: { users: [] } },
      "GET /api/v1/invitations": { body: { invitations: [] } },
      "GET /api/v1/sites": { body: { sites: [] } },
    });
    render(<App />);
    const top = await screen.findByRole("banner");
    await userEvent.click(within(top).getByRole("button", { name: /Search/ }));
    expect(screen.getByRole("dialog", { name: "Search" })).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "Search" })).not.toBeInTheDocument();
    fireEvent.keyDown(document, { key: "k", ctrlKey: true });
    expect(screen.getByRole("dialog", { name: "Search" })).toBeInTheDocument();
    window.location.hash = "";
  });

  it("opens a site address on its own page with the site in the breadcrumbs", async () => {
    window.location.hash = "#/sites/shop/php";
    mockFetch({
      ...session,
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/php/versions": { body: { installed: ["8.4"], supported: ["8.4"], legacy: [] } },
    });
    render(<App />);
    expect(await screen.findByText("There is no site named shop.")).toBeInTheDocument();
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByRole("link", { name: "Sites" })).toHaveAttribute("href", "#/sites");
    expect(within(crumbs).getByText("shop")).toHaveAttribute("aria-current", "page");
    window.location.hash = "";
  });

  it("follows address changes", async () => {
    mockFetch({ ...session, "GET /api/v1/databases": { body: { databases: [] } }, "GET /api/v1/db-servers": { body: { servers: [] } }, "GET /api/v1/sites": { body: { sites: [] } } });
    render(<App />);
    await screen.findByRole("heading", { name: "Overview", level: 1 });
    window.location.hash = "#/databases";
    window.dispatchEvent(new HashChangeEvent("hashchange"));
    expect(await screen.findByText("Create a site first. Every database belongs to a site.")).toBeInTheDocument();
    window.location.hash = "";
  });

  it("picks light, dark or the system in the top bar and saves it for the account", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session", colorScheme: "light", theme: "default" } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "PATCH /api/v1/me": (b) => ({ body: { theme: "default", ...(b as object) } }),
    });
    render(<App />);
    const button = await screen.findByRole("button", { name: "Colour scheme: Light" });
    expect(button).toHaveAttribute("data-tip", "Colour scheme: Light");
    expect(document.documentElement.dataset.scheme).toBe("light");

    await user.click(button);
    const menu = within(screen.getByRole("menu", { name: "Colour scheme" }));
    expect(menu.getAllByRole("menuitemradio").map((i) => i.textContent)).toEqual(["Follow the system", "Light", "Dark"]);
    // The current choice is ticked and has the focus.
    expect(menu.getByRole("menuitemradio", { name: "Light" })).toHaveAttribute("aria-checked", "true");
    expect(menu.getByRole("menuitemradio", { name: "Light" })).toHaveFocus();
    await user.click(menu.getByRole("menuitemradio", { name: "Dark" }));
    expect(document.documentElement.dataset.scheme).toBe("dark");
    expect(calls).toContainEqual({ method: "PATCH", path: "/api/v1/me", body: { colorScheme: "dark" } });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Colour scheme: Dark" }));
    await user.click(screen.getByRole("menuitemradio", { name: "Follow the system" }));
    expect(document.documentElement.dataset.scheme).toBeUndefined();
    expect(calls).toContainEqual({ method: "PATCH", path: "/api/v1/me", body: { colorScheme: "system" } });
    expect(screen.getByRole("button", { name: "Colour scheme: Follow the system" })).toBeInTheDocument();

    // Picking the current choice again stores nothing.
    const patches = calls.filter((c) => c.method === "PATCH").length;
    await user.click(screen.getByRole("button", { name: "Colour scheme: Follow the system" }));
    await user.click(screen.getByRole("menuitemradio", { name: "Follow the system" }));
    expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(patches);
  });

  it("keeps the top bar and the account page on the same colour scheme", async () => {
    const user = userEvent.setup();
    window.location.hash = "#/account";
    mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session", colorScheme: "dark", theme: "default" } },
      "PATCH /api/v1/me": (b) => ({ body: { theme: "default", ...(b as object) } }),
    });
    render(<App />);
    const group = within(await screen.findByRole("group", { name: "Colour scheme" }));
    await user.click(group.getByRole("radio", { name: /Follow the system/ }));
    expect(screen.getByRole("button", { name: "Colour scheme: Follow the system" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Colour scheme: Follow the system" }));
    await user.click(screen.getByRole("menuitemradio", { name: "Light" }));
    expect(group.getByRole("radio", { name: "Light" })).toBeChecked();
    window.location.hash = "";
  });

  it("offers following the system on the account page", async () => {
    const user = userEvent.setup();
    window.location.hash = "#/account";
    const calls = mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session", colorScheme: "dark", theme: "default" } },
      "PATCH /api/v1/me": (b) => ({ body: { theme: "default", ...(b as object) } }),
    });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Your account", level: 1 })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /^kim/ })).toHaveAttribute("aria-current", "page");
    const group = within(screen.getByRole("group", { name: "Colour scheme" }));
    expect(group.getByRole("radio", { name: "Dark" })).toBeChecked();
    expect(document.documentElement.dataset.scheme).toBe("dark");
    await user.click(group.getByRole("radio", { name: /Follow the system/ }));
    expect(document.documentElement.dataset.scheme).toBeUndefined();
    expect(await screen.findByText(/^Saved\./)).toBeInTheDocument();
    expect(calls).toContainEqual({ method: "PATCH", path: "/api/v1/me", body: { colorScheme: "system" } });
    window.location.hash = "";
  });

  it("switches the panel to German on the account page", async () => {
    const user = userEvent.setup();
    window.location.hash = "#/account";
    const calls = mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session", colorScheme: "light", theme: "default", language: "auto" } },
      "PATCH /api/v1/me": (b) => ({ body: { colorScheme: "light", theme: "default", ...(b as object) } }),
    });
    render(<App />);
    const group = within(await screen.findByRole("group", { name: "Language" }));
    expect(group.getByRole("radio", { name: /Follow the browser/ })).toBeChecked();
    await user.click(group.getByRole("radio", { name: "Deutsch" }));
    expect(await screen.findByRole("heading", { name: "Dein Konto", level: 1 })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Datenbanken" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Abmelden" })).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("de");
    expect(await screen.findByText(/^Gespeichert\./)).toBeInTheDocument();
    expect(calls).toContainEqual({ method: "PATCH", path: "/api/v1/me", body: { language: "de" } });
    window.location.hash = "";
  });

  it("says in the new language when the language cannot be saved", async () => {
    const user = userEvent.setup();
    window.location.hash = "#/account";
    mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session", language: "en" } },
      "PATCH /api/v1/me": err(500, "internal"),
    });
    render(<App />);
    await user.click(within(await screen.findByRole("group", { name: "Language" })).getByRole("radio", { name: "Deutsch" }));
    expect(await screen.findByText("Die Sprache konnte nicht gespeichert werden. Sie gilt bis zum Neuladen.")).toBeInTheDocument();
    window.location.hash = "";
  });

  it("starts in the account's language", async () => {
    mockFetch({ ...noSetup, "GET /api/v1/auth/me": { body: { ...admin, auth: "session", language: "de" } }, "GET /api/v1/sites": { body: { sites: [] } } });
    render(<App />);
    expect(await screen.findByRole("navigation", { name: "Hauptmenü" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Verwaltung" })).toBeInTheDocument();
    expect(document.title).toMatch(/^Übersicht · /);
  });

  it("opens and closes the menu for narrow windows", async () => {
    const user = userEvent.setup();
    mockFetch(session);
    render(<App />);
    const open = await screen.findByRole("button", { name: "Open menu" });
    expect(open).toHaveAttribute("aria-expanded", "false");
    await user.click(open);
    expect(screen.getByRole("button", { name: "Close menu" })).toHaveAttribute("aria-expanded", "true");
  });
});

describe("Login language", () => {
  it("follows the browser before an account is known", async () => {
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["de-AT", "en"]);
    mockFetch({ ...noSetup, ...loggedOut });
    render(<App />);
    expect(await screen.findByLabelText("Benutzername")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Anmelden" })).toBeDisabled();
    expect(document.documentElement.lang).toBe("de");
  });

  it("shows a known API error in German", async () => {
    const user = userEvent.setup();
    vi.spyOn(navigator, "languages", "get").mockReturnValue(["de-DE"]);
    mockFetch({ ...noSetup, ...loggedOut, "POST /api/v1/auth/login": err(401, "invalid_credentials") });
    render(<App />);
    await user.type(await screen.findByLabelText("Benutzername"), "kim");
    await user.type(screen.getByLabelText("Passwort"), "wrong");
    await user.click(screen.getByRole("button", { name: "Anmelden" }));
    expect(await screen.findByText("Benutzername oder Passwort ist falsch.")).toBeInTheDocument();
  });
});

describe("Invitation link", () => {
  it("sets the password and logs in", async () => {
    const user = userEvent.setup();
    const token = "I".repeat(43);
    window.location.hash = "#/invite/" + token;
    const calls = mockFetch({
      ...noSetup,
      "POST /api/v1/invitations/check": { body: { username: "mona" } },
      "POST /api/v1/invitations/accept": { body: { username: "mona", role: "manager", secondFactor: false, secondFactorRequired: true } },
    });
    render(<App />);
    expect(await screen.findByText("mona")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Password"), "a long enough password");
    await user.type(screen.getByLabelText("Repeat password"), "another password!!");
    await user.click(screen.getByRole("button", { name: "Set password and log in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("do not match");

    await user.clear(screen.getByLabelText("Repeat password"));
    await user.type(screen.getByLabelText("Repeat password"), "a long enough password");
    await user.click(screen.getByRole("button", { name: "Set password and log in" }));
    expect(await screen.findByRole("heading", { name: "Set up a second factor" })).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/v1/invitations/accept")?.body).toEqual({ token, password: "a long enough password" });
    expect(window.location.hash).toBe("#/");
    window.location.hash = "";
  });

  it("explains a used link", async () => {
    window.location.hash = "#/invite/" + "U".repeat(43);
    mockFetch({ ...noSetup, "POST /api/v1/invitations/check": err(404, "invitation_invalid") });
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent("invalid, used or expired");
    window.location.hash = "";
  });
});
