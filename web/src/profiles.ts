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

// previewSiteProfile renders the site's server block with that profile and
// rules, unsaved, with placeholder paths. Nothing is stored.
export function previewSiteProfile(site: string, profile: string, hardening: WebHardening): Promise<{ config: string }> {
  return request<{ config: string }>("POST", path(site) + "/preview", { profile, hardening });
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

// Own profiles (ADR-0069): an admin's copy of a built-in profile with
// changed values. The panel stores them; GET /profiles lists them after
// the built-in ones.

export type ProfileInput = Omit<Profile, "builtin">;

const ownPath = (name?: string) => "/profiles" + (name === undefined ? "" : "/" + encodeURIComponent(name));

export const createProfile = (p: ProfileInput) => request<Profile>("POST", ownPath(), p);
export const updateProfile = (p: ProfileInput) => request<Profile>("PUT", ownPath(p.name), p);
export const deleteProfile = (name: string) => request<void>("DELETE", ownPath(name));
export const importProfile = (yaml: string) => request<Profile>("POST", "/profiles/import", { yaml });

/** exportUrl is the YAML download of a profile; the session cookie authorizes it. */
export const exportUrl = (name: string) => "/api/v1" + ownPath(name) + "/yaml";

// Limits of an own profile, as the server checks them.
export const maxNameLength = 40;
const nameRe = /^[a-z][a-z0-9-]*$/;

/** profileNameProblem checks a new name; the names in taken exist. */
export function profileNameProblem(name: string, taken: string[]): string {
  if (name.length > maxNameLength || !nameRe.test(name)) {
    return `Use up to ${String(maxNameLength)} lower-case letters, digits and dashes for the name, starting with a letter.`;
  }
  if (taken.includes(name)) return `A profile named ${name} exists already.`;
  return "";
}

/** copyName is the first free name for a copy of a profile. */
export function copyName(name: string, taken: string[]): string {
  const base = (name + "-copy").slice(0, maxNameLength);
  if (!taken.includes(base)) return base;
  for (let i = 2; ; i++) {
    const n = `${base.slice(0, maxNameLength - String(i).length - 1)}-${String(i)}`;
    if (!taken.includes(n)) return n;
  }
}

export type ProfileForm = HardeningForm & {
  name: string;
  title: string;
  type: "php" | "static";
  minPhp: string;
  webDir: string;
  pageCache: string;
  cron: string;
  checklist: string;
};

type CronJob = NonNullable<Profile["cron"]>[number];

export function toProfileForm(p: ProfileInput): ProfileForm {
  return {
    ...toHardeningForm(p.hardening),
    name: p.name,
    title: p.title,
    type: p.type,
    minPhp: p.minPhp ?? "",
    webDir: p.webDir ?? "",
    pageCache: p.pageCache ?? "",
    cron: (p.cron ?? []).map((c) => `${c.schedule} ${c.command}`).join("\n"),
    checklist: (p.checklist ?? []).join("\n"),
  };
}

const lines = (text: string) =>
  text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "");

/**
 * parseCron reads one suggested job per line: a cron schedule (five fields
 * or an @ keyword) and the command. Notes of unchanged jobs stay.
 */
export function parseCron(text: string, before: CronJob[] = []): CronJob[] | string {
  const out: CronJob[] = [];
  for (const line of lines(text)) {
    const words = line.split(/\s+/);
    const n = words[0]?.startsWith("@") ? 1 : 5;
    if (words.length <= n) return `The cron job "${line}" needs a schedule and a command.`;
    const job: CronJob = { schedule: words.slice(0, n).join(" "), command: words.slice(n).join(" ") };
    const note = before.find((c) => c.schedule === job.schedule && c.command === job.command)?.note;
    out.push(note ? { ...job, note } : job);
  }
  return out;
}

/** fromProfileForm turns the form into the request, or a problem to show. */
export function fromProfileForm(f: ProfileForm, before?: CronJob[]): ProfileInput | string {
  const php = f.type === "php";
  if (f.title.trim() === "") return "Give the profile a title.";
  const minPhp = f.minPhp.trim();
  if (php && minPhp !== "" && !/^\d{1,2}\.\d{1,2}$/.test(minPhp)) return "Write the lowest PHP version like 8.2, or leave it empty.";
  const cron = parseCron(f.cron, before);
  if (typeof cron === "string") return cron;
  const p: ProfileInput = { name: f.name.trim(), title: f.title.trim(), type: f.type, hardening: fromHardeningForm(f, php) };
  const webDir = f.webDir.trim().replace(/^\/+|\/+$/g, "");
  if (webDir) p.webDir = webDir;
  if (php && minPhp) p.minPhp = minPhp;
  if (php && f.pageCache) p.pageCache = f.pageCache;
  if (cron.length > 0) p.cron = cron;
  const checklist = lines(f.checklist);
  if (checklist.length > 0) p.checklist = checklist;
  return p;
}

const sentence = (s: string) => s.replace(/^./, (c) => c.toUpperCase()) + (s.endsWith(".") ? "" : ".");

export function ownProfileMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "not_found":
        return "The profile does not exist anymore.";
      case "profile_exists":
        return "A profile with this name exists already.";
      case "profile_builtin":
        return "Built-in profiles cannot be changed or deleted. Copy one instead.";
      case "profile_in_use":
        return sentence(err.message) + " Give those sites another profile first.";
      case "forbidden":
        return "Only administrators can manage profiles.";
      case "invalid_argument":
        return sentence(err.message.replace(/^hosting: invalid input: (hardening: )?/, ""));
    }
    return err.message;
  }
  return "The profile could not be saved. Try again.";
}
