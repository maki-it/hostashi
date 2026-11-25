// SPDX-License-Identifier: AGPL-3.0-or-later

// Application profiles and hardening rules of a site (ADR-0069, ADR-0017):
// types, API calls and the form model of the card in the Security tab.

import { ApiError, request } from "./api";

export type WebHardening = {
  configFiles?: boolean;
  noPhpDirs?: string[];
  denyDirs?: string[];
  denyFiles?: string[];
  limitFiles?: string[];
};

export type Profile = {
  name: string;
  title: string;
  type: "static" | "php";
  minPhp?: string;
  webDir?: string;
  hardening: WebHardening;
  pageCache?: string;
  cron?: { schedule: string; command: string; note?: string }[];
  checklist?: string[];
  builtin: boolean;
};

export type ProfileChoices = {
  profiles: Profile[];
  configFileSuffixes: string[];
  limitPerMinute: number;
  limitBurst: number;
  maxPaths: number;
};

export type SiteProfileView = {
  profile: string;
  hardening: WebHardening;
  defaults: WebHardening;
  changed: boolean;
  choices: ProfileChoices;
  updatedAt?: string;
};

const path = (site: string) => `/sites/${encodeURIComponent(site)}/profile`;

export function getSiteProfile(site: string): Promise<SiteProfileView> {
  return request<SiteProfileView>("GET", path(site));
}

// setSiteProfile sets the profile; without hardening the site takes the
// profile's rules.
export function setSiteProfile(site: string, profile: string, hardening?: WebHardening): Promise<SiteProfileView> {
  return request<SiteProfileView>("PUT", path(site), hardening ? { profile, hardening } : { profile });
}

export function listProfiles(): Promise<ProfileChoices> {
  return request<ProfileChoices>("GET", "/profiles");
}

export type HardeningForm = {
  configFiles: boolean;
  noPhpDirs: string;
  denyDirs: string;
  denyFiles: string;
  limitFiles: string;
};

const join = (l?: string[]) => (l ?? []).join("\n");

export function toHardeningForm(h: WebHardening): HardeningForm {
  return {
    configFiles: h.configFiles === true,
    noPhpDirs: join(h.noPhpDirs),
    denyDirs: join(h.denyDirs),
    denyFiles: join(h.denyFiles),
    limitFiles: join(h.limitFiles),
  };
}

// parsePaths reads one path per line. Blank lines go, and so do the
// slashes people type around a path out of habit.
export function parsePaths(text: string): string[] {
  const out: string[] = [];
  for (const line of text.split("\n")) {
    const p = line.trim().replace(/^\/+|\/+$/g, "");
    if (p !== "" && !out.includes(p)) out.push(p);
  }
  return out;
}

export function fromHardeningForm(f: HardeningForm, php: boolean): WebHardening {
  const h: WebHardening = {};
  if (f.configFiles) h.configFiles = true;
  const lists: [keyof WebHardening, string, boolean][] = [
    ["denyDirs", f.denyDirs, true],
    ["denyFiles", f.denyFiles, true],
    ["noPhpDirs", f.noPhpDirs, php],
    ["limitFiles", f.limitFiles, php],
  ];
  for (const [key, text, keep] of lists) {
    const l = keep ? parsePaths(text) : [];
    if (l.length > 0) (h as Record<string, unknown>)[key] = l;
  }
  return h;
}

export function profileMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "not_found") return "The site does not exist anymore.";
    if (err.code === "invalid_argument") return err.message.replace(/^hosting: invalid input: (hardening: )?/, "").replace(/^./, (c) => c.toUpperCase()) + ".";
    return err.message;
  }
  return "The profile could not be loaded or saved. Try again.";
}
