// SPDX-License-Identifier: AGPL-3.0-or-later

// Screenshots for the wiki (ADR-0043). Starts the panel's frontend with
// Vite, answers every API call with the sample data in wiki-fixtures.mjs,
// and writes one PNG per entry of `shots` to docs/wiki/images/.
//
//   cd web && node e2e/wiki-shots.mjs [name ...]
//
// No server, agent or root is needed, and no real data ends up in a
// picture. CHROMIUM_PATH selects a browser binary. An API call without
// sample data fails the run, so a page that changed its calls is noticed.

import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { chromium } from "playwright";
import { createServer } from "vite";
import { answer } from "./wiki-fixtures.mjs";

const root = resolve(import.meta.dirname, "..");
// SHOTS_OUT, SHOTS_SCHEME (light, dark) and SHOTS_WIDTH take previews of
// the same pages elsewhere, for example dark or at phone width.
const out = process.env.SHOTS_OUT ? resolve(process.env.SHOTS_OUT) : resolve(root, "../docs/wiki/images");
const scheme = process.env.SHOTS_SCHEME === "dark" ? "dark" : "light";
const width = Number(process.env.SHOTS_WIDTH) || 1280;

// Each shot opens a hash route, optionally does something, and takes the
// page (or one element) as it is then; full takes the whole page.
const shots = [
  { name: "sites", hash: "#/sites", ready: "table" },
  { name: "site-new", hash: "#/sites?new=1", ready: "form" },
  { name: "site-rename", hash: "#/sites/shop", ready: "text=Save display name", full: true },
  { name: "site-domains", hash: "#/sites/blog/domains", ready: "text=Domains" },
  {
    name: "site-domain-change",
    hash: "#/sites/blog/domains",
    ready: "text=Change primary domain",
    full: true,
    act: async (page) => {
      await page.getByRole("button", { name: "Change primary domain" }).click();
      await page.getByLabel(/New primary domain/).fill("example.com");
      await page.getByRole("heading", { name: "Change primary domain" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  { name: "site-domain-pending", hash: "#/sites/news/domains", ready: "text=Domain change waiting for its certificate", full: true },
  {
    name: "site-permissions",
    hash: "#/sites/blog/files",
    ready: "text=Reset permissions",
    act: async (page) => {
      await page.getByRole("button", { name: "Reset permissions" }).click();
      await page.getByLabel(/to confirm/).fill("blog");
      await page.getByRole("heading", { name: "Reset permissions" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  {
    name: "site-permissions-done",
    hash: "#/sites/blog/files",
    ready: "text=Reset permissions",
    act: async (page) => {
      await page.getByRole("button", { name: "Reset permissions" }).click();
      await page.getByLabel(/to confirm/).fill("blog");
      await page.getByRole("button", { name: "Reset permissions of blog" }).click();
      await page.getByRole("list", { name: "Skipped entries" }).waitFor();
      await page.getByRole("heading", { name: "Reset permissions" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  {
    name: "site-www",
    hash: "#/sites/blog/domains",
    ready: "text=www redirect",
    act: async (page) => page.getByRole("heading", { name: "www redirect" }).evaluate((h) => h.scrollIntoView({ block: "start" })),
  },
  {
    name: "site-www-add",
    hash: "#/sites/shop/domains",
    ready: "text=www redirect",
    act: async (page) => page.getByRole("heading", { name: "www redirect" }).evaluate((h) => h.scrollIntoView({ block: "start" })),
  },
  { name: "site-access", hash: "#/sites/blog/access", ready: "text=Access protection", full: true },
  {
    name: "site-access-password",
    hash: "#/sites/blog/access",
    ready: "text=Access protection",
    act: async (page) => {
      await page.getByRole("textbox", { name: "User name" }).fill("agency");
      await page.getByRole("button", { name: "Add user" }).click();
      await page.getByRole("heading", { name: "Save this password now" }).waitFor();
    },
  },
  { name: "site-node", hash: "#/sites/app/web", ready: "text=Start command" },
  { name: "site-python", hash: "#/sites/api/web", ready: "text=Start command" },
  {
    name: "site-python-version",
    hash: "#/sites/api/web",
    ready: "text=Start command",
    act: async (page) => {
      await page.getByLabel("Python version").selectOption("3.13");
      await page.getByLabel(/to confirm/).fill("ap");
      await page.getByText("The environment .venv is rebuilt").evaluate((h) => h.scrollIntoView({ block: "center" }));
    },
  },
  {
    name: "site-python-venv",
    hash: "#/sites/api/web",
    ready: "text=Recreate environment",
    act: async (page) => {
      await page.getByRole("button", { name: "Recreate environment" }).click();
      await page.getByRole("heading", { name: "Python environment" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  {
    name: "site-new-python",
    hash: "#/sites?new=1",
    ready: "form",
    act: async (page) => {
      await page.getByLabel(/^Name/).fill("api");
      await page.getByLabel(/^Domains/).fill("api.example.com");
      await page.getByRole("radio", { name: /^Python/ }).click();
      await page.getByRole("radio", { name: /ASGI with uvicorn/ }).click();
      await page.getByRole("radio", { name: /^Python A Django/ }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  { name: "python-versions", hash: "#/python", ready: "table" },
  { name: "site-proxy", hash: "#/sites/intranet/web", ready: "text=Pass requests to" },
  {
    name: "site-type-change",
    hash: "#/sites/docs/web",
    ready: "text=Change type",
    act: async (page) => {
      await page.getByRole("button", { name: "Change type" }).click();
      await page.getByRole("radio", { name: /^PHP/ }).click();
      await page.getByRole("heading", { name: "Change type" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  { name: "site-git", hash: "#/sites/app/git", ready: "text=Deploy now", full: true },
  {
    name: "site-git-log",
    hash: "#/sites/app/git",
    ready: "text=Deploy now",
    act: async (page) => {
      await page.getByRole("button", { name: "Show log" }).first().click();
      await page.getByLabel("Log of deploy 14").waitFor();
    },
  },
  { name: "site-git-setup", hash: "#/sites/docs/git", ready: "form", full: true },
  {
    name: "site-git-hostkey",
    hash: "#/sites/app/git",
    ready: "text=Deploy now",
    act: async (page) => {
      await page.getByRole("button", { name: "Read the host key again" }).click();
      await page.getByRole("heading", { name: "Deploy key and host key" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  {
    name: "site-git-webhook",
    hash: "#/sites/app/git",
    ready: "text=Deploy now",
    act: async (page) => {
      await page.getByRole("button", { name: "New secret" }).click();
      await page.getByRole("heading", { name: "Webhook" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  { name: "site-malware", hash: "#/sites/blog/malware", ready: "text=Flagged files", full: true },
  {
    name: "system-malware",
    hash: "#/system",
    ready: "text=ClamAV 1.4.6+dfsg-1",
    act: async (page) => page.getByRole("heading", { name: "Virus scan" }).evaluate((h) => h.scrollIntoView({ block: "start" })),
  },
  {
    name: "system-panel",
    hash: "#/system",
    ready: "text=Real visitor address of the panel",
    act: async (page) => page.getByRole("heading", { name: /^Panel certificate/ }).evaluate((h) => h.scrollIntoView({ block: "start" })),
  },
  { name: "health-panel-tls", hash: "#/health", ready: "table" },
  { name: "users", hash: "#/users", ready: "table" },
  { name: "databases", hash: "#/databases", ready: "table" },
  { name: "backups", hash: "#/backups", ready: "text=Snapshots" },
  {
    name: "backups-target",
    hash: "#/backups",
    ready: "text=Target and schedule",
    act: async (page) => page.getByRole("heading", { name: "Target and schedule" }).evaluate((h) => h.scrollIntoView({ block: "start" })),
  },
  { name: "firewall", hash: "#/firewall", ready: "text=Allowlists" },
  {
    name: "firewall-api-tokens",
    hash: "#/firewall",
    ready: "text=Allowed addresses",
    act: async (page) => {
      await page.getByRole("heading", { name: "API tokens" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  {
    name: "account-tokens",
    hash: "#/account",
    ready: "table",
    act: async (page) => {
      await page.getByRole("heading", { name: "API tokens" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  {
    name: "account-token-new",
    hash: "#/account",
    ready: "table",
    act: async (page) => {
      await page.getByRole("button", { name: "New token" }).click();
      await page.getByLabel(/^Name/).fill("Backup check");
      await page.getByRole("button", { name: "Only reading" }).click();
      await page.getByRole("heading", { name: "New API token" }).evaluate((h) => h.scrollIntoView({ block: "start" }));
    },
  },
  {
    name: "account-token-created",
    hash: "#/account",
    ready: "table",
    act: async (page) => {
      await page.getByRole("button", { name: "New token" }).click();
      await page.getByLabel(/^Name/).fill("Backup check");
      await page.getByRole("button", { name: "Create token" }).click();
      await page.getByRole("heading", { name: "Save this token now" }).waitFor();
    },
  },
];

const only = process.argv.slice(2);
// The panel shows the host name it runs under; map an example one.
const host = "panel.example.com";
const server = await createServer({ root, configFile: resolve(root, "vite.config.ts"), server: { port: 5179, strictPort: true, allowedHosts: [host] }, logLevel: "error" });
await server.listen();

const browser = await chromium.launch({
  ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}),
  args: [`--host-resolver-rules=MAP ${host} 127.0.0.1`],
});
const context = await browser.newContext({ viewport: { width, height: width < 600 ? 844 : 820 }, deviceScaleFactor: 2, colorScheme: scheme, locale: "en-GB", timezoneId: "Europe/Berlin" });
const missing = new Set();
await context.route("**/api/v1/**", async (route) => {
  const req = route.request();
  const url = new URL(req.url());
  const path = url.pathname.replace(/^\/api\/v1/, "");
  const res = answer(req.method(), path, url.searchParams);
  if (!res) {
    missing.add(req.method() + " " + path);
    return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "no sample data" } }) });
  }
  return route.fulfill({ status: res.status ?? 200, contentType: "application/json", body: JSON.stringify(res.body ?? {}) });
});

mkdirSync(out, { recursive: true });
let failed = false;
try {
  for (const s of shots) {
    if (only.length && !only.includes(s.name)) continue;
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    await page.clock.setFixedTime(new Date("2026-11-22T18:00:00Z"));
    await page.goto(`http://${host}:5179/` + s.hash);
    await page.waitForSelector(s.ready, { timeout: 10000 });
    if (s.act) await s.act(page);
    await page.waitForLoadState("networkidle");
    await page.screenshot({ path: resolve(out, s.name + ".png"), animations: "disabled", fullPage: s.full === true });
    console.log("wrote images/" + s.name + ".png");
    if (errors.length) {
      failed = true;
      console.error(s.name + ": " + errors.join("\n"));
    }
    await page.close();
  }
} finally {
  await browser.close();
  await server.close();
}
if (missing.size) {
  console.error("API calls without sample data:\n  " + [...missing].sort().join("\n  "));
  failed = true;
}
process.exit(failed ? 1 : 0);
