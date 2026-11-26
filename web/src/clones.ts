// SPDX-License-Identifier: AGPL-3.0-or-later

// Cloning a site and staging copies (ADR-0070): a new site as a copy of
// another one, and "refresh from source" for a staging copy.

import { ApiError, request } from "./api";
import type { Site } from "./sites";

export type CloneInput = {
  /** System name of the new site. */
  name: string;
  /** Its first domain. */
  domain: string;
  /** Target storage pool; empty: the source's. */
  pool?: string;
  /** Staging copy: cron paused, password protection on, noindex, refresh offered. */
  staging?: boolean;
};

export type CloneStart = {
  site: Site;
  /** The generated basic auth login, shown only in this answer. */
  access?: { user: string; password: string };
};

export type CloneDatabase = { source: string; target: string; done: boolean };

export type CloneFiles = {
  dirs: number;
  files: number;
  links: number;
  bytes: number;
  skipped: number;
  skippedPaths: { path: string; reason: string }[];
};

export type Clone = {
  /** The source site, empty once it was deleted. */
  source: string;
  staging: boolean;
  state: "copying" | "done" | "failed";
  refresh: boolean;
  error?: string;
  filesDone: boolean;
  /** The configuration step after the copy: "" not (yet), running, done. */
  config: "" | "running" | "done";
  files?: CloneFiles;
  databases: CloneDatabase[];
  startedAt: string;
  finishedAt?: string;
};

const path = (site: string) => "/sites/" + encodeURIComponent(site) + "/clone";

export const cloneSite = (site: string, input: CloneInput) => request<CloneStart>("POST", path(site), input);

/** getClone returns null for a site that is no clone. */
export async function getClone(site: string): Promise<Clone | null> {
  try {
    return await request<Clone>("GET", path(site));
  } catch (err) {
    if (err instanceof ApiError && err.code === "not_a_clone") return null;
    throw err;
  }
}

export const refreshClone = (site: string) => request<Clone>("POST", path(site) + "/refresh");

/** cloneMessage explains the errors that are special to cloning. */
export function cloneMessage(err: unknown): string | null {
  if (!(err instanceof ApiError)) return null;
  switch (err.code) {
    case "clone_running":
      return "A copy of this site is running. Wait until it is done.";
    case "clone_unavailable":
      return "Copying sites is not available on this panel.";
    case "no_space":
      return err.message.charAt(0).toUpperCase() + err.message.slice(1) + ".";
    case "not_a_clone":
      return "This site is no staging copy of an existing site.";
    default:
      return null;
  }
}

/** Profiles whose configuration the copy changes itself. */
export const configuredProfiles = ["wordpress", "laravel", "symfony"];

/** cloneSuggestion proposes a name and domain for a staging copy. */
export function cloneSuggestion(site: Site): { name: string; domain: string } {
  const name = ("staging-" + site.name).slice(0, 32).replace(/-+$/, "");
  return { name, domain: "staging." + (site.domains[0] ?? "") };
}
