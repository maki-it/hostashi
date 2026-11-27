// SPDX-License-Identifier: AGPL-3.0-or-later

// The German catalog (ADR-0071). Typed as the English one, so a missing
// area or key fails tsc; locales.test.ts also checks the other way round.
import type { Catalog } from "../en";
import account from "./account.json";
import audit from "./audit.json";
import auth from "./auth.json";
import backups from "./backups.json";
import certificate from "./certificate.json";
import common from "./common.json";
import cron from "./cron.json";
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
import shell from "./shell.json";
import siteAccess from "./siteAccess.json";
import siteApp from "./siteApp.json";
import siteDomains from "./siteDomains.json";
import siteHeaders from "./siteHeaders.json";
import sites from "./sites.json";
import ssh from "./ssh.json";
import tokens from "./tokens.json";
import ui from "./ui.json";
import usage from "./usage.json";
import users from "./users.json";

const de: Catalog = { account, audit, auth, backups, certificate, common, cron, databases, errors, files, git, hardening, limits, logs, mail, malware, migrate, overview, pages, parked, permissions, profiles, realIp, shell, siteAccess, siteApp, siteDomains, siteHeaders, sites, ssh, tokens, ui, usage, users };

export default de;
