// SPDX-License-Identifier: AGPL-3.0-or-later

// The German catalog (ADR-0071). Typed as the English one, so a missing
// area or key fails tsc; locales.test.ts also checks the other way round.
import type { Catalog } from "../en";
import account from "./account.json";
import auth from "./auth.json";
import certificate from "./certificate.json";
import common from "./common.json";
import errors from "./errors.json";
import realIp from "./realIp.json";
import shell from "./shell.json";
import siteAccess from "./siteAccess.json";
import siteDomains from "./siteDomains.json";
import siteHeaders from "./siteHeaders.json";
import tokens from "./tokens.json";

const de: Catalog = { account, auth, certificate, common, errors, realIp, shell, siteAccess, siteDomains, siteHeaders, tokens };

export default de;
