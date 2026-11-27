// SPDX-License-Identifier: AGPL-3.0-or-later

// The English catalog (ADR-0071). It is the source: its shape is the type
// every other language must match, and i18next falls back to it.
import account from "./account.json";
import auth from "./auth.json";
import backups from "./backups.json";
import common from "./common.json";
import databases from "./databases.json";
import errors from "./errors.json";
import shell from "./shell.json";
import tokens from "./tokens.json";

const en = { account, auth, backups, common, databases, errors, shell, tokens };

export type Catalog = typeof en;

export default en;
