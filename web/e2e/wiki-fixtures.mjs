// SPDX-License-Identifier: AGPL-3.0-or-later

// Sample data for the wiki screenshots (wiki-shots.mjs). Made-up names on
// example.com/example.net only; nothing here comes from a real server.

const day = (d) => new Date(Date.UTC(2026, 10, 22 - d, 3, 12)).toISOString();

const tls = (domains, extra = {}) => ({ enabled: true, active: true, hsts: true, domains, notAfter: "2027-02-14T00:00:00Z", source: "letsencrypt", certSource: "letsencrypt", trusted: true, ...extra });

const site = (name, o) => ({
  name,
  title: "",
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
  site("blog", {
    type: "php",
    php: "8.4",
    domains: ["blog.example.com", "www.blog.example.com"],
    redirectDomains: ["blog.example.net"],
    pendingDomains: null,
    www: { mode: "to-non-www", pairs: [{ bare: "blog.example.com", www: "www.blog.example.com" }], missing: [] },
    tls: tls(["blog.example.com", "www.blog.example.com", "blog.example.net"]),
  }),
  site("shop", { title: "Müller Online-Shop", type: "php", php: "8.3", domains: ["shop.example.com"], www: { mode: "off", pairs: [], missing: ["www.shop.example.com"] }, tls: tls(["shop.example.com"], { challenge: "dns" }) }),
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
  // A wildcard below its own domain (ADR-0075), not yet on HTTPS.
  site("tenants", {
    type: "proxy",
    account: false,
    domains: ["apps.example.net", "*.apps.example.net"],
    servedElsewhere: ["status.apps.example.net"],
    wildcardsWithoutDns: ["*.apps.example.net"],
    proxy: { target: "http://10.0.0.7:8080", targetHost: false, insecure: false },
    tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null, source: "letsencrypt", certSource: "", trusted: false, challenge: "http" },
  }),
  site("old-domain", { type: "redirect", account: false, domains: ["example.net", "www.example.net"], redirect: { target: "https://www.example.com", status: 301, keepPath: true }, tls: tls(["example.net", "www.example.net"]) }),
  // A domain change that waits for its certificate (ADR-0068).
  site("news", {
    type: "php",
    php: "8.4",
    domains: ["news.example.com", "www.news.example.com"],
    redirectDomains: [],
    pendingDomains: {
      domains: ["news.example.org", "www.news.example.org"],
      redirectDomains: ["news.example.com", "www.news.example.com"],
      requestedAt: day(0),
      error: "acme: error: 400 :: urn:ietf:params:acme:error:dns :: DNS problem: NXDOMAIN looking up A for www.news.example.org",
      failedAt: day(0),
    },
    www: { mode: "to-non-www", pairs: [{ bare: "news.example.com", www: "www.news.example.com" }], missing: [] },
    tls: tls(["news.example.com", "www.news.example.com"]),
  }),
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
  settings: { version: 3, target: { user: "backup", host: "backup.example.com", port: 22, path: "hostashi/web1" }, retention: { hourly: 0, daily: 7, weekly: 4, monthly: 6, yearly: 0 }, intervalHours: 24, windowStart: 2, windowEnd: 5, readParts: 12, deletedSiteDays: 0 },
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

// fail2ban's jails (ADR-0078): a customer locked out of a staging copy,
// and an address the repeat-offender jail blocks without end.
const inHours = (h) => new Date(Date.parse("2026-11-22T18:00:00Z") + h * 3_600_000).toISOString();
const fail2ban = {
  jails: [
    { name: "sshd", running: true, currentlyFailed: 2, totalFailed: 318, currentlyBanned: 1, totalBanned: 41 },
    { name: "hostashi-panel", running: true, currentlyFailed: 0, totalFailed: 3, currentlyBanned: 0, totalBanned: 0 },
    { name: "hostashi-site-auth", running: true, currentlyFailed: 1, totalFailed: 24, currentlyBanned: 1, totalBanned: 2 },
    { name: "recidive", running: true, currentlyFailed: 0, totalFailed: 9, currentlyBanned: 1, totalBanned: 3 },
  ],
  addresses: [
    { address: "198.51.100.23", jails: ["hostashi-site-auth"], since: inHours(-0.2), until: inHours(0.8) },
    { address: "192.0.2.77", jails: ["sshd"], since: inHours(-0.4), until: inHours(0.6) },
    { address: "2001:db8:5::9", jails: ["recidive"], since: inHours(-30), until: inHours(138) },
  ],
  total: 3,
  page: 1,
  perPage: 50,
  truncated: false,
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

// The scope catalog of internal/auth/scopes.go, as an admin gets it.
const scopes = [
  { name: "audit:read", description: "Read and export the audit log of the panel (every account, site, action and the IP addresses involved)", highRisk: false },
  { name: "backups:read", description: "Read backup settings, status and snapshots", highRisk: false },
  { name: "backups:restore", description: "Restore a site's backups into its home", highRisk: true },
  { name: "backups:write", description: "Change backup settings, confirm the target's host key, start a backup", highRisk: false },
  { name: "cron:read", description: "List the cron jobs of sites with their last and next runs", highRisk: false },
  { name: "cron:write", description: "Create, change, pause, run and delete cron jobs (they run commands as the site user)", highRisk: true },
  { name: "databases:delete", description: "Delete databases and their data", highRisk: true },
  { name: "databases:read", description: "List databases and their users", highRisk: false },
  { name: "databases:write", description: "Create databases and users, set passwords, remove users", highRisk: false },
  { name: "db_servers:write", description: "Add, check and remove external database servers (stores their administrator password)", highRisk: true },
  { name: "doctor:read", description: "Run the read-only checks of hostashi doctor and read their results", highRisk: false },
  { name: "files:read", description: "List and download the files of sites", highRisk: false },
  { name: "files:write", description: "Upload, change, move, copy and delete the files of sites (this changes the sites' code)", highRisk: true },
  { name: "firewall:read", description: "Read the firewall's opened ports and the allowlists for SSH and the panel", highRisk: false },
  { name: "firewall:write", description: "Open ports and set the allowlists for SSH and the panel (a wrong list locks out until the rollback)", highRisk: true },
  { name: "limits:read", description: "Read the limit presets and the resource limits and disk use of sites", highRisk: false },
  { name: "limits:write", description: "Change the limit presets and the resource limits of sites", highRisk: false },
  { name: "git:write", description: "Set the Git repository, token, deploy key and build commands of sites (the commands run as the site user)", highRisk: true },
  { name: "logs:read", description: "Read and follow the access, error and PHP logs of sites (they hold visitors' IP addresses)", highRisk: false },
  { name: "metrics:read", description: "Read the Prometheus metrics endpoint", highRisk: false },
  { name: "migrations:read", description: "List uploaded CloudPanel scan reports and read their migration plans", highRisk: false },
  { name: "migrations:write", description: "Upload and delete CloudPanel scan reports", highRisk: false },
  { name: "node:install", description: "Install the Node.js LTS builds Hostashi pins from nodejs.org (downloads and unpacks them as root, checked against a fixed SHA-256)", highRisk: true },
  { name: "objectcache:read", description: "Read the object cache of sites: size, state and memory use", highRisk: false },
  { name: "objectcache:write", description: "Turn the object cache of sites on or off, flush it, and (admins and managers) change its size", highRisk: false },
  { name: "pagecache:purge", description: "Empty the page cache of sites, and nothing else (for a CMS that purges on save)", highRisk: false },
  { name: "pages:read", description: "List maintenance and parking pages and their files", highRisk: false },
  { name: "pages:write", description: "Upload, replace and delete maintenance and parking pages", highRisk: false },
  { name: "php:install", description: "Install more PHP versions from sury.org (runs apt as root with a fixed package list)", highRisk: true },
  { name: "python:install", description: "Install the CPython builds Hostashi pins from python-build-standalone (downloads and unpacks them as root, checked against a fixed SHA-256)", highRisk: true },
  { name: "services:read", description: "Read the state and restarts of the server's services and the dashboard's CPU, memory, disk and network figures", highRisk: false },
  { name: "services:write", description: "Start, stop, restart and reload the server's services where the service catalog allows it", highRisk: true },
  { name: "sites.maintenance:write", description: "Switch the maintenance mode of sites and park domains", highRisk: false },
  { name: "sites.snippets:write", description: "Change the custom nginx snippets of sites (checked against an allowlist)", highRisk: true },
  { name: "sites:delete", description: "Delete sites and their files", highRisk: true },
  { name: "sites:read", description: "List and read sites", highRisk: false },
  { name: "sites:write", description: "Create and change sites", highRisk: false },
  { name: "ssh:read", description: "List the SSH keys of sites with their fingerprints and access", highRisk: false },
  { name: "ssh:write", description: "Add, change and delete SSH keys of sites (a key with shell access is a login as the site user)", highRisk: true },
  { name: "system:read", description: "Read the server's hostname, time zone, clock, uptime and whether a reboot is pending", highRisk: false },
  { name: "system:write", description: "Change the server's time zone and hostname and reboot the server", highRisk: true },
]

const tokens = [
  { id: 4, name: "Deploy from CI", prefix: "hst_q7Lx2B", scopes: ["sites:read", "files:write", "pagecache:purge"], createdAt: day(20), expiresAt: "2027-02-20T00:00:00Z", neverExpires: false, lastUsedAt: day(0), lastUsedIp: "198.51.100.40", sites: ["blog"] },
  { id: 3, name: "Uptime monitoring", prefix: "hst_Mf09aZ", scopes: ["metrics:read"], createdAt: day(60), expiresAt: null, neverExpires: true, lastUsedAt: day(0), lastUsedIp: "203.0.113.80", sites: null },
  { id: 2, name: "hostashi CLI on laptop", prefix: "hst_Rk4pTe", scopes: ["sites:read", "sites:write", "databases:read", "backups:read", "services:read", "system:read"], createdAt: day(80), expiresAt: "2026-12-10T00:00:00Z", neverExpires: false, lastUsedAt: day(3), lastUsedIp: "203.0.113.24", sites: null },
  { id: 1, name: "Old script", prefix: "hst_Zp1cVb", scopes: ["sites:read"], createdAt: day(200), expiresAt: "2027-05-01T00:00:00Z", neverExpires: false, lastUsedAt: day(150), lastUsedIp: "203.0.113.24", revokedAt: day(120), sites: null },
];

const gitBase = {
  configured: false, url: "", branch: "main", deployDir: "htdocs", shared: [], commands: [], keep: 5, buildTimeoutMinutes: 15, submodules: false,
  auth: "none", tokenUser: "", hasToken: false, publicKey: "", hostKey: "", hostKeyFingerprint: "", webhook: false, webhookPath: "", updatedAt: "",
};

const git = {
  app: {
    ...gitBase,
    configured: true,
    url: "git@github.com:example/app.git",
    shared: [".env", "uploads"],
    commands: ["npm ci", "npm run build"],
    auth: "ssh",
    publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK8qX2mJt0vYz3cN5wR7uE1hB4dF6gL9pS2aQ8xV0nMc hostashi-app",
    hostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
    hostKeyFingerprint: "SHA256:+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU",
    webhook: true,
    webhookPath: "/api/v1/hooks/deploy/3f9c2a7e1b5d48c6a0e9f7b2d4c61a8e",
    updatedAt: day(3),
  },
};

const at = (d, h, m) => new Date(Date.UTC(2026, 10, 22 - d, h, m)).toISOString();

const deploys = {
  app: [
    { id: 14, release: "14", kind: "deploy", source: "webhook", commit: "9b1e4c7a2f6d3e8b5a0c1d2e3f4a5b6c7d8e9f01", author: "Alex", message: "Show the order date in the list", state: "succeeded", error: "", createdAt: at(0, 9, 41), startedAt: at(0, 9, 41), finishedAt: at(0, 9, 43) },
    { id: 13, release: "13", kind: "deploy", source: "webhook", commit: "4d2a9e1c7b3f5a8e6d0c2b4a6e8f1a3c5e7b9d02", author: "Alex", message: "Update dependencies", state: "failed", error: "a step of the deploy failed", createdAt: at(1, 16, 5), startedAt: at(1, 16, 5), finishedAt: at(1, 16, 7) },
    { id: 12, release: "12", kind: "deploy", source: "panel", commit: "e7c3b1a9d5f2e8c4a6b0d2f4e6a8c1b3d5f7e9a3", author: "Kim", message: "New start page", state: "succeeded", error: "", createdAt: at(4, 11, 20), startedAt: at(4, 11, 20), finishedAt: at(4, 11, 22) },
    { id: 11, release: "11", kind: "deploy", source: "api", commit: "1a3c5e7b9d2f4a6c8e0b2d4f6a8c0e2b4d6f8a04", author: "Kim", message: "First version", state: "succeeded", error: "", createdAt: at(9, 8, 2), startedAt: at(9, 8, 2), finishedAt: at(9, 8, 5) },
  ],
};

const deployLogText = [
  "Fetching main from git@github.com:example/app.git",
  "Commit 9b1e4c7 by Alex: Show the order date in the list",
  "Wrote 214 files to .deploy/releases/14",
  "Linked shared paths: .env uploads",
  "$ npm ci",
  "added 312 packages in 9s",
  "$ npm run build",
  "> app@1.4.0 build",
  "> vite build",
  "✓ 87 modules transformed.",
  "dist/index.html  0.62 kB",
  "✓ built in 2.31s",
  "",
].join("\n");

const sha = (c) => c.repeat(64);

// The malware scan of blog: one file flagged last night, one in the
// quarantine, a false positive and a file that is gone (ADR-0064).
const siteScan = {
  engine: "on",
  schedule: "weekly",
  nextScheduled: "2026-11-27T02:37:00Z",
  runs: [
    { id: 9, source: "schedule", state: "succeeded", error: "", found: 3, newFound: 1, dropped: 0, createdAt: at(0, 1, 37), startedAt: at(0, 1, 37), finishedAt: at(0, 1, 44) },
    { id: 8, source: "panel", state: "succeeded", error: "", found: 2, newFound: 2, dropped: 0, createdAt: at(2, 14, 3), startedAt: at(2, 14, 3), finishedAt: at(2, 14, 9) },
    { id: 7, source: "schedule", state: "succeeded", error: "", found: 0, newFound: 0, dropped: 0, createdAt: at(7, 1, 37), startedAt: at(7, 1, 37), finishedAt: at(7, 1, 43) },
  ],
  findings: [
    { id: 21, path: "htdocs/wp-content/uploads/2026/11/cache.php", signature: "Php.Webshell.Generic-9952106-0", sha256: sha("4"), size: 18_432, state: "open", firstSeen: at(0, 1, 44), lastSeen: at(0, 1, 44), decidedAt: "" },
    { id: 18, path: "htdocs/wp-content/plugins/old-gallery/upload.php", signature: "Php.Malware.Mailer-6", sha256: sha("7"), size: 5_210, state: "quarantined", firstSeen: at(2, 14, 9), lastSeen: at(2, 14, 9), decidedAt: at(2, 14, 30) },
    { id: 17, path: "htdocs/tools/eicar-test.txt", signature: "Eicar-Signature", sha256: sha("2"), size: 68, state: "ignored", firstSeen: at(2, 14, 9), lastSeen: at(0, 1, 44), decidedAt: at(2, 14, 31) },
    { id: 12, path: "htdocs/tmp/x.php", signature: "Php.Trojan.Agent-1", sha256: sha("9"), size: 912, state: "gone", firstSeen: at(14, 1, 40), lastSeen: at(14, 1, 40), decidedAt: "" },
  ],
};

const me = { username: "kim", role: "admin", secondFactor: true, secondFactorRequired: true, clientIp: "203.0.113.24" };

const homeFiles = [
  { name: ".ssh", type: "dir", size: 4096, modified: day(30), mode: "0700" },
  { name: "htdocs", type: "dir", size: 4096, modified: day(1), mode: "0755" },
  { name: "logs", type: "dir", size: 4096, modified: day(0), mode: "0755" },
  { name: "backup.tar.gz", type: "file", size: 48_000_000, modified: day(3), mode: "0600" },
];

// Application profiles (ADR-0069), as GET /api/v1/profiles lists them.
const wpRules = { configFiles: true, noPhpDirs: ["wp-content/uploads"], denyFiles: ["xmlrpc.php"], limitFiles: ["wp-login.php"] };
const wpChecklist = ["xmlrpc.php is blocked; allow it in the hardening settings if an app or plugin needs it.", "wp-login.php is rate limited per address."];
// The server block preview of blog (ADR-0069), as web.Preview renders it
// for its WordPress rules.
const serverBlock = String.raw`# Preview of the server block of site blog (read only).
# Paths below /placeholder are placeholders; the server uses its own.

# Generated by hostashi for site blog. Do not edit.

# Rate limit of the site's hardening rules (ADR-0017): per client address.
limit_req_zone $binary_remote_addr zone=hostashi_limit_626c6f67:1m rate=12r/m;
server {
    listen 80;
    listen [::]:80;
    listen unix:/placeholder/run/control.sock;
    server_name blog.example.com;
    access_log /placeholder/log/blog/access.log;
    access_log /placeholder/log/blog/traffic.log hostashi_traffic buffer=32k flush=5s;
    error_log /placeholder/log/blog/error.log warn;

    # HTTP-01 challenges of the panel's ACME client.
    location ^~ /.well-known/acme-challenge/ {
        root /placeholder/acme;
        default_type text/plain;
        try_files $uri =404;
    }

    # Health check of the agent after a reload, only through its Unix socket.
    # It never reaches the site's application, so a broken application does
    # not block configuration changes of other sites.
    location = /.hostashi/health {
        if ($remote_addr != "unix:") {
            return 404;
        }
        access_log off;
        default_type text/plain;
        return 200 "blog";
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name blog.example.com;
    access_log /placeholder/log/blog/access.log;
    access_log /placeholder/log/blog/traffic.log hostashi_traffic buffer=32k flush=5s;
    error_log /placeholder/log/blog/error.log warn;
    ssl_certificate /placeholder/certs/blog/fullchain.pem;
    ssl_certificate_key /placeholder/certs/blog/privkey.pem;
    # Headers here apply to every response of the site (ADR-0056); a
    # location with its own add_header repeats them.
    add_header Strict-Transport-Security "max-age=31536000" always;
    root /placeholder/home/blog/htdocs;
    # Follows the site's post_max_size (ADR-0049).
    client_max_body_size 64m;

    # Hidden files stay hidden, except /.well-known (RFC 8615).
    location ~ /\.(?!well-known/) {
        return 404;
    }

    # Dumps, backups, logs and editor copies are never served (ADR-0017).
    location ~* "(?:\.sql|\.sql\.gz|\.sql\.bz2|\.sql\.xz|\.sql\.zst|\.dump|\.bak|\.backup|\.old|\.orig|\.save|\.swp|\.swo|~|\.log|\.env|\.ini)$" {
        return 404;
    }

    # PHP never runs in these folders (ADR-0017).
    location ~* "^/(?:wp-content/uploads)/.*\.(?:php|phtml|phar)$" {
        return 404;
    }

    # At most 12 requests per minute per address (ADR-0017).
    location = /wp-login.php {
        limit_req zone=hostashi_limit_626c6f67 burst=10 nodelay;
        limit_req_status 429;
        try_files $fastcgi_script_name =404;
        fastcgi_pass unix:/placeholder/php/blog.sock;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $realpath_root$fastcgi_script_name;
        fastcgi_param DOCUMENT_ROOT $realpath_root;
        fastcgi_param SCRIPT_NAME $fastcgi_script_name;
        fastcgi_param QUERY_STRING $query_string;
        fastcgi_param REQUEST_METHOD $request_method;
        fastcgi_param CONTENT_TYPE $content_type;
        fastcgi_param CONTENT_LENGTH $content_length;
        fastcgi_param REQUEST_URI $request_uri;
        fastcgi_param DOCUMENT_URI $document_uri;
        fastcgi_param SERVER_PROTOCOL $server_protocol;
        fastcgi_param REQUEST_SCHEME $scheme;
        fastcgi_param HTTPS $https if_not_empty;
        fastcgi_param GATEWAY_INTERFACE CGI/1.1;
        fastcgi_param SERVER_SOFTWARE nginx;
        fastcgi_param REMOTE_ADDR $remote_addr;
        fastcgi_param REMOTE_PORT $remote_port;
        fastcgi_param SERVER_ADDR $server_addr;
        fastcgi_param SERVER_PORT $server_port;
        fastcgi_param SERVER_NAME $server_name;
        # HTTP/3 requests have no Host header for nginx to pass on.
        fastcgi_param HTTP_HOST $host;
        fastcgi_param REDIRECT_STATUS 200;
        # httpoxy: never pass a client's Proxy header on as HTTP_PROXY.
        fastcgi_param HTTP_PROXY "";
        # Follows the site's max_execution_time (ADR-0049).
        fastcgi_read_timeout 70s;
    }
    index index.php index.html index.htm;

    location / {
        try_files $uri $uri/ /index.php$is_args$args;
    }

    location ~ \.php$ {
        try_files $fastcgi_script_name =404;
        fastcgi_pass unix:/placeholder/php/blog.sock;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $realpath_root$fastcgi_script_name;
        fastcgi_param DOCUMENT_ROOT $realpath_root;
        fastcgi_param SCRIPT_NAME $fastcgi_script_name;
        fastcgi_param QUERY_STRING $query_string;
        fastcgi_param REQUEST_METHOD $request_method;
        fastcgi_param CONTENT_TYPE $content_type;
        fastcgi_param CONTENT_LENGTH $content_length;
        fastcgi_param REQUEST_URI $request_uri;
        fastcgi_param DOCUMENT_URI $document_uri;
        fastcgi_param SERVER_PROTOCOL $server_protocol;
        fastcgi_param REQUEST_SCHEME $scheme;
        fastcgi_param HTTPS $https if_not_empty;
        fastcgi_param GATEWAY_INTERFACE CGI/1.1;
        fastcgi_param SERVER_SOFTWARE nginx;
        fastcgi_param REMOTE_ADDR $remote_addr;
        fastcgi_param REMOTE_PORT $remote_port;
        fastcgi_param SERVER_ADDR $server_addr;
        fastcgi_param SERVER_PORT $server_port;
        fastcgi_param SERVER_NAME $server_name;
        # HTTP/3 requests have no Host header for nginx to pass on.
        fastcgi_param HTTP_HOST $host;
        fastcgi_param REDIRECT_STATUS 200;
        # httpoxy: never pass a client's Proxy header on as HTTP_PROXY.
        fastcgi_param HTTP_PROXY "";
        # Follows the site's max_execution_time (ADR-0049).
        fastcgi_read_timeout 70s;
    }
}

# www redirect (ADR-0065): www.blog.example.com goes to blog.example.com.
server {
    listen 80;
    listen [::]:80;
    server_name www.blog.example.com;
    access_log /placeholder/log/blog/access.log;
    access_log /placeholder/log/blog/traffic.log hostashi_traffic buffer=32k flush=5s;
    error_log /placeholder/log/blog/error.log warn;

    location ^~ /.well-known/acme-challenge/ {
        root /placeholder/acme;
        default_type text/plain;
        try_files $uri =404;
    }

    location / {
        return 301 https://blog.example.com$request_uri;
    }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name www.blog.example.com;
    access_log /placeholder/log/blog/access.log;
    access_log /placeholder/log/blog/traffic.log hostashi_traffic buffer=32k flush=5s;
    error_log /placeholder/log/blog/error.log warn;
    ssl_certificate /placeholder/certs/blog/fullchain.pem;
    ssl_certificate_key /placeholder/certs/blog/privkey.pem;
    add_header Strict-Transport-Security "max-age=31536000" always;

    location / {
        return 301 https://blog.example.com$request_uri;
    }
}
`;
const profileList = [
  { name: "static", title: "Static site", type: "static", hardening: { configFiles: true }, builtin: true },
  { name: "php", title: "Generic PHP", type: "php", hardening: { configFiles: true }, pageCache: "php", builtin: true },
  { name: "wordpress", title: "WordPress", type: "php", minPhp: "7.4", hardening: wpRules, pageCache: "wordpress", checklist: wpChecklist, builtin: true },
  { name: "woocommerce", title: "WooCommerce", type: "php", minPhp: "7.4", hardening: wpRules, pageCache: "woocommerce", checklist: wpChecklist, builtin: true },
  { name: "laravel", title: "Laravel", type: "php", minPhp: "8.2", webDir: "public", hardening: { configFiles: true }, builtin: true },
  { name: "symfony", title: "Symfony", type: "php", minPhp: "8.2", webDir: "public", hardening: { configFiles: true }, builtin: true },
  { name: "shopware6", title: "Shopware 6", type: "php", minPhp: "8.2", webDir: "public", hardening: { configFiles: true }, builtin: true },
  { name: "typo3", title: "TYPO3", type: "php", minPhp: "8.2", webDir: "public", hardening: { configFiles: true, noPhpDirs: ["fileadmin", "typo3temp"] }, builtin: true },
  { name: "drupal", title: "Drupal", type: "php", minPhp: "8.3", webDir: "web", hardening: { configFiles: true, noPhpDirs: ["sites/default/files"] }, builtin: true },
  // An own profile: a copy of WordPress with readme.html blocked as well.
  {
    name: "agency-wp",
    title: "Agency WordPress",
    type: "php",
    minPhp: "8.2",
    hardening: { ...wpRules, denyFiles: ["xmlrpc.php", "readme.html"] },
    pageCache: "wordpress",
    checklist: ["Install the agency's plugin set from the shared repository."],
    builtin: false,
  },
];
const allProfiles = {
  profiles: profileList,
  configFileSuffixes: [".sql", ".sql.gz", ".sql.bz2", ".sql.xz", ".sql.zst", ".dump", ".bak", ".backup", ".old", ".orig", ".save", ".swp", ".swo", "~", ".log", ".env", ".ini"],
  limitPerMinute: 12,
  limitBurst: 10,
  maxPaths: 20,
};
const profileChoices = {
  profiles: profileList.filter((p) => p.type === "php"),
  configFileSuffixes: [".sql", ".sql.gz", ".sql.bz2", ".sql.xz", ".sql.zst", ".dump", ".bak", ".backup", ".old", ".orig", ".save", ".swp", ".swo", "~", ".log", ".env", ".ini"],
  limitPerMinute: 12,
  limitBurst: 10,
  maxPaths: 20,
};

// Cron jobs of blog: WP-Cron every 5 minutes and a nightly export that
// writes some output (ADR-0028 Addendum 1). Times are Unix seconds.
const unix = (d, h = 0, min = 0) => Date.UTC(2026, 10, 22 - d, h, min) / 1000;
const cronJobs = [
  {
    id: 1, schedule: "*/5 * * * *", command: "php htdocs/wp-cron.php", timeoutSec: 0, nice: null, paused: false, createdAt: unix(9), updatedAt: unix(9),
    status: { running: false, result: "success", exitStatus: 0, lastStart: unix(0, 16, 55), lastEnd: unix(0, 16, 55) + 2, next: unix(0, 17, 0) },
  },
  {
    id: 2, schedule: "@daily", command: "php htdocs/wp-content/export.php --since=yesterday", timeoutSec: 0, nice: null, paused: false, createdAt: unix(9), updatedAt: unix(9),
    status: { running: false, result: "exit-code", exitStatus: 1, lastStart: unix(0), lastEnd: unix(0) + 14, next: unix(-1) },
  },
];
const cronOutputs = {
  1: { ran: true, lines: [], truncated: false },
  2: {
    ran: true,
    truncated: false,
    lines: [
      "Exporting orders since 2026-11-21 …",
      "  143 orders, 512 line items",
      "Writing exports/orders-2026-11-21.csv",
      "PHP Warning:  fopen(exports/orders-2026-11-21.csv): Failed to open stream: No such file or directory in /home/blog/htdocs/wp-content/export.php on line 88",
      "Export failed: cannot write exports/orders-2026-11-21.csv",
    ],
  },
};

// DNS accounts for DNS-01 certificates (ADR-0076).
const dnsProviders = [
  { id: "cloudflare", name: "Cloudflare", fields: [{ name: "apiToken", label: "API token", secret: true, optional: false }], hint: "An API token with Zone: Read and DNS: Edit on the needed zones only, never the global API key." },
  { id: "hetzner", name: "Hetzner", fields: [{ name: "apiToken", label: "API token (Hetzner Cloud)", secret: true, optional: false }], hint: "A read/write token of a separate Hetzner Cloud project that holds only DNS zones: a token of a project with servers can delete them." },
  {
    id: "inwx",
    name: "INWX",
    fields: [
      { name: "username", label: "Username", secret: false, optional: false },
      { name: "password", label: "Password", secret: true, optional: false },
      { name: "sharedSecret", label: "Two-factor shared secret", secret: true, optional: true },
    ],
    hint: "A sub-user with DNS rights only, if your account offers one.",
  },
];
const dnsAccounts = [
  { id: 1, label: "INWX main", provider: "inwx", zones: ["example.com", "acme.example.net"], settings: { username: "hostashi-dns" }, state: "ok", checkedAt: day(0), createdAt: day(5), updatedAt: day(0) },
  { id: 2, label: "Customer Cloudflare", provider: "cloudflare", zones: ["mueller-shop.example"], settings: {}, state: "error", lastError: "mueller-shop.example: 403 Forbidden: the token lacks Zone:DNS:Edit", checkedAt: day(1), createdAt: day(2), updatedAt: day(1) },
  { id: 3, label: "Hetzner", provider: "hetzner", zones: ["example.org"], settings: {}, state: "unchecked", createdAt: day(0), updatedAt: day(0) },
];

const routes = [
  ["GET", /^\/sites\/blog\/cron$/, () => ({ jobs: cronJobs, statusKnown: true })],
  ["GET", /^\/sites\/[^/]+\/cron$/, () => ({ jobs: [], statusKnown: true })],
  ["GET", /^\/sites\/blog\/cron\/(\d+)\/output$/, (m) => cronOutputs[m[1]]],
  ["GET", /^\/sites\/([^/]+)\/object-cache$/, (m) => ({
    enabled: true,
    memoryMb: 64,
    minMb: 32,
    maxMb: 1024,
    memoryLimitBytes: 1024 * 1024 * 1024,
    socket: `/run/hostashi-valkey/${m[1]}/valkey.sock`,
    status: { state: "active", paused: false, usedBytes: 8 * 1024 * 1024 },
  })],
  ["GET", /^\/sites\/([^/]+)\/page-cache$/, () => ({
    enabled: true,
    preset: "wordpress",
    minutes: 10,
    sizeMB: 256,
    exclude: ["/members*"],
    choices: { presets: ["wordpress", "woocommerce", "php"], minutes: [1, 5, 10, 15, 30, 60], sizesMB: [64, 128, 256, 512, 1024, 2048, 4096], maxExclude: 50 },
  })],
  ["GET", /^\/sites\/([^/]+)\/files$/, () => ({ path: "", entries: homeFiles, truncated: false, unnamed: 0 })],
  [
    "POST",
    /^\/sites\/([^/]+)\/permissions\/reset$/,
    () => ({ checked: 18_342, changed: 211, skipped: 1, skippedPaths: [{ path: "htdocs/wp-content/uploads/nfs", reason: "on another file system" }] }),
  ],
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
  ["GET", /^\/tokens$/, () => ({ tokens })],
  ["GET", /^\/scopes$/, () => ({ scopes })],
  ["GET", /^\/auth\/passkeys$/, () => ({ passkeys: [] })],
  ["GET", /^\/settings\/api-ip-allowlist$/, () => ({ entries: ["198.51.100.0/24", "203.0.113.24/32"] })],
  ["POST", /^\/tokens$/, () => ({ ...tokens[0], id: 5, name: "Backup check", scopes: ["backups:read"], sites: null, lastUsedAt: null, lastUsedIp: "", token: "hst_8bXk2mQv7RtLp0wZ4nYc1sDf6gHj9aEuKo3iTq5Vx2M", warnings: [] })],
  ["GET", /^\/account\/email$/, () => ({ email: "kim@example.com", pending: "", mail_enabled: true })],
  ["GET", /^\/account\/notifications$/, () => ({ mail_enabled: true, email: "kim@example.com", categories: [{ id: "backups", enabled: true }, { id: "certificates", enabled: true }, { id: "services", enabled: true }] })],
  ["GET", /^\/users$/, () => ({ users: accounts })],
  ["GET", /^\/databases$/, () => ({ databases })],
  ["GET", /^\/db-servers$/, () => ({ servers: dbServers })],
  ["GET", /^\/backups$/, () => backups],
  ["GET", /^\/firewall$/, () => firewall],
  ["GET", /^\/firewall\/bans$/, () => fail2ban],
  ["GET", /^\/dbtool$/, () => ({ available: true, minutes: [30, 120] })],
  ["GET", /^\/malware-scan$/, () => ({ state: "on", version: "1.4.6+dfsg-1", signatures: at(0, 13, 12), error: "", realtime: "off", realtimeActive: false, memTotal: 8 * 1024 ** 3 })],
  ["GET", /^\/sites\/[^/]+\/malware-scan$/, () => siteScan],
  ["POST", /^\/sites\/[^/]+\/malware-scans$/, () => ({ id: 42, source: "panel", state: "queued", error: "", found: 0, newFound: 0, dropped: 0, createdAt: at(0, 18, 0), startedAt: "", finishedAt: "", paths: ["htdocs", "backup.tar.gz"] })],
  ["GET", /^\/system$/, () => ({ hostname: "web1", transientHostname: "web1", timezone: "Europe/Berlin", time: "2026-11-22T18:00:00Z", uptimeSeconds: 1_234_567, ntpSynchronized: true, rebootRequired: false, cloudInit: false, updates: { installed: "0.13.0", available: "0.13.0", update: false, auto: false, listedAt: "2026-11-22T06:12:00Z", runtimes: "20261122050000", runtimesAuto: true } })],
  ["GET", /^\/system\/timezones$/, () => ({ zones: ["Europe/Berlin", "UTC"] })],
  ["GET", /^\/settings\/logs$/, () => ({ days: 14, minDays: 1, maxDays: 365, defaultDays: 14 })],
  ["GET", /^\/sites\/([^/]+)\/git$/, (m) => git[m[1]] ?? gitBase],
  ["GET", /^\/sites\/([^/]+)\/deploys$/, (m) => deploys[m[1]] ?? []],
  ["GET", /^\/sites\/[^/]+\/deploys\/\d+\/log$/, () => ({ log: deployLogText, truncated: false })],
  ["POST", /^\/sites\/[^/]+\/git\/hostkey\/scan$/, () => ({ key: git.app.hostKey, fingerprint: git.app.hostKeyFingerprint })],
  ["POST", /^\/sites\/[^/]+\/git\/webhook$/, () => ({ id: "3f9c2a7e1b5d48c6a0e9f7b2d4c61a8e", path: git.app.webhookPath, secret: "q8Zt1vLx4mN7pR0sW3yB6cE9fH2jK5aD8gU1iO4lT7w" })],
  ["GET", /^\/sites\/[^/]+\/app\/env$/, () => ({ names: ["DATABASE_URL", "SESSION_SECRET"] })],
  ["GET", /^\/sites\/[^/]+\/app$/, () => ({ state: "active", subState: "running", restarts: 0, usedBytes: 86_000_000, since: day(1) })],
  ["GET", /^\/sites\/[^/]+\/backups$/, () => ({ snapshots })],
  ["GET", /^\/backups\/deleted$/, () => ({ sites: [{ name: "oldshop", earlier: false, snapshots: 4, oldest: day(20), newest: day(9) }] })],
  ["GET", /^\/backups\/deleted\/[^/]+$/, () => ({ snapshots: [9, 10, 13].map((d) => ({ id: "old" + d + "abcd", time: day(d), kind: "files" })).concat([{ id: "olddb9abc", time: day(9), kind: "db", database: "oldshop_wp" }]) })],
  ["GET", /^\/sites\/([^/]+)\/headers$/, (m) => ({
    // A staging copy is not indexed (ADR-0070).
    headers: { noSniff: true, frameOptions: "SAMEORIGIN", referrerPolicy: "strict-origin-when-cross-origin", ...(m[1] === "staging" && { noIndex: true }) },
    defaults: { noSniff: true, frameOptions: "SAMEORIGIN", referrerPolicy: "strict-origin-when-cross-origin" },
    referrerPolicies: ["no-referrer", "same-origin", "strict-origin", "strict-origin-when-cross-origin"],
    permissionsPolicyStrict: "camera=(), microphone=(), geolocation=()",
    maxValueBytes: 4096,
  })],
  ["GET", /^\/sites\/[^/]+\/profile$/, () => ({
    profile: "wordpress",
    hardening: { configFiles: true, noPhpDirs: ["wp-content/uploads"], limitFiles: ["wp-login.php"] },
    defaults: profileList[2].hardening,
    changed: true,
    choices: profileChoices,
    updatedAt: day(1),
  })],
  ["POST", /^\/sites\/[^/]+\/profile\/preview$/, () => ({ config: serverBlock })],
  ["GET", /^\/profiles$/, () => allProfiles],
  // shop has WordPress already, for the reinstall.
  ["GET", /^\/sites\/shop\/wordpress$/, () => ({ state: "succeeded", installed: true, empty: false, database: "shop_wp", log: "Success: WordPress installed successfully.\nwordpress installed: https://shop.example.com\n" })],
  ["GET", /^\/sites\/[^/]+\/wordpress$/, () => ({ state: "none", installed: false, empty: true })],
  ["POST", /^\/sites\/[^/]+\/wordpress$/, () => ({ state: "running", database: "blog_wp", adminPassword: "Vq7mR2xK9pLt4Wz8Bn3sHc6J" })],
  ["GET", /^\/sites\/[^/]+\/access$/, () => ({
    ipMode: "deny",
    ips: ["198.51.100.23", "203.0.113.0/26"],
    password: true,
    paths: ["/wp-admin", "/wp-login.php"],
    bypass: ["192.0.2.0/28"],
    users: [
      { name: "kim", createdAt: day(12), updatedAt: day(12) },
      { name: "office", createdAt: day(3), updatedAt: day(1) },
    ],
    limits: { ips: 256, paths: 20, bypass: 64, users: 50, minPassword: 12, maxPassword: 72 },
    clientAddress: "192.0.2.10",
    updatedAt: day(1),
  })],
  ["POST", /^\/sites\/[^/]+\/access\/users$/, () => ({ name: "agency", password: "Vq7mR2xK9pLt4Wz8Bn3sHc6J" })],
  ["GET", /^\/dns-providers$/, () => ({ providers: dnsProviders })],
  ["GET", /^\/dns-accounts$/, () => ({ accounts: dnsAccounts })],
  ["GET", /^\/panel\/tls$/, () => ({ ...tls(["panel.example.com"]), domain: "panel.example.com" })],
  ["GET", /^\/panel\/tls\/certificate$/, () => ({
    certificate: { domains: ["panel.example.com"], issuer: "Let's Encrypt (E7)", notBefore: "2026-11-16T03:12:00Z", notAfter: "2027-02-14T00:00:00Z", fingerprint: "9d2e7b1a".repeat(8), trusted: true },
  })],
  ["GET", /^\/settings\/panel-real-ip$/, () => ({ realIp: {}, headers: ["X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP"], cloudflare: [], cloudflareDate: "2026-11-16", maxRanges: 64 })],
  ["GET", /^\/health$/, () => ({ status: "ok", version: "0.6.0" })],
  ["GET", /^\/sites\/[^/]+\/services$/, () => ({ services: [] })],
  [
    "GET",
    /^\/services\/events/,
    () => ({
      events: [
        { at: day(1), service: "clamonacc", kind: "action", detail: "restart", user: "kim" },
        { at: day(4), service: "mariadb", kind: "crash-restart", restarts: 1 },
      ],
    }),
  ],
  [
    "GET",
    /^\/services$/,
    () => ({
      services: [
        { id: "nginx", unit: "nginx.service", installed: true, active: "active", sub: "running", memoryBytes: 50331648, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["start", "restart", "reload"], restarts24h: 0 },
        { id: "mariadb", unit: "mariadb.service", installed: true, active: "active", sub: "running", memoryBytes: 432013312, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["start", "stop", "restart"], confirm: ["stop"], restarts24h: 0 },
        { id: "ssh", unit: "ssh.service", installed: true, active: "active", sub: "running", memoryBytes: 9437184, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["reload"], restarts24h: 0 },
        { id: "fail2ban", unit: "fail2ban.service", installed: true, active: "active", sub: "running", memoryBytes: 32505856, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["start", "stop", "restart", "reload"], restarts24h: 0 },
        { id: "firewall", unit: "hostashi-firewall.service", installed: true, active: "active", sub: "exited", memoryBytes: 0, memoryKnown: false, activeSince: day(6), startedAt: day(6), actions: ["reload"], restarts24h: 0 },
        { id: "adminer", unit: "hostashi-adminer.service", installed: true, active: "active", sub: "running", memoryBytes: 23068672, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["start", "stop", "restart"], restarts24h: 0 },
        { id: "freshclam", unit: "clamav-freshclam.service", installed: true, active: "active", sub: "running", memoryBytes: 14680064, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["start", "restart"], restarts24h: 0 },
        { id: "clamd", unit: "clamav-daemon.service", installed: true, active: "active", sub: "running", memoryBytes: 1237319680, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["start", "restart"], restarts24h: 0 },
        { id: "clamonacc", unit: "clamav-clamonacc.service", installed: true, active: "active", sub: "running", memoryBytes: 12582912, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["start", "restart"], restarts24h: 0 },
        { id: "panel", unit: "hostashi.service", installed: true, active: "active", sub: "running", memoryBytes: 39845888, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: [], restarts24h: 0 },
        { id: "agent", unit: "hostashi-agent.service", installed: true, active: "active", sub: "running", memoryBytes: 28311552, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: [], restarts24h: 0 },
        { id: "php", site: "blog", unit: "hostashi-php-8.4@blog.service", installed: true, active: "active", sub: "running", memoryBytes: 67108864, memoryKnown: true, activeSince: day(6), startedAt: day(6), actions: ["start", "restart", "reload"], restarts24h: 0 },
      ],
    }),
  ],
  ["GET", /^\/doctor$/, () => ({
    status: "warn",
    checkedAt: day(0),
    counts: { ok: 3, warn: 1, fail: 0 },
    checks: [
      { id: "panel.tls", status: "warn", summary: "certificate for panel.example.com valid until 2026-12-10 (17 days); the last renewal failed: acme: no DNS record for panel.example.com", hint: "See System → Panel certificate. Let's Encrypt needs the panel's host name to point at this server; an own certificate is never renewed automatically: upload a new one." },
      { id: "system.disk", status: "ok", summary: "31.2 GiB of 38.0 GiB free" },
      { id: "service.nginx", status: "ok", summary: "nginx is running" },
      { id: "site.tls", site: "blog", status: "ok", summary: "certificate valid until 2027-02-14 (81 days)" },
    ],
  })],
  // A staging copy of blog (ADR-0070); every other site is no copy.
  ["GET", /^\/sites\/staging\/clone$/, () => ({
    source: "blog",
    staging: true,
    state: "done",
    refresh: false,
    filesDone: true,
    config: "done",
    databases: [{ source: "blog_wp", target: "staging_wp", done: true }],
    startedAt: day(1),
    finishedAt: day(1),
  })],
  ["GET", /^\/sites\/[^/]+\/clone$/, () => ({ $status: 404, body: { error: { code: "not_a_clone", message: "the site is no staging clone of an existing site" } } })],
  ["POST", /^\/sites\/[^/]+\/clone$/, () => ({ site: sites.find((x) => x.name === "staging"), access: { user: "staging", password: "Vq7mR2xK9pLt4Wz8Bn3sHc6J" } })],
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
      if (body?.$status) return { status: body.$status, body: body.body };
      return body === undefined ? undefined : { body };
    }
  }
  return undefined;
}
