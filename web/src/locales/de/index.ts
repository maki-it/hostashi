// SPDX-License-Identifier: AGPL-3.0-or-later

// The German catalog (ADR-0071). Typed as the English one, so a missing
// area or key fails tsc; locales.test.ts also checks the other way round.
import type { Catalog } from "../en";
import account from "./account.json";
import auth from "./auth.json";
import common from "./common.json";
import errors from "./errors.json";
import shell from "./shell.json";

const de: Catalog = { account, auth, common, errors, shell };

export default de;
