// SPDX-License-Identifier: AGPL-3.0-or-later

// The English catalog (ADR-0071). It is the source: its shape is the type
// every other language must match, and i18next falls back to it.
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

const en = { account, auth, backups, common, errors, files, git, hardening, logs, malware, permissions, shell, ssh, tokens, usage };

export type Catalog = typeof en;

export default en;
