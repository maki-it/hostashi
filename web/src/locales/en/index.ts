// SPDX-License-Identifier: AGPL-3.0-or-later

// The English catalog (ADR-0071). It is the source: its shape is the type
// every other language must match, and i18next falls back to it.
import account from "./account.json";
import audit from "./audit.json";
import auth from "./auth.json";
import backups from "./backups.json";
import common from "./common.json";
import errors from "./errors.json";
import mail from "./mail.json";
import migrate from "./migrate.json";
import shell from "./shell.json";
import tokens from "./tokens.json";
import users from "./users.json";

const en = { account, audit, auth, backups, common, errors, mail, migrate, shell, tokens, users };

export type Catalog = typeof en;

export default en;
