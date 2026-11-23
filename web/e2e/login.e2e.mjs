// SPDX-License-Identifier: AGPL-3.0-or-later

// End-to-end check of setup and login against a real "hostashi serve":
// first-run setup, password login, session after reload, the mandatory
// second factor (TOTP set up in the panel), TOTP with a recovery code,
// passkey registration and login (Chromium's virtual authenticator),
// logout. The server must be fresh (setup not done) and
// started with -public-url matching HOSTASHI_URL.
//
//   HOSTASHI_URL=http://localhost:8080 HOSTASHI_SETUP_TOKEN_FILE=/path/setup-token node e2e/login.e2e.mjs
//
// CHROMIUM_PATH selects a browser binary; SCREENSHOTS=dir writes screenshots.

import { createHmac } from "node:crypto";
import { mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright";

const base = process.env.HOSTASHI_URL ?? "http://localhost:8080";
const token = readFileSync(process.env.HOSTASHI_SETUP_TOKEN_FILE ?? "setup-token", "utf8").trim();
const shots = process.env.SCREENSHOTS;
const password = "correct horse battery staple";

function base32(s) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const c of s.replace(/=+$/, "").toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, "0");
  const out = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) out.push(parseInt(bits.slice(i, i + 8), 2));
  return Buffer.from(out);
}

function totp(secret) {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const h = createHmac("sha1", base32(secret)).update(counter).digest();
  const o = h[h.length - 1] & 15;
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1e6).padStart(6, "0");
}

const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {});
const context = await browser.newContext({ viewport: { width: 480, height: 720 } });
const page = await context.newPage();
const pageErrors = [];
page.on("pageerror", (e) => pageErrors.push(String(e)));
page.on("console", (m) => {
  // 401s from /auth/me and wrong passwords are expected, and so are 503s:
  // no agent runs here, so the overview's health and backup cards cannot
  // load. CSP violations and script errors are not expected.
  const expected = m.text().includes("status of 401") || m.text().includes("status of 503");
  if (m.type() === "error" && !expected) pageErrors.push(m.text());
});

let n = 0;
async function ok(what) {
  n++;
  console.log("ok " + n + " - " + what);
  if (shots) {
    mkdirSync(shots, { recursive: true });
    await page.screenshot({ path: join(shots, String(n).padStart(2, "0") + ".png") });
  }
}

const loginButton = () => page.getByRole("button", { name: "Log in", exact: true });
// Before the second factor the panel greets the account; afterwards it opens on the overview.
const welcome = () => page.getByRole("heading", { name: /^(Welcome, admin|Overview)$/ });
// On a narrow window the sidebar is a menu; open it before using a link or
// the logout button in it.
async function openMenu() {
  await welcome().waitFor();
  const button = page.getByRole("button", { name: "Open menu" });
  if (await button.isVisible()) await button.click();
}

