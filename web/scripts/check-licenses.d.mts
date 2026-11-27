// SPDX-License-Identifier: AGPL-3.0-or-later

// Types of check-licenses.mjs for vite.config.ts.
export function bundledPaths(lock: { packages: Record<string, unknown> }): string[];
export function bundledPackages(lock: { packages: Record<string, unknown> }): [string, unknown][];
export function check(lock: { packages: Record<string, unknown> }): string[];
export function licenseAllowed(expr: unknown): boolean;
export function allowedFor(name: string, license: unknown): boolean;
