// SPDX-License-Identifier: AGPL-3.0-or-later

// Phone-width check: opens every page and every tab of a sample site at
// 390 px with the wiki sample data (wiki-fixtures.mjs) and fails when one
// of them is wider than the screen, so it would scroll sideways. Elements
// that stick out are listed to find the cause.
//
//   cd web && node e2e/phone-overflow.e2e.mjs
//
// No server or agent is needed. CHROMIUM_PATH selects a browser binary.

import { resolve } from "node:path";
import { chromium } from "playwright";
import { createServer } from "vite";
import { answer } from "./wiki-fixtures.mjs";

const root = resolve(import.meta.dirname, "..");
const width = 390;
const host = "panel.example.com";
const port = 5186;

const pages = ["", "sites", "sites?new=1", "databases", "parked", "pages", "health", "services", "system", "pools", "php", "node", "python", "backups", "firewall", "limits", "profiles", "users", "mail", "audit", "account"];
const tabs = ["overview", "domains", "access", "web", "files", "malware", "php", "cache", "cron", "ssh", "limits", "usage", "logs", "nginx", "https", "headers", "realip", "hardening"];
const hashes = [...pages.map((p) => "#/" + p), ...tabs.map((t) => "#/sites/blog/" + t)];

const server = await createServer({ root, configFile: resolve(root, "vite.config.ts"), server: { host: "127.0.0.1", port, strictPort: true, allowedHosts: [host] }, logLevel: "error" });
await server.listen();
const browser = await chromium.launch({
  ...(process.env.CHROMIUM_PATH && { executablePath: process.env.CHROMIUM_PATH }),
  args: [`--host-resolver-rules=MAP ${host} 127.0.0.1`],
});
const context = await browser.newContext({ viewport: { width, height: 844 }, locale: "en-GB", timezoneId: "Europe/Berlin" });
await context.route("**/api/v1/**", async (route) => {
  const req = route.request();
  const url = new URL(req.url());
  const res = answer(req.method(), url.pathname.replace(/^\/api\/v1/, ""), url.searchParams);
  if (!res) return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "no sample data" } }) });
  return route.fulfill({ status: res.status ?? 200, contentType: "application/json", body: JSON.stringify(res.body ?? {}) });
});

const wide = [];
for (const hash of hashes) {
  const page = await context.newPage();
  await page.goto(`http://${host}:${port}/${hash}`);
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(300);
  const found = await page.evaluate((w) => {
    if (document.documentElement.scrollWidth <= w) return null;
    const out = [];
    for (const el of document.querySelectorAll("body *")) {
      const b = el.getBoundingClientRect();
      if (b.right > w + 1 && b.width > 0) out.push(`${el.tagName.toLowerCase()}.${String(el.className).split(" ")[0]} (right edge ${Math.round(b.right)} px)`);
    }
    return { width: document.documentElement.scrollWidth, out: out.slice(0, 5) };
  }, width);
  console.log(`${found ? "WIDE" : "ok  "} ${hash}${found ? ` is ${found.width} px wide: ${found.out.join(", ")}` : ""}`);
  if (found) wide.push(hash);
  await page.close();
}
await browser.close();
await server.close();

if (wide.length > 0) {
  console.error(`\n${wide.length} page(s) scroll sideways at ${width} px: ${wide.join(" ")}`);
  process.exit(1);
}
console.log(`\nAll ${hashes.length} pages fit ${width} px.`);