try {
  await page.goto(base + "/");
  await page.getByLabel("Setup token").fill(token);
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Repeat password").fill(password);
  await page.getByRole("button", { name: "Create account" }).click();
  await page.getByRole("status").filter({ hasText: "Administrator account created" }).waitFor();
  await ok("setup creates the administrator");

  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("wrong password!");
  await loginButton().click();
  await page.getByRole("alert").filter({ hasText: "Wrong username or password." }).waitFor();
  await ok("wrong password is refused");

  await page.getByLabel("Password").fill(password);
  await loginButton().click();
  await welcome().waitFor();
  await page.reload();
  await welcome().waitFor();
  await ok("password login, session survives a reload");

  // An admin must set up a second factor before anything else (ADR-0035).
  await page.getByRole("heading", { name: "Set up a second factor" }).waitFor();
  if (await page.getByRole("link", { name: "Sites", exact: true }).count()) throw new Error("navigation shown before the second factor");
  await ok("an admin without a second factor sees only the setup");

  await page.getByRole("button", { name: "Set up an authenticator app" }).click();
  const secret = (await page.getByLabel("Authenticator key").textContent()).replace(/\s+/g, "");
  await page.getByLabel("One-time code").fill(totp(secret));
  await page.getByRole("button", { name: "Turn on" }).click();
  // allTextContents does not wait, so wait for the codes to be rendered first.
  const codeList = page.getByRole("list", { name: "Recovery codes" });
  await codeList.locator("code").first().waitFor();
  const codes = (await codeList.locator("code").allTextContents()).map((c) => c.trim());
  if (codes.length === 0) throw new Error("TOTP enrollment returned no recovery codes");
  await ok("TOTP set up in the panel, recovery codes shown");
  await page.getByRole("button", { name: "Continue" }).click();
  await openMenu();
  await page.getByRole("link", { name: "Sites", exact: true }).waitFor();

  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  const reg = await page.evaluate(async () => {
    const dec = (s) => Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(s.length / 4) * 4, "=")), (c) => c.charCodeAt(0));
    const enc = (b) => btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    const begin = await (await fetch("/api/v1/auth/passkeys/register/begin", { method: "POST" })).json();
    const pk = begin.options.publicKey;
    pk.challenge = dec(pk.challenge);
    pk.user.id = dec(pk.user.id);
    for (const c of pk.excludeCredentials ?? []) c.id = dec(c.id);
    const c = await navigator.credentials.create({ publicKey: pk });
    const credential = {
      id: c.id,
      rawId: enc(c.rawId),
      type: c.type,
      response: { attestationObject: enc(c.response.attestationObject), clientDataJSON: enc(c.response.clientDataJSON) },
    };
    const r = await fetch("/api/v1/auth/passkeys/register/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ceremonyId: begin.ceremonyId, name: "e2e", credential }),
    });
    return r.status;
  });
  if (reg !== 201) throw new Error("passkey registration: status " + reg);
  await ok("a passkey enrolled through the API");

  await page.getByRole("button", { name: "Log out" }).click();
  await loginButton().waitFor();
  await page.reload();
  await loginButton().waitFor();
  await ok("logout ends the session");

  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill(password);
  await loginButton().click();
  await page.getByLabel(/One-time code/).fill("000000");
  await page.getByRole("button", { name: "Verify" }).click();
  await page.getByRole("alert").filter({ hasText: "Wrong code" }).waitFor();
  await page.getByLabel(/One-time code/).fill(codes[0]);
  await page.getByRole("button", { name: "Verify" }).click();
  await welcome().waitFor();
  await ok("second step: wrong code refused, recovery code accepted");

  await openMenu();
  await page.getByRole("button", { name: "Log out" }).click();
  await page.getByRole("button", { name: "Log in with a passkey" }).click();
  await welcome().waitFor();
  await ok("passkey login without password or code");

  // The admin invites a manager; the manager opens the link in another
  // browser, sets a password and must set up a second factor (ADR-0035).
  await openMenu();
  await page.getByRole("link", { name: "Users" }).click();
  await page.getByRole("link", { name: "Invite someone" }).click();
  await page.getByLabel(/Username/).fill("mona");
  await page.getByLabel(/Role/).selectOption("manager");
  await page.getByRole("button", { name: "Create invitation" }).click();
  const link = await page.getByLabel("Invitation link").inputValue();
  if (!link.startsWith(base + "/#/invite/")) throw new Error("invitation link " + link);
  await ok("the admin invites a manager");

  const other = await browser.newContext({ viewport: { width: 480, height: 720 } });
  const invited = await other.newPage();
  invited.on("pageerror", (e) => pageErrors.push(String(e)));
  await invited.goto(link);
  await invited.getByText("mona", { exact: true }).waitFor();
  await invited.getByLabel("Password", { exact: true }).fill(password);
  await invited.getByLabel("Repeat password").fill(password);
  await invited.getByRole("button", { name: "Set password and log in" }).click();
  await invited.getByRole("heading", { name: "Set up a second factor" }).waitFor();
  if (shots) await invited.screenshot({ path: join(shots, "invited.png") });
  await other.close();
  await ok("the manager sets a password through the link and is asked for a second factor");

  await page.reload();
  await page.getByText("second factor on").or(page.getByText("no second factor")).first().waitFor();
  await ok("the users page lists both accounts");

  // The colour scheme belongs to the account: it survives a reload and
  // the login page after logout follows the system again (ADR-0037).
  const scheme = () => page.evaluate(() => document.documentElement.dataset.scheme ?? "system");
  const [saved] = await Promise.all([
    page.waitForResponse((r) => r.url().endsWith("/api/v1/me") && r.request().method() === "PATCH"),
    page.getByRole("button", { name: /^Use (dark|light) colours$/ }).click(),
  ]);
  if (!saved.ok()) throw new Error("saving the scheme: " + saved.status());
  const picked = await scheme();
  if (picked === "system") throw new Error("the scheme button set nothing");
  await page.reload();
  await page.getByRole("button", { name: /^Use (dark|light) colours$/ }).waitFor();
  if ((await scheme()) !== picked) throw new Error("scheme after reload " + (await scheme()) + ", want " + picked);
  await ok("the colour scheme is stored for the account");

  if (pageErrors.length > 0) throw new Error("browser errors:\n" + pageErrors.join("\n"));
  await ok("no script errors or CSP violations");
} finally {
  await browser.close();
}
