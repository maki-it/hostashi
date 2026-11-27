// SPDX-License-Identifier: AGPL-3.0-or-later

// The German catalog (ADR-0071). Typed as the English one, so a missing
// area or key fails tsc; locales.test.ts also checks the other way round.
import type { Catalog } from "../en";
import account from "./account.json";
import auth from "./auth.json";
import backups from "./backups.json";
import common from "./common.json";
import errors from "./errors.json";
import files from "./files.json";
import git from "./git.json";
import hardening from "./hardening.json";
import logs from "./logs.json";
import malware from "./malware.json";
import permissions from "./permissions.json";
import shell from "./shell.json";
import ssh from "./ssh.json";
import tokens from "./tokens.json";
import usage from "./usage.json";

const de: Catalog = { account, auth, backups, common, errors, files, git, hardening, logs, malware, permissions, shell, ssh, tokens, usage };

export default de;
