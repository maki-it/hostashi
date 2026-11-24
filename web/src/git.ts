// SPDX-License-Identifier: AGPL-3.0-or-later

// Git deploy of a site (ADR-0062): a repository and branch, build commands
// that run as the site user, releases below .deploy/releases with an
// atomic switch, rollback, an SSH deploy key with a confirmed host key and
// an optional webhook.

import { ApiError, request } from "./api";
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
  const url = g.url.trim();
  if (url === "") return "Enter the address of the repository.";
  if (/^(file|git):\/\//i.test(url) || url.startsWith("/") || url.startsWith(".")) return "Use an https:// or SSH address; local paths and git:// are not supported.";
  if (g.auth === "ssh" && !isSSHURL(url)) return "A deploy key needs an SSH address, like git@github.com:acme/site.git.";
  if (g.auth !== "ssh" && !/^https:\/\//i.test(url)) return "Without a deploy key the address must start with https://.";
  if (g.auth === "token" && g.token === "" && !hasToken) return "Enter the access token.";
  if (g.branch.trim() === "") return "Enter the branch to deploy.";
  if (g.deployDir.trim() === "" || g.deployDir.split("/").some((p) => p === ".." || p === ".") || g.deployDir.startsWith("/"))
    return "The deploy directory is a folder below the home, like htdocs.";
  if (g.shared.length > maxShared) return `At most ${maxShared} shared paths.`;
  if (g.shared.some((p) => p.startsWith("/") || p.split("/").includes(".."))) return "Shared paths are relative to the release, like .env.";
  if (g.commands.length > maxCommands) return `At most ${maxCommands} build commands.`;
  if (!Number.isInteger(g.keep) || g.keep < minKeep || g.keep > maxKeep) return `Keep ${minKeep} to ${maxKeep} releases.`;
  if (!Number.isInteger(g.buildTimeoutMinutes) || g.buildTimeoutMinutes < 1 || g.buildTimeoutMinutes > maxBuildMinutes)
    return `The build time limit is 1 to ${maxBuildMinutes} minutes.`;
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

export function deployWord(d: Deploy): { word: string; tone: "ok" | "warn" | "err" | "off" } {
  switch (d.state) {
    case "succeeded":
      return { word: d.kind === "rollback" ? "Rolled back" : "Live", tone: "ok" };
    case "failed":
      return { word: "Failed", tone: "err" };
    case "running":
      return { word: "Running", tone: "warn" };
    default:
      return { word: "Queued", tone: "off" };
  }
}

export const sourceLabel = (s: Deploy["source"]) => ({ panel: "Panel", api: "API", webhook: "Webhook" })[s] ?? s;

export function gitMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "git_unavailable":
        return "Git deploy is not available on this panel.";
      case "deploy_unavailable":
        return "Git deploys are not available on this panel: the agent does not answer.";
      case "git_not_configured":
        return "Set up the repository first.";
      case "deploy_running":
        return "A deploy of this site is running. Wait until it ends.";
      case "host_key_unconfirmed":
        return "Confirm the server's host key first.";
      case "not_ssh":
        return "The site does not deploy over SSH.";
      case "unreachable":
        return "The repository's server did not answer over SSH. Check the address and port.";
      case "invalid_host_key":
        return "This is not a host key Hostashi can pin: Ed25519, ECDSA or RSA with at least 3072 bits.";
      case "not_deployable":
        return "Only a successful deploy can be rolled back to.";
      case "deploy_not_found":
        return "This deploy is no longer in the history.";
    }
  }
  return siteMessage(err);
}
