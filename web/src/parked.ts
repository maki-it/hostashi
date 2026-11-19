// SPDX-License-Identifier: AGPL-3.0-or-later

// Parked domains (ADR-0027): a domain without a site that shows a parking
// page with 200 on every path, over HTTPS once its certificate is there.

import { ApiError, request } from "./api";

export type ParkedTLS = {
  enabled: boolean; // HTTPS wanted
  active: boolean; // the web server serves the domain over HTTPS now
  notAfter: string | null;
  error?: string;
  failedAt?: string;
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

export function parkedMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "domain_taken":
        return "A site or the panel already uses this domain.";
      case "tls_unavailable":
        return "Certificates are not available on this panel. Park the domain without HTTPS.";
      case "parked_not_found":
        return "This domain is not parked any more. Reload the page.";
      case "page_not_found":
        return "This page does not exist any more. Reload the page.";
      case "forbidden":
        return "Only administrators can park domains.";
      case "agent_timeout":
        return "The server did not answer in time. Reload the page to see whether the change was made.";
      case "agent_unavailable":
        return "The change could not be completed. Try again.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
