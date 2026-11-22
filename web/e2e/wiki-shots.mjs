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
const out = resolve(root, "../docs/wiki/images");

// Each shot opens a hash route, optionally does something, and takes the
// page (or one element) as it is then.
const shots = [
  { name: "sites", hash: "#/sites", ready: "table" },
  { name: "site-new", hash: "#/sites?new=1", ready: "form" },
  { name: "site-domains", hash: "#/sites/blog/domains", ready: "text=Domains" },
  { name: "users", hash: "#/users", ready: "table" },
  { name: "databases", hash: "#/databases", ready: "table" },
  { name: "backups", hash: "#/backups", ready: "text=Snapshots" },
  { name: "firewall", hash: "#/firewall", ready: "text=Allowlists" },
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
const context = await browser.newContext({ viewport: { width: 1280, height: 820 }, deviceScaleFactor: 2, colorScheme: "light", locale: "en-GB", timezoneId: "Europe/Berlin" });
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
    await page.screenshot({ path: resolve(out, s.name + ".png"), animations: "disabled" });
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
