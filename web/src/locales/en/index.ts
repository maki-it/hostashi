// SPDX-License-Identifier: AGPL-3.0-or-later

// The English catalog (ADR-0071). It is the source: its shape is the type
// every other language must match, and i18next falls back to it.
import account from "./account.json";
import audit from "./audit.json";
import auth from "./auth.json";
import backups from "./backups.json";
import certificate from "./certificate.json";
import common from "./common.json";
import databases from "./databases.json";
import errors from "./errors.json";
import files from "./files.json";
import git from "./git.json";
import hardening from "./hardening.json";
import limits from "./limits.json";
import logs from "./logs.json";
import mail from "./mail.json";
import malware from "./malware.json";
import migrate from "./migrate.json";
import overview from "./overview.json";
import pages from "./pages.json";
import parked from "./parked.json";
import permissions from "./permissions.json";
import profiles from "./profiles.json";
import realIp from "./realIp.json";
import server from "./server.json";
import shell from "./shell.json";
import siteAccess from "./siteAccess.json";
import siteDomains from "./siteDomains.json";
import siteHeaders from "./siteHeaders.json";
import ssh from "./ssh.json";
import tokens from "./tokens.json";
import ui from "./ui.json";
import usage from "./usage.json";
import users from "./users.json";

const en = { account, audit, auth, backups, certificate, common, databases, errors, files, git, hardening, limits, logs, mail, malware, migrate, overview, pages, parked, permissions, profiles, realIp, server, shell, siteAccess, siteDomains, siteHeaders, ssh, tokens, ui, usage, users };

export type Catalog = typeof en;

export default en;
