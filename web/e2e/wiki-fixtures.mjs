// SPDX-License-Identifier: AGPL-3.0-or-later

// Sample data for the wiki screenshots (wiki-shots.mjs). Made-up names on
// example.com/example.net only; nothing here comes from a real server.

const day = (d) => new Date(Date.UTC(2026, 10, 22 - d, 3, 12)).toISOString();

const tls = (domains, extra = {}) => ({ enabled: true, active: true, hsts: true, domains, notAfter: "2027-02-14T00:00:00Z", source: "letsencrypt", certSource: "letsencrypt", trusted: true, ...extra });

const site = (name, o) => ({
  name,
  pool: "default",
  state: "active",
  php: "",
  account: true,
  redirect: null,
  proxy: null,
  node: null,
  python: null,
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  createdAt: day(40),
  updatedAt: day(2),
  ...o,
});

export const sites = [
  site("blog", { type: "php", php: "8.4", domains: ["blog.example.com", "www.blog.example.com"], tls: tls(["blog.example.com", "www.blog.example.com"]) }),
  site("shop", { type: "php", php: "8.3", domains: ["shop.example.com"], tls: tls(["shop.example.com"]) }),
  site("docs", { type: "static", domains: ["docs.example.com"], web: { dir: "public", spa: false, cache: true }, tls: tls(["docs.example.com"]) }),
  site("app", {
    type: "node",
    domains: ["app.example.com"],
    node: { version: "24", workdir: "htdocs", startKind: "npm", start: "start", port: 20000, stopped: false },
    tls: tls(["app.example.com"]),
  }),
  site("api", {
    type: "python",
    domains: ["api.example.com"],
    python: { version: "3.12", workdir: "htdocs", startKind: "wsgi", target: "mysite.wsgi:application", args: ["--workers", "3"], port: 20001, stopped: false },
    tls: tls(["api.example.com"]),
  }),
  site("intranet", { type: "proxy", account: false, domains: ["intranet.example.com"], proxy: { target: "http://10.0.0.5:8080", targetHost: false, insecure: false }, tls: tls(["intranet.example.com"]) }),
  site("old-domain", { type: "redirect", account: false, domains: ["example.net", "www.example.net"], redirect: { target: "https://www.example.com", status: 301, keepPath: true }, tls: tls(["example.net", "www.example.net"]) }),
  site("staging", { type: "php", php: "8.4", domains: ["staging.example.com"], tls: tls([], { active: false, hsts: false, notAfter: null, error: "staging.example.com has no DNS record pointing to this server", failedAt: day(0) }) }),
];


const accounts = [
  { id: 1, username: "kim", role: "admin", disabled: false, totpEnabled: true, passkeys: 2, requireSecondFactor: true, invited: false, invitationExpiresAt: null },
  { id: 2, username: "alex", role: "manager", disabled: false, totpEnabled: true, passkeys: 0, requireSecondFactor: true, invited: false, invitationExpiresAt: null },
  { id: 3, username: "shop-team", role: "user", disabled: false, totpEnabled: false, passkeys: 1, requireSecondFactor: false, invited: false, invitationExpiresAt: null },
  { id: 4, username: "sam", role: "user", disabled: false, totpEnabled: false, passkeys: 0, requireSecondFactor: false, invited: true, invitationExpiresAt: "2026-11-23T18:00:00Z" },
];

const databases = [
  { name: "blog_wp", site: "blog", server: "localhost", users: ["blog_wp"], createdAt: day(40), sizeBytes: 48_300_000 },
  { name: "shop_main", site: "shop", server: "localhost", users: ["shop_app", "shop_report"], createdAt: day(38), sizeBytes: 612_000_000 },
  { name: "staging_wp", site: "staging", server: "localhost", users: ["staging_wp"], createdAt: day(5), sizeBytes: 21_700_000 },
];

const dbServers = [{ name: "localhost", local: true, default: true, engine: "MariaDB", version: "11.8.3", state: "ok", databases: 3, tlsMode: "off", tlsActive: false, checkedAt: day(0) }];

const backupItems = sites
  .filter((s) => s.account)
  .map((s) => ({ kind: "files", site: s.name, snapshotId: "a1b2c3d4", dataAdded: 1_200_000, files: 2400, bytes: 180_000_000 }))
  .concat(databases.map((d) => ({ kind: "db", site: d.site, database: d.name, snapshotId: "e5f6a7b8", dataAdded: 400_000, files: 1, bytes: d.sizeBytes })));

const backups = {
  settings: { version: 3, target: { user: "u123456", host: "u123456.your-storagebox.de", port: 23, path: "hostashi" }, retention: { daily: 7, weekly: 4, monthly: 6 }, windowStart: 2, windowEnd: 5, readParts: 12 },
  publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleExampleExampleExampleExampleExample hostashi-backup",
  hostKey: "SHA256:Ex4mpleHostKeyFingerprintOnlyForTheWikiPictures0",
  ready: true,
  status: { running: false, started: day(0), finished: day(0), ok: true, items: backupItems, lastPrune: day(0), lastCheck: day(3), lastReadCheck: day(3), maintenanceOk: true },
};

