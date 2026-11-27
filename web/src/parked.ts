// SPDX-License-Identifier: AGPL-3.0-or-later

// Parked domains (ADR-0027): a domain without a site that shows a parking
// page with 200 on every path, over HTTPS once its certificate is there.

import { request } from "./api";
import { areaErrorText } from "./i18n";

export type ParkedTLS = {
  enabled: boolean; // HTTPS wanted
  active: boolean; // the web server serves the domain over HTTPS now
  notAfter: string | null;
  error?: string;
  failedAt?: string;
  // As for sites (ADR-0039).
  source?: "letsencrypt" | "custom";
  certSource?: "" | "letsencrypt" | "custom";
  trusted?: boolean;
  renewPending?: boolean;
};

export type ParkedDomain = {
  domain: string;
  page: string;
  tls: ParkedTLS;
  createdAt: string;
  updatedAt: string;
};

const parkedPath = (domain: string) => "/parked-domains/" + encodeURIComponent(domain);

export const listParked = async () => (await request<{ parkedDomains: ParkedDomain[] }>("GET", "/parked-domains")).parkedDomains;

export const setParked = (domain: string, page: string, tls: boolean) => request<ParkedDomain>("PUT", parkedPath(domain), { page, tls });

export const deleteParked = (domain: string) => request<void>("DELETE", parkedPath(domain));

/** parkedMessage explains an error of the parked domains in the current language. */
export const parkedMessage = (err: unknown): string => areaErrorText("parked.errors", err);
