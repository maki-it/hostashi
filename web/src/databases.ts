// SPDX-License-Identifier: AGPL-3.0-or-later

// MariaDB databases per site (ADR-0024). The panel stores no database
// password: a generated one is in exactly one response, and this code keeps
// it only in component state until the user closes the notice.

import { ApiError, request } from "./api";
import i18n, { areaErrorText } from "./i18n";

// sizeBytes is the size of a local database (ADR-0057); external ones and
// failed measurements have none.
export type Database = { name: string; site: string; server: string; users: string[]; createdAt: string; sizeBytes?: number };

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
  if (v === "") return i18n.t("databases.problems.enterName");
  if (!re.test(v)) return i18n.t("databases.problems.nameChars", { max });
  const taken =
    kind === "database"
      ? reservedDatabases.includes(v) || v.startsWith("hostashi")
      : reservedUsers.includes(v) || v.startsWith("hostashi") || v.startsWith("mariadb");
  if (taken) return i18n.t("databases.problems.reserved");
  return "";
}

// suggestUser derives a user name from a database name.
export const suggestUser = (db: string) => db.slice(0, 32);

// databaseMessage turns an API error into a sentence.
export function databaseMessage(err: unknown): string {
  if (err instanceof ApiError && err.code === "db_server_check_failed") return i18n.t("databases.errors.checkFailed", { reason: err.message });
  return areaErrorText("databases.errors", err);
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
// A PostgreSQL database also takes .dump, pg_dump's custom format
// (agentapi.ValidatePGDumpPath, ADR-0051).
const dumpComponentRe = /^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$/;

export function pathProblem(p: string, postgres = false): string {
  if (p === "" || p.length > 255) return i18n.t("databases.problems.pathLength");
  if (postgres) {
    if (!p.endsWith(".dump") && !p.endsWith(".sql") && !p.endsWith(".sql.gz")) return i18n.t("databases.problems.endingPg");
  } else if (!p.endsWith(".sql") && !p.endsWith(".sql.gz")) {
    return i18n.t("databases.problems.ending");
  }
  for (const c of p.split("/")) {
    if (!dumpComponentRe.test(c)) return i18n.t("databases.problems.pathParts");
  }
  return "";
}

// defaultDumpPath suggests a new export file name with the date: for
// PostgreSQL the custom format, which pg_restore loads back exactly.
export const defaultDumpPath = (db: string, now = new Date(), postgres = false) =>
  db + "-" + now.toISOString().slice(0, 10) + (postgres ? ".dump" : ".sql.gz");

// Dump formats an export can write; the file name's ending names it. The
// custom format of pg_dump (.dump) is compressed already.
export type DumpFormat = ".dump" | ".sql.gz" | ".sql";

export const dumpFormats = (postgres: boolean): { value: DumpFormat; label: string }[] => [
  ...(postgres ? [{ value: ".dump" as DumpFormat, label: i18n.t("databases.dump.formats.dump") }] : []),
  { value: ".sql.gz", label: i18n.t("databases.dump.formats.sqlgz") },
  { value: ".sql", label: i18n.t("databases.dump.formats.sql") },
];

// formatOf is the format a path ends in, or "" for none.
export function formatOf(path: string): DumpFormat | "" {
  for (const f of [".dump", ".sql.gz", ".sql"] as DumpFormat[]) if (path.endsWith(f)) return f;
  return "";
}

// withFormat replaces the path's ending by format, or appends it.
export function withFormat(path: string, format: DumpFormat): string {
  const cur = formatOf(path);
  return (cur ? path.slice(0, -cur.length) : path) + format;
}

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

// POSTGRESQL is the engine's name, the same in every language.
export const POSTGRESQL = "PostgreSQL";

export const isPostgres = (s: DBServer | undefined) => s?.engine === POSTGRESQL;

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
  if (!s.engine) return s.local ? "MariaDB" : i18n.t("databases.dbServers.notChecked");
  return s.engine + (s.version ? " " + s.version : "");
}

// serverNameProblem mirrors hosting.ValidateDBServerName.
export function serverNameProblem(v: string): string {
  if (v === "") return i18n.t("databases.problems.enterName");
  if (!/^[a-z][a-z0-9-]{0,31}$/.test(v)) return i18n.t("databases.problems.serverChars");
  if (v === LOCAL_SERVER) return i18n.t("databases.problems.serverReserved");
  return "";
}

// The folder in the site's home that takes a dump uploaded for an import;
// the panel removes the file after the import.
export const uploadDir = "hostashi-imports";

/**
 * uploadPath names the file an uploaded dump gets in the site's home:
 * hostashi-imports/<time>-<name>, with the name reduced to the characters
 * a dump path allows. "" when the file's ending is not one the database
 * takes.
 */
export function uploadPath(fileName: string, now = new Date(), postgres = false): string {
  const lower = fileName.toLowerCase();
  const ext = [".sql.gz", ".sql", ...(postgres ? [".dump"] : [])].find((e) => lower.endsWith(e));
  if (!ext) return "";
  const stem = fileName
    .slice(0, fileName.length - ext.length)
    .replace(/[^A-Za-z0-9._-]+/g, "_")
    .replace(/^[._-]+/, "")
    .slice(0, 80);
  const stamp = now.toISOString().slice(0, 19).replace(/[:T]/g, "-");
  return `${uploadDir}/${stamp}${stem ? "-" + stem : ""}${ext}`;
}
