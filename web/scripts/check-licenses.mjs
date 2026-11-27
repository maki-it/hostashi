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

// Licenses allowed only for packages under a given scope. Fonts under the
// SIL Open Font License may be bundled with AGPL software (ADR-0037); the
// license is not accepted for code.
export const ALLOWED_FOR_SCOPE = new Map([["@fontsource/", new Set(["OFL-1.1"])]]);

// allowedFor reports whether the license of the named package is allowed,
// either generally or for the package's scope.
export function allowedFor(name, license) {
  if (licenseAllowed(license)) return true;
  for (const [prefix, extra] of ALLOWED_FOR_SCOPE) {
    if (name.startsWith(prefix) && typeof license === "string" && extra.has(license.trim())) return true;
  }
  return false;
}

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

// bundledPaths returns the lockfile paths of the packages the bundle can
// contain: those the root's dependencies reach through dependencies,
// optionalDependencies and required peerDependencies, resolved as Node does (the
// nearest node_modules up the tree). The dev flags in the lockfile are not
// enough: npm drops them from the optional platform packages of a
// devOptional package (typescript 7), which then look bundled. An optional
// package missing from the lockfile (another platform) is skipped.
export function bundledPaths(lock) {
  if (!lock || typeof lock.packages !== "object" || lock.packages === null) {
    throw new Error("package-lock.json has no 'packages' (lockfileVersion 2 or newer is required)");
  }
  const pkgs = lock.packages;
  const resolveDep = (from, name) => {
    for (let dir = from; ; ) {
      const path = (dir === "" ? "" : dir + "/") + "node_modules/" + name;
      if (path in pkgs) return path;
      if (dir === "") return undefined;
      const i = dir.lastIndexOf("/node_modules/");
      dir = i < 0 ? "" : dir.slice(0, i);
    }
  };
  // An optional peer is the app's choice; the root lists it if it is used.
  const requiredPeers = (pkg) =>
    Object.fromEntries(Object.entries(pkg.peerDependencies ?? {}).filter(([n]) => !pkg.peerDependenciesMeta?.[n]?.optional));
  const seen = new Set();
  const visit = (path, depMaps) => {
    for (const deps of depMaps) {
      for (const name of Object.keys(deps ?? {})) {
        const dep = resolveDep(path, name);
        if (dep === undefined || seen.has(dep)) continue;
        seen.add(dep);
        const pkg = pkgs[dep];
        visit(dep, [pkg.dependencies, pkg.optionalDependencies, requiredPeers(pkg)]);
      }
    }
  };
  // From the root only its dependencies: the rest are dev or build tools.
  visit("", [pkgs[""]?.dependencies]);
  return Object.keys(pkgs).filter((path) => seen.has(path));
}

// bundledPackages returns [name, license] for every package the bundle can
// contain (see bundledPaths).
export function bundledPackages(lock) {
  return bundledPaths(lock).map((path) => [path.replace(/^(.*\/)?node_modules\//, ""), lock.packages[path].license]);
}

// check returns the offending packages as "name: license" strings.
export function check(lock) {
  return bundledPackages(lock)
    .filter(([name, license]) => !allowedFor(name, license))
    .map(([name, license]) => `${name}: ${license ?? "(no license field)"}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const file = process.argv[2] ?? new URL("../package-lock.json", import.meta.url);
  const lock = JSON.parse(readFileSync(file, "utf8"));
  const pkgs = bundledPackages(lock);
  for (const [name, license] of pkgs) console.log(`${name}\t${license}`);
  const bad = check(lock);
  if (bad.length > 0) {
    const scoped = [...ALLOWED_FOR_SCOPE].map(([p, l]) => `${[...l].join(", ")} for ${p}*`).join("; ");
    console.error(`\nLicenses not on the allowlist (${[...ALLOWED].join(", ")}; ${scoped}):`);
    for (const b of bad) console.error(`  ${b}`);
    console.error("\nCheck compatibility with AGPL-3.0-or-later before adding it to ALLOWED.");
    process.exit(1);
  }
  console.log(`\n${pkgs.length} bundled packages, all licenses allowed.`);
}
