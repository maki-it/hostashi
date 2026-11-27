// SPDX-License-Identifier: AGPL-3.0-or-later

// Security headers of a site (ADR-0056): types, API calls and the form
// model of the card in the "Domains & HTTPS" tab.

import { ApiError, request } from "./api";
import i18n from "./i18n";

export type WebHeaders = {
  noSniff?: boolean;
  frameOptions?: string;
  referrerPolicy?: string;
  permissionsPolicy?: string;
  csp?: string;
  cspReportOnly?: boolean;
  noIndex?: boolean;
};

export type HeadersView = {
  headers: WebHeaders;
  defaults: WebHeaders;
  referrerPolicies: string[];
  permissionsPolicyStrict: string;
  maxValueBytes: number;
  updatedAt?: string;
};

export function getHeaders(site: string): Promise<HeadersView> {
  return request<HeadersView>(
    "GET",
    `/sites/${encodeURIComponent(site)}/headers`,
  );
}

export function setHeaders(site: string, h: WebHeaders): Promise<HeadersView> {
  return request<HeadersView>(
    "PUT",
    `/sites/${encodeURIComponent(site)}/headers`,
    h,
  );
}

export type HeadersForm = {
  noSniff: boolean;
  frameOptions: string; // "" off
  referrerPolicy: string; // "" off
  permissions: "off" | "strict" | "own";
  permissionsValue: string;
  csp: "off" | "report" | "enforce";
  cspValue: string;
  // noIndex (X-Robots-Tag) has no control on this card yet; it is kept
  // as it is, so saving the card does not drop a staging clone's noindex.
  noIndex: boolean;
};

export function toForm(h: WebHeaders, strict: string): HeadersForm {
  const pp = h.permissionsPolicy ?? "";
  return {
    noSniff: h.noSniff === true,
    frameOptions: h.frameOptions ?? "",
    referrerPolicy: h.referrerPolicy ?? "",
    permissions: pp === "" ? "off" : pp === strict ? "strict" : "own",
    permissionsValue: pp === strict ? "" : pp,
    csp: !h.csp ? "off" : h.cspReportOnly ? "report" : "enforce",
    cspValue: h.csp ?? "",
    noIndex: h.noIndex === true,
  };
}

// fromForm builds the request body; it returns an error text for a form
// the server would refuse anyway (an own value left empty).
export function fromForm(
  f: HeadersForm,
  strict: string,
): { headers: WebHeaders; error?: string } {
  const h: WebHeaders = {};
  if (f.noSniff) h.noSniff = true;
  if (f.frameOptions) h.frameOptions = f.frameOptions;
  if (f.referrerPolicy) h.referrerPolicy = f.referrerPolicy;
  if (f.noIndex) h.noIndex = true;
  if (f.permissions === "strict") h.permissionsPolicy = strict;
  if (f.permissions === "own") {
    const v = f.permissionsValue.trim();
    if (v === "")
      return {
        headers: h,
        error: i18n.t("siteHeaders.problems.permissions"),
      };
    h.permissionsPolicy = v;
  }
  if (f.csp !== "off") {
    const v = f.cspValue.trim();
    if (v === "")
      return {
        headers: h,
        error: i18n.t("siteHeaders.problems.csp"),
      };
    h.csp = v;
    if (f.csp === "report") h.cspReportOnly = true;
  }
  return { headers: h };
}

export function headersMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "header_rejected")
      return i18n.t("siteHeaders.errors.rejected", {
        reason: err.message.replace(/^header rejected: /, ""),
      });
    if (err.code === "header_conflict")
      return i18n.t("siteHeaders.errors.conflict", {
        reason: err.message
          .replace(/^header conflict: /, "")
          .replace(/^./, (c) => c.toUpperCase()),
      });
    if (err.code === "not_found") return i18n.t("siteHeaders.errors.siteGone");
    return err.message;
  }
  return i18n.t("siteHeaders.errors.generic");
}
