// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { listScopes, type Scope } from "./device";
import { listSites, type Site } from "./sites";
import { Badge, Button, Field, Notice } from "./ui";

// Grant is what a new API token may do: its scopes, its sites and how long
// it lives. The device login and the token form on the account page share it.
export type Grant = {
  scopes: Set<string>;
  allSites: boolean;
  sites: Set<string>;
  days: string;
  unlimited: boolean;
};

/** grantReady reports whether a grant can be sent. */
export function grantReady(g: Grant): boolean {
  const d = Number(g.days);
  return g.scopes.size > 0 && (g.allSites || g.sites.size > 0) && (g.unlimited || (Number.isInteger(d) && d >= 1 && d <= 365));
}

// scopeGroups sorts the scope catalog into areas by the part before the
// colon. A scope no group names lands in "Other", so a new one never hides.
const scopeGroups: { id: string; label: string; areas: string[] }[] = [
  {
    id: "sites",
    label: "Sites",
    areas: ["sites", "sites.maintenance", "sites.snippets", "files", "git", "logs", "cron", "ssh", "pages", "pagecache", "objectcache", "limits"],
  },
  { id: "databases", label: "Databases", areas: ["databases", "db_servers"] },
  { id: "backups", label: "Backups", areas: ["backups"] },
  { id: "server", label: "Server", areas: ["services", "system", "firewall", "doctor", "metrics", "php", "node", "python"] },
  { id: "panel", label: "Panel", areas: ["audit", "migrations"] },
];

const actionOrder = ["read", "write", "purge", "install", "restore", "delete"];

const areaOf = (s: Scope) => s.name.split(":")[0] ?? "";

const isReading = (s: Scope) => s.name.endsWith(":read");

/**
 * scopeRows places each area on rows of its own: its reading scopes on the
 * left, the ones that change on the right, so that sites:read and
 * sites:write share a row and an area never shares one with the next.
 * Row 1 holds the column headings.
 */
export function scopeRows(scopes: Scope[]): { scope: Scope; row: number }[] {
  const out: { scope: Scope; row: number }[] = [];
  let row = 2;
  for (let i = 0; i < scopes.length; ) {
    let j = i;
    while (j < scopes.length && areaOf(scopes[j]!) === areaOf(scopes[i]!)) j++;
    const area = scopes.slice(i, j);
    const reads = area.filter(isReading);
    const changes = area.filter((s) => !isReading(s));
    reads.forEach((s, k) => out.push({ scope: s, row: row + k }));
    changes.forEach((s, k) => out.push({ scope: s, row: row + k }));
    row += Math.max(reads.length, changes.length);
    i = j;
  }
  return out;
}

export type ScopeGroup = { id: string; label: string; scopes: Scope[] };

/** groupScopes puts scopes into areas, each area's reading scope first. Empty groups are left out. */
export function groupScopes(scopes: Scope[]): ScopeGroup[] {
  const area = areaOf;
  const rank = (list: string[], v: string) => (list.includes(v) ? list.indexOf(v) : list.length);
  const out: ScopeGroup[] = scopeGroups.map((g) => ({ id: g.id, label: g.label, scopes: [] }));
  const other: ScopeGroup = { id: "other", label: "Other", scopes: [] };
  for (const s of scopes) {
    const i = scopeGroups.findIndex((g) => g.areas.includes(area(s)));
    (i >= 0 ? out[i]! : other).scopes.push(s);
  }
  out.push(other);
  for (const [i, g] of out.entries()) {
    const areas = scopeGroups[i]?.areas ?? [];
    g.scopes.sort(
      (a, b) =>
        rank(areas, area(a)) - rank(areas, area(b)) ||
        area(a).localeCompare(area(b)) ||
        rank(actionOrder, a.name.split(":")[1] ?? "") - rank(actionOrder, b.name.split(":")[1] ?? ""),
    );
  }
  return out.filter((g) => g.scopes.length > 0);
}

function toggle(set: Set<string>, name: string, on: boolean): Set<string> {
  const next = new Set(set);
  if (on) next.add(name);
  else next.delete(name);
  return next;
}

/**
 * useGrant loads the scopes the account's role can use and its sites. Every
 * scope but the high-risk ones starts ticked; those must be chosen on
 * purpose.
 */
