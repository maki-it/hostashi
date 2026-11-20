// SPDX-License-Identifier: AGPL-3.0-or-later

// SSH and SFTP access of a site (ADR-0032): one login per site, the site
// user, with public keys only. Each key is SFTP only or allows a shell.
// The panel database holds the keys; every change is sent to the agent.

import { ApiError, request } from "./api";
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
  if (v === "") return "Enter a label, for example the name of the computer.";
  if (new TextEncoder().encode(v).length > maxLabelBytes) return `The label is longer than ${maxLabelBytes} characters.`;
  if (v.startsWith(" ") || v.endsWith(" ")) return "The label must not start or end with a space.";
  if (!/^[A-Za-z0-9 ._\-@+]+$/.test(v)) return "The label may contain letters, digits, spaces and . _ - @ + only.";
  return "";
}

export function keyProblem(v: string): string {
  const s = v.trim();
  if (s === "") return "Paste a public key, the contents of a file like id_ed25519.pub.";
  if (s.includes("PRIVATE KEY")) return "This is a private key. Never paste it anywhere; paste the .pub file instead.";
  if (/[\r\n]/.test(s)) return "Paste one key on one line.";
  const type = s.split(/\s+/)[0] ?? "";
  if (!keyTypes.includes(type)) return "This does not look like a supported public key. Use an Ed25519, ECDSA or RSA (3072 bits or more) key.";
  return "";
}

// suggestLabel takes the key's comment (often user@host) as the label.
export function suggestLabel(key: string): string {
  const parts = key.trim().split(/\s+/);
  const comment = parts.slice(2).join(" ").replace(/[^A-Za-z0-9 ._\-@+]/g, "").trim();
  return comment.slice(0, maxLabelBytes).trim();
}

export const accessName = (a: SSHAccess) => (a === "shell" ? "Shell and SFTP" : "SFTP only");

// sshMessage turns an API error into a sentence.
export function sshMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "ssh_unavailable":
        return "SSH access is not available on this panel.";
      case "too_many_ssh_keys":
        return `A site has at most ${maxSSHKeys} keys. Delete one first.`;
      case "ssh_key_exists":
        return "The site already has this key.";
      case "invalid_argument":
        return err.message.replace(/^.*sshkeys: invalid: /, "The key was refused: ");
      case "site_busy":
        return "The site is not active right now. Reload the page in a moment.";
      case "forbidden":
        return "Only administrators can manage SSH keys.";
      case "not_found":
        if (err.message.includes("SSH key")) return "This key does not exist any more. Reload the page.";
        break;
    }
  }
  return siteMessage(err);
}
