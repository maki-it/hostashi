// SPDX-License-Identifier: AGPL-3.0-or-later

// Custom nginx snippets of a site (ADR-0029): one inside the server block,
// one inside the site's "location /". Only allowed directives pass; the
// agent checks every save and names each rejected line.

import { ApiError, request } from "./api";

export type Snippets = { server: string; root: string; updatedAt?: string };

export type SnippetProblem = { line: number; msg: string };
export type SnippetProblems = { server: SnippetProblem[]; root: SnippetProblem[] };

export type DraftItem = {
  context: string;
  source: string;
  line: number;
  slot?: "server" | "root";
  action: "kept" | "rewritten" | "dropped";
  reason?: string;
};
export type SnippetDraft = { server: string; root: string; items: DraftItem[] };

// maxSnippetBytes mirrors agentapi.MaxSnippetBytes; the server checks again.
export const maxSnippetBytes = 64 * 1024;

const snippetsPath = (site: string) => "/sites/" + encodeURIComponent(site) + "/snippets";

export const getSnippets = (site: string) => request<Snippets>("GET", snippetsPath(site));
export const setSnippets = (site: string, s: { server: string; root: string }) => request<Snippets>("PUT", snippetsPath(site), s);
export const snippetDraft = (report: string, site: string) =>
  request<SnippetDraft>(
    "GET",
    "/migrations/cloudpanel/reports/" + encodeURIComponent(report) + "/sites/" + encodeURIComponent(site) + "/snippets-draft",
  );

export function sizeProblem(s: { server: string; root: string }): string {
  const enc = new TextEncoder();
  if (enc.encode(s.server).length > maxSnippetBytes || enc.encode(s.root).length > maxSnippetBytes)
    return "A snippet can be at most 64 KiB.";
  return "";
}

// snippetProblems returns the rejected lines of a failed save, or null.
export function snippetProblems(err: unknown): SnippetProblems | null {
  if (!(err instanceof ApiError) || err.code !== "snippet_rejected") return null;
  const p = (err.body as { problems?: Partial<SnippetProblems> } | undefined)?.problems;
  return { server: p?.server ?? [], root: p?.root ?? [] };
}

export function snippetMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "snippet_rejected":
        return "Some lines are not allowed. Nothing was saved.";
      case "site_missing":
        return "Create the site on this server first.";
      case "site_busy":
        return "The site is being created or deleted. Try again in a moment.";
      case "not_found":
      case "no_site":
        return "The site does not exist.";
      case "forbidden":
        return "Only administrators can change snippets.";
    }
    if (err.status === 409 || err.status === 502 || err.status === 503)
      return "The web server did not accept the change. The previous snippets stay active.";
    return err.message;
  }
  return "The request failed. Check the connection and try again.";
}
