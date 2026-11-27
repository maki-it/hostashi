// SPDX-License-Identifier: AGPL-3.0-or-later

// The German catalog (ADR-0071). Typed as the English one, so a missing
// area or key fails tsc; locales.test.ts also checks the other way round.
import type { Catalog } from "../en";
import account from "./account.json";
import auth from "./auth.json";
import backups from "./backups.json";
import certificate from "./certificate.json";
import common from "./common.json";
import errors from "./errors.json";
import limits from "./limits.json";
import overview from "./overview.json";
import pages from "./pages.json";
import parked from "./parked.json";
import profiles from "./profiles.json";
import realIp from "./realIp.json";
import shell from "./shell.json";
import siteAccess from "./siteAccess.json";
import siteDomains from "./siteDomains.json";
import siteHeaders from "./siteHeaders.json";
import tokens from "./tokens.json";
import ui from "./ui.json";

const de: Catalog = { account, auth, backups, certificate, common, errors, limits, overview, pages, parked, profiles, realIp, shell, siteAccess, siteDomains, siteHeaders, tokens, ui };

export default de;
