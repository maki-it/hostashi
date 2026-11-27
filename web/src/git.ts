// SPDX-License-Identifier: AGPL-3.0-or-later

// Git deploy of a site (ADR-0062): a repository and branch, build commands
// that run as the site user, releases below .deploy/releases with an
// atomic switch, rollback, an SSH deploy key with a confirmed host key and
// an optional webhook.

import { ApiError, request } from "./api";
import i18n from "./i18n";
import { siteMessage, siteType, type Site } from "./sites";

export type GitAuth = "none" | "token" | "ssh";

export type GitSetting = {
  configured: boolean;
  url: string;
  branch: string;
  deployDir: string;
  shared: string[];
  commands: string[];
  keep: number;
  buildTimeoutMinutes: number;
  submodules: boolean;
  auth: GitAuth;
  tokenUser: string;
  hasToken: boolean;
  publicKey: string;
  hostKey: string;
  hostKeyFingerprint: string;
  webhook: boolean;
  /** The webhook's address below the panel's origin; empty while it is off. */
  webhookPath: string;
  updatedAt: string;
};

export type GitInput = {
  url: string;
  branch: string;
  deployDir: string;
  shared: string[];
  commands: string[];
  keep: number;
  buildTimeoutMinutes: number;
  submodules: boolean;
  auth: GitAuth;
  tokenUser: string;
  /** Empty with auth "token" keeps the stored token. */
  token: string;
};

export type HostKey = { key: string; fingerprint: string };

export type Deploy = {
  id: number;
  release: string;
  kind: "deploy" | "rollback";
  source: "panel" | "api" | "webhook";
  commit: string;
  author: string;
  message: string;
  state: "queued" | "running" | "succeeded" | "failed";
  error: string;
  createdAt: string;
  startedAt: string;
  finishedAt: string;
};

export type DeployLog = { log: string; truncated: boolean };

export type Webhook = { id: string; path: string; secret: string };

const base = (site: string) => "/sites/" + encodeURIComponent(site);

export const getGit = (site: string) => request<GitSetting>("GET", base(site) + "/git");
export const setGit = (site: string, g: GitInput) => request<GitSetting>("PUT", base(site) + "/git", g);
export const deleteGit = (site: string) => request<void>("DELETE", base(site) + "/git");
export const newGitKey = (site: string) => request<GitSetting>("POST", base(site) + "/git/key");
export const scanHostKey = (site: string) => request<HostKey>("POST", base(site) + "/git/hostkey/scan");
export const setHostKey = (site: string, key: string) => request<GitSetting>("PUT", base(site) + "/git/hostkey", { key });
export const setWebhook = (site: string) => request<Webhook>("POST", base(site) + "/git/webhook");
export const deleteWebhook = (site: string) => request<void>("DELETE", base(site) + "/git/webhook");
export const listDeploys = (site: string) => request<Deploy[]>("GET", base(site) + "/deploys");
export const startDeploy = (site: string) => request<Deploy>("POST", base(site) + "/deploys");
export const rollback = (site: string, id: number) => request<Deploy>("POST", base(site) + "/deploys/" + String(id) + "/rollback");
export const deployLog = (site: string, id: number) => request<DeployLog>("GET", base(site) + "/deploys/" + String(id) + "/log");

/** The site types that deploy from Git: those whose files Hostashi serves or runs. */
export const gitSiteTypes = ["static", "php", "node", "app", "python"];

export const canDeploy = (s: Site) => s.account !== false && gitSiteTypes.includes(siteType(s));

export const inputOf = (g: GitSetting): GitInput => ({
  url: g.url,
  branch: g.branch,
  deployDir: g.deployDir,
  shared: g.shared,
  commands: g.commands,
  keep: g.keep,
  buildTimeoutMinutes: g.buildTimeoutMinutes,
  submodules: g.submodules,
  auth: g.auth,
  tokenUser: g.tokenUser,
  token: "",
});

