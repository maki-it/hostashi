// SPDX-License-Identifier: AGPL-3.0-or-later

// The German catalog (ADR-0071). Typed as the English one, so a missing
// area or key fails tsc; locales.test.ts also checks the other way round.
import type { Catalog } from "../en";
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

const de: Catalog = { account, audit, auth, backups, common, errors, mail, migrate, shell, tokens, users };

export default de;
