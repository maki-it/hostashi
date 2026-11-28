// SPDX-License-Identifier: AGPL-3.0-or-later

// Desktop check: the page column must not move sideways when the browser
// shows or hides its vertical scrollbar. It opens a short page (no
// scrollbar) and a long page (scrollbar) at 1920 px with the wiki sample
// data (wiki-fixtures.mjs) and compares the left edge of the content.
// Overlay scrollbars (macOS default) never shift anything; headless
// Chromium on Linux uses classic scrollbars once Playwright stops hiding
// them, so it shows the problem.
//
//   cd web && node e2e/scrollbar-shift.e2e.mjs
//
// No server or agent is needed. CHROMIUM_PATH selects a browser binary.
// SHOTS_DIR saves a screenshot of each page there.

import { resolve } from "node:path";
import { chromium } from "playwright";
import { createServer } from "vite";
import { answer } from "./wiki-fixtures.mjs";

const root = resolve(import.meta.dirname, "..");
const host = "panel.example.com";
const port = 5187;
// A page that fits the window and one that is much longer.
const pages = [
  { name: "short", hash: "#/sites/blog/https" },
  { name: "long", hash: "#/sites/blog/overview" },
];

const server = await createServer({ root, configFile: resolve(root, "vite.config.ts"), server: { host: "127.0.0.1", port, strictPort: true, allowedHosts: [host] }, logLevel: "error" });
await server.listen();
const browser = await chromium.launch({
  ...(process.env.CHROMIUM_PATH && { executablePath: process.env.CHROMIUM_PATH }),
  args: [`--host-resolver-rules=MAP ${host} 127.0.0.1`],
  // Playwright hides scrollbars by default; this check needs them.
  ignoreDefaultArgs: ["--hide-scrollbars"],
});
const context = await browser.newContext({ viewport: { width: 1920, height: 1080 }, locale: "en-GB", timezoneId: "Europe/Berlin" });
await context.route("**/api/v1/**", async (route) => {
  const req = route.request();
  const url = new URL(req.url());
  const res = answer(req.method(), url.pathname.replace(/^\/api\/v1/, ""), url.searchParams);
  if (!res) return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "no sample data" } }) });
  return route.fulfill({ status: res.status ?? 200, contentType: "application/json", body: JSON.stringify(res.body ?? {}) });
});

const page = await context.newPage();
const seen = [];
for (const p of pages) {
  await page.goto(`http://${host}:${port}/${p.hash}`);
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(300);
  const m = await page.evaluate(() => ({
    left: Math.round(document.querySelector(".app-content").getBoundingClientRect().left),
    scrolls: document.documentElement.scrollHeight > document.documentElement.clientHeight,
    bar: window.innerWidth - document.documentElement.clientWidth,
  }));
  console.log(`${p.name}: ${p.hash} scrolls=${m.scrolls} content left=${m.left} px scrollbar=${m.bar} px`);
  if (process.env.SHOTS_DIR) await page.screenshot({ path: resolve(process.env.SHOTS_DIR, `${p.name}.png`) });
  seen.push({ ...p, ...m });
}
await browser.close();
await server.close();

const [short, long] = seen;
if (short.scrolls || !long.scrolls) {
  console.error(`\nThe sample pages no longer fit the test: ${short.hash} must fit the window and ${long.hash} must scroll.`);
  process.exit(1);
}
if (short.left !== long.left) {
  console.error(`\nThe content moves ${short.left - long.left} px sideways when the scrollbar appears.`);
  process.exit(1);
}
console.log("\nThe content stays in place when the scrollbar appears.");