/** lines splits a text area into its non-empty lines, trimmed. */
export const lines = (text: string) =>
  text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "");

/** The auth an address needs: SSH for ssh:// and user@host:path, else https. */
export const isSSHURL = (url: string) => /^ssh:\/\//i.test(url.trim()) || /^[^/@\s]+@[^/:\s]+:/.test(url.trim());

// Mirrors of internal/agentapi so a form can say what is wrong before a
// request; the server checks again.
export const maxShared = 32;
export const maxCommands = 20;
export const minKeep = 2;
export const maxKeep = 20;
export const maxBuildMinutes = 60;

export function inputProblem(g: GitInput, hasToken: boolean): string {
  const t = i18n.t;
  const url = g.url.trim();
  if (url === "") return t("git.check.url");
  if (/^(file|git):\/\//i.test(url) || url.startsWith("/") || url.startsWith(".")) return t("git.check.local");
  if (g.auth === "ssh" && !isSSHURL(url)) return t("git.check.sshNeeded");
  if (g.auth !== "ssh" && !/^https:\/\//i.test(url)) return t("git.check.httpsNeeded");
  if (g.auth === "token" && g.token === "" && !hasToken) return t("git.check.token");
  if (g.branch.trim() === "") return t("git.check.branch");
  if (g.deployDir.trim() === "" || g.deployDir.split("/").some((p) => p === ".." || p === ".") || g.deployDir.startsWith("/"))
    return t("git.check.deployDir");
  if (g.shared.length > maxShared) return t("git.check.sharedMax", { max: maxShared });
  if (g.shared.some((p) => p.startsWith("/") || p.split("/").includes(".."))) return t("git.check.sharedRelative");
  if (g.commands.length > maxCommands) return t("git.check.commandsMax", { max: maxCommands });
  if (!Number.isInteger(g.keep) || g.keep < minKeep || g.keep > maxKeep) return t("git.check.keep", { min: minKeep, max: maxKeep });
  if (!Number.isInteger(g.buildTimeoutMinutes) || g.buildTimeoutMinutes < 1 || g.buildTimeoutMinutes > maxBuildMinutes)
    return t("git.check.timeout", { max: maxBuildMinutes });
  return "";
}

/** Presets of build commands; they fill the field, which stays editable. */
export const presets: { label: string; commands: string[]; shared: string[] }[] = [
  { label: "Composer (PHP)", commands: ["composer install --no-dev --optimize-autoloader"], shared: [".env"] },
  {
    label: "Laravel",
    commands: ["composer install --no-dev --optimize-autoloader", "php artisan migrate --force", "php artisan optimize"],
    shared: [".env", "storage"],
  },
  { label: "WordPress", commands: [], shared: ["wp-config.php", "wp-content/uploads"] },
  { label: "Node.js (npm)", commands: ["npm ci", "npm run build"], shared: [".env"] },
  { label: "Static site (npm build)", commands: ["npm ci", "npm run build"], shared: [] },
  { label: "Python (pip)", commands: ["pip install -r requirements.txt"], shared: [".env"] },
];

export type Preset = (typeof presets)[number];

const sameLines = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((l, i) => l === b[i]);

/** fromPreset tells whether a text area holds nothing or exactly some preset's value. */
function fromPreset(text: string, field: "commands" | "shared"): boolean {
  const l = lines(text);
  return l.length === 0 || presets.some((p) => sameLines(l, p[field]));
}

/**
 * applyPreset fills both text areas with a preset. Text that is empty or
 * came from a preset is replaced silently; own text is replaced too, but
 * named in edited so the form can offer to undo it.
 */
export function applyPreset(p: Preset, commands: string, shared: string) {
  const edited: ("build commands" | "shared paths")[] = [];
  if (!fromPreset(commands, "commands") && !sameLines(lines(commands), p.commands)) edited.push("build commands");
  if (!fromPreset(shared, "shared") && !sameLines(lines(shared), p.shared)) edited.push("shared paths");
  return { commands: p.commands.join("\n"), shared: p.shared.join("\n"), edited };
}

/** activePreset is the preset both text areas match, if any. */
export function activePreset(commands: string, shared: string): string {
  return presets.find((p) => sameLines(lines(commands), p.commands) && sameLines(lines(shared), p.shared))?.label ?? "";
}

/** Where Git hosts publish the fingerprints of their SSH host keys. */
export const publishedFingerprints: { host: string; label: string; href: string }[] = [
  { host: "github.com", label: "GitHub", href: "https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints" },
  { host: "gitlab.com", label: "GitLab", href: "https://docs.gitlab.com/user/gitlab_com/#ssh-host-keys-fingerprints" },
  { host: "codeberg.org", label: "Codeberg", href: "https://docs.codeberg.org/security/ssh-fingerprint/" },
];

/** hostOf returns the host of an https, ssh:// or scp-style address. */
export function hostOf(url: string): string {
  const u = url.trim();
  const m = /^[a-z]+:\/\/(?:[^@/]+@)?(\[[^\]]+\]|[^:/]+)/i.exec(u) ?? /^[^@/\s]+@([^:/\s]+):/.exec(u);
  return m?.[1]?.toLowerCase() ?? "";
}

/** live is the release that serves now: the newest successful run's. */
export function liveRelease(list: readonly Deploy[]): string {
  return list.find((d) => d.state === "succeeded")?.release ?? "";
}

export const shortCommit = (c: string) => c.slice(0, 7);

/**
 * commitURL is the web page of a commit at the Git host, or "" when the
 * address gives none. SSH addresses become https on the same host without
 * the SSH port. GitLab (a host named gitlab…) uses /-/commit/, Bitbucket
 * /commits/; GitHub, Gitea, Forgejo and others /commit/.
 */
export function commitURL(repo: string, sha: string): string {
  if (!/^[0-9a-f]{7,64}$/i.test(sha)) return "";
  const u = repo.trim();
  let host = "";
  let path = "";
  const scp = /^[^@/\s]+@([^:/\s]+):(.+)$/.exec(u);
  if (scp && !u.includes("://")) {
    host = scp[1]!;
    path = scp[2]!;
  } else {
    let parsed: URL;
    try {
      parsed = new URL(u);
    } catch {
      return "";
    }
    if (parsed.protocol !== "https:" && parsed.protocol !== "ssh:") return "";
    host = parsed.protocol === "https:" ? parsed.host : parsed.hostname;
    path = parsed.pathname;
  }
  path = path.replace(/^\/+/, "").replace(/\.git\/?$/, "").replace(/\/+$/, "");
  if (!host || !path || path.split("/").some((x) => x === "" || x === "." || x === "..")) return "";
  const name = host.toLowerCase();
  const kind = name.startsWith("gitlab") ? "/-/commit/" : name === "bitbucket.org" ? "/commits/" : "/commit/";
  return "https://" + host + "/" + path + kind + sha.toLowerCase();
}

export function deployWord(d: Deploy): { word: string; tone: "ok" | "warn" | "err" | "off" } {
  switch (d.state) {
    case "succeeded":
      return { word: i18n.t(d.kind === "rollback" ? "git.words.rolledBack" : "git.words.live"), tone: "ok" };
    case "failed":
      return { word: i18n.t("git.words.failed"), tone: "err" };
    case "running":
      return { word: i18n.t("git.words.running"), tone: "warn" };
    default:
      return { word: i18n.t("git.words.queued"), tone: "off" };
  }
}

const sources = new Set(["panel", "api", "webhook"]);
export const sourceLabel = (s: Deploy["source"]) => (sources.has(s) ? i18n.t(`git.sources.${s as "panel"}`) : s);


export function gitMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `git.errors.${err.code}`;
    if (i18n.exists(key)) return (i18n.t as (k: string) => string)(key);
  }
  return siteMessage(err);

}
