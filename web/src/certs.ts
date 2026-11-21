// SPDX-License-Identifier: AGPL-3.0-or-later

// Certificates of a site (ADR-0039): from Let's Encrypt, renewed by the
// panel, or an own one, uploaded with its key or issued elsewhere for a
// CSR from the server.

import { request } from "./api";

export type CertSource = "letsencrypt" | "custom";

export type CertDetails = {
  domains: string[];
  issuer: string;
  notBefore: string | null;
  notAfter: string | null;
  fingerprint: string; // SHA-256 of the leaf, hex
  trusted: boolean; // the chain leads to a publicly trusted root
};

export type CertInfo = { certificate: CertDetails | null; csr?: string };

export type CertUpload = { certificate: string; chain: string; key: string; allowUntrusted: boolean };

// Limits of the server (internal/agentapi): certificate and chain together,
// and the key.
export const maxChainBytes = 32 << 10;
export const maxKeyBytes = 16 << 10;

// The certificate routes of a site, a parked domain or the panel address.
export const siteCertBase = (name: string) => "/sites/" + encodeURIComponent(name) + "/tls";
export const parkedCertBase = (domain: string) => "/parked-domains/" + encodeURIComponent(domain) + "/tls";
export const panelCertBase = "/panel/tls";

export const certInfo = (base: string) => request<CertInfo>("GET", base + "/certificate");
export const uploadCert = (base: string, u: CertUpload) => request<unknown>("PUT", base + "/certificate", u);
export const deleteCert = (base: string) => request<unknown>("DELETE", base + "/certificate");
export const createCSR = async (base: string) => (await request<{ csr: string }>("POST", base + "/csr")).csr;
export const setCertSource = (base: string, source: CertSource) => request<unknown>("PUT", base + "/source", { source });
export const renewCert = (base: string) => request<unknown>("POST", base + "/renew");

// CertState is the HTTPS state of what the card shows: wanted (enabled),
// served (active), and the sources as the API reports them.
export type CertState = {
  enabled: boolean;
  active: boolean;
  source?: CertSource;
  certSource?: "" | CertSource;
  renewPending?: boolean;
};

const pemRe = (type: string) => new RegExp(`-----BEGIN ${type}-----[\\s\\S]+?-----END ${type}-----`);

// uploadProblem checks a form before it is sent; the agent checks the
// certificate itself. withCSR: an empty key uses the key of the CSR.
export function uploadProblem(u: CertUpload, withCSR: boolean): string {
  if (!pemRe("CERTIFICATE").test(u.certificate)) return "Paste the certificate in PEM form, starting with -----BEGIN CERTIFICATE-----.";
  if (u.chain.trim() !== "" && !pemRe("CERTIFICATE").test(u.chain)) return "The chain must be PEM certificates, or leave it empty.";
  if (u.certificate.length + u.chain.length + 1 > maxChainBytes) return "Certificate and chain are too large (at most 32 KiB together).";
  if (/PRIVATE KEY/.test(u.certificate + u.chain)) return "A private key belongs in the key field, not with the certificates.";
  if (u.key.trim() === "") return withCSR ? "" : "Paste the private key, or create a CSR first.";
  if (/ENCRYPTED/.test(u.key)) return "The key is protected by a passphrase. Remove it first (openssl pkey -in key.pem -out plain.pem).";
  if (!/-----BEGIN (RSA |EC )?PRIVATE KEY-----/.test(u.key)) return "Paste the private key in PEM form, starting with -----BEGIN PRIVATE KEY-----.";
  if (u.key.length > maxKeyBytes) return "The key is too large (at most 16 KiB).";
  return "";
}

// daysLeft counts whole days from now until iso; negative when past.
export function daysLeft(iso: string | null, now: Date = new Date()): number | null {
  if (!iso) return null;
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return null;
  return Math.floor((t - now.getTime()) / 86_400_000);
}

// fingerprintText groups a hex fingerprint as AB:CD:... for reading.
export const fingerprintText = (hex: string) => (hex.toUpperCase().match(/.{1,2}/g) ?? []).join(":");

// PanelTLS is the HTTPS state of the panel's own address (ADR-0023).
export type PanelTLS = CertState & { domain: string; domains: string[]; notAfter: string | null; error?: string };

export const panelTLS = () => request<PanelTLS>("GET", panelCertBase);