const snapshots = [0, 1, 2, 3, 4, 5, 6].map((d) => ({ id: "snap" + d + "abcd", time: day(d), kind: "files" })).concat([{ id: "dbsnap0abc", time: day(0), kind: "db", database: "blog_wp" }]);

const firewall = {
  enabled: true,
  state: { open: [{ proto: "tcp", from: 3000, sources: ["198.51.100.0/24"], note: "Staging preview" }], sshAllow: ["203.0.113.24", "198.51.100.0/24"], panelAllow: [] },
  pending: null,
  rolledBack: null,
  clientIp: "203.0.113.24",
  confirmWithin: 60,
};

const node = { installed: [{ major: "24", version: "24.11.1" }], last: "", state: "idle", log: [], releases: [{ major: "24", version: "24.11.1", codename: "Krypton", end: "2028-04-30" }] };

const python = {
  installed: [
    { line: "3.13", version: "3.13.9", build: "20261111" },
    { line: "3.12", version: "3.12.12", build: "20261014" },
  ],
  last: "",
  state: "idle",
  log: [],
  releases: [
    { line: "3.11", version: "3.11.14", build: "20261111", end: "2027-10-31" },
    { line: "3.12", version: "3.12.12", build: "20261111", end: "2028-10-31" },
    { line: "3.13", version: "3.13.9", build: "20261111", end: "2029-10-31" },
    { line: "3.14", version: "3.14.0", build: "20261111", end: "2030-10-31" },
  ],
};

const me = { username: "kim", role: "admin", secondFactor: true, secondFactorRequired: true, clientIp: "203.0.113.24" };

const routes = [
  ["GET", /^\/auth\/me$/, () => me],
  ["GET", /^\/setup$/, () => ({ required: false })],
  ["GET", /^\/sites$/, () => ({ sites })],
  ["GET", /^\/sites\/([^/]+)$/, (m) => sites.find((s) => s.name === m[1])],
  ["GET", /^\/pools$/, () => ({ pools: [{ name: "default", path: "/home", default: true }] })],
  ["GET", /^\/php\/versions$/, () => ({ installed: ["8.3", "8.4"], supported: ["8.3", "8.4", "8.5"], legacy: ["7.4", "8.1", "8.2"], distro: "8.4" })],
  ["GET", /^\/parked-domains$/, () => ({ parkedDomains: [] })],
  ["GET", /^\/status-pages$/, () => ({ pages: [] })],
  ["GET", /^\/node$/, () => node],
  ["GET", /^\/python$/, () => python],
  ["GET", /^\/users$/, () => ({ users: accounts })],
  ["GET", /^\/databases$/, () => ({ databases })],
  ["GET", /^\/db-servers$/, () => ({ servers: dbServers })],
  ["GET", /^\/backups$/, () => backups],
  ["GET", /^\/firewall$/, () => firewall],
  ["GET", /^\/dbtool$/, () => ({ available: true, minutes: [30, 120] })],
  ["GET", /^\/sites\/[^/]+\/app\/env$/, () => ({ names: ["DATABASE_URL", "SESSION_SECRET"] })],
  ["GET", /^\/sites\/[^/]+\/app$/, () => ({ state: "active", subState: "running", restarts: 0, usedBytes: 86_000_000, since: day(1) })],
  ["GET", /^\/sites\/[^/]+\/backups$/, () => ({ snapshots })],
  ["GET", /^\/sites\/[^/]+\/headers$/, () => ({
    headers: { noSniff: true, frameOptions: "SAMEORIGIN", referrerPolicy: "strict-origin-when-cross-origin" },
    defaults: { noSniff: true, frameOptions: "SAMEORIGIN", referrerPolicy: "strict-origin-when-cross-origin" },
    referrerPolicies: ["no-referrer", "same-origin", "strict-origin", "strict-origin-when-cross-origin"],
    permissionsPolicyStrict: "camera=(), microphone=(), geolocation=()",
    maxValueBytes: 4096,
  })],
  ["GET", /^\/sites\/[^/]+\/real-ip$/, () => ({ realIp: {}, headers: ["X-Forwarded-For", "X-Real-IP"], cloudflare: [], cloudflareDate: "2026-11-16", maxRanges: 64 })],
  ["GET", /^\/sites\/([^/]+)\/tls\/certificate$/, (m) => {
    const t = sites.find((x) => x.name === m[1])?.tls;
    return { certificate: t?.active ? { domains: t.domains, issuer: "Let's Encrypt (E7)", notBefore: "2026-11-16T03:12:00Z", notAfter: t.notAfter, fingerprint: "4f1c9a0b".repeat(8), trusted: true } : null };
  }],
];

/** answer returns { status?, body } for a request, or undefined. */
export function answer(method, path, query) {
  for (const [m, re, fn] of routes) {
    if (m !== method) continue;
    const match = path.match(re);
    if (match) {
      const body = fn(match, query);
      return body === undefined ? undefined : { body };
    }
  }
  return undefined;
}
