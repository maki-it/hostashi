// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
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
    expect(await screen.findByRole("heading", { name: "Welcome, kim" })).toBeInTheDocument();
  });

  it("opens the backup page from the navigation", async () => {
    const user = userEvent.setup();
    mockFetch({
      ...noSetup,
      "GET /api/v1/auth/me": { body: { ...admin, auth: "session" } },
      "GET /api/v1/backups": {
        body: {
          settings: { version: 1, retention: { daily: 7, weekly: 4, monthly: 6 }, windowStart: 1, windowEnd: 5, readParts: 12 },
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
      "GET /api/v1/sites": { body: { sites: [] } },
    });
    render(<App />);
    await user.click(await screen.findByRole("link", { name: "Databases" }));
    expect(await screen.findByText("No databases yet.")).toBeInTheDocument();
    window.location.hash = "";
  });

  it("shows the login form without a session", async () => {
    mockFetch({ ...noSetup, ...loggedOut });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeInTheDocument();
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
    expect(await screen.findByRole("heading", { name: "Welcome, kim" })).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/v1/auth/login")?.body).toEqual({ username: "kim", password: "correct horse", totp: "" });
    await user.click(screen.getByRole("button", { name: "Log out" }));
    expect(await screen.findByRole("heading", { name: "Log in" })).toBeInTheDocument();
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
    expect(await screen.findByRole("heading", { name: "Welcome, kim" })).toBeInTheDocument();
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
    expect(await screen.findByRole("heading", { name: "Welcome, kim" })).toBeInTheDocument();
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
