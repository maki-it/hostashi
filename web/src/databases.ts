// SPDX-License-Identifier: AGPL-3.0-or-later

// MariaDB databases per site (ADR-0024). The panel stores no database
// password: a generated one is in exactly one response, and this code keeps
// it only in component state until the user closes the notice.

import { ApiError, request } from "./api";

export type Database = { name: string; site: string; server: string; users: string[]; createdAt: string };

// Credentials is what a page shows once after a password was generated.
// host is what the site uses as DB_HOST.
export type Credentials = { database: string; user: string; password: string; host: string };

export const listDatabases = async () => (await request<{ databases: Database[] }>("GET", "/databases")).databases;

// createDatabase creates a database on the given server; without one it
// goes to the panel's default server (ADR-0041).
export const createDatabase = (site: string, name: string, user: string, server = "") =>
  request<Database & { password?: string }>("POST", "/sites/" + encodeURIComponent(site) + "/databases", {
    name,
    ...(user ? { user } : {}),
    ...(server ? { server } : {}),
  });

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
      case "postgresql_not_yet":
        return "This does not work for PostgreSQL databases yet.";
      case "db_server_local_only":
        return "This only works for databases on the local server.";
      case "db_server_unresolved":
        return "The database server's name does not resolve to an address this machine can reach. Check its DNS records.";
      case "unknown_db_server":
        return "This database server does not exist any more. Reload the page.";
      case "db_server_unavailable":
        return "The database server is not reachable. Check it on the Servers tab or try again later.";
      case "db_server_denied":
        return "The database server refused the panel's administrator. Check the server's privileges.";
      case "db_server_exists":
        return "A database server with this name exists. Choose another name.";
      case "db_server_in_use":
        return "The server still has databases. Delete or move them first.";
      case "db_server_check_failed":
        return "The check failed: " + err.message + ".";
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

// LOCAL_SERVER is the MariaDB on this machine, reached over its socket.
export const LOCAL_SERVER = "localhost";

// DBServer is a database server the panel creates databases on (ADR-0041).
// adminUser, clientHost and lastError are only sent to administrators.
export type TLSMode = "socket" | "verify" | "verify-ca" | "none";

export type DBServer = {
  name: string;
  local: boolean;
  default: boolean;
  // What the last check saw; PostgreSQL servers are added as such (ADR-0051).
  engine?: "MariaDB" | "MySQL" | "PostgreSQL";
  version?: string;
  state: "ok" | "error" | "unchecked";
  databases: number;
  tlsMode: TLSMode;
  tlsActive: boolean;
  checkedAt?: string;
  certExpires?: string;
  host?: string;
  port?: number;
  adminUser?: string;
  clientHost?: string;
  lastError?: string;
};

export type DBEngine = "mariadb" | "postgresql";

export type NewDBServer = {
  engine: DBEngine;
  name: string;
  host: string;
  port: number;
  adminUser: string;
  adminPassword: string;
  tlsMode: "verify" | "verify-ca" | "none";
  caCertificate: string;
};

export const listServers = async () => (await request<{ servers: DBServer[] }>("GET", "/db-servers")).servers;

export const addServer = (s: NewDBServer) =>
  request<DBServer & { warnings?: string[] }>("POST", "/db-servers", {
    engine: s.engine,
    name: s.name,
    host: s.host,
    port: s.port,
    adminUser: s.adminUser,
    adminPassword: s.adminPassword,
    tlsMode: s.tlsMode,
    ...(s.tlsMode === "verify-ca" ? { caCertificate: s.caCertificate } : {}),
  });

export const checkServer = (name: string) => request<DBServer>("POST", "/db-servers/" + encodeURIComponent(name) + "/check");

export const setDefaultServer = (name: string) => request<void>("PUT", "/db-servers/" + encodeURIComponent(name) + "/default", {});

export const deleteServer = (name: string) => request<void>("DELETE", "/db-servers/" + encodeURIComponent(name));

// defaultPort is the usual port of an engine.
export const defaultPort = (engine: DBEngine) => (engine === "postgresql" ? 5432 : 3306);

export const isPostgres = (s: DBServer | undefined) => s?.engine === "PostgreSQL";

// serverAddress is what goes into the site's configuration as DB_HOST.
export function serverAddress(s: DBServer | undefined): string {
  if (!s || s.local || !s.host) return "localhost";
  const host = s.host.includes(":") ? "[" + s.host + "]" : s.host;
  return s.port && s.port !== defaultPort(isPostgres(s) ? "postgresql" : "mariadb") ? host + ":" + s.port : host;
}

// pgSSLMode is the sslmode a site's PostgreSQL client should use for s.
export function pgSSLMode(s: DBServer): string {
  switch (s.tlsMode) {
    case "verify":
      return "verify-full";
    case "verify-ca":
      return "verify-ca";
    default:
      return "disable";
  }
}

// engineLabel names the engine and version, like "MariaDB 11.8.6".
export function engineLabel(s: DBServer): string {
  if (!s.engine) return s.local ? "MariaDB" : "Not checked yet";
  return s.engine + (s.version ? " " + s.version : "");
}

// serverNameProblem mirrors hosting.ValidateDBServerName.
export function serverNameProblem(v: string): string {
  if (v === "") return "Enter a name.";
  if (!/^[a-z][a-z0-9-]{0,31}$/.test(v)) return "Use 1 to 32 characters: lowercase letters, digits and -, starting with a letter.";
  if (v === LOCAL_SERVER) return "This name is reserved for the local server.";
  return "";
}
