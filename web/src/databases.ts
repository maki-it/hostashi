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
