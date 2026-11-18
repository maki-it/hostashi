// End-to-end check and measurements for both candidates.
// Usage: node e2e/run.cjs   (after ./build.sh; needs playwright and bash)
const { chromium } = require("playwright");
const { spawn } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");

const TOKEN = "0123456789abcdef0123456789abcdef";
const OUT = path.join(__dirname, "out");
fs.mkdirSync(OUT, { recursive: true });

function startServer(fw, port) {
  const bin = path.join(__dirname, "..", "bin", `s7-${fw}`);
  const p = spawn(bin, ["-addr", `127.0.0.1:${port}`, "-token", TOKEN, "-terminal"], { stdio: "inherit" });
  return p;
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let failures = 0;
function check(name, ok, extra = "") {
  console.log(`${ok ? "PASS" : "FAIL"} ${name}${extra ? " (" + extra + ")" : ""}`);
  if (!ok) failures++;
}

async function run(fw, port) {
  const srv = startServer(fw, port);
  await sleep(500);
  const base = `http://127.0.0.1:${port}`;
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1100, height: 800 } });
  const page = await ctx.newPage();
  const errors = [];
  page.on("console", (m) => m.type() === "error" && !/status of 422/.test(m.text()) && errors.push(m.text()));
  page.on("pageerror", (e) => errors.push(String(e)));
  const cdp = await ctx.newCDPSession(page);
  await cdp.send("Network.enable");
  await cdp.send("Performance.enable");
  let bytes = 0;
  cdp.on("Network.loadingFinished", (e) => (bytes += e.encodedDataLength));
  const res = { fw };

  // 0. API needs the token
  const noTok = await fetch(`${base}/api/sites`);
  check(`${fw}: API without token is 401`, noTok.status === 401);

  // 1. Initial load
  const t0 = Date.now();
  await page.goto(`${base}/#token=${TOKEN}`);
  await page.waitForSelector("tbody tr");
  res.firstRowsMs = Date.now() - t0;
  res.initialBytes = bytes;
  check(`${fw}: 22 sites listed`, (await page.locator("tbody tr").count()) === 22);

  // 2. Filter and sort
  await page.getByLabel("Filter").fill("kunde0");
  check(`${fw}: filter`, (await page.locator("tbody tr").count()) === 9);
  await page.getByLabel("Filter").fill("");
  await page.getByRole("button", { name: "Disk" }).click();
  const first = await page.locator("tbody tr td").first().innerText();
  check(`${fw}: sort by disk`, first !== "kunde01.example.com", first);

  // 3. Form validation: client, server, success
  await page.locator('input[name="domain"]').fill("bad domain");
  await page.locator('input[name="user"]').fill("x");
  await page.getByRole("button", { name: "Create site" }).click();
  check(`${fw}: client-side errors`, (await page.locator(".err").allInnerTexts()).filter(Boolean).length === 2);
  await page.locator('input[name="domain"]').fill("neu.example.com");
  await page.locator('input[name="user"]').fill("kunde01");
  await page.getByRole("button", { name: "Create site" }).click();
  await page.waitForFunction(() => document.body.innerText.includes("user already exists"));
  check(`${fw}: server-side error shown`, true);
  await page.locator('input[name="user"]').fill("neu");
  await page.getByRole("button", { name: "Create site" }).click();
  await page.waitForFunction(() => document.querySelectorAll("tbody tr").length === 23);
  check(`${fw}: site created`, true);

  // 4. Live metrics via SSE
  await page.getByRole("tab", { name: "Metrics" }).click();
  await page.waitForSelector("canvas");
  await sleep(4500);
  const status = await page.getByTestId("metrics-status").innerText();
  const n = Number(/(\d+) samples/.exec(status)?.[1] ?? 0);
  check(`${fw}: live samples arrive`, n >= 3, status);
  await page.screenshot({ path: path.join(OUT, `${fw}-metrics.png`) });

  // 5. Terminal (lazy chunk)
  const before = bytes;
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.waitForSelector(".xterm");
  await sleep(800);
  res.terminalChunkBytes = bytes - before;
  await page.locator(".xterm").click();
  await page.keyboard.type("echo hi$((40+2))\n");
  await page.waitForFunction(() => /hi42/.test(document.querySelector(".xterm-rows")?.textContent ?? ""), null, { timeout: 5000 }).catch(() => {});
  const txt = await page.locator(".xterm-rows").innerText();
  check(`${fw}: terminal round trip`, /hi42/.test(txt));
  await page.screenshot({ path: path.join(OUT, `${fw}-terminal.png`) });

  // 6. Leaving the tab closes the stream and socket cleanly
  await page.getByRole("tab", { name: "Sites" }).click();
  await sleep(300);

  // 7. Memory after the above, with a forced GC
  await cdp.send("HeapProfiler.collectGarbage");
  const m = await cdp.send("Performance.getMetrics");
  res.jsHeapKB = Math.round(m.metrics.find((x) => x.name === "JSHeapUsedSize").value / 1024);
  await page.screenshot({ path: path.join(OUT, `${fw}-sites.png`) });

  check(`${fw}: no console errors (CSP clean)`, errors.length === 0, errors.join(" | "));
  await browser.close();
  srv.kill();
  return res;
}


(async () => {
  const results = [];
  results.push(await run("react", 18081));
  results.push(await run("svelte", 18082));
  console.log(JSON.stringify(results, null, 2));
  process.exit(failures ? 1 : 0);
})();