export function useGrant(days = "90") {
  const [scopes, setScopes] = useState<Scope[] | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [grant, setGrant] = useState<Grant>({ scopes: new Set(), allSites: true, sites: new Set(), days, unlimited: false });

  useEffect(() => {
    listScopes()
      .then((s) => {
        setScopes(s);
        setGrant((g) => ({ ...g, scopes: new Set(s.filter((x) => !x.highRisk).map((x) => x.name)) }));
      })
      .catch((err: unknown) => setError(err));
    listSites()
      .then(setSites)
      .catch(() => setSites([]));
  }, []);

  return { scopes, sites, error, grant, setGrant };
}

export function GrantFields({
  scopes,
  sites,
  grant,
  onChange,
  allowUnlimited = false,
}: {
  scopes: Scope[] | null;
  sites: Site[];
  grant: Grant;
  onChange: (g: Grant) => void;
  /** Offers a token that never expires (only on the account page). */
  allowUnlimited?: boolean;
}) {
  const pick = (keep: (s: Scope) => boolean) => onChange({ ...grant, scopes: new Set((scopes ?? []).filter(keep).map((s) => s.name)) });

  return (
    <>
      <fieldset className="ui-fieldset ui-stack-tight">
        <legend>What the token may do</legend>
        {scopes === null && <p aria-busy="true">Loading…</p>}
        {scopes !== null && scopes.length > 0 && (
          <div className="ui-row">
            <Button onClick={() => pick((s) => s.name.endsWith(":read"))}>Only reading</Button>
            <Button onClick={() => pick((s) => !s.highRisk)}>All but high risk</Button>
            <Button onClick={() => pick(() => false)}>None</Button>
          </div>
        )}
        {scopes !== null &&
          groupScopes(scopes).map((g) => (
            <div key={g.label} className="ui-scope-group" role="group" aria-labelledby={"scope-group-" + g.id}>
              <div className="ui-scope-head">
                <h3 id={"scope-group-" + g.id}>{g.label}</h3>
                <span className="ui-hint">
                  {g.scopes.filter((s) => grant.scopes.has(s.name)).length} of {g.scopes.length}
                </span>
              </div>
              <div className="ui-scope-grid">
                <span className="ui-scope-col" aria-hidden="true">
                  Read
                </span>
                <span className="ui-scope-col" aria-hidden="true">
                  Change
                </span>
                {scopeRows(g.scopes).map(({ scope: s, row }) => (
                  <label
                    key={s.name}
                    className={"ui-check ui-check-hint " + (isReading(s) ? "ui-scope-read" : "ui-scope-change")}
                    style={{ gridRow: row }}
                  >
                    <input
                      type="checkbox"
                      checked={grant.scopes.has(s.name)}
                      onChange={(e) => onChange({ ...grant, scopes: toggle(grant.scopes, s.name, e.target.checked) })}
                    />
                    <span>
                      <span>
                        <code>{s.name}</code> {s.highRisk && <Badge tone="warn">high risk</Badge>}
                      </span>
                      <span className="ui-hint">{s.description}</span>
                    </span>
                  </label>
                ))}
              </div>
            </div>
          ))}
      </fieldset>
      <fieldset className="ui-fieldset ui-stack-tight">
        <legend>Sites</legend>
        <label className="ui-check">
          <input type="checkbox" checked={grant.allSites} onChange={(e) => onChange({ ...grant, allSites: e.target.checked })} />
          Every site I can reach, also later ones
        </label>
        {!grant.allSites && sites.length === 0 && <p className="ui-hint ui-flush">You cannot reach any site yet.</p>}
        {!grant.allSites &&
          sites.map((s) => (
            <label key={s.name} className="ui-check">
              <input
                type="checkbox"
                checked={grant.sites.has(s.name)}
                onChange={(e) => onChange({ ...grant, sites: toggle(grant.sites, s.name, e.target.checked) })}
              />
              {s.name}
            </label>
          ))}
      </fieldset>
      <div className="ui-narrow">
        <Field label="Valid for (days)" hint="1 to 365. The token stops working after that.">
          <input
            className="ui-input"
            type="number"
            min={1}
            max={365}
            value={grant.days}
            disabled={grant.unlimited}
            onChange={(e) => onChange({ ...grant, days: e.target.value })}
          />
        </Field>
      </div>
      {allowUnlimited && (
        <label className="ui-check">
          <input type="checkbox" checked={grant.unlimited} onChange={(e) => onChange({ ...grant, unlimited: e.target.checked })} />
          <span>
            Never expires
            <span className="ui-hint"> Only for a monitoring or deploy job you look after.</span>
          </span>
        </label>
      )}
      {grant.unlimited && (
        <Notice tone="warn" title="This token works until you revoke it">
          If it leaks, it stays usable for as long as nobody notices. Prefer a lifetime and renew it.
        </Notice>
      )}
    </>
  );
}
