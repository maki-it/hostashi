// SPDX-License-Identifier: AGPL-3.0-or-later

// MariaDB databases per site (ADR-0024). The panel stores no database
// password: a generated one is in exactly one response, and this code keeps
// it only in component state until the user closes the notice.

import { ApiError, request } from "./api";

export type Database = { name: string; site: string; users: string[]; createdAt: string };

// Credentials is what a page shows once after a password was generated.
export type Credentials = { database: string; user: string; password: string };

export const listDatabases = async () => (await request<{ databases: Database[] }>("GET", "/databases")).databases;

export const createDatabase = (site: string, name: string, user: string) =>
  request<Database & { password?: string }>("POST", "/sites/" + encodeURIComponent(site) + "/databases", user ? { name, user } : { name });

export const deleteDatabase = (db: string) => request<void>("DELETE", "/databases/" + encodeURIComponent(db));

export const createUser = (db: string, user: string) =>
  request<{ user: string; database: string; password: string }>("POST", "/databases/" + encodeURIComponent(db) + "/users", { user });

export const resetPassword = (db: string, user: string) =>
  request<{ user: string; password: string }>(
    "PUT",
    "/databases/" + encodeURIComponent(db) + "/users/" + encodeURIComponent(user) + "/password",
    {},
  );

export const deleteUser = (db: string, user: string) =>
  request<void>("DELETE", "/databases/" + encodeURIComponent(db) + "/users/" + encodeURIComponent(user));

// The names MariaDB accepts here, mirrored from internal/agentapi so the
// form can say what is wrong before a request. The server checks again.
const dbNameRe = /^[a-z][a-z0-9_]{0,63}$/;
const dbUserRe = /^[a-z][a-z0-9_]{0,31}$/;
const reservedDatabases = ["mysql", "information_schema", "performance_schema", "sys", "test"];
const reservedUsers = ["root", "mysql", "mariadb", "debian_sys_maint", "healthcheck", "admin"];

export function nameProblem(kind: "database" | "user", v: string): string {
  const re = kind === "database" ? dbNameRe : dbUserRe;
  const max = kind === "database" ? 64 : 32;
  if (v === "") return "Enter a name.";
  if (!re.test(v)) return `Use 1 to ${max} characters: lowercase letters, digits and _, starting with a letter.`;
  const taken =
    kind === "database"
      ? reservedDatabases.includes(v) || v.startsWith("hostashi")
      : reservedUsers.includes(v) || v.startsWith("hostashi") || v.startsWith("mariadb");
  if (taken) return "This name is reserved.";
  return "";
}

// suggestUser derives a user name from a database name.
export const suggestUser = (db: string) => db.slice(0, 32);

// databaseMessage turns an API error into a sentence.
export function databaseMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "database_exists":
        return "A database or database user with this name exists. Choose another name.";
      case "not_found":
        return "This database or user does not exist any more. Reload the page.";
      case "forbidden":
        return "Only administrators can manage databases.";
      case "agent_timeout":
        return "The server did not answer in time. Reload the page to see whether the change was made.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}

// DumpJob is an import or export running in the agent (ADR-0025).
export type DumpJob = {
  id: string;
  kind: "import" | "export";
  database: string;
  path: string;
  state: "running" | "done" | "failed";
  error?: string;
};

export const startDump = (db: string, kind: "import" | "export", path: string) =>
  request<DumpJob>("POST", "/databases/" + encodeURIComponent(db) + "/" + kind, { path });

export const getDumpJob = (db: string, id: string) =>
  request<DumpJob>("GET", "/databases/" + encodeURIComponent(db) + "/jobs/" + encodeURIComponent(id));

// pathProblem mirrors agentapi.ValidateDumpPath: a path relative to the
// site home, ending in .sql or .sql.gz, without hidden or option-like parts.
const dumpComponentRe = /^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$/;

export function pathProblem(p: string): string {
  if (p === "" || p.length > 255) return "Enter a path of 1 to 255 characters.";
  if (!p.endsWith(".sql") && !p.endsWith(".sql.gz")) return "The file must end in .sql or .sql.gz.";
  for (const c of p.split("/")) {
    if (!dumpComponentRe.test(c)) return "Use a path inside the site's home, like backups/shop.sql.gz, without parts that start with a dot or dash.";
  }
  return "";
}

// defaultDumpPath suggests a new export file name with the date.
export const defaultDumpPath = (db: string, now = new Date()) => db + "-" + now.toISOString().slice(0, 10) + ".sql.gz";
