// SPDX-License-Identifier: AGPL-3.0-or-later

// SSH and SFTP access of a site (ADR-0032): one login per site, the site
// user, with public keys only. Each key is SFTP only or allows a shell.
// The panel database holds the keys; every change is sent to the agent.

import { ApiError, request } from "./api";
import i18n from "./i18n";
import { siteMessage } from "./sites";

export type SSHAccess = "sftp" | "shell";

export type SSHKey = {
  id: number;
  label: string;
  access: SSHAccess;
  public: string; // type and base64, no comment
  fingerprint: string; // SHA256:...
  createdAt: number; // Unix seconds
  updatedAt: number;
};

export type SSHHostKey = { type: string; fingerprint: string };

const keyPath = (site: string, id?: number) =>
  "/sites/" + encodeURIComponent(site) + "/ssh-keys" + (id === undefined ? "" : "/" + String(id));

export const listSSHKeys = (site: string) => request<{ keys: SSHKey[] }>("GET", keyPath(site));
export const addSSHKey = (site: string, k: { label: string; access: SSHAccess; public: string }) => request<SSHKey>("POST", keyPath(site), k);
export const changeSSHKey = (site: string, id: number, k: { label: string; access: SSHAccess }) => request<SSHKey>("PUT", keyPath(site, id), k);
export const deleteSSHKey = (site: string, id: number) => request<void>("DELETE", keyPath(site, id));
export const hostKeys = () => request<{ keys: SSHHostKey[] }>("GET", "/ssh/host-keys");

// Mirrors of internal/sshkeys so a form can say what is wrong before a
// request. The server parses the key itself.
export const maxSSHKeys = 50;
export const maxLabelBytes = 64;

const keyTypes = [
  "ssh-ed25519",
  "sk-ssh-ed25519@openssh.com",
  "ecdsa-sha2-nistp256",
  "ecdsa-sha2-nistp384",
  "ecdsa-sha2-nistp521",
  "sk-ecdsa-sha2-nistp256@openssh.com",
  "ssh-rsa",
];

export function labelProblem(v: string): string {
  if (v === "") return i18n.t("ssh.check.labelEmpty");
  if (new TextEncoder().encode(v).length > maxLabelBytes) return i18n.t("ssh.check.labelLong", { max: maxLabelBytes });
  if (v.startsWith(" ") || v.endsWith(" ")) return i18n.t("ssh.check.labelSpace");
  if (!/^[A-Za-z0-9 ._\-@+]+$/.test(v)) return i18n.t("ssh.check.labelChars");
  return "";
}

export function keyProblem(v: string): string {
  const s = v.trim();
  if (s === "") return i18n.t("ssh.check.keyEmpty");
  if (s.includes("PRIVATE KEY")) return i18n.t("ssh.check.keyPrivate");
  if (/[\r\n]/.test(s)) return i18n.t("ssh.check.keyLines");
  const type = s.split(/\s+/)[0] ?? "";
  if (!keyTypes.includes(type)) return i18n.t("ssh.check.keyType");
  return "";
}

// suggestLabel takes the key's comment (often user@host) as the label.
export function suggestLabel(key: string): string {
  const parts = key.trim().split(/\s+/);
  const comment = parts.slice(2).join(" ").replace(/[^A-Za-z0-9 ._\-@+]/g, "").trim();
  return comment.slice(0, maxLabelBytes).trim();
}

export const accessName = (a: SSHAccess) => i18n.t(a === "shell" ? "ssh.access.shell" : "ssh.access.sftp");

// sshMessage turns an API error into a sentence.
export function sshMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "ssh_unavailable":
      case "ssh_key_exists":
      case "site_busy":
      case "forbidden":
        return i18n.t(`ssh.errors.${err.code}`);
      case "too_many_ssh_keys":
        return i18n.t("ssh.errors.too_many_ssh_keys", { max: maxSSHKeys });
      case "invalid_argument":
        if (/sshkeys: invalid: /.test(err.message)) return i18n.t("ssh.errors.refused", { reason: err.message.replace(/^.*sshkeys: invalid: /, "") });
        return err.message;
      case "not_found":
        if (err.message.includes("SSH key")) return i18n.t("ssh.errors.keyGone");
        break;

    }
  }
  return siteMessage(err);
}
