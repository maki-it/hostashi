// SPDX-License-Identifier: AGPL-3.0-or-later

// The English catalog (ADR-0071). It is the source: its shape is the type
// every other language must match, and i18next falls back to it.
import account from "./account.json";
import auth from "./auth.json";
import common from "./common.json";
import errors from "./errors.json";
import limits from "./limits.json";
import pages from "./pages.json";
import parked from "./parked.json";
import profiles from "./profiles.json";
import shell from "./shell.json";
import tokens from "./tokens.json";

const en = { account, auth, common, errors, limits, pages, parked, profiles, shell, tokens };

export type Catalog = typeof en;

export default en;
