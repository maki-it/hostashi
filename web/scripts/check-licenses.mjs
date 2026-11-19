// SPDX-License-Identifier: AGPL-3.0-or-later

// Checks the licenses of the npm packages that end up in the embedded
// frontend bundle (everything in package-lock.json that is not a dev
// dependency) against an allowlist of licenses compatible with
// AGPL-3.0-or-later. Dev dependencies (build and test tools) are not
// distributed and are not checked. Needs only Node, no installed packages.
//
// Usage: node scripts/check-licenses.mjs [path/to/package-lock.json]

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Keep in sync with ALLOWED in .github/workflows/licenses.yml (Go).
export const ALLOWED = new Set([
  "0BSD",
  "Apache-2.0",
  "BSD-2-Clause",
  "BSD-3-Clause",
  "ISC",
  "MIT",
  "MPL-2.0",
]);

// licenseAllowed accepts a single SPDX identifier or a simple expression
// with OR / AND and optional parentheses, e.g. "(MIT OR Apache-2.0)". An OR
// needs one allowed side, an AND needs both. Anything it cannot parse
// (WITH exceptions, nested groups, LicenseRef, missing values) is rejected so
// that a human looks at it.
export function licenseAllowed(expr) {
  if (typeof expr !== "string") return false;
  const s = expr.trim().replace(/^\((.*)\)$/, "$1").trim();
  if (s === "" || /[()]/.test(s) || /\bWITH\b/.test(s)) return false;
  if (/\bOR\b/.test(s) && /\bAND\b/.test(s)) return false;
  if (/\bOR\b/.test(s)) return s.split(/\s+OR\s+/).some((p) => ALLOWED.has(p.trim()));
  if (/\bAND\b/.test(s)) return s.split(/\s+AND\s+/).every((p) => ALLOWED.has(p.trim()));
  return ALLOWED.has(s);
}

// bundledPackages returns [name, license] for every non-dev package in a
// lockfile (lockfileVersion 2 or 3).
export function bundledPackages(lock) {
  if (!lock || typeof lock.packages !== "object" || lock.packages === null) {
    throw new Error("package-lock.json has no 'packages' (lockfileVersion 2 or newer is required)");
  }
  return Object.entries(lock.packages)
    .filter(([path, pkg]) => path !== "" && !pkg.dev && !pkg.devOptional)
    .map(([path, pkg]) => [path.replace(/^(.*\/)?node_modules\//, ""), pkg.license]);
}

// check returns the offending packages as "name: license" strings.
export function check(lock) {
  return bundledPackages(lock)
    .filter(([, license]) => !licenseAllowed(license))
    .map(([name, license]) => `${name}: ${license ?? "(no license field)"}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const file = process.argv[2] ?? new URL("../package-lock.json", import.meta.url);
  const lock = JSON.parse(readFileSync(file, "utf8"));
  const pkgs = bundledPackages(lock);
  for (const [name, license] of pkgs) console.log(`${name}\t${license}`);
  const bad = check(lock);
  if (bad.length > 0) {
    console.error(`\nLicenses not on the allowlist (${[...ALLOWED].join(", ")}):`);
    for (const b of bad) console.error(`  ${b}`);
    console.error("\nCheck compatibility with AGPL-3.0-or-later before adding it to ALLOWED.");
    process.exit(1);
  }
  console.log(`\n${pkgs.length} bundled packages, all licenses allowed.`);
}
